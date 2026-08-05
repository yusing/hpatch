package router

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunHReadWorkerUsesCompleteInputArgument(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "path with spaces.txt"), []byte("alpha\nbeta\r\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHReadWorker(
		t.Context(),
		hreadExecutableName,
		[]string{`"path with spaces.txt" 2:3`},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	if got, want := stdout.String(), "2:f44e beta\n3:be9d gamma\n"; got != want {
		t.Fatalf("worker output = %q, want %q", got, want)
	}
}

func TestRunHReadWorkerAcceptsSeparatePathAndRangeArguments(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "path with spaces.txt"), []byte("alpha\nbeta\r\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHReadWorker(
		t.Context(),
		hreadExecutableName,
		[]string{"path with spaces.txt", "2:3"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	if got, want := stdout.String(), "2:f44e beta\n3:be9d gamma\n"; got != want {
		t.Fatalf("worker output = %q, want %q", got, want)
	}
}

func TestRunHReadWorkerAcceptsCompleteBatchInput(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "alpha.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "beta.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	var stdout, stderr bytes.Buffer
	input := "\"alpha.txt\"\n\"missing.txt\"\n\"beta.txt\""
	handled, exitCode := RunHReadWorker(
		t.Context(),
		hreadExecutableName,
		[]string{input},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("worker = handled %t, exit %d, stderr %q", handled, exitCode, stderr.String())
	}
	for _, fragment := range []string{
		"==> \"alpha.txt\" <==\n1:8ed3 alpha\n",
		"==> \"missing.txt\" <==\nhread: reading ",
		"missing.txt:",
		"==> \"beta.txt\" <==\n1:f44e beta\n",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("worker output lacks %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestRunHReadWorkerReturnsConciseFailures(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)

	for _, test := range []struct {
		name    string
		args    []string
		want    string
		notWant string
	}{
		{name: "missing input", want: "expected PATH"},
		{name: "missing file", args: []string{`"missing.txt"`}, want: "missing.txt", notWant: "openat missing.txt"},
		{name: "too many arguments", args: []string{"file.txt", "1:2", "extra"}, want: "expected PATH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			handled, exitCode := RunHReadWorker(t.Context(), hreadExecutableName, test.args, &stdout, &stderr)
			if !handled || exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
			}
			if test.notWant != "" && strings.Contains(stderr.String(), test.notWant) {
				t.Fatalf("worker diagnostic contains internal wrapper %q: %q", test.notWant, stderr.String())
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("worker diagnostic is not concise: %q", stderr.String())
			}
		})
	}
}

func TestConciseHReadError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "filesystem failure",
			err: fmt.Errorf(
				"reading missing.txt: %w",
				&os.PathError{Op: "openat", Path: "missing.txt", Err: os.ErrNotExist},
			),
			want: "reading missing.txt: " + os.ErrNotExist.Error(),
		},
		{
			name: "nested generic failure",
			err:  fmt.Errorf("writing result: %w", fmt.Errorf("flush buffer: %w", io.ErrClosedPipe)),
			want: "writing result: " + io.ErrClosedPipe.Error(),
		},
		{
			name: "plain failure",
			err:  io.ErrUnexpectedEOF,
			want: io.ErrUnexpectedEOF.Error(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := conciseHReadError(test.err); got != test.want {
				t.Fatalf("conciseHReadError() = %q, want %q", got, test.want)
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

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunHReadWorker(t.Context(), hreadExecutableName, []string{fmt.Sprintf("%q", path)}, &stdout, &stderr)
	if !handled || exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "outside cwd") {
		t.Fatalf("worker = handled %t, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
	}
}

func TestRunHReadWorkerIgnoresNormalRouterInvocation(t *testing.T) {
	handled, exitCode := RunHReadWorker(t.Context(), "hpatch-router", []string{"--help"}, &bytes.Buffer{}, &bytes.Buffer{})
	if handled || exitCode != 0 {
		t.Fatalf("normal invocation = handled %t, exit %d", handled, exitCode)
	}
}

func TestHReadStartupSymlinkExecutesPrivateWorkerEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires symbolic-link support")
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

	hreadExecutable, err := ensureWorkerSymlinkInDirectory(binary, filepath.Dir(binary), hreadExecutableName)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ensureWorkerSymlinkInDirectory(binary, filepath.Dir(binary), hreadExecutableName); err != nil || second != hreadExecutable {
		t.Fatalf("second startup symlink = %q, %v; want %q", second, err, hreadExecutable)
	}
	linkInfo, err := os.Lstat(hreadExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("hread mode = %v, want symlink", linkInfo.Mode())
	}
	if target, err := os.Readlink(hreadExecutable); err != nil || target != binary {
		t.Fatalf("hread target = %q, %v; want %q", target, err, binary)
	}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "line's name.txt"), []byte("alpha\r\nomega"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), hreadExecutable, `"line's name.txt" 1:999`)
	command.Dir = workspace
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute hread: %v\n%s", err, output)
	}
	if got, want := string(output), "1:8ed3 alpha\n2:304b omega\n"; got != want {
		t.Fatalf("hread output = %q, want %q", got, want)
	}

	batchInput := "\"line's name.txt\" 2:2\n\"missing.txt\""
	command = exec.CommandContext(t.Context(), hreadExecutable, batchInput)
	command.Dir = workspace
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute batch hread: %v\n%s", err, output)
	}
	for _, fragment := range []string{
		"==> \"line's name.txt\" 2:2 <==\n2:304b omega\n",
		"==> \"missing.txt\" <==\nhread: reading ",
		"missing.txt:",
	} {
		if !strings.Contains(string(output), fragment) {
			t.Fatalf("batch hread output lacks %q:\n%s", fragment, output)
		}
	}
}

func TestHReadStartupSymlinkOutlivesProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires symbolic-link support")
	}
	executable := filepath.Join(t.TempDir(), "hpatch-router")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	hreadExecutable, err := ensureWorkerSymlinkInDirectory(executable, filepath.Dir(executable), hreadExecutableName)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(hreadExecutable); err != nil {
		t.Fatalf("proxy shutdown removed startup hread symlink: %v", err)
	}
}
