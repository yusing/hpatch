package hpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/gofrs/flock"
	"github.com/tiktoken-go/tokenizer"
)

const (
	metricsFilename       = "metrics.bin"
	metricsLockname       = "metrics.lock"
	metricsMagic          = "HPATCH06"
	metricsSlotSize       = 256
	metricsFileSize       = 2 * metricsSlotSize
	metricsChecksumOffset = 224
	commandCount          = 11
)

var commandOperations = [commandCount]string{
	"in", "new", "mv", "rm",
	"sel", "tsel", "bsel", "rsel",
	"type", "del", "dup",
}

type commandMetric struct {
	Invocations uint64
	Errors      uint64
}

type commandMetrics [commandCount]commandMetric

type metrics struct {
	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
	Commands                commandMetrics
}

func commandOperationIndex(operation string) int {
	for index, candidate := range commandOperations {
		if operation == candidate {
			return index
		}
	}
	return -1
}

func (m *commandMetrics) invoke(operation string) {
	if index := commandOperationIndex(operation); index >= 0 {
		m[index].Invocations++
	}
}

func (m *commandMetrics) fail(operation string) {
	if index := commandOperationIndex(operation); index >= 0 {
		m[index].Errors++
	}
}

func (m *commandMetrics) invokeFailure(operation string) {
	m.invoke(operation)
	m.fail(operation)
}

func (m commandMetrics) total() (commandMetric, bool) {
	var total commandMetric
	for _, entry := range m {
		if entry.Errors > entry.Invocations ||
			entry.Invocations > ^uint64(0)-total.Invocations ||
			entry.Errors > ^uint64(0)-total.Errors {
			return commandMetric{}, false
		}
		total.Invocations += entry.Invocations
		total.Errors += entry.Errors
	}
	return total, true
}

func (m commandMetric) errorRate() float64 {
	if m.Invocations == 0 {
		return 0
	}
	return float64(m.Errors) / float64(m.Invocations) * 100
}

func countMetrics(script, patch string) (metrics, error) {
	hpatchPayload, applyPatchPayload := metricPayloads(script, patch)
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return metrics{}, fmt.Errorf("loading GPT-5 tokenizer: %w", err)
	}
	hpatchTokens, err := codec.Count(hpatchPayload)
	if err != nil {
		return metrics{}, fmt.Errorf("tokenizing hpatch output: %w", err)
	}
	applyPatchTokens, err := codec.Count(applyPatchPayload)
	if err != nil {
		return metrics{}, fmt.Errorf("tokenizing apply_patch output: %w", err)
	}
	return metrics{HPatchTokens: uint64(hpatchTokens), ApplyPatchTokens: uint64(applyPatchTokens)}, nil
}

func countIneffectiveMetrics(script string) (metrics, error) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return metrics{}, fmt.Errorf("loading GPT-5 tokenizer: %w", err)
	}
	hpatchTokens, err := codec.Count(hpatchMetricPayload(script))
	if err != nil {
		return metrics{}, fmt.Errorf("tokenizing ineffective hpatch output: %w", err)
	}
	return metrics{IneffectiveHPatchTokens: uint64(hpatchTokens)}, nil
}

func recordMetrics(dataDirectory, script, patch string, commands commandMetrics) error {
	entry, err := countMetrics(script, patch)
	if err != nil {
		return err
	}
	entry.Commands = commands
	return updateMetrics(dataDirectory, entry)
}

func recordIneffectiveMetrics(dataDirectory, script string, commands commandMetrics) error {
	entry, err := countIneffectiveMetrics(script)
	if err != nil {
		return err
	}
	entry.Commands = commands
	return updateMetrics(dataDirectory, entry)
}

func recordCommandMetrics(dataDirectory string, commands commandMetrics) error {
	if commands == (commandMetrics{}) {
		return nil
	}
	return updateMetrics(dataDirectory, metrics{Commands: commands})
}

func updateMetrics(dataDirectory string, entry metrics) (err error) {
	if dataDirectory == "" {
		return fmt.Errorf("metrics directory is unavailable")
	}
	if _, ok := entry.Commands.total(); !ok {
		return fmt.Errorf("updating metrics: invalid command counters")
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
	if err := total.add(entry); err != nil {
		return err
	}
	if generation == ^uint64(0) {
		return fmt.Errorf("updating metrics: generation overflow")
	}
	nextGeneration := generation + 1
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
	if entry.HPatchTokens > ^uint64(0)-m.HPatchTokens || entry.ApplyPatchTokens > ^uint64(0)-m.ApplyPatchTokens || entry.IneffectiveHPatchTokens > ^uint64(0)-m.IneffectiveHPatchTokens {
		return fmt.Errorf("updating metrics: token count overflow")
	}
	for index := range commandCount {
		if entry.Commands[index].Invocations > ^uint64(0)-m.Commands[index].Invocations || entry.Commands[index].Errors > ^uint64(0)-m.Commands[index].Errors {
			return fmt.Errorf("updating metrics: command count overflow")
		}
	}
	m.HPatchTokens += entry.HPatchTokens
	m.ApplyPatchTokens += entry.ApplyPatchTokens
	m.IneffectiveHPatchTokens += entry.IneffectiveHPatchTokens
	for index := range commandCount {
		m.Commands[index].Invocations += entry.Commands[index].Invocations
		m.Commands[index].Errors += entry.Commands[index].Errors
	}
	if _, ok := m.Commands.total(); !ok {
		return fmt.Errorf("updating metrics: aggregate command count overflow")
	}
	return nil
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
	if !mismatchedVersion && info.Size() <= 128 {
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
	const priorSlotSize = 64
	for offset := int64(0); offset+priorSlotSize <= size; offset += priorSlotSize {
		var encoded [priorSlotSize]byte
		if _, err := file.ReadAt(encoded[:], offset); err != nil {
			return false, fmt.Errorf("reading metrics: %w", err)
		}
		if !bytes.HasPrefix(encoded[:8], []byte("HPATCH")) || string(encoded[:8]) == metricsMagic {
			continue
		}
		checksum := sha256.Sum256(encoded[:40])
		generation := binary.LittleEndian.Uint64(encoded[8:16])
		if generation != 0 && bytes.Equal(encoded[40:], checksum[:24]) {
			return true, nil
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
	for index, entry := range value.Commands {
		offset := 40 + index*16
		binary.LittleEndian.PutUint64(encoded[offset:offset+8], entry.Invocations)
		binary.LittleEndian.PutUint64(encoded[offset+8:offset+16], entry.Errors)
	}
	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	copy(encoded[metricsChecksumOffset:], checksum[:])
	return encoded
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
	value := metrics{HPatchTokens: binary.LittleEndian.Uint64(encoded[16:24]), ApplyPatchTokens: binary.LittleEndian.Uint64(encoded[24:32]), IneffectiveHPatchTokens: binary.LittleEndian.Uint64(encoded[32:40])}
	for index := range commandCount {
		offset := 40 + index*16
		value.Commands[index] = commandMetric{Invocations: binary.LittleEndian.Uint64(encoded[offset : offset+8]), Errors: binary.LittleEndian.Uint64(encoded[offset+8 : offset+16])}
	}
	if _, ok := value.Commands.total(); !ok {
		return metrics{}, 0, false
	}
	return value, generation, true
}

func (m metrics) reduction() float64 {
	if m.ApplyPatchTokens == 0 {
		return 0
	}
	return (float64(m.ApplyPatchTokens) - float64(m.HPatchTokens)) / float64(m.ApplyPatchTokens) * 100
}

func (m metrics) overallReduction() float64 {
	if m.ApplyPatchTokens == 0 {
		return 0
	}
	return (float64(m.ApplyPatchTokens) - float64(m.HPatchTokens) - float64(m.IneffectiveHPatchTokens)) / float64(m.ApplyPatchTokens) * 100
}

func gainReport(m metrics) string {
	var report strings.Builder
	fmt.Fprintf(&report, "estimated hpatch output tokens: %d\nestimated apply_patch output tokens: %d\nestimated reduction: %.1f%%\nestimated ineffective hpatch output tokens: %d\nestimated overall reduction: %.1f%%\ncommand metrics:\n", m.HPatchTokens, m.ApplyPatchTokens, m.reduction(), m.IneffectiveHPatchTokens, m.overallReduction())
	table := tabwriter.NewWriter(&report, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "command\tinvocations\terrors\terror rate")
	fmt.Fprintln(table, "-------\t-----------\t------\t----------")
	for index, operation := range commandOperations {
		entry := m.Commands[index]
		fmt.Fprintf(table, "%s\t%d\t%d\t%.1f%%\n", operation, entry.Invocations, entry.Errors, entry.errorRate())
	}
	total, _ := m.Commands.total()
	fmt.Fprintf(table, "total\t%d\t%d\t%.1f%%\n", total.Invocations, total.Errors, total.errorRate())
	_ = table.Flush()
	return report.String()
}
