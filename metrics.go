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

	"github.com/gofrs/flock"
	"github.com/tiktoken-go/tokenizer"
)

const (
	metricsFilename      = "metrics.bin"
	metricsLockname      = "metrics.lock"
	legacyMetricsMagic   = "HPATCH01"
	wrapperMetricsMagic  = "HPATCH02"
	previousMetricsMagic = "HPATCH03"
	metricsMagic         = "HPATCH04"
	metricsSlotSize      = 64
	metricsFileSize      = 2 * metricsSlotSize
)

type metrics struct {
	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
}

func countMetrics(workingDirectory, script, patch string) (metrics, error) {
	hpatchPayload, applyPatchPayload := metricPayloads(workingDirectory, script, patch)
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

func countIneffectiveMetrics(workingDirectory, script string) (metrics, error) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return metrics{}, fmt.Errorf("loading GPT-5 tokenizer: %w", err)
	}
	hpatchTokens, err := codec.Count(hpatchMetricPayload(workingDirectory, script))
	if err != nil {
		return metrics{}, fmt.Errorf("tokenizing ineffective hpatch output: %w", err)
	}
	return metrics{IneffectiveHPatchTokens: uint64(hpatchTokens)}, nil
}

func recordMetrics(dataDirectory, workingDirectory, script, patch string) error {
	entry, err := countMetrics(workingDirectory, script, patch)
	if err != nil {
		return err
	}
	return updateMetrics(dataDirectory, entry)
}

func recordIneffectiveMetrics(dataDirectory, workingDirectory, script string) error {
	entry, err := countIneffectiveMetrics(workingDirectory, script)
	if err != nil {
		return err
	}
	return updateMetrics(dataDirectory, entry)
}

func updateMetrics(dataDirectory string, entry metrics) (err error) {
	if dataDirectory == "" {
		return fmt.Errorf("metrics directory is unavailable")
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
	if entry.HPatchTokens > ^uint64(0)-total.HPatchTokens ||
		entry.ApplyPatchTokens > ^uint64(0)-total.ApplyPatchTokens ||
		entry.IneffectiveHPatchTokens > ^uint64(0)-total.IneffectiveHPatchTokens {
		return fmt.Errorf("updating metrics: token count overflow")
	}
	if generation == ^uint64(0) {
		return fmt.Errorf("updating metrics: generation overflow")
	}
	total.HPatchTokens += entry.HPatchTokens
	total.ApplyPatchTokens += entry.ApplyPatchTokens
	total.IneffectiveHPatchTokens += entry.IneffectiveHPatchTokens
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

	var (
		latest                 metrics
		latestGeneration       uint64
		latestLegacyGeneration uint64
		valid                  bool
		legacyValid            bool
		unknownMagic           string
	)
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
		if bytes.HasPrefix(encoded[:8], []byte("HPATCH")) &&
			magic != metricsMagic && magic != previousMetricsMagic &&
			magic != legacyMetricsMagic && magic != wrapperMetricsMagic {
			generation := binary.LittleEndian.Uint64(encoded[8:16])
			checksum := sha256.Sum256(encoded[:40])
			if generation != 0 && bytes.Equal(encoded[40:], checksum[:24]) {
				unknownMagic = magic
			}
			continue
		}

		candidate, generation, ok := decodeMetricsSlot(encoded)
		if ok && (!valid || generation > latestGeneration) {
			latest = candidate
			latestGeneration = generation
			valid = true
		}
		candidate, generation, ok = decodePreviousMetricsSlot(encoded)
		if ok && (!valid || generation > latestGeneration) {
			latest = candidate
			latestGeneration = generation
			valid = true
		}
		for _, magic := range []string{legacyMetricsMagic, wrapperMetricsMagic} {
			_, generation, ok := decodeTwoCounterMetricsSlot(encoded, magic)
			if ok && (!legacyValid || generation > latestLegacyGeneration) {
				latestLegacyGeneration = generation
				legacyValid = true
			}
		}
	}
	if unknownMagic != "" {
		return metrics{}, 0, fmt.Errorf("reading metrics: unknown counter format %q", unknownMagic)
	}
	if valid {
		return latest, latestGeneration, nil
	}
	if legacyValid {
		return metrics{}, latestLegacyGeneration, nil
	}
	return metrics{}, 0, fmt.Errorf("reading metrics: no valid counter slot")
}

func encodeMetricsSlot(value metrics, generation uint64) [metricsSlotSize]byte {
	var encoded [metricsSlotSize]byte
	copy(encoded[:8], metricsMagic)
	binary.LittleEndian.PutUint64(encoded[8:16], generation)
	binary.LittleEndian.PutUint64(encoded[16:24], value.HPatchTokens)
	binary.LittleEndian.PutUint64(encoded[24:32], value.ApplyPatchTokens)
	binary.LittleEndian.PutUint64(encoded[32:40], value.IneffectiveHPatchTokens)
	checksum := sha256.Sum256(encoded[:40])
	copy(encoded[40:], checksum[:24])
	return encoded
}

func decodeMetricsSlot(encoded [metricsSlotSize]byte) (metrics, uint64, bool) {
	if !bytes.Equal(encoded[:8], []byte(metricsMagic)) {
		return metrics{}, 0, false
	}
	checksum := sha256.Sum256(encoded[:40])
	if !bytes.Equal(encoded[40:], checksum[:24]) {
		return metrics{}, 0, false
	}
	generation := binary.LittleEndian.Uint64(encoded[8:16])
	if generation == 0 {
		return metrics{}, 0, false
	}
	return metrics{
		HPatchTokens:            binary.LittleEndian.Uint64(encoded[16:24]),
		ApplyPatchTokens:        binary.LittleEndian.Uint64(encoded[24:32]),
		IneffectiveHPatchTokens: binary.LittleEndian.Uint64(encoded[32:40]),
	}, generation, true
}

func decodePreviousMetricsSlot(encoded [metricsSlotSize]byte) (metrics, uint64, bool) {
	return decodeTwoCounterMetricsSlot(encoded, previousMetricsMagic)
}

func decodeTwoCounterMetricsSlot(encoded [metricsSlotSize]byte, magic string) (metrics, uint64, bool) {
	if !bytes.Equal(encoded[:8], []byte(magic)) {
		return metrics{}, 0, false
	}
	checksum := sha256.Sum256(encoded[:32])
	if !bytes.Equal(encoded[32:], checksum[:]) {
		return metrics{}, 0, false
	}
	generation := binary.LittleEndian.Uint64(encoded[8:16])
	if generation == 0 {
		return metrics{}, 0, false
	}
	return metrics{
		HPatchTokens:     binary.LittleEndian.Uint64(encoded[16:24]),
		ApplyPatchTokens: binary.LittleEndian.Uint64(encoded[24:32]),
	}, generation, true
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
