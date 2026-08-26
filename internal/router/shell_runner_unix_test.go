//go:build unix

package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const shellPTYHelperEnvironment = "HPATCH_SHELL_PTY_HELPER"

func TestShellRunnerExternalPipelineReadsPTY(t *testing.T) {
	if os.Getenv(shellPTYHelperEnvironment) == "1" {
		registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := registry.Close(); err != nil {
				t.Error(err)
			}
		})
		stdout, stderr, exitCode := runShellWorkerTest(
			t,
			registry,
			"bash",
			nil,
			`printf 'stream\n' | sh -c 'IFS= read -r stream; IFS= read -r terminal </dev/tty; printf "pty:%s:%s" "$stream" "$terminal"'`,
			os.Stdin,
		)
		_, _ = fmt.Fprintf(os.Stdout, "HPATCH_PTY_RESULT=%d|%s|%s\n", exitCode, stdout, stderr)
		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShellRunnerExternalPipelineReadsPTY$")
	command.Env = append(os.Environ(), shellPTYHelperEnvironment+"=1")
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if _, err := io.WriteString(terminal, "hello\n"); err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(terminal)
	waitErr := command.Wait()
	if ctx.Err() != nil {
		t.Fatalf("PTY shell command did not finish: %v", ctx.Err())
	}
	if readErr != nil && !errors.Is(readErr, syscall.EIO) {
		t.Fatal(readErr)
	}
	if waitErr != nil {
		t.Fatalf("PTY helper failed: %v\n%s", waitErr, output)
	}
	if !strings.Contains(string(output), "HPATCH_PTY_RESULT=0|pty:stream:hello|") {
		t.Fatalf("PTY shell output = %q", output)
	}
}
