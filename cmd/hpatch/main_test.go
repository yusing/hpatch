package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestGainDoesNotRequireWorkingDirectory(t *testing.T) {
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr)
	want := "estimated hpatch output tokens: 0\nestimated apply_patch output tokens: 0\nestimated reduction: 0.0%\n"
	if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}
