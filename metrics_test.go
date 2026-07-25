package hpatch

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestGainReportsPersistedTotals(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	patch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"
	entry, err := countMetrics(root, script, patch)
	if err != nil {
		t.Fatal(err)
	}

	var translateStdout, translateStderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), &translateStdout, &translateStderr, root, dataDirectory)
	if exitCode != 0 || translateStdout.String() != patch || translateStderr.Len() != 0 {
		t.Fatalf("translate = exit %d, stdout %q, stderr %q", exitCode, translateStdout.String(), translateStderr.String())
	}

	var normalStdout, normalStderr bytes.Buffer
	exitCode = Run(nil, strings.NewReader(script), &normalStdout, &normalStderr, root, dataDirectory)
	if exitCode != 0 || normalStdout.Len() != 0 || normalStderr.Len() != 0 {
		t.Fatalf("normal = exit %d, stdout %q, stderr %q", exitCode, normalStdout.String(), normalStderr.String())
	}

	var stdout, stderr bytes.Buffer
	exitCode = Run([]string{"gain"}, strings.NewReader("not a script"), &stdout, &stderr, root, dataDirectory)
	want := fmt.Sprintf(
		"estimated hpatch output tokens: %d\nestimated apply_patch output tokens: %d\nestimated reduction: %.1f%%\n",
		entry.HPatchTokens*2,
		entry.ApplyPatchTokens*2,
		entry.reduction(),
	)
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout.String(), stderr.String(), want)
	}
}

func TestGainWithoutMetricsReportsZero(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "absent")
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr, t.TempDir(), dataDirectory)
	want := "estimated hpatch output tokens: 0\nestimated apply_patch output tokens: 0\nestimated reduction: 0.0%\n"
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout.String(), stderr.String(), want)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("gain created an empty metrics directory: %v", err)
	}
}

func TestMetricsPersistenceFailureWarnsWithoutPreventingMutation(t *testing.T) {
	root := t.TempDir()
	dataPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataPath, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("new note.txt\ntype \"hello\"\n"), &stdout, &stderr, root, dataPath)
	if exitCode != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "warning: creating metrics directory") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "hello" {
		t.Fatalf("note.txt = %q, error %v", content, err)
	}
}

func TestMetricsPersistenceFailureWarnsWithoutPreventingTranslation(t *testing.T) {
	root := t.TempDir()
	dataPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataPath, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "new note.txt\ntype \"hello\"\n"
	wantPatch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"

	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataPath)
	if exitCode != 0 || stdout.String() != wantPatch || !strings.Contains(stderr.String(), "warning: creating metrics directory") {
		t.Fatalf("translate = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("translate created note.txt: %v", err)
	}
}

func TestGainRejectsCorruptMetrics(t *testing.T) {
	badChecksum := encodeMetricsSlot(metrics{HPatchTokens: 5, ApplyPatchTokens: 9}, 1)
	badChecksum[20] ^= 0xff
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{name: "short slot", content: []byte("corrupt")},
		{name: "bad checksum", content: badChecksum[:]},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDirectory := t.TempDir()
			if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), test.content, 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			exitCode := Run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr, t.TempDir(), dataDirectory)
			if exitCode == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "reading metrics") {
				t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestMetricsRecoversPreviousSlotAfterTornWrite(t *testing.T) {
	dataDirectory := t.TempDir()
	want := metrics{HPatchTokens: 5, ApplyPatchTokens: 9}
	if err := updateMetrics(dataDirectory, want); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dataDirectory, metricsFilename), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt(bytes.Repeat([]byte{0xff}, metricsSlotSize/2), metricsSlotSize); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics = %+v, want %+v", got, want)
	}
}

func TestMetricsFileSizeIsBounded(t *testing.T) {
	dataDirectory := t.TempDir()
	for range 100 {
		if err := updateMetrics(dataDirectory, metrics{HPatchTokens: 1, ApplyPatchTokens: 2}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(filepath.Join(dataDirectory, metricsFilename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != metricsFileSize {
		t.Fatalf("metrics file size = %d, want %d", info.Size(), metricsFileSize)
	}
}

func TestMetricsConcurrentReadersAndWriters(t *testing.T) {
	dataDirectory := t.TempDir()
	const (
		writers         = 24
		writesPerWriter = 20
		readers         = 8
	)
	entry := metrics{HPatchTokens: 3, ApplyPatchTokens: 7}

	start := make(chan struct{})
	errors := make(chan error, writers+readers)
	var writersDone sync.WaitGroup
	writersDone.Add(writers)
	for range writers {
		go func() {
			defer writersDone.Done()
			<-start
			for range writesPerWriter {
				if err := updateMetrics(dataDirectory, entry); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	writersFinished := make(chan struct{})
	go func() {
		writersDone.Wait()
		close(writersFinished)
	}()

	var readersDone sync.WaitGroup
	readersDone.Add(readers)
	for range readers {
		go func() {
			defer readersDone.Done()
			<-start
			for {
				if _, err := readMetrics(dataDirectory); err != nil {
					errors <- err
					return
				}
				select {
				case <-writersFinished:
					return
				default:
				}
			}
		}()
	}

	close(start)
	writersDone.Wait()
	readersDone.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := uint64(writers * writesPerWriter)
	want := metrics{HPatchTokens: wantWrites * entry.HPatchTokens, ApplyPatchTokens: wantWrites * entry.ApplyPatchTokens}
	if got != want {
		t.Fatalf("metrics = %+v, want %+v", got, want)
	}
}

func TestMetricsConcurrentProcesses(t *testing.T) {
	dataDirectory := t.TempDir()
	const (
		processes        = 12
		writesPerProcess = 15
	)

	commands := make([]*exec.Cmd, 0, processes)
	for range processes {
		command := exec.Command(os.Args[0], "-test.run=^TestMetricsProcessHelper$")
		command.Env = append(os.Environ(),
			"HPATCH_METRICS_HELPER=1",
			"HPATCH_METRICS_DIRECTORY="+dataDirectory,
			fmt.Sprintf("HPATCH_METRICS_WRITES=%d", writesPerProcess),
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Errorf("metrics process failed: %v", err)
		}
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := uint64(processes * writesPerProcess)
	want := metrics{HPatchTokens: wantWrites * 2, ApplyPatchTokens: wantWrites * 5}
	if got != want {
		t.Fatalf("metrics = %+v, want %+v", got, want)
	}
}

func TestMetricsProcessHelper(t *testing.T) {
	if os.Getenv("HPATCH_METRICS_HELPER") != "1" {
		return
	}
	writes, err := strconv.Atoi(os.Getenv("HPATCH_METRICS_WRITES"))
	if err != nil {
		t.Fatal(err)
	}
	for range writes {
		if err := updateMetrics(os.Getenv("HPATCH_METRICS_DIRECTORY"), metrics{HPatchTokens: 2, ApplyPatchTokens: 5}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyMetricsAreResetBeforeNewEstimates(t *testing.T) {
	dataDirectory := t.TempDir()
	legacy := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5, ApplyPatchTokens: 9}, 7), legacyMetricsMagic)
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), legacy[:], 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != (metrics{}) {
		t.Fatalf("legacy metrics were mixed into estimates: %+v", got)
	}

	want := metrics{HPatchTokens: 11, ApplyPatchTokens: 13}
	if err := updateMetrics(dataDirectory, want); err != nil {
		t.Fatal(err)
	}
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics after legacy reset = %+v, want %+v", got, want)
	}
}

func TestMetricsRejectUnknownFutureFormat(t *testing.T) {
	dataDirectory := t.TempDir()
	future := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5, ApplyPatchTokens: 9}, 1), "HPATCH99")
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), future[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetrics(dataDirectory); err == nil || !strings.Contains(err.Error(), "no valid counter slot") {
		t.Fatalf("readMetrics() error = %v, want unknown-format failure", err)
	}
}

func rewriteMetricsMagic(encoded [metricsSlotSize]byte, magic string) [metricsSlotSize]byte {
	copy(encoded[:8], magic)
	checksum := sha256.Sum256(encoded[:32])
	copy(encoded[32:], checksum[:])
	return encoded
}
