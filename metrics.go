package hpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/gofrs/flock"
)

const (
	metricsFilename = "metrics.bin"
	metricsLockname = "metrics.lock"
	metricsMagic    = "HPATCH15"

	metricsSlotSize       = 2160
	metricsFileSize       = 2 * metricsSlotSize
	metricsChecksumOffset = 2128
	commandCount          = 12
)

var commandOperations = [commandCount]string{
	"in", "new", "mv", "rm",
	"sel", "tsel", "bsel", "bsel_next", "rsel",
	"type", "del", "dup",
}

type commandMetric struct {
	Invocations uint64 `json:"invocations"`
	Errors      uint64 `json:"errors"`
}

type commandMetrics [commandCount]commandMetric

type metrics struct {
	invocationMetrics

	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
	FailedApplyPatchTokens  uint64
	ReportInputTokens       uint64
	DiagnosticInputTokens   uint64

	// Sessions counts distinct agent sessions that carried the routed definition
	// change. DefinitionRequests counts every request carrying that context.
	Sessions           uint64
	DefinitionRequests uint64
	// DefinitionInputTokens is the cumulative once-per-session hpatch definition
	// added by the router. RemovedDefinitionInputTokens is the corresponding
	// once-per-session Code Mode apply_patch section removed by the router.
	DefinitionInputTokens        uint64
	RemovedDefinitionInputTokens uint64
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

func (m commandMetric) errorRate() float64 {
	if m.Invocations == 0 {
		return 0
	}
	return float64(m.Errors) / float64(m.Invocations) * 100
}

func updateMetrics(dataDirectory string, entry metrics) error {
	return updateMetricsForSession(dataDirectory, entry, "")
}

func updateMetricsForSession(dataDirectory string, entry metrics, session string) (err error) {
	if dataDirectory == "" {
		return fmt.Errorf("metrics directory is unavailable")
	}
	if !validInvocationMetrics(entry.invocationMetrics) {
		return fmt.Errorf("updating metrics: invalid command or feature counters")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return fmt.Errorf("creating metrics directory: %w", err)
	}
	lock := flock.New(filepath.Join(dataDirectory, metricsLockname))
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("locking metrics: %w", err)
	}
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
			entry.DefinitionInputTokens = 0
			entry.RemovedDefinitionInputTokens = 0
		}
	}
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
		{&m.Sessions, entry.Sessions},
		{&m.DefinitionRequests, entry.DefinitionRequests},
		{&m.DefinitionInputTokens, entry.DefinitionInputTokens},
		{&m.RemovedDefinitionInputTokens, entry.RemovedDefinitionInputTokens},
	} {
		if !addCounter(counter.destination, counter.increment) {
			return fmt.Errorf("updating metrics: token count overflow")
		}
	}
	for index := range commandCount {
		if !addCommandMetric(&m.Commands[index], entry.Commands[index]) {
			return fmt.Errorf("updating metrics: command count overflow")
		}
	}
	for index := range textSpanVariantCount {
		if !addCommandMetric(&m.TextSpans[index], entry.TextSpans[index]) {
			return fmt.Errorf("updating metrics: tsel span count overflow")
		}
	}
	for index := range blockOutcomeCount {
		if !addCounter(&m.BlockOutcomes[index], entry.BlockOutcomes[index]) {
			return fmt.Errorf("updating metrics: block outcome count overflow")
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
	if !validInvocationMetrics(m.invocationMetrics) {
		return fmt.Errorf("updating metrics: aggregate command or feature counters are inconsistent")
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
	for _, entry := range events.TextSpans {
		if entry.Errors > entry.Invocations {
			return false
		}
	}
	spans, ok := sumCommandMetrics(events.TextSpans[0], events.TextSpans[1])
	if !ok || spans != events.Commands[commandOperationIndex("tsel")] {
		return false
	}
	for _, operation := range []string{"bsel", "bsel_next"} {
		base := blockOutcomeIndex(operation, false)
		successes, ok := sumCounters(events.BlockOutcomes[base], events.BlockOutcomes[base+1])
		command := events.Commands[commandOperationIndex(operation)]
		if !ok || command.Errors > command.Invocations || successes != command.Invocations-command.Errors {
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

func sumCommandMetrics(first, second commandMetric) (commandMetric, bool) {
	result := first
	if !addCommandMetric(&result, second) || result.Errors > result.Invocations {
		return commandMetric{}, false
	}
	return result, true
}

func sumCounters(first, second uint64) (uint64, bool) {
	if second > ^uint64(0)-first {
		return 0, false
	}
	return first + second, true
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
	lock := flock.New(filepath.Join(dataDirectory, metricsLockname))
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
		{slotSize: 264, checksumOffset: 232, checksumSize: 32},
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
	for index, entry := range value.Commands {
		putCommandMetric(encoded[:], 96+index*16, entry)
	}
	for index, entry := range value.TextSpans {
		putCommandMetric(encoded[:], 384+index*16, entry)
	}
	for index, count := range value.BlockOutcomes {
		binary.LittleEndian.PutUint64(encoded[416+index*8:424+index*8], count)
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
	binary.LittleEndian.PutUint64(encoded[2112:2120], value.DiagnosticInputTokens)
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
		HPatchTokens:                 binary.LittleEndian.Uint64(encoded[16:24]),
		ApplyPatchTokens:             binary.LittleEndian.Uint64(encoded[24:32]),
		IneffectiveHPatchTokens:      binary.LittleEndian.Uint64(encoded[32:40]),
		ReportInputTokens:            binary.LittleEndian.Uint64(encoded[40:48]),
		Sessions:                     binary.LittleEndian.Uint64(encoded[48:56]),
		DefinitionInputTokens:        binary.LittleEndian.Uint64(encoded[56:64]),
		RemovedDefinitionInputTokens: binary.LittleEndian.Uint64(encoded[64:72]),
		FailedApplyPatchTokens:       binary.LittleEndian.Uint64(encoded[72:80]),
		DefinitionRequests:           binary.LittleEndian.Uint64(encoded[80:88]),
		DiagnosticInputTokens:        binary.LittleEndian.Uint64(encoded[2112:2120]),
	}
	for index := range commandCount {
		value.Commands[index] = getCommandMetric(encoded[:], 96+index*16)
	}
	for index := range textSpanVariantCount {
		value.TextSpans[index] = getCommandMetric(encoded[:], 384+index*16)
	}
	for index := range blockOutcomeCount {
		value.BlockOutcomes[index] = binary.LittleEndian.Uint64(encoded[416+index*8 : 424+index*8])
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
	if !validInvocationMetrics(value.invocationMetrics) {
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

func (m *metrics) reduction() float64 {
	if m.ApplyPatchTokens == 0 {
		return 0
	}
	return (float64(m.ApplyPatchTokens) - float64(m.HPatchTokens)) / float64(m.ApplyPatchTokens) * 100
}

// overallReduction compares all measured hpatch output with the generated
// apply_patch output. Failed hpatch calls are represented by the empty
// apply_patch carrier emitted by the router.
func (m *metrics) overallReduction() float64 {
	baseline := float64(m.ApplyPatchTokens) + float64(m.FailedApplyPatchTokens)
	if baseline == 0 {
		return 0
	}
	return (float64(m.ApplyPatchTokens) + float64(m.FailedApplyPatchTokens) - float64(m.HPatchTokens) - float64(m.IneffectiveHPatchTokens)) / baseline * 100
}

const defaultGainReportWidth = 80

func gainReportAtWidth(m metrics, width int) string {
	var report strings.Builder
	writeOutputGainTable(&report, m)
	writeInputGainTable(&report, m, width)

	writeCommandTable(&report, "command metrics:", "command", commandOperations[:], m.Commands[:], true)
	writeCommandTable(&report, "tsel span metrics:", "span", textSpanVariantNames[:], m.TextSpans[:], false)

	report.WriteString("block selector successes:\n")
	table := tabwriter.NewWriter(&report, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "selector\tmatch\tsuccesses")
	fmt.Fprintln(table, "--------\t-----\t---------")
	for index, name := range blockOutcomeNames {
		operation, match, _ := strings.Cut(name, " ")
		fmt.Fprintf(table, "%s\t%s\t%d\n", operation, match, m.BlockOutcomes[index])
	}
	_ = table.Flush()
	report.WriteByte('\n')

	report.WriteString("failure reasons:\n")
	table = tabwriter.NewWriter(&report, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "reason\terrors")
	fmt.Fprintln(table, "------\t------")
	var totalReasons uint64
	for index, name := range failureReasonNames {
		fmt.Fprintf(table, "%s\t%d\n", name, m.Reasons[index])
		totalReasons += m.Reasons[index]
	}
	fmt.Fprintf(table, "total\t%d\n", totalReasons)
	_ = table.Flush()
	report.WriteByte('\n')

	writeCommandReasonTable(&report, m.CommandReasons)
	return report.String()
}

func writeOutputGainTable(report *strings.Builder, m metrics) {
	totalHPatch := new(big.Int).SetUint64(m.HPatchTokens)
	totalHPatch.Add(totalHPatch, new(big.Int).SetUint64(m.IneffectiveHPatchTokens))
	totalApplyPatch := new(big.Int).SetUint64(m.ApplyPatchTokens)
	totalApplyPatch.Add(totalApplyPatch, new(big.Int).SetUint64(m.FailedApplyPatchTokens))

	report.WriteString("output token estimates:\n")
	table := tabwriter.NewWriter(report, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "calls\thpatch\tapply_patch\treduction")
	fmt.Fprintln(table, "-----\t------\t-----------\t---------")
	fmt.Fprintf(table, "successful\t%d\t%d\t%.1f%%\n", m.HPatchTokens, m.ApplyPatchTokens, m.reduction())
	fmt.Fprintf(table, "failed\t%d\t%d\tn/a\n", m.IneffectiveHPatchTokens, m.FailedApplyPatchTokens)
	fmt.Fprintf(table, "all\t%s\t%s\t%.1f%%\n", totalHPatch, totalApplyPatch, m.overallReduction())
	_ = table.Flush()
	report.WriteString("failed apply_patch output is the empty carrier emitted by the router.\n\n")
}

func writeInputGainTable(report *strings.Builder, m metrics, width int) {
	added := new(big.Int).SetUint64(m.ReportInputTokens)
	for _, count := range []uint64{m.DiagnosticInputTokens, m.DefinitionInputTokens} {
		added.Add(added, new(big.Int).SetUint64(count))
	}
	removed := new(big.Int).SetUint64(m.RemovedDefinitionInputTokens)
	net := new(big.Int).Sub(new(big.Int).Set(added), removed)

	removedText := "0"
	if m.RemovedDefinitionInputTokens != 0 {
		removedText = "-" + strconv.FormatUint(m.RemovedDefinitionInputTokens, 10)
	}
	report.WriteString("input token estimates:\n")
	writeWrappedTable(report, width, []string{"source", "tokens", "description"}, [][]string{
		{"state reports", strconv.FormatUint(m.ReportInputTokens, 10), "final state returned after successful calls"},
		{"failure diagnostics", strconv.FormatUint(m.DiagnosticInputTokens, 10), "errors and repair context returned after failed calls"},
		{"hpatch definition installed", strconv.FormatUint(m.DefinitionInputTokens, 10), "standalone tool definition added by the router"},
		{"apply_patch definition removed", removedText, "exact Code Mode section removed by the router"},
		{"net added input", net.String(), "measured additions minus the removed definition"},
	})
	writeWrappedText(report, width, fmt.Sprintf("definition routing covers %d accounted request(s) in %d distinct session(s) (%s).", m.DefinitionRequests, m.Sessions, describeDefinitionSources(m)))
	report.WriteByte('\n')
}

const gainTableGap = 2

func writeWrappedTable(report *strings.Builder, width int, headers []string, rows [][]string) {
	widths := gainTableWidths(width, headers, rows)
	writeWrappedRow(report, headers, widths)
	separator := make([]string, len(widths))
	for index, columnWidth := range widths {
		separator[index] = strings.Repeat("-", columnWidth)
	}
	writeWrappedRow(report, separator, widths)
	for _, row := range rows {
		writeWrappedRow(report, row, widths)
	}
}

func gainTableWidths(width int, headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = utf8.RuneCountInString(header)
	}
	for _, row := range rows {
		for index, cell := range row {
			widths[index] = max(widths[index], utf8.RuneCountInString(cell))
		}
	}

	available := max(len(widths), width-gainTableGap*(len(widths)-1))
	fixed := 0
	for _, columnWidth := range widths[:len(widths)-1] {
		fixed += columnWidth
	}
	widths[len(widths)-1] = max(1, available-fixed)
	for sum(widths) > available {
		widest := 0
		for index := range len(widths) - 1 {
			if widths[index] > widths[widest] {
				widest = index
			}
		}
		if widths[widest] == 1 {
			break
		}
		widths[widest]--
	}
	return widths
}

func sum(values []int) int {
	var total int
	for _, value := range values {
		total += value
	}
	return total
}

func writeWrappedRow(report *strings.Builder, cells []string, widths []int) {
	wrapped := make([][]string, len(cells))
	height := 1
	for index, cell := range cells {
		wrapped[index] = wrapCell(cell, widths[index])
		height = max(height, len(wrapped[index]))
	}
	for line := range height {
		for column, columnWidth := range widths {
			var cell string
			if line < len(wrapped[column]) {
				cell = wrapped[column][line]
			}
			report.WriteString(cell)
			if column < len(widths)-1 {
				report.WriteString(strings.Repeat(" ", columnWidth-utf8.RuneCountInString(cell)+gainTableGap))
			}
		}
		report.WriteByte('\n')
	}
}

func wrapCell(value string, width int) []string {
	var lines []string
	for word := range strings.FieldsSeq(value) {
		wordRunes := []rune(word)
		if len(lines) > 0 && utf8.RuneCountInString(lines[len(lines)-1])+1+len(wordRunes) <= width {
			lines[len(lines)-1] += " " + word
			continue
		}
		for len(wordRunes) > width {
			lines = append(lines, string(wordRunes[:width]))
			wordRunes = wordRunes[width:]
		}
		if len(wordRunes) > 0 {
			lines = append(lines, string(wordRunes))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func writeWrappedText(report *strings.Builder, width int, value string) {
	for _, line := range wrapCell(value, max(1, width)) {
		report.WriteString(line)
		report.WriteByte('\n')
	}
}

// writeCommandReasonTable attributes each error to the command that raised it.
// Only nonzero pairs appear, because the full 12-by-16 grid is mostly empty and
// the useful reading is which primitive fails and how.
func writeCommandReasonTable(report *strings.Builder, commandReasons [commandCount][failureReasonCount]uint64) {
	report.WriteString("command failure reasons:\n")
	table := tabwriter.NewWriter(report, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "command\treason\terrors")
	fmt.Fprintln(table, "-------\t------\t------")
	var rows int
	for command, reasons := range commandReasons {
		for reason, count := range reasons {
			if count == 0 {
				continue
			}
			fmt.Fprintf(table, "%s\t%s\t%d\n", commandOperations[command], failureReasonNames[reason], count)
			rows++
		}
	}
	if rows == 0 {
		fmt.Fprintln(table, "none\tnone\t0") //nolint:dupword // Both columns intentionally report the empty state.
	}
	_ = table.Flush()
}

func writeCommandTable(report *strings.Builder, title, firstHeader string, names []string, values []commandMetric, includeTotal bool) {
	report.WriteString(title + "\n")
	table := tabwriter.NewWriter(report, 0, 4, 2, ' ', 0)
	fmt.Fprintf(table, "%s\tinvocations\terrors\terror rate\n", firstHeader)
	fmt.Fprintln(table, "-------\t-----------\t------\t----------")
	var total commandMetric
	for index, name := range names {
		entry := values[index]
		fmt.Fprintf(table, "%s\t%d\t%d\t%.1f%%\n", name, entry.Invocations, entry.Errors, entry.errorRate())
		if includeTotal {
			total.Invocations += entry.Invocations
			total.Errors += entry.Errors
		}
	}
	if includeTotal {
		fmt.Fprintf(table, "total\t%d\t%d\t%.1f%%\n", total.Invocations, total.Errors, total.errorRate())
	}
	_ = table.Flush()
	report.WriteByte('\n')
}
