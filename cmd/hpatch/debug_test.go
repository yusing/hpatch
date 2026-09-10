package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugPathsPrintAfterCodexExit(t *testing.T) {
	for _, exit := range []string{"0", "23"} {
		t.Run(exit, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("TMPDIR", directory)
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(directory, "codex"), []byte("#!/bin/sh\necho codex-finished >&2\nexit "+exit+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := os.CreateTemp(directory, "stderr-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = output.Close() })
			old := os.Stderr
			os.Stderr = output
			defer func() { os.Stderr = old }()
			code, err := wrapCodex(context.Background(), []string{"--debug", "--mode", "passthrough"}, nil)
			if err != nil || (exit == "0" && code != 0) || (exit == "23" && code != 23) {
				t.Fatalf("wrap exit: %d, %v", code, err)
			}
			data, err := os.ReadFile(output.Name())
			if err != nil {
				t.Fatal(err)
			}
			before, after, ok := strings.Cut(string(data), "codex-finished\n")
			if !ok || strings.Contains(before, "hpatch debug:") || strings.Count(after, "hpatch debug: ") != 4 {
				t.Fatalf("debug paths did not print only after child exit: %q", data)
			}
			for line := range strings.Lines(after) {
				path := strings.TrimSuffix(strings.TrimPrefix(line, "hpatch debug: "), "\n")
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
