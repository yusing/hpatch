package router

import (
	"bytes"
	"encoding/json"
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

func TestShellRunnerJoinsIndependentBackgroundJobsAndPreservesFailure(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	for _, interpreter := range []string{"bash", "sh"} {
		t.Run(interpreter, func(t *testing.T) {
			t.Chdir(t.TempDir())
			// Each job needs the other's marker, so sequential execution fails
			// instead of passing a timing-only assertion. Polling is bounded.
			script := `
check_one() {
	: > first.started
	attempt=0
	until test -f second.started; do
		attempt=$((attempt + 1))
		test "$attempt" -lt 250 || return 90
		sleep 0.02
	done
	printf 'first finished\n'
	return 7
}
check_two() {
	: > second.started
	attempt=0
	until test -f first.started; do
		attempt=$((attempt + 1))
		test "$attempt" -lt 250 || return 91
		sleep 0.02
	done
	printf 'second finished\n'
}
check_one &
first_check_pid=$!
check_two &
second_check_pid=$!
checks_status=0
wait "$first_check_pid" || checks_status=$?
wait "$second_check_pid" || checks_status=$?
printf 'joined both\n'
exit "$checks_status"
`
			stdout, stderr, exitCode := runShellWorkerTest(t, registry, interpreter, nil, script, nil)
			if exitCode != 7 || stderr != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if !strings.HasSuffix(stdout, "joined both\n") ||
				strings.Count(stdout, "first finished\n") != 1 ||
				strings.Count(stdout, "second finished\n") != 1 {
				t.Fatalf("jobs were not both joined: %q", stdout)
			}
		})
	}
}

func TestShellRunnerUsesInterpreterBasenameForLanguageVariant(t *testing.T) {
	registry := sharedProxyTestRegistry(t)

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
	registry := sharedProxyTestRegistry(t)
	for _, name := range []string{"hcat", "hgrep", "hsymbol", "inspect_file"} {
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
			"cd nested\nhcat 'space name.txt' 1:1 | { read -r row; printf 'row:%s\\n' \"$row\"; }\nhcat missing 2>/dev/null || printf recovered",
			nil,
		)
		if exitCode != 0 || stdout != "row:1:8ed3 alpha\nrecovered" || stderr != "" {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", interpreter, exitCode, stdout, stderr)
		}
	}
}

func TestShellRunnerQueriesCurrentSymbol(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	workspace := t.TempDir()
	caller := t.TempDir()
	bin := t.TempDir()
	source := filepath.Join(workspace, "source.go")
	if err := os.WriteFile(source, []byte("package p\nfunc Pick() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", source+":2:6-10")
	if err := os.WriteFile(filepath.Join(bin, "gopls"), []byte(resolver), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(caller)
	alias := filepath.Join(caller, "project")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runShellWorkerTest(t, registry, "/bin/sh", nil,
		fmt.Sprintf("hsymbol --workspace %q refs source.go 2 Pick", alias), nil)
	if status != 0 || !strings.Contains(stdout, fmt.Sprintf("%q:2:", source)) ||
		!strings.Contains(stdout, "func Pick() {}") || !strings.Contains(stderr, "(current snapshot)") {
		t.Fatalf("semantic lookup: stdout=%q stderr=%q exit=%d", stdout, stderr, status)
	}
}

func TestShellRunnerInspectsOutsideFile(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "value.json")
	if err := os.WriteFile(outside, []byte(`{"answer":{"nested":42},"other":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	stdout, stderr, status := runShellWorkerTest(t, registry, "/bin/sh", nil,
		fmt.Sprintf("inspect_file %q", outside), nil)
	var result struct {
		OK   bool `json:"ok"`
		Data struct {
			Outline []struct {
				Pointer string `json:"pointer"`
			} `json:"outline"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if status != 0 || stderr != "" || !result.OK || len(result.Data.Outline) != 4 ||
		result.Data.Outline[1].Pointer != "/answer" {
		t.Fatalf("inspection: stdout=%s stderr=%q exit=%d", stdout, stderr, status)
	}
}

func TestShellRunnerReadsRetainedHCatArtifact(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	runtimeDirectory := t.TempDir()
	retainedDirectory := filepath.Join(runtimeDirectory, "mekugi-scripts-thread-id")
	if err := os.MkdirAll(retainedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retainedDirectory, "call-id"), []byte("first\nretained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEKUGI_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("CODEX_THREAD_ID", "thread-id")

	stdout, stderr, exitCode := runShellWorkerTest(
		t,
		registry,
		"/bin/sh",
		nil,
		"hcat @shell/call-id 2:2",
		nil,
	)
	preview, previewErr, previewExit := runShellWorkerTest(t, registry, "/bin/sh", nil,
		"hcat --max-tokens 100 --preview-bytes 3 @shell/call-id 2:2", nil)
	tail, tailErr, tailExit := runShellWorkerTest(t, registry, "/bin/sh", nil,
		"hcat --tail --max-tokens 10 @shell/call-id", nil)
	lineTail, lineTailErr, lineTailExit := runShellWorkerTest(t, registry, "/bin/sh", nil,
		"hcat --tail -n 1 @shell/call-id", nil)
	if lineTailExit != 1 || lineTail != "2:ca67 retained\n" || !strings.Contains(lineTailErr, "1-line limit") {
		t.Fatalf("retained line tail: stdout=%q stderr=%q exit=%d", lineTail, lineTailErr, lineTailExit)
	}

	if tailExit != 1 || tail != "2:ca67 retained\n" || !strings.Contains(tailErr, "output incomplete") {
		t.Fatalf("retained tail: stdout=%q stderr=%q exit=%d", tail, tailErr, tailExit)
	}
	if previewExit != 0 || previewErr != "" || preview != "{\"row\":\"2:ca67\",\"preview\":\"ret\",\"source_bytes\":8,\"omitted_bytes\":5}\n" {
		t.Fatalf("retained preview: stdout=%q stderr=%q exit=%d", preview, previewErr, previewExit)
	}
	if exitCode != 0 || stdout != "2:ca67 retained\n" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestShellRunnerConfinesRetainedHCatArtifact(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
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
	threadDirectory := filepath.Join(runtimeDirectory, "mekugi-scripts-thread-id")
	if err := os.MkdirAll(threadDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEKUGI_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("CODEX_THREAD_ID", "thread-id")
	assertRejected := func(script string) {
		t.Helper()
		stdout, _, exitCode := runShellWorkerTest(t, registry, "/bin/sh", nil, script, nil)
		if exitCode == 0 || strings.Contains(stdout, sentinel) {
			t.Fatalf("%q: exit %d, stdout %q", script, exitCode, stdout)
		}
	}
	for _, reference := range []string{
		"@shell/../mekugi-scripts-other/call-id",
		"@shell//absolute",
		"@shell/.runtime",
	} {
		assertRejected("hcat " + reference)
	}
	t.Setenv("MEKUGI_RUNTIME_DIR", "relative-runtime")
	stdout, stderr, exitCode := runShellWorkerTest(t, registry, "/bin/sh", nil, "hcat @shell/call-id", nil)
	if exitCode == 0 || stdout != "" || !strings.Contains(stderr, "MEKUGI_RUNTIME_DIR must be an absolute path") {
		t.Fatalf("relative runtime: exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	t.Setenv("MEKUGI_RUNTIME_DIR", runtimeDirectory)

	if err := os.Symlink(outsideDirectory, filepath.Join(runtimeDirectory, "mekugi-scripts-thread-link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "thread-link")
	assertRejected("hcat @shell/call-id")

	artifactLinkDirectory := filepath.Join(runtimeDirectory, "mekugi-scripts-artifact-link")
	if err := os.MkdirAll(artifactLinkDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDirectory, "call-id"), filepath.Join(artifactLinkDirectory, "call-id")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "artifact-link")
	assertRejected("hcat @shell/call-id")
}

func TestShellRunnerPreservesStdinAndExternalCommands(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
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
	registry := sharedProxyTestRegistry(t)
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
