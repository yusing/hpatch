package hpatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func updateMetrics(dataDirectory string, entry metrics) error {
	return updateMetricsForSessionContext(context.TODO(), dataDirectory, entry, "")
}

func toolPersistenceFixture() metrics {
	value := metrics{ToolCount: 1}
	value.Tools[0] = toolMetric{
		PluginID: "example.plugin", ToolName: "example_tool",
		Calls: 1, EmittedTokens: 3, TranslatedTokens: 7,
		FailedTranslations: 1, FailedEmittedTokens: 2,
	}
	return value
}

func TestMetricsRejectInvalidToolCollectionBeforeCreatingStore(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "metrics")
	err := updateMetrics(dataDirectory, metrics{ToolCount: maxMetricTools + 1})
	if err == nil || !strings.Contains(err.Error(), "invalid command, feature, or tool counters") {
		t.Fatalf("updateMetrics() error = %v", err)
	}
	if _, statErr := os.Stat(dataDirectory); !os.IsNotExist(statErr) {
		t.Fatalf("invalid metrics created data directory: %v", statErr)
	}
}

func TestHostOperationsPersistInvocationTotals(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	translated, err := translateForHostAtTest(t, root, script, dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if string(translated.Patch) != "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n" {
		t.Fatalf("patch = %q", translated.Patch)
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, HostMetricRecord{Invocation: translated.Invocation}); err != nil {
		t.Fatal(err)
	}
	applied, err := applyForHostAtTest(t, root, script, dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, HostMetricRecord{Invocation: applied.Invocation}); err != nil {
		t.Fatal(err)
	}

	invocation := invocationMetrics{}
	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		Invocation:        InvocationMetrics{value: invocation},
		HPatchTokens:      20,
		ApplyPatchTokens:  40,
		ReportInputTokens: 6,
	})

	gain, err := LoadGainMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if gain.HPatchTokens != 20 || gain.ApplyPatchTokens != 40 || gain.ReportInputTokens != 6 ||
		gain.Commands[commandOperationIndex("new")].Invocations != 2 ||
		gain.Commands[commandOperationIndex("type")].Invocations != 2 {
		t.Fatalf("gain = %+v", gain)
	}
}

func TestLoadGainMetricsWithoutStoreDoesNotCreateDirectory(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "absent")
	got, err := LoadGainMetrics(dataDirectory)
	if err != nil || got.HPatchTokens != 0 {
		t.Fatalf("LoadGainMetrics() = %+v, %v", got, err)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("gain created an empty metrics directory: %v", err)
	}
}

func TestStructuredGainPreservesLargeAndSignedArithmetic(t *testing.T) {
	precise := metrics{HPatchTokens: 9214148664817921031, ApplyPatchTokens: ^uint64(0)}
	if got := precise.gainMetrics().SuccessfulReduction; got != "50.1" {
		t.Fatalf("large-counter successful reduction = %s, want 50.1", got)
	}

	credited := metrics{DefinitionInputTokens: 5, RemovedDefinitionInputTokens: 9}
	if got := credited.gainMetrics().NetAddedInput; got != "-4" {
		t.Fatalf("credited net added input = %s, want -4", got)
	}
}

func TestUpdateMetricsRejectsAggregateOverflowAcrossDistinctTools(t *testing.T) {
	overflow := metrics{ToolCount: 2}
	overflow.Tools[0] = toolMetric{PluginID: "a", ToolName: "one", EmittedTokens: ^uint64(0)}
	overflow.Tools[1] = toolMetric{PluginID: "b", ToolName: "two", EmittedTokens: 1}
	if err := updateMetrics(t.TempDir(), overflow); err == nil || !strings.Contains(err.Error(), "invalid command, feature, or tool counters") {
		t.Fatalf("aggregate tool overflow error = %v", err)
	}
}

func TestLoadGainMetricsMatchesGainReportTotals(t *testing.T) {
	dataDirectory := t.TempDir()
	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		HPatchTokens:                            40,
		ApplyPatchTokens:                        100,
		IneffectiveHPatchTokens:                 30,
		FailedApplyPatchTokens:                  10,
		ReportInputTokens:                       5,
		DiagnosticInputTokens:                   7,
		MisuseWarningInputTokens:                3,
		DefinitionRequests:                      1,
		DefinitionInputTokens:                   11,
		RemovedDefinitionInputTokens:            9,
		RemovedExecCommandDefinitionInputTokens: 4,

		SessionID: "session-gain",
		ToolMetrics: []ToolMetricRecord{{
			PluginID: "builtin.hpatch", ToolName: "hpatch", DefinitionInputTokens: 11,
			Executions: 1, CurrentInputTokens: 20, StockInputTokens: 10,
		}},
	})
	entry := metrics{}
	entry.Commands[commandOperationIndex("add")].Invocations = 1
	entry.Commands[commandOperationIndex("add")].Errors = 1
	entry.Targets[targetVariantLine-1] = commandMetric{Invocations: 1, Errors: 1}
	entry.Reasons[reasonRowStale] = 1
	entry.CommandReasons[commandOperationIndex("add")][reasonRowStale] = 1
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
	if got.NetAddedInput != "23" || got.MisuseWarningInputTokens != 3 || got.DefinitionSources != "installation and removal measured" || got.RemovedDefinitionInputTokens != 9 || got.RemovedExecCommandDefinitionInputTokens != 4 {
		t.Fatalf("input = net %q sources %q", got.NetAddedInput, got.DefinitionSources)
	}
	if len(got.ToolInputs) != 1 || got.ToolInputs[0].CurrentTokens != 20 ||
		got.ToolInputs[0].StockTokens != 10 || got.ToolInputs[0].Reduction != "-100.0" {
		t.Fatalf("tool input estimates = %#v", got.ToolInputs)
	}
	if len(got.Commands) != commandCount || got.Commands[commandOperationIndex("add")].Errors != 1 {
		t.Fatalf("commands = %#v", got.Commands)
	}
	if len(got.CommandReasons) != 1 || got.CommandReasons[0].Command != "add" || got.CommandReasons[0].Reason != "row-stale" {
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
	if len(got.Commands) != commandCount || len(got.Targets) != targetVariantCount {
		t.Fatalf("empty tables = commands %d targets %d", len(got.Commands), len(got.Targets))
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
		{name: "success with unrelated command-name text", args: []string{"translate"}, script: "new note.txt\ntype \"future-command data\"\n", success: true},
		{name: "execution error", args: []string{"translate"}, script: "new failed.txt\ntype \"ignored\"\ntype 1:ffff \"x\"\n"},
		{name: "unknown command", args: []string{"translate"}, script: "unknown-command 0 1:1\n"},
		{name: "unknown future command", args: []string{"translate"}, script: "future-command\n"},
		{name: "successful no-op", script: "new transient.txt\nrm\n", success: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var result HostTranslation
			var err error
			if len(test.args) == 0 {
				result, err = applyForHostAtTest(t, root, test.script, dataDirectory)
			} else {
				result, err = translateForHostAtTest(t, root, test.script, dataDirectory)
			}
			if (err == nil) != test.success {
				t.Fatalf("host operation error = %v", err)
			}
			if err := RecordHostMetrics(t.Context(), dataDirectory, HostMetricRecord{Invocation: result.Invocation}); err != nil {
				t.Fatal(err)
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

	wantCommands[commandOperationIndex("type")] = commandMetric{Invocations: 3, Errors: 1}
	if got.Commands != wantCommands {
		t.Fatalf("command metrics = %+v, want %+v", got.Commands, wantCommands)
	}
	projected := got.gainMetrics()
	if projected.Commands[commandOperationIndex("type")].ErrorRate != "33.3" {
		t.Fatalf("projected command metrics = %+v", projected.Commands)
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
	projected := got.gainMetrics()
	if projected.AllTools.EmittedTokens != 70 || projected.AllTools.TranslatedTokens != 110 || projected.AllTools.Reduction != "36.4" {
		t.Fatalf("structured gain does not include host-accounted outcomes: %+v", projected.AllTools)
	}
}

func TestMetricsPersistenceFailureDoesNotPreventCompletedMutation(t *testing.T) {
	root := t.TempDir()
	dataPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataPath, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := applyForHostAtTest(t, root, "new note.txt\ntype \"hello\"\n", dataPath)
	if err != nil || result.Diagnostic != "" {
		t.Fatalf("ApplyForHost() error = %v, diagnostic %q", err, result.Diagnostic)
	}
	if err := RecordHostMetrics(t.Context(), dataPath, HostMetricRecord{Invocation: result.Invocation}); err == nil || !strings.Contains(err.Error(), "creating metrics directory") {
		t.Fatalf("RecordHostMetrics() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "hello" {
		t.Fatalf("note.txt = %q, error %v", content, err)
	}
}

func TestMetricsPersistenceFailureDoesNotPreventCompletedTranslation(t *testing.T) {
	root := t.TempDir()
	dataPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dataPath, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "new note.txt\ntype \"hello\"\n"
	wantPatch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"

	result, err := translateForHostAtTest(t, root, script, dataPath)
	if err != nil || string(result.Patch) != wantPatch || result.Diagnostic != "" {
		t.Fatalf("TranslateForHost() error = %v, patch %q, diagnostic %q", err, result.Patch, result.Diagnostic)
	}
	if err := RecordHostMetrics(t.Context(), dataPath, HostMetricRecord{Invocation: result.Invocation}); err == nil || !strings.Contains(err.Error(), "creating metrics directory") {
		t.Fatalf("RecordHostMetrics() error = %v", err)
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

			if _, err := LoadGainMetrics(dataDirectory); err == nil || !strings.Contains(err.Error(), "reading metrics") {
				t.Fatalf("LoadGainMetrics() error = %v", err)
			}
		})
	}
}

func TestMetricsRecoversPreviousSlotAfterTornWrite(t *testing.T) {
	dataDirectory := t.TempDir()
	want := toolPersistenceFixture()
	want.HPatchTokens, want.ApplyPatchTokens = 5, 9
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
	entry := toolPersistenceFixture()
	entry.HPatchTokens, entry.ApplyPatchTokens, entry.ReportInputTokens = 3, 7, 2
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
	want := metrics{HPatchTokens: wantWrites * entry.HPatchTokens, ApplyPatchTokens: wantWrites * entry.ApplyPatchTokens, ReportInputTokens: wantWrites * entry.ReportInputTokens, ToolCount: 1}
	want.Tools[0] = toolMetric{
		PluginID: "example.plugin", ToolName: "example_tool",
		Calls: wantWrites, EmittedTokens: wantWrites * 3, TranslatedTokens: wantWrites * 7,
		FailedTranslations: wantWrites, FailedEmittedTokens: wantWrites * 2,
	}
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
	want := toolPersistenceFixture()
	want.HPatchTokens, want.ApplyPatchTokens = 5, 9
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
