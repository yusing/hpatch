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

	"github.com/yusing/hpatch/internal/router/toolplugin"
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

func TestNonBashOutputTruncationPreservesExitStatus(t *testing.T) {
	commentaryBytes := len("Running the requested commands.")
	execution := toolplugin.ExecutionOutput{
		Stdout: strings.Repeat("x", toolplugin.ExecutionOutputBudgetBytes), ExitCode: 0,
	}
	got := truncateShellExecutionForCommentary(execution, commentaryBytes)
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d", got.ExitCode)
	}
	if !strings.HasSuffix(got.Stderr, shellCommentaryTruncationDiagnostic) {
		t.Fatalf("stderr = %q", got.Stderr)
	}
	if visible := len(got.Stdout) + len(got.Stderr) + commentaryBytes; visible > toolplugin.ExecutionOutputBudgetBytes {
		t.Fatalf("combined visible bytes = %d", visible)
	}
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
	captureBudget := 16<<20 - max(len(shellOverflowDiagnostic), len(shellCommentaryTruncationDiagnostic)) - 3
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
	commentaryBytes := len("Running the requested commands.") + len(shellCommentaryVisibleText(shellCommentaryEvent{
		Text: "Failed: Running the requested commands.", Reason: "shell evaluation failed",
	}))
	if execution.ExitCode != 1 || !strings.Contains(execution.Stderr, "interpreter output exceeds") ||
		!utf8.ValidString(execution.Stdout) ||
		len(execution.Stdout)+len(execution.Stderr)+commentaryBytes > 16<<20 || time.Since(started) >= 5*time.Second {
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
