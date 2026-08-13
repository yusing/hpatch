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

func TestInformationalCommandsNeedNoEnvironmentOrStdin(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", "")

	tests := []struct {
		name         string
		args         []string
		want         string
		wantFragment string
	}{
		{name: "top-level help", args: []string{"--help"}, want: helpTextBase, wantFragment: "hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT"},
		{name: "translate help", args: []string{"translate", "--help"}, want: translateHelpText, wantFragment: "without modifying"},
		{name: "tool help", args: []string{"--tool-help"}, want: toolHelpText(), wantFragment: "HPATCH/2"},
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
	normalized := strings.Join(strings.Fields(helpTextBase), " ")
	for _, fragment := range []string{
		"hpatch [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch gain",
		"standard input",
		"type TARGET VALUE",
		"type- TARGET VALUE",
		"type+ TARGET VALUE",
		"empty target-bearing type value deletes",
		"LINE:HASH..LINE:HASH",
		"fixed <<PATCH frame",
		"unindented closing line must be exactly PATCH",
		"immutable baseline",
		"introduced content is not targetable",
		"Use hgrep to locate matching complete lines",
		"Use hread for surrounding or nonmatching context",
		"Issue every independent hread call",
		"submit every known related edit in one atomic script",
		"including related multiline declarations",
		"Split only when a later edit depends on validation",
		"Keep unrelated large <<PATCH values",
		"Prefer the smallest mutation that expresses the semantic change",
		"Successful final-state LINE:HASH rows are current references",
		"Multiple insertions",
		"Every requested match must exist",
		"preserve the target's final LF",
		"Translation always",
		"stable failure reasons",
		"Recovery is a router-only tool",
		"syntax-checked when Tree-sitter support is available",
		"Supported indentation corrections are automatic",
		"diagnostics begin with the operation",
		"hooks.error",
		"hooks.diagnose",
		"format_markdown",
		"shellquote",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}
}

func TestHelpDoesNotAdvertiseRemovedSelectors(t *testing.T) {
	for name, text := range map[string]string{"top-level": helpTextBase, "tool": toolHelpText(), "translate": translateHelpText} {
		for _, removed := range []string{"bsel", "sel LINE", "`sel` columns", "at most one syntax-sensitive multiline Go"} {
			if strings.Contains(text, removed) {
				t.Errorf("%s help still advertises removed selector %q", name, removed)
			}
		}
	}
}

func TestToolHelpGuidesAgentCommandChoice(t *testing.T) {
	help := toolHelpText()
	normalized := strings.Join(strings.Fields(help), " ")
	for _, required := range []string{
		"HPATCH/2 applies one complete target-bearing edit script atomically.",
		"`type-` inserts before",
		"`type+` inserts after",
		"fixed `<<PATCH` frame",
		"one immutable baseline",
		"not targetable in the same call",
		"submit every known related edit in one atomic script",
		"including related multiline declarations",
		"Split only when a later edit depends on validation",
		"Keep unrelated large `<<PATCH` values",
		"Prefer the smallest mutation that expresses the semantic change",
		"When a formatter owns formatting, alignment, or indentation",
		"add one struct field with one insertion",
		"indentation-sensitive languages such as Python",
		"target exact known current text without a row",
		"Multiple insertions at the same boundary render in script order.",
		"standalone CLI and ordinary hpatch grammar have no recovery mode",
		"syntax-checked when Tree-sitter support is available",
		"supported indentation corrections are automatic",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("tool help does not contain %q", required)
		}
	}
	for _, excluded := range []string{
		"Usage:",
		"hpatch gain",
		"--root",
		"--cwd",
		"Metrics:",
		"INDEX: COMMAND",
		": accept",
		"COMMAND.ROW",
		"matches literals only",
		"HPATCH/1",
		"prefer one range `type`",
		"<<TAG",
		"at most one syntax-sensitive multiline Go",
		"discard its saved references",
		"Use `hgrep` as replacement of `rg` or `grep`",
		"Use `hread` as replacement of `cat` or `sed`",
	} {
		if strings.Contains(help, excluded) {
			t.Fatalf("tool help contains unnecessary or inaccurate text %q", excluded)
		}
	}
	if !strings.HasPrefix(help, "HPATCH/2 applies one complete target-bearing edit script atomically.") {
		t.Fatal("command-choice guidance is not at the top of tool help")
	}
	if len(help) >= len(helpTextBase) {
		t.Fatalf("tool help exceeds CLI help: %d bytes versus %d-byte CLI help", len(help), len(helpTextBase))
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
			strings.NewReader("in main.go\ntype 1:5128 \"package graph\"\n"),
			&stdout,
			&stderr,
		)
		if exitCode != 0 || !strings.HasPrefix(stderr.String(), "in nested/main.go\n") {
			t.Fatalf("run(cwd %q) = exit %d, stdout %q, stderr %q", cwd, exitCode, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "refs 2 type nested/main.go\n") {
			t.Fatalf("translation report lacks final references: %q", stderr.String())
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

func TestTopLevelHelpDescribesTargetOperandConstraints(t *testing.T) {
	normalized := strings.Join(strings.Fields(helpTextBase), " ")
	for _, fragment := range []string{
		"verifies the named row first",
		"location changed while the referenced row remained unchanged",
		"exactly one matching baseline hash relocates it",
		"an absent or ambiguous hash rejects",
		"COUNT defaults to one",
		"non-overlapping",
		"Every requested match must exist",
		"anchored text target verifies its row",
		"unanchored text target searches the complete immutable baseline",
		"complete immutable baseline",
		"exact authored unanchored text",
		"use hread only when the target remains unknown",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}
	if strings.Contains(translateHelpText, "COUNT defaults") {
		t.Fatal("translate help duplicates top-level operand constraints")
	}
	if !strings.Contains(translateHelpText, "Run hpatch --help for editing, target, safety, and workflow guidance.") {
		t.Fatal("translate help does not point to top-level help")
	}
}

func TestHelpDoesNotLeakSourceTreeReferences(t *testing.T) {
	for _, output := range []string{helpTextBase, toolHelpText(), translateHelpText} {
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
	removedDirectory := t.TempDir()
	t.Chdir(removedDirectory)
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
