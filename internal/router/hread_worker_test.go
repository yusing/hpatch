package router

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRunHReadWorkerUsesTrustedWorkspaceAndExactGrammarInput(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "path with spaces.txt"), []byte("alpha\nbeta\r\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	t.Setenv(hreadWorkerEnvironment, "1")

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHReadWorker(
		t.Context(),
		[]string{`"path with spaces.txt" 2:3`},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	if got, want := stdout.String(), "f44e: beta\nbe9d: gamma\n"; got != want {
		t.Fatalf("worker output = %q, want %q", got, want)
	}
}

func TestRunHReadWorkerReturnsConciseFailures(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	t.Setenv(hreadWorkerEnvironment, "1")

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing input", want: "expected exactly one"},
		{name: "missing file", args: []string{`"missing.txt"`}, want: "missing.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, exitCode := RunHReadWorker(t.Context(), test.args, &stdout, &stderr)
			if !handled || exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("worker diagnostic is not concise: %q", stderr.String())
			}
		})
	}
}

func TestRunHReadWorkerDefersAbsolutePathPermissionToExecSandbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absolute.txt")
	if err := os.WriteFile(path, []byte("outside cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv(hreadWorkerEnvironment, "1")

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHReadWorker(t.Context(), []string{strconv.Quote(path)}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "outside cwd") {
		t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
	}
}

func TestRunHReadWorkerIgnoresNormalRouterInvocation(t *testing.T) {
	t.Setenv(hreadWorkerEnvironment, "")
	handled, exitCode := RunHReadWorker(t.Context(), []string{"--help"}, &bytes.Buffer{}, &bytes.Buffer{})
	if handled || exitCode != 0 {
		t.Fatalf("normal invocation = handled %t, exit %d", handled, exitCode)
	}
}

func TestHReadWrapperExecutesPrivateWorkerEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("session wrapper is a POSIX shell script")
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "hpatch-router")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/hpatch-router")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hpatch-router: %v\n%s", err, output)
	}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "line's name.txt"), []byte("alpha\r\nomega"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapperDirectory, err := createHReadWrapperForExecutable(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanupHReadWrapper(wrapperDirectory); err != nil {
			t.Error(err)
		}
	})
	wrapperPath := filepath.Join(wrapperDirectory, hreadWrapperName)
	info, err := os.Stat(wrapperPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("wrapper mode = %o, want 700", info.Mode().Perm())
	}
	if relative, err := filepath.Rel(workspace, wrapperPath); err == nil && filepath.IsLocal(relative) {
		t.Fatalf("wrapper was created in the user worktree: %s", wrapperPath)
	}

	command := exec.CommandContext(
		t.Context(),
		"/bin/sh",
		"-c",
		shellSingleQuote(wrapperPath)+" "+shellSingleQuote(`"line's name.txt" 1:999`),
	)
	command.Dir = workspace
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute wrapper: %v\n%s", err, output)
	}
	if got, want := string(output), "8ed3: alpha\n304b: omega\n"; got != want {
		t.Fatalf("wrapper output = %q, want %q", got, want)
	}
}

func TestHReadWrapperCleanupFollowsProxyLifetime(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	wrapperDirectory := transform.hreadWrapperDirectory
	if _, err := os.Stat(filepath.Join(wrapperDirectory, hreadWrapperName)); err != nil {
		t.Fatal(err)
	}
	transform.Close()
	if _, err := os.Stat(wrapperDirectory); err != nil {
		t.Fatalf("proxy wrapper was released with a turn: %v", err)
	}

	secondWrapper, err := proxy.ensureHReadWrapper()
	if err != nil {
		t.Fatal(err)
	}
	if secondWrapper != wrapperDirectory {
		t.Fatalf("reused wrapper = %q, want %q", secondWrapper, wrapperDirectory)
	}
	if _, err := os.Stat(wrapperDirectory); err != nil {
		t.Fatalf("proxy wrapper was released with second turn: %v", err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wrapperDirectory); !os.IsNotExist(err) {
		t.Fatalf("proxy shutdown left wrapper behind: %v", err)
	}
}
