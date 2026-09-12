//go:build unix

package router

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHRunCancellationDrainsDescendantPipes(t *testing.T) {
	testHRunCancellation(t, `hrun --max-tokens 20 --tail -- sh -c 'sleep 30 & printf ready > ready; wait'`)
}

func TestHRunLineCancellation(t *testing.T) {
	testHRunCancellation(t, `hrun -n 20 -- sh -c 'printf ready > ready; exec yes'`)
}

func testHRunCancellation(t *testing.T, script string) {
	t.Helper()

	registry := sharedProxyTestRegistry(t)
	directory := t.TempDir()
	t.Chdir(directory)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		_, status := RunToolPluginWorker(ctx, registry.shellRuntime,
			[]string{"bash", script},
			nil, io.Discard, io.Discard)
		finished <- status
	}()
	ticker := time.Tick(10 * time.Millisecond)
	for {
		if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("command exited before ready: %v", err)
		case <-ctx.Done():
			t.Fatal("command did not become ready")
		case <-ticker:
		}
	}
	cancel()
	select {
	case status := <-finished:
		if status == 0 {
			t.Fatal("canceled command succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation left descendant output pipes open")
	}
}

func TestShellRunnerClosedInspectionPipes(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.WriteFile("rows.txt", []byte(strings.Repeat(strings.Repeat("x", 80)+"\n", 20000)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"hrun -n 20000 -- cat rows.txt",
		"hcat -n 20000 rows.txt",
		"hrun --max-tokens 15500 -- cat rows.txt",
		"hcat --max-tokens 15500 rows.txt",
	} {
		for _, mode := range []string{"", "set -o pipefail\n", "set -eo pipefail\n"} {
			stdout, stderr, status := runShellWorkerTest(t, registry, "bash", nil,
				mode+command+" | head -n 1 >/dev/null\nprintf 'AFTER:%s' \"$?\"", nil)
			if mode == "set -eo pipefail\n" {
				if stdout != "" || status != 141 {
					t.Fatalf("%s %s: %q %q status=%d", mode, command, stdout, stderr, status)
				}
				continue
			}
			want := "AFTER:0"
			if mode != "" {
				want = "AFTER:141"
			}
			if stdout != want || status != 0 || strings.Contains(stderr, "broken pipe") {
				t.Fatalf("%s %s: %q %q status=%d", mode, command, stdout, stderr, status)
			}
		}
	}
}
