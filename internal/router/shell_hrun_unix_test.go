//go:build unix

package router

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHRunCancellationDrainsDescendantPipes(t *testing.T) {
	registry := sharedProxyTestRegistry(t)
	directory := t.TempDir()
	t.Chdir(directory)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		_, status := RunToolPluginWorker(ctx, registry.shellRuntime,
			[]string{"bash", `hrun --max-tokens 20 --tail -- sh -c 'sleep 30 & printf ready > ready; wait'`},
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
