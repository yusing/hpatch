package hpatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestErrorHookReceivesFailureAndRepairContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("present words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDirectory := t.TempDir()
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	writeSettingsForTest(t, dataDirectory, []string{
		"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(bodyPath),
	})

	script := "in note.txt\ntsel " + hashLine("present words") + " \"missing\"\ntype \"replacement\"\n"
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, dataDirectory)
	if exitCode != 1 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"# hpatch command failed",
		"- Command: `2`",
		"- Source line: `2`",
		"- Operation: `tsel`",
		"- Category: `selection`",
		"- Path: `note.txt`",
		"## Failed command\n\n    tsel " + hashLine("present words") + " \"missing\"",
		"## Failure\n\n    found 0 of 1 requested matches of \"missing\" at or after line 1",
		"## Diagnostic\n\n    hpatch: command 2, source line 2",
		"## Repair context",
		"found 0 of 1 requested matches at or after line 1",
		"#|present words",
	} {
		if !strings.Contains(normalizeHashlineRows(string(body)), fragment) {
			t.Fatalf("hook body does not contain %q:\n%s", fragment, body)
		}
	}
	if strings.Contains(stderr.String(), "warning:") {
		t.Fatalf("successful hook produced warning: %q", stderr.String())
	}
}

func TestErrorHookReceivesMalformedCommand(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	writeSettingsForTest(t, dataDirectory, []string{
		"printf '%s' {{shellquote .Body}} > " + shellQuote(bodyPath),
	})

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("select the file\n"), &stdout, &stderr, root, dataDirectory)
	if exitCode != 1 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"- Command: `1`",
		"- Operation: `select`",
		"- Category: `syntax`",
		"## Failed command\n\n    select the file",
		"unknown or malformed command",
	} {
		if !strings.Contains(normalizeHashlineRows(string(body)), fragment) {
			t.Fatalf("hook body does not contain %q:\n%s", fragment, body)
		}
	}
}

func TestErrorHookFailureDoesNotReplaceDiagnostic(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	writeSettingsForTest(t, dataDirectory, []string{"exit 7"})

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("del\n"), &stdout, &stderr, root, dataDirectory)
	if exitCode != 1 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "hpatch: command 1, source line 1, operation \"del\", category edit: del requires an active file\n") {
		t.Fatalf("original diagnostic was not preserved: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "hpatch: warning: running error hook 1: exit status 7\n") {
		t.Fatalf("hook failure was not reported: %q", stderr.String())
	}
}

func TestSettingsAreReadOnlyForEvaluationFailures(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if exitCode := Run(nil, strings.NewReader("new note.txt\ntype \"ok\"\n"), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("successful Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode := Run(nil, strings.NewReader("del\n"), &stdout, &stderr, root, dataDirectory)
	if exitCode != 1 || !strings.Contains(stderr.String(), "hpatch: warning: decoding settings:") {
		t.Fatalf("failed Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestEnvironmentalCommandFailureDoesNotRunErrorHook(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	dataDirectory := t.TempDir()
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	writeSettingsForTest(t, dataDirectory, []string{"touch " + shellQuote(bodyPath)})

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in folder\n"), &stdout, &stderr, root, dataDirectory)
	if exitCode != 1 || !strings.Contains(stderr.String(), "folder is not a regular file") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("environmental failure ran hook: stat error %v", err)
	}
}

func TestExecuteErrorHookTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := executeErrorHook(ctx, "sleep 10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executeErrorHook() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("executeErrorHook() took %s", elapsed)
	}
}

func TestAggregatedErrorHooksShareOneTimeout(t *testing.T) {
	dataDirectory := t.TempDir()
	writeSettingsForTest(t, dataDirectory, []string{"sleep 10"})
	sourceErrors := []*commandError{
		{Reason: reasonSyntax, Command: 1, Line: 1, Operation: "bad", Category: "syntax", Source: "bad", Message: "unknown command"},
		{Reason: reasonSyntax, Command: 2, Line: 2, Operation: "bad", Category: "syntax", Source: "bad", Message: "unknown command"},
	}

	started := time.Now()
	errs := runCommandErrorHooks(t.Context(), dataDirectory, sourceErrors, "failed", 20*time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runCommandErrorHooks() took %s", elapsed)
	}
	if len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
		t.Fatalf("runCommandErrorHooks() errors = %v", errs)
	}
}

func TestErrorHooksShareOneTimeout(t *testing.T) {
	dataDirectory := t.TempDir()
	writeSettingsForTest(t, dataDirectory, []string{"sleep 10", "sleep 10"})
	sourceError := &commandError{Reason: reasonSyntax, Command: 1, Line: 1, Operation: "bad", Category: "syntax", Source: "bad", Message: "unknown command"}

	started := time.Now()
	errs := runCommandErrorHooks(t.Context(), dataDirectory, []*commandError{sourceError}, failureDiagnostic(sourceError.Error()), 20*time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runCommandErrorHooks() took %s", elapsed)
	}
	if len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
		t.Fatalf("runCommandErrorHooks() errors = %v", errs)
	}
}

func TestReadSettingsRejectsOversizeContent(t *testing.T) {
	dataDirectory := t.TempDir()
	content := append([]byte(`{"hooks":{"error":[]}}`), bytes.Repeat([]byte(" "), maxSettingsBytes)...)
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readSettings(dataDirectory)
	if err == nil || !strings.Contains(err.Error(), "file exceeds 1048576 bytes") {
		t.Fatalf("readSettings() error = %v", err)
	}
}

func TestMarkdownCodeSpanHandlesBackticks(t *testing.T) {
	body := formatErrorHookMarkdown(errorHookEvent{Path: "dir/`quoted`/file.md"})
	if !strings.Contains(body, "- Path: `` dir/`quoted`/file.md ``") {
		t.Fatalf("formatErrorHookMarkdown() = %q", body)
	}
}

func TestOutcomeHookFailureWarnsWithoutReplacingSuccess(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dataDirectory := t.TempDir()
	content, err := json.Marshal(settings{Hooks: hooks{Outcome: []string{"exit 9"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := WithAttemptMetadata(t.Context(), AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call", Attempt: 1})
	translated, err := TranslateForHost(ctx, Workspace{Root: root}, "new note.txt\ntype \"ok\"\n", dataDirectory)
	if err != nil || len(translated.Patch) == 0 {
		t.Fatalf("translation = %+v, error %v", translated, err)
	}
	if !strings.Contains(translated.Diagnostic, "warning: running outcome hook 1: exit status 9") {
		t.Fatalf("outcome warning = %q", translated.Diagnostic)
	}
}

func TestRejectedAttemptReportsSettingsFailureOnce(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := WithAttemptMetadata(t.Context(), AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call", Attempt: 1})

	translated, err := TranslateForHost(ctx, Workspace{Root: root}, "del\n", dataDirectory)
	if err == nil {
		t.Fatalf("TranslateForHost() translation = %+v, want rejection", translated)
	}
	if count := strings.Count(translated.Diagnostic, "hpatch: warning: decoding settings:"); count != 1 {
		t.Fatalf("settings warning count = %d, diagnostic:\n%s", count, translated.Diagnostic)
	}
}

func TestErrorAndOutcomeHooksReceiveAttemptMetadata(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dataDirectory := t.TempDir()
	errorPath := filepath.Join(t.TempDir(), "error.md")
	outcomePath := filepath.Join(t.TempDir(), "outcome.md")
	metadataPath := filepath.Join(t.TempDir(), "metadata.txt")
	content, err := json.Marshal(settings{Hooks: hooks{
		Error: []string{"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(errorPath)},
		Outcome: []string{
			"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(outcomePath),
			"printf '%s' {{shellquote .CorrelationID}}'|'{{shellquote .CallID}}'|'{{.Attempt}}'|'{{.Correction}}'|'{{shellquote .Outcome}} > " + shellQuote(metadataPath),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := AttemptMetadata{SessionID: "session-1", CorrelationID: "chain-1", CallID: "call-2", Attempt: 2, Correction: true}
	ctx := WithAttemptMetadata(t.Context(), metadata)

	failed, err := TranslateForHost(ctx, Workspace{Root: root}, "del\n", dataDirectory)
	if err == nil || failed.Diagnostic == "" {
		t.Fatalf("failed translation = %+v, error %v", failed, err)
	}
	body, err := os.ReadFile(errorPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- Session ID: `session-1`", "- Correlation ID: `chain-1`", "- Call ID: `call-2`", "- Attempt: `2`", "- Correction: `true`", "- Outcome: `rejected`"} {
		if !strings.Contains(normalizeHashlineRows(string(body)), want) {
			t.Fatalf("error hook lacks %q:\n%s", want, body)
		}
	}
	outcome, err := os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outcome), "# hpatch attempt rejected") {
		t.Fatalf("rejected outcome hook = %q", outcome)
	}

	translated, err := TranslateForHost(ctx, Workspace{Root: root}, "new note.txt\ntype \"ok\"\n", dataDirectory)
	if err != nil || translated.Diagnostic != "" {
		t.Fatalf("successful translation = %+v, error %v", translated, err)
	}
	outcome, err = os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outcome), "# hpatch attempt corrected") {
		t.Fatalf("corrected outcome hook = %q", outcome)
	}
	metadataBody, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(metadataBody) != "chain-1|call-2|2|true|corrected" {
		t.Fatalf("outcome metadata = %q", metadataBody)
	}
}

func writeSettingsForTest(t *testing.T, dataDirectory string, errorHooks []string) {
	t.Helper()
	content, err := json.Marshal(settings{Hooks: hooks{Error: errorHooks}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
}
