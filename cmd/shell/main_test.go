//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusing/hpatch/internal/shellruntime"
)

func TestShellHelperReadsAndRunsCurrentRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv(shellruntime.RuntimeDirectoryEnvironment, root)
	executable := filepath.Join(root, "router")
	if err := os.WriteFile(executable, []byte(`#!/bin/sh
for argument do
  printf 'argument=%s\n' "$argument"
done
`), 0o700); err != nil {
		t.Fatal(err)
	}
	const threadID = "thread-one"
	runtimePath, err := shellruntime.Path(root, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, runtimePath); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--", "/usr/bin/bash", "printf ok")
	command.Env = append(os.Environ(),
		"HPATCH_SHELL_HELPER_PROCESS=1",
		shellruntime.ThreadIDEnvironment+"="+threadID,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("shell helper: %v\n%s", err, output)
	}
	want := strings.Join([]string{
		"argument=/usr/bin/bash",
		"argument=printf ok",
		"",
	}, "\n")
	if string(output) != want {
		t.Fatalf("runtime invocation = %q, want %q", output, want)
	}
}

func TestShellHelperRejectsInvalidThreadID(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--")
	command.Env = append(os.Environ(),
		"HPATCH_SHELL_HELPER_PROCESS=1",
		shellruntime.RuntimeDirectoryEnvironment+"="+t.TempDir(),
		shellruntime.ThreadIDEnvironment+"=nested/thread",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("shell helper succeeded for invalid thread ID; output %q", output)
	}
	if !strings.Contains(string(output), "single filename component") {
		t.Fatalf("shell helper error = %q, want invalid thread ID error", output)
	}
}

func TestShellHelperProcess(t *testing.T) {
	if os.Getenv("HPATCH_SHELL_HELPER_PROCESS") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	os.Args = append([]string{"shell"}, os.Args[separator+1:]...)
	main()
}
