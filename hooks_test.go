package hpatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	script := "in note.txt\ntype 1:" + hashLine("present words") + " \"missing\" \"replacement\"\n"
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
		"Command: 2 `type`",
		"Source: note.txt:2",
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("hook body does not contain %q:\n%s", fragment, body)
		}
	}
	for _, omitted := range []string{"# hpatch command failed", "Description:", "Outcome:", "Category:", "Failed command", "Failure", "Diagnostic", "Repair context"} {
		if strings.Contains(string(body), omitted) {
			t.Fatalf("hook body unexpectedly contains %q:\n%s", omitted, body)
		}
	}
	if strings.Contains(stderr.String(), "warning:") {
		t.Fatalf("successful hook produced warning: %q", stderr.String())
	}
}

func TestReportIssueRunsDiagnoseHooksWithExactMarkdown(t *testing.T) {
	dataDirectory := t.TempDir()
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	diagnoseHooks := NewDiagnoseHooks(dataDirectory)
	content, err := json.Marshal(settings{Hooks: hooks{
		Error: []string{"exit 9"},
		Diagnose: []string{
			"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(bodyPath),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}

	markdown := "# Misleading repair context\n\nThe suggested target cannot match."
	if err := diagnoseHooks.Report(t.Context(), markdown); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != markdown {
		t.Fatalf("diagnose hook body = %q, want %q", body, markdown)
	}
}

func TestReportIssueReturnsDiagnoseHookFailure(t *testing.T) {
	dataDirectory := t.TempDir()
	diagnoseHooks := NewDiagnoseHooks(dataDirectory)
	content, err := json.Marshal(settings{Hooks: hooks{Diagnose: []string{"exit 9"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}

	err = diagnoseHooks.Report(t.Context(), "diagnostic")
	if err == nil || !strings.Contains(err.Error(), "running diagnose hook 1: exit status 9") {
		t.Fatalf("ReportIssue() error = %v", err)
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
	if !strings.Contains(string(body), "Command: 1 `select`") {
		t.Fatalf("hook body does not contain command:\n%s", body)
	}
	if strings.Contains(string(body), "Description:") || strings.HasPrefix(string(body), "#") {
		t.Fatalf("error hook body unexpectedly contains title or description:\n%s", body)
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
	if !strings.HasPrefix(stderr.String(), "del: command 1, reason script-syntax: unknown or malformed command\n") {
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
	body := formatErrorHookMarkdown(errorHookEvent{Command: 1, Operation: "type`quoted"})
	if !strings.Contains(body, "Command: 1 `` type`quoted ``") {
		t.Fatalf("formatErrorHookMarkdown() = %q", body)
	}
}

func TestOutcomeHookMarkdownUsesSafeFence(t *testing.T) {
	event := outcomeHookEvent{
		attemptHookFields: attemptHookFields{Outcome: "succeeded"},
		Title:             "hpatch attempt succeeded",
		EmittedPayload:    "type <<PATCH\n```\nPATCH\n",
	}
	body := formatOutcomeHookMarkdown(event)
	if event.Title != "hpatch attempt succeeded" {
		t.Fatalf("outcome title = %q", event.Title)
	}
	if !strings.Contains(body, "````hpatch\ntype <<PATCH\n```\nPATCH\n````") {
		t.Fatalf("formatOutcomeHookMarkdown() = %q", body)
	}
	if strings.HasPrefix(body, "#") {
		t.Fatalf("outcome hook body unexpectedly contains title: %q", body)
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
	titlePath := filepath.Join(filepath.Dir(metadataPath), "outcome-title.txt")
	content, err := json.Marshal(settings{Hooks: hooks{
		Error: []string{
			"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(errorPath),
		},
		Outcome: []string{
			"printf '%s' {{shellquote (format_markdown .)}} > " + shellQuote(outcomePath),
			"printf '%s' {{shellquote .CorrelationID}}'|'{{shellquote .CallID}}'|'{{.Attempt}}'|'{{.Correction}}'|'{{shellquote .ToolName}}'|'{{shellquote .Stage}}'|'{{shellquote .Outcome}}'|'{{.EmittedBytes}}'|'{{.EvaluatedBytes}}'|'{{.PatchBytes}} > " + shellQuote(metadataPath),
			"printf '%s' {{shellquote .Title}} > " + shellQuote(titlePath),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}

	rejectedScript := "del\n"
	rejectedMetadata := AttemptMetadata{
		SessionID:       "session-1",
		CorrelationID:   "chain-1",
		CallID:          "call-1",
		Attempt:         1,
		Model:           "gpt-5.6-sol medium",
		ToolName:        "functions.hpatch",
		EmittedPayload:  rejectedScript,
		EvaluatedScript: rejectedScript,
	}
	failed, err := TranslateForHost(
		WithAttemptMetadata(t.Context(), rejectedMetadata),
		Workspace{Root: root},
		rejectedScript,
		dataDirectory,
	)
	if err == nil || failed.Diagnostic == "" {
		t.Fatalf("failed translation = %+v, error %v", failed, err)
	}
	if _, err := os.Stat(errorPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("routed rejection invoked command-error hook: %v", err)
	}
	outcome, err := os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Tool: `functions.hpatch`",
		"Stage: `evaluated`",
		"Outcome: `rejected`",
		"## Emitted hpatch script",
		"```hpatch\ndel\n```",
	} {
		if !strings.Contains(string(outcome), want) {
			t.Fatalf("rejected outcome hook lacks %q:\n%s", want, outcome)
		}
	}

	evaluatedScript := "new note.txt\ntype \"ok\"\n"
	recoveryPayload := "C2:abcd target 2:bbbb"
	delta := "C2:abcd target: 1:aaaa -> 2:bbbb"
	recoveryMetadata := AttemptMetadata{
		SessionID:       "session-1",
		CorrelationID:   "chain-1",
		CallID:          "call-2",
		Attempt:         2,
		Correction:      true,
		Model:           "gpt-5.6-sol medium",
		ToolName:        "functions.hpatch_recover",
		EmittedPayload:  recoveryPayload,
		EvaluatedScript: evaluatedScript,
		RecoveryDelta:   delta,
		Title:           "Update note",
	}
	translated, err := TranslateForHost(
		WithAttemptMetadata(t.Context(), recoveryMetadata),
		Workspace{Root: root},
		evaluatedScript,
		dataDirectory,
	)
	if err != nil || translated.Diagnostic != "" {
		t.Fatalf("successful translation = %+v, error %v", translated, err)
	}
	outcomeTitle, err := os.ReadFile(titlePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(outcomeTitle) != "hpatch recovery attempt succeeded: Update note" {
		t.Fatalf("outcome hook title = %q", outcomeTitle)
	}
	outcome, err = os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Tool: `functions.hpatch_recover`",
		"Stage: `translated`",
		"Outcome: `succeeded`",
		"## Emitted recovery payload",
		"```hpatch-recover\n" + recoveryPayload + "\n```",
		"## Resolved recovery delta",
		"    " + delta,
		fmt.Sprintf("Router rebuilt a %d-byte complete HPATCH script; it was not model-emitted.", len(evaluatedScript)),
	} {
		if !strings.Contains(string(outcome), want) {
			t.Fatalf("recovery outcome hook lacks %q:\n%s", want, outcome)
		}
	}
	if strings.Contains(string(outcome), "```hpatch\n"+evaluatedScript) {
		t.Fatalf("recovery outcome presents rebuilt script as emitted:\n%s", outcome)
	}
	metadataBody, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	wantMetadata := fmt.Sprintf(
		"chain-1|call-2|2|true|functions.hpatch_recover|translated|succeeded|%d|%d|%d",
		len(recoveryPayload),
		len(evaluatedScript),
		len(translated.Patch),
	)
	if string(metadataBody) != wantMetadata {
		t.Fatalf("outcome metadata = %q, want %q", metadataBody, wantMetadata)
	}
}

func TestApplicationFailureReportsAppliedStage(t *testing.T) {
	dataDirectory := t.TempDir()
	metadataPath := filepath.Join(t.TempDir(), "metadata.txt")
	content, err := json.Marshal(settings{Hooks: hooks{Outcome: []string{
		"printf '%s' {{shellquote .Stage}}'|'{{shellquote .Outcome}} > " + shellQuote(metadataPath),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDirectory, settingsFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := AttemptMetadata{
		SessionID:       "session",
		CorrelationID:   "chain",
		CallID:          "call",
		Attempt:         1,
		ToolName:        "functions.hpatch",
		EmittedPayload:  "new note.txt\ntype \"ok\"\n",
		EvaluatedScript: "new note.txt\ntype \"ok\"\n",
	}
	_, err = finishHostChange(
		WithAttemptMetadata(t.Context(), metadata),
		dataDirectory,
		metadata.EvaluatedScript,
		HostTranslation{},
		"applied",
		errors.New("changing note.txt: permission denied"),
		true,
	)
	if err == nil {
		t.Fatal("application failure succeeded")
	}
	got, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "applied|failed" {
		t.Fatalf("outcome metadata = %q", got)
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
