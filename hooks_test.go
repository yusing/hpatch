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

	script := "in note.txt\ntsel 1 1 \"missing\"\ntype \"replacement\"\n"
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
		"## Failed command\n\n    tsel 1 1 \"missing\"",
		"## Failure\n\n    occurrence 1 of \"missing\" not found on line 1",
		"## Diagnostic\n\n    hpatch: command 2, source line 2",
		"## Repair context",
		"line 1 does not contain the requested occurrence",
		">1 present words",
	} {
		if !strings.Contains(string(body), fragment) {
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
		if !strings.Contains(string(body), fragment) {
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

func TestErrorHooksShareOneTimeout(t *testing.T) {
	dataDirectory := t.TempDir()
	writeSettingsForTest(t, dataDirectory, []string{"sleep 10", "sleep 10"})
	sourceError := &commandError{Reason: reasonSyntax, Command: 1, Line: 1, Operation: "bad", Category: "syntax", Source: "bad", Message: "unknown command"}

	started := time.Now()
	errs := runErrorHooks(dataDirectory, sourceError, failureDiagnostic(sourceError.Error()), 20*time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runErrorHooks() took %s", elapsed)
	}
	if len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
		t.Fatalf("runErrorHooks() errors = %v", errs)
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
