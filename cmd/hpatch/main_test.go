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
	wantPrefix := "estimated effective hpatch output tokens: 0\nestimated apply_patch output tokens: 0\nestimated effective reduction: 0.0%\nestimated ineffective hpatch output tokens: 0\nestimated total hpatch output tokens: 0\nestimated overall reduction: 0.0%\ncommand metrics:\n"
	wantSuffix := "total      0            0       0.0%\n"
	if exitCode != 0 || !strings.HasPrefix(stdout.String(), wantPrefix) || !strings.HasSuffix(stdout.String(), wantSuffix) || stderr.Len() != 0 {
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
		{name: "top-level help", args: []string{"--help"}, want: helpText, wantFragment: "hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT"},
		{name: "translate help", args: []string{"translate", "--help"}, want: translateHelpText, wantFragment: "without modifying"},
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
		"rsel START:END",
		"functions.hpatch",
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
	} {
		if !strings.Contains(helpText, fragment) {
			t.Fatalf("help does not contain %q", fragment)
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
		if exitCode != 0 || stderr.Len() != 0 {
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
	const constraints = "`tsel` occurrence must be nonzero: positive values count from the start, and\n  negative values count from the end. Both `bsel` and `bsel_next` anchors must be\n  nonempty and different."
	if !strings.Contains(helpText, constraints) {
		t.Fatalf("help does not contain accepted operand constraints %q", constraints)
	}

	for _, fragment := range []string{
		"`tsel` occurrence must be nonzero",
		"positive values count from the start",
		"negative values count from the end",
		"Both `bsel` and `bsel_next` anchors must be",
		"anchors must be\n  nonempty and different",
	} {
		if !strings.Contains(helpText, fragment) {
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
	for _, output := range []string{helpText, translateHelpText} {
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
