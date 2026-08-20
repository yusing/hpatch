package hpatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	metricsFilename = "metrics.bin"
	metricsLockname = "metrics.lock"
	metricsMagic    = "HPATCH27"

	metricsToolOffset          = 1280
	metricsRecoveryOffset      = 208
	metricsToolEntrySize       = 280
	metricsChecksumOffset      = metricsToolOffset + maxMetricTools*metricsToolEntrySize
	metricsSlotSize            = metricsChecksumOffset + sha256.Size
	metricsFileSize            = 2 * metricsSlotSize
	metricsDiagnosticOffset    = 1248
	metricsToolCountOffset     = 1256
	metricsSharedOffset        = 1264
	metricsMisuseWarningOffset = 1272

	commandCount          = 6
	metricsLockRetryDelay = 10 * time.Millisecond
)

var commandOperations = [commandCount]string{
	"in", "new", "mv", "rm", "type", "add",
}

type pendingMetricsWriterState struct {
	count uint64
	done  chan struct{}
}

// pendingMetricsWriters prevents this process's repeated shared-lock readers
// from starving a writer while it uses cancellable, non-blocking lock attempts.
// Entries are scoped to the canonical lock path; the filesystem lock remains
// the authority across processes.
var pendingMetricsWriters = struct {
	mu         sync.Mutex
	byLockPath map[string]*pendingMetricsWriterState
}{byLockPath: make(map[string]*pendingMetricsWriterState)}

func metricLockPath(dataDirectory string) string {
	directory, err := filepath.Abs(dataDirectory)
	if err != nil {
		directory = filepath.Clean(dataDirectory)
	}
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		directory = resolved
	}
	return filepath.Join(directory, metricsLockname)
}

func registerPendingMetricsWriter(lockPath string) {
	pendingMetricsWriters.mu.Lock()
	defer pendingMetricsWriters.mu.Unlock()
	state := pendingMetricsWriters.byLockPath[lockPath]
	if state == nil {
		state = &pendingMetricsWriterState{done: make(chan struct{})}
		pendingMetricsWriters.byLockPath[lockPath] = state
	}
	state.count++
}

func unregisterPendingMetricsWriter(lockPath string) {
	pendingMetricsWriters.mu.Lock()
	defer pendingMetricsWriters.mu.Unlock()
	state := pendingMetricsWriters.byLockPath[lockPath]
	state.count--
	if state.count == 0 {
		delete(pendingMetricsWriters.byLockPath, lockPath)
		close(state.done)
	}
}

func waitForPendingMetricsWriters(lockPath string) {
	pendingMetricsWriters.mu.Lock()
	state := pendingMetricsWriters.byLockPath[lockPath]
	pendingMetricsWriters.mu.Unlock()
	if state != nil {
		<-state.done
	}
}

type commandMetric struct {
	Invocations uint64 `json:"invocations"`
	Errors      uint64 `json:"errors"`
}

type commandMetrics [commandCount]commandMetric

type metrics struct {
	invocationMetrics

	HPatchTokens             uint64
	ApplyPatchTokens         uint64
	IneffectiveHPatchTokens  uint64
	FailedApplyPatchTokens   uint64
	ReportInputTokens        uint64
	DiagnosticInputTokens    uint64
	MisuseWarningInputTokens uint64

	// Sessions counts distinct agent sessions that carried the routed definition
	// change. DefinitionRequests counts every request carrying that context.
	Sessions           uint64
	DefinitionRequests uint64
	// DefinitionInputTokens is the cumulative once-per-session installed tool
	// collection added by the router. RemovedDefinitionInputTokens is the
	// corresponding Code Mode apply_patch definition removed by the router.
	// RemovedExecCommandDefinitionInputTokens tracks the independent command sections.
	DefinitionInputTokens                   uint64
	RemovedDefinitionInputTokens            uint64
	RemovedExecCommandDefinitionInputTokens uint64
	SharedDefinitionInputTokens             int64
	ToolCount                               uint16
	Tools                                   [maxMetricTools]toolMetric
	compensation                            HostMetricCompensation
}

func commandOperationIndex(operation string) int {
	for index, candidate := range commandOperations {
		if operation == candidate {
			return index
		}
	}
	return -1
}

func (m *commandMetrics) total() (commandMetric, bool) {
	var total commandMetric
	for _, entry := range m {
		if entry.Errors > entry.Invocations || !addCounter(&total.Invocations, entry.Invocations) || !addCounter(&total.Errors, entry.Errors) {
			return commandMetric{}, false
		}
	}
	return total, true
}

func (m commandMetric) errorRate() string {
	return percentage(new(big.Int).SetUint64(m.Errors), new(big.Int).SetUint64(m.Invocations))
}

func updateMetricsForSessionContext(ctx context.Context, dataDirectory string, entry metrics, session string) (err error) {
	if dataDirectory == "" {
		return fmt.Errorf("metrics directory is unavailable")
	}
	if !validInvocationMetrics(entry.invocationMetrics) || !validToolMetrics(entry) {
		return fmt.Errorf("updating metrics: invalid command, feature, or tool counters")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return fmt.Errorf("creating metrics directory: %w", err)
	}
	lockPath := metricLockPath(dataDirectory)
	lock := flock.New(lockPath)
	registerPendingMetricsWriter(lockPath)
	waitingForLock := true
	defer func() {
		if waitingForLock {
			unregisterPendingMetricsWriter(lockPath)
		}
	}()
	locked, err := lock.TryLockContext(ctx, metricsLockRetryDelay)
	if err != nil {
		return fmt.Errorf("locking metrics: %w", err)
	}
	if !locked {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("locking metrics: %w", err)
		}
		return fmt.Errorf("locking metrics: lock was not acquired")
	}
	unregisterPendingMetricsWriter(lockPath)
	waitingForLock = false
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("unlocking metrics: %w", unlockErr))
		}
	}()

	file, err := os.OpenFile(filepath.Join(dataDirectory, metricsFilename), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening metrics: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing metrics: %w", closeErr))
		}
	}()
	total, generation, err := readMetricsFile(file)
	if err != nil {
		return err
	}
	if generation == ^uint64(0) {
		return fmt.Errorf("updating metrics: generation overflow")
	}
	nextGeneration := generation + 1
	if session != "" && entry.DefinitionRequests != 0 {
		fresh, err := claimSession(dataDirectory, session, generation, nextGeneration, generation != 0 && total.Sessions == 0 && total.DefinitionRequests == 0)
		if err != nil {
			return err
		}
		if fresh {
			entry.Sessions = 1
		} else {
			entry.clearDefinitionMetrics()
		}
	}
	if err := total.subtract(entry.compensation); err != nil {
		return err
	}
	entry.compensation = HostMetricCompensation{}
	if err := total.add(entry); err != nil {
		return err
	}
	encoded := encodeMetricsSlot(total, nextGeneration)
	offset := int64((nextGeneration-1)%2) * metricsSlotSize
	written, err := file.WriteAt(encoded[:], offset)
	if err != nil {
		return fmt.Errorf("writing metrics: %w", err)
	}
	if written != len(encoded) {
		return fmt.Errorf("writing metrics: %w", io.ErrShortWrite)
	}
	return nil
}

func (m *metrics) add(entry metrics) error {
	for _, counter := range []struct {
		destination *uint64
		increment   uint64
	}{
		{&m.HPatchTokens, entry.HPatchTokens},
		{&m.ApplyPatchTokens, entry.ApplyPatchTokens},
		{&m.IneffectiveHPatchTokens, entry.IneffectiveHPatchTokens},
		{&m.FailedApplyPatchTokens, entry.FailedApplyPatchTokens},
		{&m.ReportInputTokens, entry.ReportInputTokens},
		{&m.DiagnosticInputTokens, entry.DiagnosticInputTokens},
		{&m.MisuseWarningInputTokens, entry.MisuseWarningInputTokens},
		{&m.Sessions, entry.Sessions},
		{&m.DefinitionRequests, entry.DefinitionRequests},
		{&m.DefinitionInputTokens, entry.DefinitionInputTokens},
		{&m.RemovedDefinitionInputTokens, entry.RemovedDefinitionInputTokens},
		{&m.RemovedExecCommandDefinitionInputTokens, entry.RemovedExecCommandDefinitionInputTokens},
	} {
		if !addCounter(counter.destination, counter.increment) {
			return fmt.Errorf("updating metrics: token count overflow")
		}
	}
	if !addSignedCounter(&m.SharedDefinitionInputTokens, entry.SharedDefinitionInputTokens) {
		return fmt.Errorf("updating metrics: shared definition framing overflow")
	}
	for _, tool := range entry.Tools[:entry.ToolCount] {
		if err := m.addTool(tool); err != nil {
			return err
		}
	}
	for index := range recoveryKindCount {
		if !addCounter(&m.Recoveries[index], entry.Recoveries[index]) {
			return fmt.Errorf("updating metrics: recovery count overflow")
		}
	}
	for index := range commandCount {
		if !addCommandMetric(&m.Commands[index], entry.Commands[index]) {
			return fmt.Errorf("updating metrics: command count overflow")
		}
	}
	for index := range targetVariantCount {
		if !addCommandMetric(&m.Targets[index], entry.Targets[index]) {
			return fmt.Errorf("updating metrics: target count overflow")
		}
	}
	for index := range failureReasonCount {
		if !addCounter(&m.Reasons[index], entry.Reasons[index]) {
			return fmt.Errorf("updating metrics: failure reason count overflow")
		}
	}
	for command := range commandCount {
		for reason := range failureReasonCount {
			if !addCounter(&m.CommandReasons[command][reason], entry.CommandReasons[command][reason]) {
				return fmt.Errorf("updating metrics: command reason count overflow")
			}
		}
	}
	if !validInvocationMetrics(m.invocationMetrics) || !validToolMetrics(*m) {
		return fmt.Errorf("updating metrics: aggregate command, feature, or tool counters are inconsistent")
	}
	return nil
}

func (m *metrics) subtract(removal HostMetricCompensation) error {
	for _, counter := range []struct {
		destination *uint64
		decrement   uint64
	}{
		{&m.HPatchTokens, removal.HPatchTokens},
		{&m.ApplyPatchTokens, removal.ApplyPatchTokens},
		{&m.IneffectiveHPatchTokens, removal.IneffectiveHPatchTokens},
		{&m.FailedApplyPatchTokens, removal.FailedApplyPatchTokens},
	} {
		if counter.decrement > *counter.destination {
			return fmt.Errorf("updating metrics: compensation exceeds recorded token count")
		}
		*counter.destination -= counter.decrement
	}
	for _, removed := range removal.ToolMetrics[:removal.ToolCount] {
		found := false
		for index := range m.ToolCount {
			current := &m.Tools[index]
			if current.PluginID != removed.PluginID || current.ToolName != removed.ToolName {
				continue
			}
			if !subtractToolMetricRecord(current, removed) {
				return fmt.Errorf("updating metrics: tool compensation exceeds recorded counters")
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("updating metrics: compensated tool is not recorded")
		}
	}
	return nil
}

func addCommandMetric(destination *commandMetric, increment commandMetric) bool {
	return addCounter(&destination.Invocations, increment.Invocations) && addCounter(&destination.Errors, increment.Errors)
}

func addCounter(destination *uint64, increment uint64) bool {
	if increment > ^uint64(0)-*destination {
		return false
	}
	*destination += increment
	return true
}

func validInvocationMetrics(events invocationMetrics) bool {
	total, ok := events.Commands.total()
	if !ok {
		return false
	}
	for _, entry := range events.Targets {
		if entry.Errors > entry.Invocations {
			return false
		}
	}
	var reasons uint64
	for _, count := range events.Reasons {
		if !addCounter(&reasons, count) {
			return false
		}
	}
	if reasons != total.Errors {
		return false
	}
	// The cross-tabulation must reconcile with both margins: each command's
	// reasons sum to that command's errors, and each reason's commands sum to
	// that reason's total.
	for command := range commandCount {
		var perCommand uint64
		for reason := range failureReasonCount {
			if !addCounter(&perCommand, events.CommandReasons[command][reason]) {
				return false
			}
		}
		if perCommand != events.Commands[command].Errors {
			return false
		}
	}
	for reason := range failureReasonCount {
		var perReason uint64
		for command := range commandCount {
			if !addCounter(&perReason, events.CommandReasons[command][reason]) {
				return false
			}
		}
		if perReason != events.Reasons[reason] {
			return false
		}
	}
	return true
}

func readMetrics(dataDirectory string) (total metrics, err error) {
	if dataDirectory == "" {
		return metrics{}, fmt.Errorf("metrics directory is unavailable")
	}
	metricsPath := filepath.Join(dataDirectory, metricsFilename)
	if _, err := os.Stat(metricsPath); err != nil {
		if os.IsNotExist(err) {
			return metrics{}, nil
		}
		return metrics{}, fmt.Errorf("checking metrics: %w", err)
	}
	lockPath := metricLockPath(dataDirectory)
	waitForPendingMetricsWriters(lockPath)
	lock := flock.New(lockPath)
	if err := lock.RLock(); err != nil {
		return metrics{}, fmt.Errorf("locking metrics: %w", err)
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("unlocking metrics: %w", unlockErr))
		}
	}()
	file, err := os.Open(metricsPath)
	if err != nil {
		return metrics{}, fmt.Errorf("opening metrics: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing metrics: %w", closeErr))
		}
	}()
	total, _, err = readMetricsFile(file)
	return total, err
}

func readMetricsFile(file *os.File) (metrics, uint64, error) {
	info, err := file.Stat()
	if err != nil {
		return metrics{}, 0, fmt.Errorf("reading metrics: %w", err)
	}
	if info.Size() == 0 {
		return metrics{}, 0, nil
	}
	if info.Size() > metricsFileSize {
		return metrics{}, 0, fmt.Errorf("reading metrics: unexpected file size %d", info.Size())
	}
	var latest metrics
	var latestGeneration uint64
	var valid, mismatchedVersion bool
	for index := range 2 {
		var encoded [metricsSlotSize]byte
		read, err := file.ReadAt(encoded[:], int64(index*metricsSlotSize))
		if err != nil && err != io.EOF {
			return metrics{}, 0, fmt.Errorf("reading metrics: %w", err)
		}
		if read != metricsSlotSize {
			continue
		}
		magic := string(encoded[:8])
		if bytes.HasPrefix(encoded[:8], []byte("HPATCH")) && magic != metricsMagic {
			checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
			generation := binary.LittleEndian.Uint64(encoded[8:16])
			if generation != 0 && bytes.Equal(encoded[metricsChecksumOffset:], checksum[:]) {
				mismatchedVersion = true
			}
			continue
		}
		candidate, generation, ok := decodeMetricsSlot(encoded)
		if ok && (!valid || generation > latestGeneration) {
			latest, latestGeneration, valid = candidate, generation, true
		}
	}
	if valid {
		return latest, latestGeneration, nil
	}
	if !mismatchedVersion {
		mismatchedVersion, err = hasValidPriorMetricsSlot(file, info.Size())
		if err != nil {
			return metrics{}, 0, err
		}
	}
	if mismatchedVersion {
		return metrics{}, 0, nil
	}
	return metrics{}, 0, fmt.Errorf("reading metrics: no valid counter slot")
}

func hasValidPriorMetricsSlot(file *os.File, size int64) (bool, error) {
	formats := []struct {
		slotSize       int64
		checksumOffset int
		checksumSize   int
	}{
		{slotSize: 34080, checksumOffset: 34048, checksumSize: 32},
		{slotSize: 2432, checksumOffset: 2400, checksumSize: 32},
		{slotSize: 2304, checksumOffset: 2272, checksumSize: 32},
		{slotSize: 264, checksumOffset: 232, checksumSize: 32},
		{slotSize: 2160, checksumOffset: 2128, checksumSize: 32},
		{slotSize: 2152, checksumOffset: 2120, checksumSize: 32},
		{slotSize: 256, checksumOffset: 224, checksumSize: 32},
		{slotSize: 64, checksumOffset: 40, checksumSize: 24},
	}
	for _, format := range formats {
		for offset := int64(0); offset+format.slotSize <= size; offset += format.slotSize {
			encoded := make([]byte, format.slotSize)
			if _, err := file.ReadAt(encoded, offset); err != nil {
				return false, fmt.Errorf("reading metrics: %w", err)
			}
			if !bytes.HasPrefix(encoded[:8], []byte("HPATCH")) || string(encoded[:8]) == metricsMagic {
				continue
			}
			checksum := sha256.Sum256(encoded[:format.checksumOffset])
			generation := binary.LittleEndian.Uint64(encoded[8:16])
			if generation != 0 && bytes.Equal(encoded[format.checksumOffset:], checksum[:format.checksumSize]) {
				return true, nil
			}
		}
	}
	return false, nil
}

func encodeMetricsSlot(value metrics, generation uint64) [metricsSlotSize]byte {
	var encoded [metricsSlotSize]byte
	copy(encoded[:8], metricsMagic)
	binary.LittleEndian.PutUint64(encoded[8:16], generation)
	binary.LittleEndian.PutUint64(encoded[16:24], value.HPatchTokens)
	binary.LittleEndian.PutUint64(encoded[24:32], value.ApplyPatchTokens)
	binary.LittleEndian.PutUint64(encoded[32:40], value.IneffectiveHPatchTokens)
	binary.LittleEndian.PutUint64(encoded[40:48], value.ReportInputTokens)
	binary.LittleEndian.PutUint64(encoded[48:56], value.Sessions)
	binary.LittleEndian.PutUint64(encoded[56:64], value.DefinitionInputTokens)
	binary.LittleEndian.PutUint64(encoded[64:72], value.RemovedDefinitionInputTokens)
	binary.LittleEndian.PutUint64(encoded[72:80], value.FailedApplyPatchTokens)
	binary.LittleEndian.PutUint64(encoded[80:88], value.DefinitionRequests)
	binary.LittleEndian.PutUint64(encoded[88:96], value.RemovedExecCommandDefinitionInputTokens)
	for index, entry := range value.Commands {
		putCommandMetric(encoded[:], 96+index*16, entry)
	}
	for index, entry := range value.Targets {
		putCommandMetric(encoded[:], 384+index*16, entry)
	}
	for index, count := range value.Reasons {
		binary.LittleEndian.PutUint64(encoded[448+index*8:456+index*8], count)
	}
	for command, reasons := range value.CommandReasons {
		base := 576 + command*int(failureReasonCount)*8
		for reason, count := range reasons {
			binary.LittleEndian.PutUint64(encoded[base+reason*8:base+reason*8+8], count)
		}
	}
	for index, count := range value.Recoveries {
		binary.LittleEndian.PutUint64(encoded[metricsRecoveryOffset+index*8:metricsRecoveryOffset+index*8+8], count)
	}
	binary.LittleEndian.PutUint64(encoded[metricsDiagnosticOffset:metricsDiagnosticOffset+8], value.DiagnosticInputTokens)
	binary.LittleEndian.PutUint64(encoded[metricsMisuseWarningOffset:metricsMisuseWarningOffset+8], value.MisuseWarningInputTokens)
	encodeToolMetrics(encoded[:], value)

	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	copy(encoded[metricsChecksumOffset:], checksum[:])
	return encoded
}

func putCommandMetric(encoded []byte, offset int, entry commandMetric) {
	binary.LittleEndian.PutUint64(encoded[offset:offset+8], entry.Invocations)
	binary.LittleEndian.PutUint64(encoded[offset+8:offset+16], entry.Errors)
}

func decodeMetricsSlot(encoded [metricsSlotSize]byte) (metrics, uint64, bool) {
	if !bytes.Equal(encoded[:8], []byte(metricsMagic)) {
		return metrics{}, 0, false
	}
	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	if !bytes.Equal(encoded[metricsChecksumOffset:], checksum[:]) {
		return metrics{}, 0, false
	}
	generation := binary.LittleEndian.Uint64(encoded[8:16])
	if generation == 0 {
		return metrics{}, 0, false
	}
	value := metrics{
		HPatchTokens:                            binary.LittleEndian.Uint64(encoded[16:24]),
		ApplyPatchTokens:                        binary.LittleEndian.Uint64(encoded[24:32]),
		IneffectiveHPatchTokens:                 binary.LittleEndian.Uint64(encoded[32:40]),
		ReportInputTokens:                       binary.LittleEndian.Uint64(encoded[40:48]),
		Sessions:                                binary.LittleEndian.Uint64(encoded[48:56]),
		DefinitionInputTokens:                   binary.LittleEndian.Uint64(encoded[56:64]),
		RemovedDefinitionInputTokens:            binary.LittleEndian.Uint64(encoded[64:72]),
		FailedApplyPatchTokens:                  binary.LittleEndian.Uint64(encoded[72:80]),
		DefinitionRequests:                      binary.LittleEndian.Uint64(encoded[80:88]),
		RemovedExecCommandDefinitionInputTokens: binary.LittleEndian.Uint64(encoded[88:96]),
		DiagnosticInputTokens:                   binary.LittleEndian.Uint64(encoded[metricsDiagnosticOffset : metricsDiagnosticOffset+8]),
		MisuseWarningInputTokens:                binary.LittleEndian.Uint64(encoded[metricsMisuseWarningOffset : metricsMisuseWarningOffset+8]),
	}
	for index := range recoveryKindCount {
		value.Recoveries[index] = binary.LittleEndian.Uint64(encoded[metricsRecoveryOffset+index*8 : metricsRecoveryOffset+index*8+8])
	}
	for index := range commandCount {
		value.Commands[index] = getCommandMetric(encoded[:], 96+index*16)
	}
	for index := range targetVariantCount {
		value.Targets[index] = getCommandMetric(encoded[:], 384+index*16)
	}
	for index := range int(failureReasonCount) {
		value.Reasons[index] = binary.LittleEndian.Uint64(encoded[448+index*8 : 456+index*8])
	}
	for command := range commandCount {
		base := 576 + command*int(failureReasonCount)*8
		for reason := range int(failureReasonCount) {
			value.CommandReasons[command][reason] = binary.LittleEndian.Uint64(encoded[base+reason*8 : base+reason*8+8])
		}
	}
	if !decodeToolMetrics(encoded[:], &value) {
		return metrics{}, 0, false
	}
	if !validInvocationMetrics(value.invocationMetrics) || !validToolMetrics(value) {
		return metrics{}, 0, false
	}
	return value, generation, true
}

func getCommandMetric(encoded []byte, offset int) commandMetric {
	return commandMetric{
		Invocations: binary.LittleEndian.Uint64(encoded[offset : offset+8]),
		Errors:      binary.LittleEndian.Uint64(encoded[offset+8 : offset+16]),
	}
}

func (m *metrics) reduction() string {
	baseline := new(big.Int).SetUint64(m.ApplyPatchTokens)
	difference := new(big.Int).Sub(new(big.Int).Set(baseline), new(big.Int).SetUint64(m.HPatchTokens))
	return percentage(difference, baseline)
}

// overallReduction compares all measured hpatch output with the generated
// apply_patch output. Failed hpatch calls use the empty-patch semantic baseline.
func (m *metrics) overallReduction() string {
	baseline := new(big.Int).SetUint64(m.ApplyPatchTokens)
	baseline.Add(baseline, new(big.Int).SetUint64(m.FailedApplyPatchTokens))
	actual := new(big.Int).SetUint64(m.HPatchTokens)
	actual.Add(actual, new(big.Int).SetUint64(m.IneffectiveHPatchTokens))
	difference := new(big.Int).Sub(new(big.Int).Set(baseline), actual)
	return percentage(difference, baseline)
}

func percentage(numerator, denominator *big.Int) string {
	if denominator.Sign() == 0 {
		return "0.0"
	}
	scaled := new(big.Int).Mul(new(big.Int).Set(numerator), big.NewInt(100))
	return new(big.Rat).SetFrac(scaled, denominator).FloatString(1)
}

// NamedCommandMetric is one labeled invocations/errors pair in a gain report.
type NamedCommandMetric struct {
	Name        string `json:"name"`
	Invocations uint64 `json:"invocations"`
	Errors      uint64 `json:"errors"`
	ErrorRate   string `json:"error_rate_percent"`
}

// NamedCount is one labeled counter in a gain report table.
type NamedCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// CommandReasonMetric attributes nonzero errors to the command that raised them.
type CommandReasonMetric struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
	Errors  uint64 `json:"errors"`
}

// GainMetrics is the durable aggregate exposed to hosts.
type GainMetrics struct {
	HPatchTokens            uint64 `json:"hpatch_tokens"`
	ApplyPatchTokens        uint64 `json:"apply_patch_tokens"`
	IneffectiveHPatchTokens uint64 `json:"ineffective_hpatch_tokens"`
	FailedApplyPatchTokens  uint64 `json:"failed_apply_patch_tokens"`
	SuccessfulReduction     string `json:"successful_reduction_percent"`
	OverallReduction        string `json:"overall_reduction_percent"`

	ReportInputTokens                       uint64 `json:"report_input_tokens"`
	DiagnosticInputTokens                   uint64 `json:"diagnostic_input_tokens"`
	MisuseWarningInputTokens                uint64 `json:"misuse_warning_input_tokens"`
	DefinitionInputTokens                   uint64 `json:"definition_input_tokens"`
	RemovedDefinitionInputTokens            uint64 `json:"removed_definition_input_tokens"`
	RemovedExecCommandDefinitionInputTokens uint64 `json:"removed_exec_command_definition_input_tokens"`

	// NetAddedInput is measured additions plus current-minus-stock tool results,
	// minus the removed definition credit. It is a decimal string.
	NetAddedInput      string `json:"net_added_input"`
	Sessions           uint64 `json:"sessions"`
	DefinitionRequests uint64 `json:"definition_requests"`
	DefinitionSources  string `json:"definition_sources"`

	Tools                  []ToolGainMetric           `json:"tools"`
	AllTools               ToolGainMetric             `json:"all_tools"`
	ToolInputs             []ToolInputGainMetric      `json:"tool_inputs"`
	AllToolInputs          ToolInputGainMetric        `json:"all_tool_inputs"`
	ToolDefinitions        []ToolDefinitionGainMetric `json:"tool_definitions"`
	SharedDefinitionTokens int64                      `json:"shared_definition_tokens"`
	Recoveries             []NamedCount               `json:"recoveries"`

	Commands       []NamedCommandMetric  `json:"commands"`
	Targets        []NamedCommandMetric  `json:"targets"`
	Reasons        []NamedCount          `json:"reasons"`
	CommandReasons []CommandReasonMetric `json:"command_reasons"`
}

// EmptyGainMetrics returns the zero aggregate used when no metrics file exists.
func EmptyGainMetrics() GainMetrics {
	return metrics{}.gainMetrics()
}

// LoadGainMetrics reads the durable metrics aggregate.
// Missing metrics yield a zero value with populated empty metric rows.
func LoadGainMetrics(dataDirectory string) (GainMetrics, error) {
	total, err := readMetrics(dataDirectory)
	if err != nil {
		return GainMetrics{}, err
	}
	return total.gainMetrics(), nil
}

func (m metrics) netAddedInput(allToolInputs ToolInputGainMetric) *big.Int {
	added := new(big.Int).SetUint64(m.ReportInputTokens)
	for _, count := range []uint64{m.DiagnosticInputTokens, m.MisuseWarningInputTokens, m.DefinitionInputTokens} {
		added.Add(added, new(big.Int).SetUint64(count))
	}
	removed := new(big.Int).SetUint64(m.RemovedDefinitionInputTokens)
	removed.Add(removed, new(big.Int).SetUint64(m.RemovedExecCommandDefinitionInputTokens))
	net := new(big.Int).Sub(added, removed)
	net.Add(net, new(big.Int).SetUint64(allToolInputs.CurrentTokens))
	net.Sub(net, new(big.Int).SetUint64(allToolInputs.StockTokens))
	return net
}

// gainMetrics projects the durable aggregate into structured host fields.
func (m metrics) gainMetrics() GainMetrics {
	tools, allTools, toolDefinitions := m.gainToolRows()
	toolInputs, allToolInputs := m.gainToolInputRows()
	net := m.netAddedInput(allToolInputs)

	recoveries := make([]NamedCount, 0, recoveryKindCount)
	for kind, name := range recoveryKindNames {
		recoveries = append(recoveries, NamedCount{Name: name, Count: m.Recoveries[kind]})
	}

	commands := make([]NamedCommandMetric, 0, commandCount)
	for index, name := range commandOperations {
		entry := m.Commands[index]
		commands = append(commands, NamedCommandMetric{
			Name:        name,
			Invocations: entry.Invocations,
			Errors:      entry.Errors,
			ErrorRate:   entry.errorRate(),
		})
	}
	targets := make([]NamedCommandMetric, 0, targetVariantCount)
	for index, name := range targetVariantNames {
		entry := m.Targets[index]
		targets = append(targets, NamedCommandMetric{
			Name:        name,
			Invocations: entry.Invocations,
			Errors:      entry.Errors,
			ErrorRate:   entry.errorRate(),
		})
	}
	reasons := make([]NamedCount, 0, failureReasonCount)
	for index, name := range failureReasonNames {
		reasons = append(reasons, NamedCount{Name: name, Count: m.Reasons[index]})
	}
	commandReasons := make([]CommandReasonMetric, 0)
	for command, commandReasonsRow := range m.CommandReasons {
		for reason, count := range commandReasonsRow {
			if count == 0 {
				continue
			}
			commandReasons = append(commandReasons, CommandReasonMetric{
				Command: commandOperations[command],
				Reason:  failureReasonNames[reason],
				Errors:  count,
			})
		}
	}
	if len(commandReasons) == 0 {
		commandReasons = []CommandReasonMetric{{Command: "none", Reason: "none", Errors: 0}}
	}

	return GainMetrics{
		HPatchTokens:                            m.HPatchTokens,
		ApplyPatchTokens:                        m.ApplyPatchTokens,
		IneffectiveHPatchTokens:                 m.IneffectiveHPatchTokens,
		FailedApplyPatchTokens:                  m.FailedApplyPatchTokens,
		SuccessfulReduction:                     m.reduction(),
		OverallReduction:                        m.overallReduction(),
		ReportInputTokens:                       m.ReportInputTokens,
		DiagnosticInputTokens:                   m.DiagnosticInputTokens,
		MisuseWarningInputTokens:                m.MisuseWarningInputTokens,
		DefinitionInputTokens:                   m.DefinitionInputTokens,
		RemovedDefinitionInputTokens:            m.RemovedDefinitionInputTokens,
		RemovedExecCommandDefinitionInputTokens: m.RemovedExecCommandDefinitionInputTokens,

		NetAddedInput:          net.String(),
		Sessions:               m.Sessions,
		DefinitionRequests:     m.DefinitionRequests,
		DefinitionSources:      describeDefinitionSources(m),
		Tools:                  tools,
		AllTools:               allTools,
		ToolInputs:             toolInputs,
		AllToolInputs:          allToolInputs,
		ToolDefinitions:        toolDefinitions,
		SharedDefinitionTokens: m.SharedDefinitionInputTokens,
		Recoveries:             recoveries,
		Commands:               commands,
		Targets:                targets,
		Reasons:                reasons,
		CommandReasons:         commandReasons,
	}
}
