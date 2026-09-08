//go:build unix

package toolplugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesRegexValidatorBeforeHostIsolation(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatal(err)
	}
	rg, err = filepath.Abs(rg)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "invocations")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	wrapper := "#!/bin/sh\nprintf invoked >> " + quote(marker) + "\nexec " + quote(rg) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "rg"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	snapshot, err := Load(t.Context(), t.TempDir(), filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Diagnostics) != 0 {
		t.Fatalf("declaration diagnostics = %v", snapshot.Diagnostics)
	}
	encoded, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 {
		t.Fatal("router-resolved validator was not invoked")
	}
}
