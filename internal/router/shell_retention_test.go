package router

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellRetentionLifecycle(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "shell")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := &hpatchProxy{shellDirectory: directory, shellSessions: make(map[string]struct{})}
	const sessionID = "019fe9b0-c75b-7f92-9ce0-1580bca5e4ab"
	originalTTL := shellArtifactTTL
	shellArtifactTTL = 10 * time.Millisecond
	t.Cleanup(func() { shellArtifactTTL = originalTTL })

	reference, retained := proxy.retainShell(sessionID, "call-id", "printf ok\n")
	if !retained || reference != "@shell/call-id" {
		t.Fatalf("retention = %q, %v", reference, retained)
	}
	path := filepath.Join(proxy.shellSessionDirectory(sessionID), "call-id")
	if content, err := os.ReadFile(path); err != nil || string(content) != "printf ok\n" {
		t.Fatalf("retained content = %q, %v", content, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("retained script did not expire")
		}
		time.Sleep(time.Millisecond)
	}

	if _, retained := proxy.retainShell(sessionID, "call-next", "printf next\n"); !retained {
		t.Fatal("second script was not retained")
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(proxy.shellSessionDirectory(sessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory survived close: %v", err)
	}
}

func TestHPatchAppliesRetainedShellArtifactDirectly(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, newInProcessHPatchTranslator(""))
	reference, retained := proxy.retainShell(transform.sessionID, "call-shell", "printf ok\n")
	if !retained {
		t.Fatal("shell script was not retained")
	}
	history, err := transform.translate("call-edit", "in "+reference+"\ntype 1:ef86 \"printf fixed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proxy.shellSessionDirectory(transform.sessionID), "call-shell")
	if content, err := os.ReadFile(path); err != nil || string(content) != "printf fixed\n" {
		t.Fatalf("applied content = %q, %v", content, err)
	}
	if !history.applied || history.patch != "" || strings.Contains(history.carrierInput(), "apply_patch") || strings.Contains(history.carrierInput(), "exec_command") {
		t.Fatalf("retained edit used host patch carrier: %+v, %s", history, history.carrierInput())
	}
}
func TestShellResultMetadata(t *testing.T) {
	carrier, err := workerExecInputWithParams(
		"shell",
		[]string{"bash", "printf ok"},
		nil,
		map[string]json.RawMessage{"retained": mustMarshalJSON(false)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(carrier, `"retained":false`) {
		t.Fatalf("carrier omitted retention result: %s", carrier)
	}
}
