package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGainDoesNotRequireWorkingDirectory(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr)
	wantPrefix := "output token estimates:\n"
	wantFragment := "\n\ncommand metrics:\n"
	if exitCode != 0 || !strings.HasPrefix(stdout.String(), wantPrefix) || !strings.Contains(stdout.String(), wantFragment) || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestRecordMetricsDoesNotRequireWorkingDirectory(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"record-metrics"}, strings.NewReader(`{"invocation":{}}`), &stdout, &stderr)
	if exitCode != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("record-metrics = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestInformationalCommandsNeedNoEnvironmentOrStdin(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", "")

	tests := []struct {
		name         string
		args         []string
		want         string
		wantFragment string
	}{
		{name: "top-level help", args: []string{"--help"}, want: helpText(true), wantFragment: "hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT"},
		{name: "translate help", args: []string{"translate", "--help"}, want: translateHelpText, wantFragment: "without modifying"},
		{name: "tool help", args: []string{"--tool-help"}, want: toolHelpText(true), wantFragment: "one free-form script"},
		{name: "version", args: []string{"--version"}, want: "hpatch devel\n", wantFragment: "hpatch devel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(test.args, errorReader{}, &stdout, &stderr)
			if exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantFragment) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), test.wantFragment)
			}
		})
	}
}

func TestTopLevelHelpDescribesCompletePublicSurface(t *testing.T) {
	for _, fragment := range []string{
		"hpatch [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch gain",
		"standard input",
		"bsel \"START\" \"END\"",
		"bsel_next \"START\" \"END\"",
		"rsel LINE_REF:LINE_REF",
		"functions.hpatch",
		"Build selectors against each existing file's immutable baseline.",
		"Invoke hpatch once per attempt; do not encode, shell-wrap, or route it",
		"immutable baseline",
		"Text introduced by an",
		"multiple insertions",
		"complete active-file baseline",
		"bsel_next searches inside the current baseline selection",
		"current baseline cursor to end-of-file",
		"never wraps",
		"ASCII space and tab runs match interchangeably",
		"preserves the selected final",
		"translate always emits root-relative paths",
		"caller-accounted hpatch",
		"absolute and relative selectors",
		"terminal failure reasons",
	} {
		if !strings.Contains(helpText(true), fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}
}

func TestToolHelpUsesFocusedLarkSyntaxAndAuthoritativeSemantics(t *testing.T) {
	help := toolHelpText(true)
	for _, required := range []string{
		"one free-form script",
		"rejected script changes nothing",
		"Lark syntax:",
		`?command: path_cmd | "rm" | sel | tsel | block | rsel | type_cmd | "del" | "dup"`,
		`path_cmd: ("in" | "new" | "mv") " " PATH`,
		`sel: "sel" " " line_ref " " POS ":" POS`,
		`tsel: "tsel" " " line_ref " " OCC " " HWS? STRING tsel_tail?`,
		`block: ("bsel" | "bsel_next") " " HWS? STRING HWS STRING HWS?`,
		`%import common.ESCAPED_STRING -> STRING`,
		"nonzero\n(start if positive, end if negative)",
		"nonempty ASCII space and tab runs",
		"The first in for an existing file captures an immutable baseline.",
		"Final-state report:",
		"multiple",
		"insertions at one baseline position",
		"up to three",
		"workspace-relative paths",
		"Parent directories for new",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("tool help does not contain %q", required)
		}
	}
	for _, excluded := range []string{
		"Usage:",
		"Agent workflow:",
		"hpatch gain",
		"--root",
		"--cwd",
		"Metrics:",
		toolHelpLineGrammarMarker,
		toolHelpRelativeHelpMarker,
	} {
		if strings.Contains(help, excluded) {
			t.Fatalf("tool help contains non-tool text %q", excluded)
		}
	}
	if len(help) >= len(helpText(true))/2 {
		t.Fatalf("tool help is not focused: %d bytes versus %d-byte CLI help", len(help), len(helpText(true)))
	}
}

func TestRelativeLineHelpCanBeDisabled(t *testing.T) {
	t.Setenv("HPATCH_DISABLE_RELATIVE_LINES", "1")
	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"--help"}, errorReader{}, &stdout, &stderr); exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	for _, relative := range []string{"LINE_REF", "+0", "HPATCH_DISABLE_RELATIVE_LINES"} {
		if strings.Contains(stdout.String(), relative) {
			t.Fatalf("disabled help contains relative-line text %q", relative)
		}
		if strings.Contains(toolHelpText(false), relative) {
			t.Fatalf("disabled tool help contains relative-line text %q", relative)
		}
	}
	for _, absolute := range []string{"sel LINE START:END", "tsel LINE OCCURRENCE", "rsel START:END"} {
		if !strings.Contains(stdout.String(), absolute) {
			t.Fatalf("disabled help does not contain %q", absolute)
		}
	}
}

func TestRootAndCWDOptionsTranslateRootRelativePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{"nested", filepath.Join(root, "nested")} {
		var stdout, stderr bytes.Buffer
		exitCode := run(
			[]string{"translate", "--root", root, "--cwd", cwd},
			strings.NewReader("in main.go\ntsel 1 1 \"package main\"\ntype \"package graph\"\n"),
			&stdout,
			&stderr,
		)
		if exitCode != 0 || !strings.HasPrefix(stderr.String(), "in nested/main.go ") {
			t.Fatalf("run(cwd %q) = exit %d, stdout %q, stderr %q", cwd, exitCode, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "*** Update File: nested/main.go\n") {
			t.Fatalf("translation for cwd %q is not root-relative:\n%s", cwd, stdout.String())
		}
	}
}

func TestWorkspaceOptionsRejectInvalidBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"--root", "relative"},
		{"--root", root, "--cwd", outside},
		{"--root", root, "--cwd", "escape"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(args, errorReader{}, &stdout, &stderr)
			if exitCode != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("run(%q) = exit %d, stdout %q, stderr %q", args, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestTopLevelHelpDescribesSelectionOperandConstraints(t *testing.T) {
	const constraints = "`tsel` occurrence must be nonzero: positive values count from the start, and\n  negative values count from the end. Its optional count must be a positive integer and\n  selects consecutive nonoverlapping occurrences, including intervening source text.\n  Both `bsel` and `bsel_next` anchors must be nonempty and different."
	if !strings.Contains(helpText(true), constraints) {
		t.Fatalf("help does not contain accepted operand constraints %q", constraints)
	}

	for _, fragment := range []string{
		"`tsel` occurrence must be nonzero",
		"positive values count from the start",
		"negative values count from the end",
		"optional count must be a positive integer",
		"including intervening source text",
		"Both `bsel` and `bsel_next` anchors must be",
		"anchors must be nonempty and different",
	} {
		if !strings.Contains(helpText(true), fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}

	if strings.Contains(translateHelpText, constraints) {
		t.Fatal("translate help duplicates top-level operand constraints")
	}
	if !strings.Contains(translateHelpText, "Run hpatch --help for the complete editing and agent workflow.") {
		t.Fatal("translate help does not point to top-level help")
	}
}

func TestHelpDoesNotLeakSourceTreeReferences(t *testing.T) {
	for _, output := range []string{helpText(true), toolHelpText(true), translateHelpText} {
		for _, stale := range []string{"doc/spec", "AGENT_INSTRUCTIONS.md"} {
			if strings.Contains(output, stale) {
				t.Fatalf("help contains source-tree reference %q", stale)
			}
		}
	}
}

func TestUnsupportedInformationalAliasesRemainErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tests := [][]string{
		{"-h"},
		{"help"},
		{"--help", "extra"},
		{"translate", "-h"},
		{"translate", "--help", "extra"},
		{"--tool-help", "extra"},
		{"translate", "--version"},
		{"gain", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(args, errorReader{}, &stdout, &stderr)
			if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "expected no arguments") {
				t.Fatalf("run(%q) = exit %d, stdout %q, stderr %q", args, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestInformationalWriteFailureIsReported(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run([]string{"--help"}, errorReader{}, errorWriter{}, &stderr)
	if exitCode != 1 || !strings.Contains(stderr.String(), "hpatch: writing help:") {
		t.Fatalf("run() = exit %d, stderr %q", exitCode, stderr.String())
	}
}

func removeCurrentDirectory(t *testing.T) {
	t.Helper()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restoring working directory: %v", err)
		}
	})

	removedDirectory := t.TempDir()
	if err := os.Chdir(removedDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removedDirectory); err != nil {
		t.Skipf("platform does not allow removing the current directory: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("stdin must not be read")
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
