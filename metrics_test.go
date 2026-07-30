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
	"unicode/utf8"
)

func gainReport(m metrics) string {
	return gainReportAtWidth(m, defaultGainReportWidth)
}

func TestGainReportsPersistedTotals(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	patch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"

	var translateStdout, translateStderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), &translateStdout, &translateStderr, root, dataDirectory)
	wantState := "in note.txt 1:6\nlast edit in note.txt: type 1 edit: 1:1-1:6\nfiles: 1 added\n1|hello\n"
	if exitCode != 0 || translateStdout.String() != patch || translateStderr.String() != wantState {
		t.Fatalf("translate = exit %d, stdout %q, stderr %q", exitCode, translateStdout.String(), translateStderr.String())
	}

	var normalStdout, normalStderr bytes.Buffer
	exitCode = Run(nil, strings.NewReader(script), &normalStdout, &normalStderr, root, dataDirectory)
	if exitCode != 0 || normalStdout.Len() != 0 || normalStderr.String() != wantState {
		t.Fatalf("normal = exit %d, stdout %q, stderr %q", exitCode, normalStdout.String(), normalStderr.String())
	}

	invocation := invocationMetrics{}
	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		Invocation:        InvocationMetrics{value: invocation},
		HPatchTokens:      20,
		ApplyPatchTokens:  40,
		ReportInputTokens: 6,
	})

	var stdout, stderr bytes.Buffer
	exitCode = Run([]string{"gain"}, strings.NewReader("not a script"), &stdout, &stderr, root, dataDirectory)
	wantMetrics := metrics{HPatchTokens: 20, ApplyPatchTokens: 40, ReportInputTokens: 6}
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
	metricValues := metrics{
		HPatchTokens:                 2404,
		ApplyPatchTokens:             4764,
		IneffectiveHPatchTokens:      2172,
		FailedApplyPatchTokens:       300,
		ReportInputTokens:            11,
		DiagnosticInputTokens:        13,
		DefinitionInputTokens:        100,
		RemovedDefinitionInputTokens: 30,
		Sessions:                     2,
		DefinitionRequests:           3,
	}
	report := gainReport(metricValues)
	for _, want := range []string{
		"output token estimates:\n",
		"successful  2404    4764         49.5%\n",
		"failed      2172    300          n/a\n",
		"all         4576    5064         9.6%\n",
		"input token estimates:\n",
		"hpatch definition installed",
		"apply_patch definition removed",
		"net added input",
		"definition routing covers 3 accounted request(s)",
		"installation and removal measured",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("gain report %q does not contain %q", report, want)
		}
	}
	input := strings.Join(strings.Fields(gainInputSection(t, report)), " ")
	for _, want := range []string{
		"state reports 11",
		"failure diagnostics 13",
		"hpatch definition installed 100",
		"apply_patch definition removed -30",
		"net added input 94",
	} {
		if !strings.Contains(input, want) {
			t.Fatalf("input report %q does not contain %q", input, want)
		}
	}
	for _, nextTable := range []string{
		"input token estimates:",
		"command metrics:",
		"tsel selection metrics:",
		"failure reasons:",
		"command failure reasons:",
	} {
		if !strings.Contains(report, "\n\n"+nextTable+"\n") {
			t.Fatalf("gain report has no blank line before %q: %q", nextTable, report)
		}
	}

	for _, width := range []int{64, 80, 117} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			section := gainInputSection(t, gainReportAtWidth(metricValues, width))
			for line := range strings.SplitSeq(strings.TrimSuffix(section, "\n"), "\n") {
				if utf8.RuneCountInString(line) > width {
					t.Fatalf("line exceeds width %d: %q", width, line)
				}
			}
			for _, text := range []string{
				"final state returned after successful calls",
				"errors and repair context returned after failed calls",
				"standalone tool definition added by the router",
				"exact Code Mode section removed by the router",
				"measured additions minus the removed definition",
			} {
				if !strings.Contains(strings.Join(strings.Fields(section), " "), text) {
					t.Fatalf("width %d report lost description %q: %q", width, text, section)
				}
			}
		})
	}

	overflowSafe := gainReport(metrics{HPatchTokens: ^uint64(0), IneffectiveHPatchTokens: ^uint64(0), ApplyPatchTokens: ^uint64(0), FailedApplyPatchTokens: ^uint64(0)})
	if !strings.Contains(overflowSafe, "36893488147419103230") {
		t.Fatalf("overflow-safe gain report = %q", overflowSafe)
	}
	precise := metrics{HPatchTokens: 9214148664817921031, ApplyPatchTokens: ^uint64(0)}
	if got := precise.reduction(); got != "50.1" {
		t.Fatalf("large-counter reduction = %s, want 50.1", got)
	}
	creditCanExceedAdded := strings.Join(strings.Fields(gainInputSection(t, gainReport(metrics{DefinitionInputTokens: 5, RemovedDefinitionInputTokens: 9}))), " ")
	if !strings.Contains(creditCanExceedAdded, "net added input -4") {
		t.Fatalf("signed definition credit report = %q", creditCanExceedAdded)
	}
}

func gainInputSection(t *testing.T, report string) string {
	t.Helper()
	start := strings.Index(report, "input token estimates:\n")
	end := strings.Index(report, "command metrics:\n")
	if start < 0 || end < start {
		t.Fatalf("gain report has no bounded input section: %q", report)
	}
	return report[start:end]
}

func TestLoadGainMetricsMatchesGainReportTotals(t *testing.T) {
	dataDirectory := t.TempDir()
	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		HPatchTokens:                 40,
		ApplyPatchTokens:             100,
		IneffectiveHPatchTokens:      30,
		FailedApplyPatchTokens:       10,
		ReportInputTokens:            5,
		DiagnosticInputTokens:        7,
		DefinitionRequests:           1,
		DefinitionInputTokens:        11,
		RemovedDefinitionInputTokens: 9,
		SessionID:                    "session-gain",
	})
	entry := metrics{}
	entry.Commands[commandOperationIndex("sel")].Invocations = 1
	entry.Commands[commandOperationIndex("sel")].Errors = 1
	entry.Reasons[reasonCoordinateBounds] = 1
	entry.CommandReasons[commandOperationIndex("sel")][reasonCoordinateBounds] = 1
	if err := updateMetrics(dataDirectory, entry); err != nil {
		t.Fatal(err)
	}

	got, err := LoadGainMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.HPatchTokens != 40 || got.ApplyPatchTokens != 100 || got.IneffectiveHPatchTokens != 30 || got.FailedApplyPatchTokens != 10 {
		t.Fatalf("output tokens = %#v", got)
	}
	if got.SuccessfulReduction != "60.0" || got.OverallReduction != "36.4" {
		t.Fatalf("reductions = %q / %q", got.SuccessfulReduction, got.OverallReduction)
	}
	if got.NetAddedInput != "14" || got.DefinitionSources != "installation and removal measured" {
		t.Fatalf("input = net %q sources %q", got.NetAddedInput, got.DefinitionSources)
	}
	if len(got.Commands) != commandCount || got.Commands[commandOperationIndex("sel")].Errors != 1 {
		t.Fatalf("commands = %#v", got.Commands)
	}
	if len(got.CommandReasons) != 1 || got.CommandReasons[0].Command != "sel" || got.CommandReasons[0].Reason != "coordinate-bounds" {
		t.Fatalf("command reasons = %#v", got.CommandReasons)
	}
}

func TestLoadGainMetricsAbsentDirectoryIsZero(t *testing.T) {
	got, err := LoadGainMetrics(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if got.HPatchTokens != 0 || got.SuccessfulReduction != "0.0" || got.OverallReduction != "0.0" {
		t.Fatalf("empty gain = %#v", got)
	}
	if len(got.Commands) != commandCount || len(got.TextSpans) != textSpanVariantCount {
		t.Fatalf("empty tables = commands %d text spans %d", len(got.Commands), len(got.TextSpans))
	}
	if len(got.CommandReasons) != 1 || got.CommandReasons[0] != (CommandReasonMetric{Command: "none", Reason: "none"}) {
		t.Fatalf("empty command reasons = %#v", got.CommandReasons)
	}
	if got.DefinitionSources != "not measured (missing caller session)" {
		t.Fatalf("definition sources = %q", got.DefinitionSources)
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
		{name: "success with unrelated command-name text", args: []string{"translate"}, script: "new note.txt\ntype \"rsel sel future-command\"\n", success: true},
		{name: "execution error", args: []string{"translate"}, script: "new failed.txt\ntype \"ignored\"\nrsel 99:99\n"},
		{name: "malformed absolute line", args: []string{"translate"}, script: "sel 0 1:1\n"},
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
	wantCommands[commandOperationIndex("new")] = commandMetric{Invocations: 3}
	wantCommands[commandOperationIndex("rm")] = commandMetric{Invocations: 1}
	wantCommands[commandOperationIndex("sel")] = commandMetric{Invocations: 1, Errors: 1}
	wantCommands[commandOperationIndex("rsel")] = commandMetric{Invocations: 1, Errors: 1}
	wantCommands[commandOperationIndex("type")] = commandMetric{Invocations: 2}
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
		"command  invocations  errors  error rate\n" +
		"-------  -----------  ------  ----------\n" +
		"in       0            0       0.0%\n" +
		"new      3            0       0.0%\n" +
		"mv       0            0       0.0%\n" +
		"rm       1            0       0.0%\n" +
		"sel      1            1       100.0%\n" +
		"tsel     0            0       0.0%\n" +
		"rsel     1            1       100.0%\n" +
		"type     2            0       0.0%\n" +
		"del      0            0       0.0%\n" +
		"copy     0            0       0.0%\n" +
		"cut      0            0       0.0%\n" +
		"paste    0            0       0.0%\n" +
		"commit   0            0       0.0%\n" +
		"total    8            2       25.0%\n\n"
	end := strings.Index(stdout.String()[start:], "tsel selection metrics:\n")
	if end < 0 {
		t.Fatalf("gain report has no tsel selection metrics: %q", stdout.String())
	}
	if got := stdout.String()[start : start+end]; got != want {
		t.Fatalf("command report = %q, want %q", got, want)
	}
}

func TestHostRecordCombinesEffectiveAndIneffectiveOutput(t *testing.T) {
	dataDirectory := t.TempDir()
	invocation := invocationMetrics{}
	invocation.Commands[commandOperationIndex("new")].Invocations = 1
	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		Invocation:              InvocationMetrics{value: invocation},
		HPatchTokens:            40,
		ApplyPatchTokens:        100,
		IneffectiveHPatchTokens: 30,
		FailedApplyPatchTokens:  10,
		ReportInputTokens:       5,
		DiagnosticInputTokens:   7,
	})

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := metrics{
		HPatchTokens:            40,
		ApplyPatchTokens:        100,
		IneffectiveHPatchTokens: 30,
		FailedApplyPatchTokens:  10,
		ReportInputTokens:       5,
		DiagnosticInputTokens:   7,
	}
	want.Commands[commandOperationIndex("new")].Invocations = 1
	if got != want {
		t.Fatalf("host metrics = %+v, want %+v", got, want)
	}
	if report := gainReport(got); !strings.Contains(report, "failed      30      10") || !strings.Contains(report, "all         70      110") {
		t.Fatalf("gain report does not include host-accounted outcomes: %q", report)
	}
}

func TestTranslateOutputFailureCountsEvaluatorCommandsOnly(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"

	var stderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader(script), metricsErrorWriter{}, &stderr, root, dataDirectory)
	if exitCode == 0 || !strings.Contains(stderr.String(), "writing patch") {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := metrics{}
	want.Commands[commandOperationIndex("new")].Invocations = 1
	want.Commands[commandOperationIndex("type")].Invocations = 1
	if got != want {
		t.Fatalf("metrics = %+v, want evaluator commands only %+v", got, want)
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
			"TEST_METRICS_HELPER=1",
			"TEST_METRICS_DIRECTORY="+dataDirectory,
			fmt.Sprintf("TEST_METRICS_WRITES=%d", writesPerProcess),
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
	if os.Getenv("TEST_METRICS_HELPER") != "1" {
		return
	}
	writes, err := strconv.Atoi(os.Getenv("TEST_METRICS_WRITES"))
	if err != nil {
		t.Fatal(err)
	}
	for range writes {
		if err := updateMetrics(os.Getenv("TEST_METRICS_DIRECTORY"), metrics{HPatchTokens: 2, ApplyPatchTokens: 5}); err != nil {
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

func TestHPATCH18MetricsReset(t *testing.T) {
	dataDirectory := t.TempDir()
	const priorSlotSize = 2432
	const priorChecksumOffset = 2400
	var prior [priorSlotSize]byte
	copy(prior[:8], "HPATCH18")
	binary.LittleEndian.PutUint64(prior[8:16], 7)
	binary.LittleEndian.PutUint64(prior[16:24], 5)
	checksum := sha256.Sum256(prior[:priorChecksumOffset])
	copy(prior[priorChecksumOffset:], checksum[:])
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), prior[:], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != (metrics{}) {
		t.Fatalf("HPATCH18 metrics were not reset: %+v", got)
	}
}

func TestMismatchedMetricsVersionResetsTotals(t *testing.T) {
	dataDirectory := t.TempDir()
	mismatched := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5, ApplyPatchTokens: 9}, 7))
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
	malformed := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 5}, 7))
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
	mismatched := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 7, ApplyPatchTokens: 11}, 2))
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
	torn := rewriteMetricsMagic(encodeMetricsSlot(metrics{HPatchTokens: 7, ApplyPatchTokens: 11}, 2))
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

func rewriteMetricsMagic(encoded [metricsSlotSize]byte) [metricsSlotSize]byte {
	copy(encoded[:8], "HPATCH99")
	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	copy(encoded[metricsChecksumOffset:], checksum[:])
	return encoded
}
