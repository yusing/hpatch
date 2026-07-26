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
	"strings"
	"text/tabwriter"

	"github.com/gofrs/flock"
	"github.com/tiktoken-go/tokenizer"
)

const (
	metricsFilename       = "metrics.bin"
	metricsLockname       = "metrics.lock"
	metricsMagic          = "HPATCH08"
	metricsSlotSize       = 560
	metricsFileSize       = 2 * metricsSlotSize
	metricsChecksumOffset = 528
	commandCount          = 12
)

var commandOperations = [commandCount]string{
	"in", "new", "mv", "rm",
	"sel", "tsel", "bsel", "bsel_next", "rsel",
	"type", "del", "dup",
}

type commandMetric struct {
	Invocations uint64
	Errors      uint64
}

type commandMetrics [commandCount]commandMetric

type metrics struct {
	invocationMetrics

	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
	ReportInputTokens       uint64
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

func (m commandMetrics) total() (commandMetric, bool) {
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

func countMetrics(script, patch string) (metrics, error) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return metrics{}, fmt.Errorf("loading GPT-5 tokenizer: %w", err)
	}
	hpatchTokens, err := codec.Count(hpatchMetricPayload(script))
	if err != nil {
		return metrics{}, fmt.Errorf("tokenizing hpatch output: %w", err)
	}
	applyPatchTokens, err := codec.Count(applyPatchMetricPayload(patch))
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

func countReportInputTokens(report string) (uint64, error) {
	if report == "" {
		return 0, nil
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return 0, fmt.Errorf("loading GPT-5 tokenizer: %w", err)
	}
	count, err := codec.Count(report)
	if err != nil {
		return 0, fmt.Errorf("tokenizing state report input: %w", err)
	}
	return uint64(count), nil
}

func recordMetrics(dataDirectory, script, patch, emittedReport string, events invocationMetrics) error {
	entry, err := countMetrics(script, patch)
	if err != nil {
		return err
	}
	entry.ReportInputTokens, err = countReportInputTokens(emittedReport)
	if err != nil {
		return err
	}
	entry.invocationMetrics = events
	return updateMetrics(dataDirectory, entry)
}

func recordIneffectiveMetrics(dataDirectory, script string, events invocationMetrics) error {
	entry, err := countIneffectiveMetrics(script)
	if err != nil {
		return err
	}
	entry.invocationMetrics = events
	return updateMetrics(dataDirectory, entry)
}

func recordCommandMetrics(dataDirectory, emittedReport string, events invocationMetrics) error {
	if events == (invocationMetrics{}) && emittedReport == "" {
		return nil
	}
	reportTokens, err := countReportInputTokens(emittedReport)
	if err != nil {
		return err
	}
	entry := metrics{ReportInputTokens: reportTokens}
	entry.invocationMetrics = events
	return updateMetrics(dataDirectory, entry)
}

func updateMetrics(dataDirectory string, entry metrics) (err error) {
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
	for _, counter := range []struct {
		destination *uint64
		increment   uint64
	}{
		{&m.HPatchTokens, entry.HPatchTokens},
		{&m.ApplyPatchTokens, entry.ApplyPatchTokens},
		{&m.IneffectiveHPatchTokens, entry.IneffectiveHPatchTokens},
		{&m.ReportInputTokens, entry.ReportInputTokens},
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
	for index := range selectorVariantCount {
		if !addCommandMetric(&m.SelectorVariants[index], entry.SelectorVariants[index]) {
			return fmt.Errorf("updating metrics: selector variant count overflow")
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
	for _, entry := range events.SelectorVariants {
		if entry.Errors > entry.Invocations {
			return false
		}
	}
	for _, entry := range events.TextSpans {
		if entry.Errors > entry.Invocations {
			return false
		}
	}
	for _, operation := range []string{"sel", "tsel", "rsel"} {
		base := selectorVariantIndex(operation, coordinateAbsolute)
		combined, ok := sumCommandMetrics(events.SelectorVariants[base], events.SelectorVariants[base+1])
		if !ok || combined != events.Commands[commandOperationIndex(operation)] {
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
	return reasons == total.Errors
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
	for index, entry := range value.Commands {
		putCommandMetric(encoded[:], 48+index*16, entry)
	}
	for index, entry := range value.SelectorVariants {
		putCommandMetric(encoded[:], 240+index*16, entry)
	}
	for index, entry := range value.TextSpans {
		putCommandMetric(encoded[:], 336+index*16, entry)
	}
	for index, count := range value.BlockOutcomes {
		binary.LittleEndian.PutUint64(encoded[368+index*8:376+index*8], count)
	}
	for index, count := range value.Reasons {
		binary.LittleEndian.PutUint64(encoded[400+index*8:408+index*8], count)
	}
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
		HPatchTokens:            binary.LittleEndian.Uint64(encoded[16:24]),
		ApplyPatchTokens:        binary.LittleEndian.Uint64(encoded[24:32]),
		IneffectiveHPatchTokens: binary.LittleEndian.Uint64(encoded[32:40]),
		ReportInputTokens:       binary.LittleEndian.Uint64(encoded[40:48]),
	}
	for index := range commandCount {
		value.Commands[index] = getCommandMetric(encoded[:], 48+index*16)
	}
	for index := range selectorVariantCount {
		value.SelectorVariants[index] = getCommandMetric(encoded[:], 240+index*16)
	}
	for index := range textSpanVariantCount {
		value.TextSpans[index] = getCommandMetric(encoded[:], 336+index*16)
	}
	for index := range blockOutcomeCount {
		value.BlockOutcomes[index] = binary.LittleEndian.Uint64(encoded[368+index*8 : 376+index*8])
	}
	for index := range int(failureReasonCount) {
		value.Reasons[index] = binary.LittleEndian.Uint64(encoded[400+index*8 : 408+index*8])
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

func (m metrics) weightedOverallReduction(outputToInputRatio float64) float64 {
	if m.ApplyPatchTokens == 0 {
		return 0
	}
	weightedCost := float64(m.HPatchTokens) + float64(m.IneffectiveHPatchTokens) + float64(m.ReportInputTokens)/outputToInputRatio
	return (float64(m.ApplyPatchTokens) - weightedCost) / float64(m.ApplyPatchTokens) * 100
}

func gainReport(m metrics) string {
	totalHPatchTokens := new(big.Int).SetUint64(m.HPatchTokens)
	totalHPatchTokens.Add(totalHPatchTokens, new(big.Int).SetUint64(m.IneffectiveHPatchTokens))
	var report strings.Builder
	fmt.Fprintf(
		&report,
		"estimated effective hpatch output tokens: %d\nestimated apply_patch output tokens: %d\nestimated effective reduction: %.1f%%\nestimated ineffective hpatch output tokens: %d\nestimated total hpatch output tokens: %s\nestimated overall output-token reduction: %.1f%%\nestimated state-report input tokens: %d\nestimated weighted overall reduction at 5:1: %.1f%%\nestimated weighted overall reduction at 6:1: %.1f%%\n",
		m.HPatchTokens,
		m.ApplyPatchTokens,
		m.reduction(),
		m.IneffectiveHPatchTokens,
		totalHPatchTokens,
		m.overallReduction(),
		m.ReportInputTokens,
		m.weightedOverallReduction(5),
		m.weightedOverallReduction(6),
	)
	writeCommandTable(&report, "command metrics:", "command", commandOperations[:], m.Commands[:], true)
	writeCommandTable(&report, "selector coordinate metrics:", "selector", selectorVariantNames[:], m.SelectorVariants[:], false)
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
	return report.String()
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
}
