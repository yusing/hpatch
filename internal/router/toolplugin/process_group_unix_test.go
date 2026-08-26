//go:build unix

package toolplugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInvokeCancellationTerminatesPluginProcessGroup(t *testing.T) {
	node, err := resolveNodeRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	pidPath := filepath.Join(directory, "child.pid")
	hostPath := filepath.Join(directory, "host.mjs")
	script := `import {spawn} from "node:child_process";
import {writeFileSync} from "node:fs";
const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {stdio: "ignore"});
writeFileSync(` + strconv.Quote(pidPath) + `, String(child.pid));
setInterval(() => {}, 1000);
`
	if err := os.WriteFile(hostPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		var response map[string]any
		result <- invoke(ctx, node, hostPath, "", "", nil, 1024, nil, nil, map[string]any{}, &response)
	}()

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		encoded, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(encoded)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("plugin child process did not start")
	}

	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("invoke cancellation error = %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("plugin child process %d survived cancellation", childPID)
}
