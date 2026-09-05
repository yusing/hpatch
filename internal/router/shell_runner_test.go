package router

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func runShellWorkerTest(
	t *testing.T,
	registry *toolRegistry,
	interpreter string,
	interpreterArguments []string,
	script string,
	stdin *os.File,
) (stdout, stderr string, exitCode int) {
	t.Helper()
	var stdoutBuffer, stderrBuffer bytes.Buffer
	workerArguments := []string{interpreter}
	workerArguments = append(workerArguments, interpreterArguments...)
	workerArguments = append(workerArguments, script)
	handled, exitCode := RunToolPluginWorker(
		t.Context(),
		registry.shellRuntime,
		workerArguments,
		stdin,
		&stdoutBuffer,
		&stderrBuffer,
	)
	if !handled {
		t.Fatal("direct shell worker was not handled")
	}
	return stdoutBuffer.String(), stderrBuffer.String(), exitCode
}

func TestShellRunnerUsesInterpreterBasenameForLanguageVariant(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})

	for _, interpreter := range []string{"bash", "/usr/bin/bash"} {
		t.Run(interpreter, func(t *testing.T) {
			stdout, stderr, exitCode := runShellWorkerTest(
				t,
				registry,
				interpreter,
				nil,
				"values=(zero one); printf '%s' \"${values[1]}\"",
				nil,
			)
			if exitCode != 0 || stdout != "one" || stderr != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
		})
	}

	for _, interpreter := range []string{"sh", "/bin/sh"} {
		t.Run(interpreter+" POSIX", func(t *testing.T) {
			stdout, stderr, exitCode := runShellWorkerTest(
				t,
				registry,
				interpreter,
				nil,
				"value=posix; printf '%s' \"$value\"",
				nil,
			)
			if exitCode != 0 || stdout != "posix" || stderr != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
		})
		t.Run(interpreter+" rejects Bash", func(t *testing.T) {
			_, stderr, exitCode := runShellWorkerTest(
				t,
				registry,
				interpreter,
				nil,
				"values=(zero one)",
				nil,
			)
			if exitCode == 0 || !strings.Contains(stderr, "feature") {
				t.Fatalf("exit %d, stderr %q", exitCode, stderr)
			}
		})
	}

	stdout, stderr, exitCode := runShellWorkerTest(
		t,
		registry,
		"/usr/bin/bash",
		[]string{"-u", "--", "zero", "one"},
		"printf '%s' \"$2\"",
		nil,
	)
	if exitCode != 0 || stdout != "one" || stderr != "" {
		t.Fatalf("argument handling: exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestShellRunnerEvaluatesPrivateToolsWithoutFrontends(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"hread", "hgrep", "hsymbol", "inspect_file"} {
		if wrapper, ok := registry.wrapper(name); ok {
			t.Fatalf("private tool %q unexpectedly has wrapper %q", name, wrapper)
		}
	}

	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "space name.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	t.Setenv("PATH", "/usr/bin:/bin")

	for _, interpreter := range []string{"bash", "/bin/sh"} {
		stdout, stderr, exitCode := runShellWorkerTest(
			t,
			registry,
			interpreter,
			nil,
			"cd nested\nhread 'space name.txt' 1:1 | { read -r row; printf 'row:%s\\n' \"$row\"; }\nhread missing 2>/dev/null || printf recovered",
			nil,
		)
		if exitCode != 0 || stdout != "row:1:8ed3 alpha\nrecovered" || stderr != "" {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", interpreter, exitCode, stdout, stderr)
		}
	}
}

func TestShellRunnerReadsRetainedHReadArtifact(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	runtimeDirectory := t.TempDir()
	retainedDirectory := filepath.Join(runtimeDirectory, "hpatch-thread-id", "scripts")
	if err := os.MkdirAll(retainedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retainedDirectory, "call-id"), []byte("first\nretained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("CODEX_THREAD_ID", "thread-id")

	stdout, stderr, exitCode := runShellWorkerTest(
		t,
		registry,
		"/bin/sh",
		nil,
		"hread @shell/call-id 2:2",
		nil,
	)
	if exitCode != 0 || stdout != "2:ca67 retained\n" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestShellRunnerConfinesRetainedHReadArtifact(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	runtimeDirectory := t.TempDir()
	outsideDirectory := t.TempDir()
	sentinel := "outside-retained-sentinel"
	if err := os.Mkdir(filepath.Join(outsideDirectory, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join(outsideDirectory, "call-id"),
		filepath.Join(outsideDirectory, "scripts", "call-id"),
	} {
		if err := os.WriteFile(name, []byte(sentinel+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	threadDirectory := filepath.Join(runtimeDirectory, "hpatch-thread-id")
	if err := os.MkdirAll(filepath.Join(threadDirectory, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("CODEX_THREAD_ID", "thread-id")
	assertRejected := func(script string) {
		t.Helper()
		stdout, _, exitCode := runShellWorkerTest(t, registry, "/bin/sh", nil, script, nil)
		if exitCode == 0 || strings.Contains(stdout, sentinel) {
			t.Fatalf("%q: exit %d, stdout %q", script, exitCode, stdout)
		}
	}
	for _, reference := range []string{
		"@shell/../hpatch-other/scripts/call-id",
		"@shell//absolute",
		"@shell/.runtime",
	} {
		assertRejected("hread " + reference)
	}
	t.Setenv("HPATCH_RUNTIME_DIR", "relative-runtime")
	stdout, stderr, exitCode := runShellWorkerTest(t, registry, "/bin/sh", nil, "hread @shell/call-id", nil)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "HPATCH_RUNTIME_DIR must be an absolute path") {
		t.Fatalf("relative runtime: exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	t.Setenv("HPATCH_RUNTIME_DIR", runtimeDirectory)

	if err := os.Symlink(outsideDirectory, filepath.Join(runtimeDirectory, "hpatch-thread-link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "thread-link")
	assertRejected("hread @shell/call-id")

	scriptsLinkThread := filepath.Join(runtimeDirectory, "hpatch-scripts-link")
	if err := os.Mkdir(scriptsLinkThread, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDirectory, "scripts"), filepath.Join(scriptsLinkThread, "scripts")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "scripts-link")
	assertRejected("hread @shell/call-id")

	artifactLinkDirectory := filepath.Join(runtimeDirectory, "hpatch-artifact-link", "scripts")
	if err := os.MkdirAll(artifactLinkDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDirectory, "call-id"), filepath.Join(artifactLinkDirectory, "call-id")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "artifact-link")
	assertRejected("hread @shell/call-id")
}

func TestShellRunnerPreservesStdinAndExternalCommands(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	inputPath := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(inputPath, []byte("stream\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()

	stdout, stderr, exitCode := runShellWorkerTest(
		t,
		registry,
		"/usr/bin/bash",
		nil,
		"read -r value\nprintf 'stdin:%s\\n' \"$value\"\nprintf external | tr a-z A-Z",
		input,
	)
	if exitCode != 0 || stdout != "stdin:stream\nEXTERNAL" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestShellRunnerBoundsAndValidatesOutput(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	manifest, err := readToolWorkerManifest(filepath.Join(registry.SnapshotDir, toolPluginManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	shell, ok := registry.contribution("shell")
	if !ok {
		t.Fatal("shell contribution is unavailable")
	}
	runtimeRoot := filepath.Join(registry.SnapshotDir, manifest.RuntimeRoot)

	started := time.Now()
	captureBudget := 16<<20 - len(shellOverflowDiagnostic) - 3
	execution, err := executeShellTool(
		t.Context(),
		manifest,
		runtimeRoot,
		&shell,
		[]string{"bash", fmt.Sprintf(
			`python3 -c 'import subprocess, sys; subprocess.Popen(["sleep", "10"]); sys.stdout.buffer.write(b"a"*%d + "😀".encode()); sys.stdout.buffer.flush()'`,
			captureBudget-1,
		)},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ExitCode != 1 || !strings.Contains(execution.Stderr, "interpreter output exceeds") ||
		len(execution.Stdout) != captureBudget-1 || !utf8.ValidString(execution.Stdout) ||
		len(execution.Stdout)+len(execution.Stderr) > 16<<20 || time.Since(started) >= 5*time.Second {
		t.Fatalf(
			"overflow: exit %d, stdout bytes %d, stderr %q",
			execution.ExitCode,
			len(execution.Stdout),
			execution.Stderr,
		)
	}

	execution, err = executeShellTool(
		t.Context(),
		manifest,
		runtimeRoot,
		&shell,
		[]string{"bash", `python3 -c 'import os; os.write(1, b"\xff")'`},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ExitCode != 1 || execution.Stdout != "" ||
		execution.Stderr != "shell: interpreter output is not UTF-8\n" {
		t.Fatalf(
			"UTF-8: exit %d, stdout %q, stderr %q",
			execution.ExitCode,
			execution.Stdout,
			execution.Stderr,
		)
	}
}
