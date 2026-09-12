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

const shellPTYHelperEnvironment = "MEKUGI_SHELL_PTY_HELPER"

func TestShellRunnerExternalPipelineReadsPTY(t *testing.T) {
	if mode := os.Getenv(shellPTYHelperEnvironment); mode != "" {
		prefix := ""
		if mode == "hrun" {
			prefix = "hrun --max-tokens 100 --tail -- "
		}
		registry, err := buildToolRegistry(t.Context(), t.TempDir(), testMekugiToolDescription, false)
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
			`printf 'stream\n' | `+prefix+`sh -c 'IFS= read -r stream; IFS= read -r terminal </dev/tty; printf "pty:%s:%s" "$stream" "$terminal"'`,
			os.Stdin,
		)
		_, _ = fmt.Fprintf(os.Stdout, "MEKUGI_PTY_RESULT=%d|%s|%s\n", exitCode, stdout, stderr)
		return
	}

	for _, mode := range []string{"direct", "hrun"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShellRunnerExternalPipelineReadsPTY$")
			command.Env = append(os.Environ(), shellPTYHelperEnvironment+"="+mode)
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
			if !strings.Contains(string(output), "MEKUGI_PTY_RESULT=0|pty:stream:hello|") {
				t.Fatalf("PTY shell output = %q", output)
			}
		})
	}
}
