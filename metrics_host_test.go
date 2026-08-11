package hpatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestTranslateForHostReturnsCompleteSuccessAndFailureAccounting(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "note.txt", "alpha\n", 0o600)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	workspace := Workspace{Root: root}

	success, err := TranslateForHost(t.Context(), workspace, "in note.txt\ntype 1:8ed3 \"beta\\n\"\n", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(success.Patch), "*** Update File: note.txt") || !strings.HasPrefix(success.Report, "in note.txt\n") || success.Diagnostic != "" {
		t.Fatalf("successful host translation = %+v", success)
	}
	if success.Invocation.value.Commands[commandOperationIndex("in")].Invocations != 1 || success.Invocation.value.Commands[commandOperationIndex("type")].Invocations != 1 {
		t.Fatalf("successful invocation metrics = %+v", success.Invocation.value.Commands)
	}
	if got := success.Invocation.EvaluatedCommandCount(); got != 2 {
		t.Fatalf("successful evaluated commands = %d, want 2", got)
	}

	rejected, err := TranslateForHost(t.Context(), workspace, "in note.txt\ntype 1:ffff \"x\"\n", t.TempDir())
	if err == nil {
		t.Fatal("missing-hash selector unexpectedly succeeded")
	}
	mutation := rejected.Invocation.value.Commands[commandOperationIndex("type")]
	if mutation.Invocations != 1 || mutation.Errors != 1 || rejected.Invocation.value.Reasons[reasonRowStale] != 1 {
		t.Fatalf("rejected invocation metrics = %+v", rejected.Invocation.value)
	}
	if got := rejected.Invocation.EvaluatedCommandCount(); got != 2 {
		t.Fatalf("rejected evaluated commands = %d, want 2", got)
	}
	if !strings.HasPrefix(rejected.Diagnostic, "type: command 2, path \"note.txt\", reason row-stale:") {
		t.Fatalf("rejected diagnostic = %q", rejected.Diagnostic)
	}
}

func TestTranslateForHostCancellationHasNoDiagnostic(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := TranslateForHost(ctx, Workspace{Root: root}, "new note.txt\n", t.TempDir())
	if !errors.Is(err, context.Canceled) || result.Diagnostic != "" {
		t.Fatalf("canceled translation = %+v, error %v", result, err)
	}
}

func TestTranslateForHostCancellationSkipsOutcomeHook(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dataDirectory := t.TempDir()
	outcomePath := filepath.Join(t.TempDir(), "outcome")
	content, err := json.Marshal(settings{Hooks: hooks{Outcome: []string{"touch " + shellQuote(outcomePath)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(WithAttemptMetadata(t.Context(), AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call", Attempt: 1}))
	cancel()
	result, err := TranslateForHost(ctx, Workspace{Root: root}, "new note.txt\n", dataDirectory)
	if !errors.Is(err, context.Canceled) || result.Diagnostic != "" {
		t.Fatalf("canceled translation = %+v, error %v", result, err)
	}
	if _, err := os.Stat(outcomePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled attempt ran outcome hook: %v", err)
	}
}

func TestTranslateForHostCancellationStopsErrorHook(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dataDirectory := t.TempDir()
	writeSettingsForTest(t, dataDirectory, []string{"sleep 10"})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := TranslateForHost(ctx, Workspace{Root: root}, "del\n", dataDirectory)
	if !errors.Is(err, context.DeadlineExceeded) || result.Diagnostic != "" {
		t.Fatalf("canceled hook translation = %+v, error %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled hook translation took %s", elapsed)
	}
}

func TestHostMetricRecordPersistsCompleteCallerEntry(t *testing.T) {
	dataDirectory := t.TempDir()
	invocation := invocationMetrics{}
	invocation.Commands[commandOperationIndex("new")].Invocations = 1
	record := HostMetricRecord{
		Invocation:                              InvocationMetrics{value: invocation},
		SessionID:                               "session-one",
		HPatchTokens:                            11,
		ApplyPatchTokens:                        19,
		IneffectiveHPatchTokens:                 7,
		FailedApplyPatchTokens:                  5,
		ReportInputTokens:                       3,
		DiagnosticInputTokens:                   4,
		DefinitionRequests:                      1,
		DefinitionInputTokens:                   13,
		RemovedDefinitionInputTokens:            9,
		RemovedExecCommandDefinitionInputTokens: 6,
		ToolMetrics:                             []ToolMetricRecord{{PluginID: "builtin.hpatch", ToolName: "hpatch", DefinitionInputTokens: 13}},
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, record); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, record); err != nil {
		t.Fatal(err)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := metrics{
		HPatchTokens:                            22,
		ApplyPatchTokens:                        38,
		IneffectiveHPatchTokens:                 14,
		FailedApplyPatchTokens:                  10,
		ReportInputTokens:                       6,
		DiagnosticInputTokens:                   8,
		Sessions:                                1,
		DefinitionRequests:                      2,
		DefinitionInputTokens:                   13,
		RemovedDefinitionInputTokens:            9,
		RemovedExecCommandDefinitionInputTokens: 6,
	}
	want.ToolCount = 1
	want.Tools[0] = toolMetric{PluginID: "builtin.hpatch", ToolName: "hpatch", DefinitionInputTokens: 13}
	want.Commands[commandOperationIndex("new")].Invocations = 2
	if got != want {
		t.Fatalf("persisted host metrics = %+v, want %+v", got, want)
	}
}

func TestRecordHostMetricsRejectsInvalidDefinitionAttribution(t *testing.T) {
	tests := []struct {
		name   string
		record HostMetricRecord
		want   string
	}{
		{name: "definition tokens without request", record: HostMetricRecord{DefinitionInputTokens: 1}, want: "require a definition request"},
		{name: "exec definition tokens without request", record: HostMetricRecord{RemovedExecCommandDefinitionInputTokens: 1}, want: "require a definition request"},
		{name: "shared definition tokens without request", record: HostMetricRecord{SharedDefinitionInputTokens: 1}, want: "require a definition request"},
		{name: "tool definition tokens without request", record: HostMetricRecord{ToolMetrics: []ToolMetricRecord{{PluginID: "example.plugin", ToolName: "tool", DefinitionInputTokens: 1}}}, want: "require a definition request"},
		{name: "definition request without session", record: HostMetricRecord{DefinitionRequests: 1}, want: "require a session"},
		{name: "too many definition requests", record: HostMetricRecord{SessionID: "s", DefinitionRequests: 2}, want: "more than one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDirectory := filepath.Join(t.TempDir(), "metrics")
			err := RecordHostMetrics(t.Context(), dataDirectory, test.record)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RecordHostMetrics() error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(dataDirectory); !os.IsNotExist(statErr) {
				t.Fatalf("invalid record created metrics directory: %v", statErr)
			}
		})
	}
}

func TestRecordHostMetricsCancellationInterruptsLockWait(t *testing.T) {
	dataDirectory := t.TempDir()
	lock := flock.New(filepath.Join(dataDirectory, metricsLockname))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := RecordHostMetrics(ctx, dataDirectory, HostMetricRecord{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RecordHostMetrics() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled metrics lock took %s", elapsed)
	}
}

func TestPendingMetricsWriterDoesNotBlockOtherDirectory(t *testing.T) {
	blockedDirectory := t.TempDir()
	readableDirectory := t.TempDir()
	if err := updateMetrics(readableDirectory, metrics{HPatchTokens: 1}); err != nil {
		t.Fatal(err)
	}
	lockPath := metricLockPath(blockedDirectory)
	registerPendingMetricsWriter(lockPath)
	defer unregisterPendingMetricsWriter(lockPath)

	result := make(chan error, 1)
	go func() {
		_, err := readMetrics(readableDirectory)
		result <- err
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("reader for an unrelated metrics directory was blocked")
	}
}

func recordHostMetricForTest(t *testing.T, dataDirectory string, record HostMetricRecord) {
	t.Helper()
	if err := RecordHostMetrics(t.Context(), dataDirectory, record); err != nil {
		t.Fatal(err)
	}
}
