package hpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
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
	entry, err := countMetrics(script, patch)
	if err != nil {
		t.Fatal(err)
	}
	reportTokens, err := countReportInputTokens("in note.txt 1:6\n1 hello\n")
	if err != nil {
		t.Fatal(err)
	}

	var translateStdout, translateStderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), &translateStdout, &translateStderr, root, dataDirectory)
	if exitCode != 0 || translateStdout.String() != patch || translateStderr.String() != "in note.txt 1:6\n1 hello\n" {
		t.Fatalf("translate = exit %d, stdout %q, stderr %q", exitCode, translateStdout.String(), translateStderr.String())
	}

	var normalStdout, normalStderr bytes.Buffer
	exitCode = Run(nil, strings.NewReader(script), &normalStdout, &normalStderr, root, dataDirectory)
	if exitCode != 0 || normalStdout.Len() != 0 || normalStderr.String() != "in note.txt 1:6\n1 hello\n" {
		t.Fatalf("normal = exit %d, stdout %q, stderr %q", exitCode, normalStdout.String(), normalStderr.String())
	}

	var stdout, stderr bytes.Buffer
	exitCode = Run([]string{"gain"}, strings.NewReader("not a script"), &stdout, &stderr, root, dataDirectory)
	wantMetrics := metrics{HPatchTokens: entry.HPatchTokens * 2, ApplyPatchTokens: entry.ApplyPatchTokens * 2, ReportInputTokens: reportTokens * 2}
	wantMetrics.Commands[commandOperationIndex("new")].Invocations = 2
	wantMetrics.Commands[commandOperationIndex("type")].Invocations = 2
	want := gainReport(wantMetrics)
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout.String(), stderr.String(), want)
	}
}

func TestGainWithoutMetricsReportsZero(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "absent")
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr, t.TempDir(), dataDirectory)
	want := gainReport(metrics{})
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout.String(), stderr.String(), want)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("gain created an empty metrics directory: %v", err)
	}
}

func TestGainReportReconcilesEffectiveAndIneffectiveTokens(t *testing.T) {
	report := gainReport(metrics{
		HPatchTokens:            2404,
		ApplyPatchTokens:        4764,
		IneffectiveHPatchTokens: 2172,
	})
	want := "estimated effective hpatch output tokens: 2404\n" +
		"estimated apply_patch output tokens: 4764\n" +
		"estimated effective reduction: 49.5%\n" +
		"estimated ineffective hpatch output tokens: 2172\n" +
		"estimated total hpatch output tokens: 4576\n" +
		"estimated credited baseline retry output tokens: 0 (0 of 0 failures)\n" +
		"estimated baseline output tokens including retries: 4764\n" +
		"estimated overall output-token reduction: 3.9%\n" +
		"estimated state-report input tokens: 0\n" +
		"estimated tool-definition input tokens: 0 hpatch, 0 baseline, 0 net over 0 session(s) " +
		"(not measured; host set no HPATCH_SESSION_ID)\n" +
		"estimated weighted overall reduction at 5:1: 3.9%\n" +
		"estimated weighted overall reduction at 6:1: 3.9%\n"
	if !strings.HasPrefix(report, want) {
		t.Fatalf("gain report = %q, want prefix %q", report, want)
	}

	overflowSafe := gainReport(metrics{HPatchTokens: ^uint64(0), IneffectiveHPatchTokens: ^uint64(0)})
	if !strings.Contains(overflowSafe, "estimated total hpatch output tokens: 36893488147419103230\n") {
		t.Fatalf("overflow-safe gain report = %q", overflowSafe)
	}
}

func TestGainReportsCommandInvocationsErrorsAndRates(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	tests := []struct {
		name    string
		args    []string
		script  string
		success bool
	}{
		{name: "success with unrelated command-name text", args: []string{"translate"}, script: "new note.txt\ntype \"bsel sel future-command\"\n", success: true},
		{name: "execution error", args: []string{"translate"}, script: "new failed.txt\ntype \"ignored\"\nbsel \"missing\" \"end\"\n"},
		{name: "bsel_next execution error", args: []string{"translate"}, script: "new failed-next.txt\ntype \"ignored\"\nbsel_next \"missing\" \"end\"\n"},
		{name: "malformed known command", args: []string{"translate"}, script: "sel +x 1:1\n"},
		{name: "unknown future command", args: []string{"translate"}, script: "future-command\n"},
		{name: "successful no-op", script: "new transient.txt\nrm\n", success: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := Run(test.args, strings.NewReader(test.script), &stdout, &stderr, root, dataDirectory)
			if (exitCode == 0) != test.success {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := commandMetrics{}
	wantCommands[commandOperationIndex("new")] = commandMetric{Invocations: 4}
	wantCommands[commandOperationIndex("rm")] = commandMetric{Invocations: 1}
	wantCommands[commandOperationIndex("sel")] = commandMetric{Invocations: 1, Errors: 1}
	wantCommands[commandOperationIndex("bsel")] = commandMetric{Invocations: 1, Errors: 1}
	wantCommands[commandOperationIndex("bsel_next")] = commandMetric{Invocations: 1, Errors: 1}
	wantCommands[commandOperationIndex("type")] = commandMetric{Invocations: 3}
	if got.Commands != wantCommands {
		t.Fatalf("command metrics = %+v, want %+v", got.Commands, wantCommands)
	}

	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr, root, dataDirectory); exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	start := strings.Index(stdout.String(), "command metrics:\n")
	if start < 0 {
		t.Fatalf("gain report has no command metrics: %q", stdout.String())
	}
	want := "command metrics:\n" +
		"command    invocations  errors  error rate\n" +
		"-------    -----------  ------  ----------\n" +
		"in         0            0       0.0%\n" +
		"new        4            0       0.0%\n" +
		"mv         0            0       0.0%\n" +
		"rm         1            0       0.0%\n" +
		"sel        1            1       100.0%\n" +
		"tsel       0            0       0.0%\n" +
		"bsel       1            1       100.0%\n" +
		"bsel_next  1            1       100.0%\n" +
		"rsel       0            0       0.0%\n" +
		"type       3            0       0.0%\n" +
		"del        0            0       0.0%\n" +
		"dup        0            0       0.0%\n" +
		"total      11           3       27.3%\n"
	end := strings.Index(stdout.String()[start:], "selector coordinate metrics:\n")
	if end < 0 {
		t.Fatalf("gain report has no selector metrics: %q", stdout.String())
	}
	if got := stdout.String()[start : start+end]; got != want {
		t.Fatalf("command report = %q, want %q", got, want)
	}
}

func TestFailedHPatchCountsOnlyAsIneffectiveOutput(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	validScript := "new note.txt\ntype \"hello\"\n"
	patch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"
	effective, err := countMetrics(validScript, patch)
	if err != nil {
		t.Fatal(err)
	}
	reportTokens, err := countReportInputTokens("in note.txt 1:6\n1 hello\n")
	if err != nil {
		t.Fatal(err)
	}
	effective.ReportInputTokens = reportTokens

	var validStdout, validStderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader(validScript), &validStdout, &validStderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("valid translate = exit %d, stdout %q, stderr %q", exitCode, validStdout.String(), validStderr.String())
	}

	invalidScript := "future-command\n"
	ineffective, err := countIneffectiveMetrics(invalidScript, invocationMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	var invalidStdout, invalidStderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader(invalidScript), &invalidStdout, &invalidStderr, root, dataDirectory); exitCode == 0 || invalidStdout.Len() != 0 || !strings.Contains(invalidStderr.String(), "unknown or malformed command") {
		t.Fatalf("invalid translate = exit %d, stdout %q, stderr %q", exitCode, invalidStdout.String(), invalidStderr.String())
	}

	wantMetrics := effective
	wantMetrics.Commands[commandOperationIndex("new")].Invocations = 1
	wantMetrics.Commands[commandOperationIndex("type")].Invocations = 1
	wantMetrics.IneffectiveHPatchTokens = ineffective.IneffectiveHPatchTokens
	// An unrecognized command has no apply_patch analogue, so its wasted
	// output stays attributed to hpatch rather than credited to the baseline.
	wantMetrics.AttributableFailures = 1
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantMetrics {
		t.Fatalf("metrics = %+v, want %+v", got, wantMetrics)
	}

	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr, root, dataDirectory); exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	wantReport := gainReport(wantMetrics)
	if stdout.String() != wantReport {
		t.Fatalf("gain stdout = %q, want %q", stdout.String(), wantReport)
	}
}

func TestTranslateOutputFailureCountsOnlyAsIneffective(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	want, err := countIneffectiveMetrics(script, invocationMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	want.Commands[commandOperationIndex("new")].Invocations = 1
	want.Commands[commandOperationIndex("type")].Invocations = 1

	var stderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), metricsErrorWriter{}, &stderr, root, dataDirectory)
	if exitCode == 0 || !strings.Contains(stderr.String(), "writing patch") {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics = %+v, want ineffective-only %+v", got, want)
	}
}

type metricsErrorWriter struct{}

func (metricsErrorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
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
	entry := metrics{HPatchTokens: 3, ApplyPatchTokens: 7, ReportInputTokens: 2}
	entry.Commands[commandOperationIndex("new")] = commandMetric{Invocations: 1}

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
	want := metrics{HPatchTokens: wantWrites * entry.HPatchTokens, ApplyPatchTokens: wantWrites * entry.ApplyPatchTokens, ReportInputTokens: wantWrites * entry.ReportInputTokens}
	want.Commands[commandOperationIndex("new")] = commandMetric{Invocations: wantWrites}
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

func TestPriorMetricsVersionResetsTotals(t *testing.T) {
	dataDirectory := t.TempDir()
	var prior [256]byte
	copy(prior[:8], "HPATCH06")
	binary.LittleEndian.PutUint64(prior[8:16], 7)
	binary.LittleEndian.PutUint64(prior[16:24], 5)
	checksum := sha256.Sum256(prior[:224])
	copy(prior[224:], checksum[:])
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), prior[:], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != (metrics{}) {
		t.Fatalf("prior metrics were not reset: %+v", got)
	}
}

func TestMismatchedMetricsVersionResetsTotals(t *testing.T) {
	dataDirectory := t.TempDir()
	mismatched := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5, ApplyPatchTokens: 9}, 7), "HPATCH99")
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), mismatched[:], 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != (metrics{}) {
		t.Fatalf("mismatched metrics were not reset: %+v", got)
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
		t.Fatalf("metrics after version reset = %+v, want %+v", got, want)
	}
}

func TestMalformedMismatchedMetricsVersionDoesNotReset(t *testing.T) {
	dataDirectory := t.TempDir()
	malformed := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5}, 7), "HPATCH99")
	malformed[40] ^= 0xff
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), malformed[:], 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readMetrics(dataDirectory); err == nil || !strings.Contains(err.Error(), "no valid counter slot") {
		t.Fatalf("readMetrics() error = %v, want malformed-slot failure", err)
	}
}
func TestMismatchedMetricsVersionDoesNotOverrideCurrent(t *testing.T) {
	dataDirectory := t.TempDir()
	want := metrics{HPatchTokens: 5, ApplyPatchTokens: 9}
	current := encodeMetricsSlot(want, 1)
	mismatched := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 7, ApplyPatchTokens: 11}, 2), "HPATCH99")
	content := append(current[:], mismatched[:]...)
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics = %+v, want current-format totals %+v", got, want)
	}
}

func TestMetricsRecoversFromTornUnknownInactiveSlot(t *testing.T) {
	dataDirectory := t.TempDir()
	want := metrics{HPatchTokens: 5, ApplyPatchTokens: 9}
	current := encodeMetricsSlot(want, 1)
	torn := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 7, ApplyPatchTokens: 11}, 2), "HPATCH99")
	torn[40] ^= 0xff
	content := append(current[:], torn[:]...)
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics = %+v, want recovered %+v", got, want)
	}
	entry := metrics{IneffectiveHPatchTokens: 3}
	if err := updateMetrics(dataDirectory, entry); err != nil {
		t.Fatal(err)
	}
	want.IneffectiveHPatchTokens = entry.IneffectiveHPatchTokens
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metrics after recovery update = %+v, want %+v", got, want)
	}
}

func rewriteMetricsMagic(encoded [metricsSlotSize]byte, magic string) [metricsSlotSize]byte {
	copy(encoded[:8], magic)
	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	copy(encoded[metricsChecksumOffset:], checksum[:])
	return encoded
}
