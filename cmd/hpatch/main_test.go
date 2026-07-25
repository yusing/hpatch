package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestGainDoesNotRequireWorkingDirectory(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr)
	want := "estimated hpatch output tokens: 0\nestimated apply_patch output tokens: 0\nestimated reduction: 0.0%\n"
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
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
		{name: "top-level help", args: []string{"--help"}, want: helpText, wantFragment: "hpatch translate < SCRIPT"},
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
		"hpatch < SCRIPT",
		"hpatch translate < SCRIPT",
		"hpatch gain",
		"standard input",
		"bsel \"START\" \"END\"",
		"rsel START:END",
		"native non-PTY stdin field",
		"Do not use Python",
		"current selection when one exists",
		"current cursor to end-of-file",
		"never wraps to the file beginning",
		"preserves the selected final",
	} {
		if !strings.Contains(helpText, fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
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
