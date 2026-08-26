package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yusing/hpatch/internal/shellruntime"
)

func TestShellRetentionLifecycle(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "shell")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := &hpatchProxy{shellDirectory: directory, shellSessions: make(map[string]struct{})}
	const sessionID = "019fe9b0-c75b-7f92-9ce0-1580bca5e4ab"
	sessionDirectory := filepath.Dir(shellruntime.Path(directory, sessionID))
	originalTTL := shellArtifactTTL
	shellArtifactTTL = 10 * time.Millisecond
	t.Cleanup(func() { shellArtifactTTL = originalTTL })

	reference, retained := proxy.retainShell(sessionDirectory, "call-id", "printf ok\n")
	if !retained || reference != "@shell/call-id" {
		t.Fatalf("retention = %q, %v", reference, retained)
	}
	path := filepath.Join(sessionDirectory, "call-id")
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

	if _, retained := proxy.retainShell(sessionDirectory, "call-next", "printf next\n"); !retained {
		t.Fatal("second script was not retained")
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory survived close: %v", err)
	}
}

func TestHPatchAppliesRetainedShellArtifactDirectly(t *testing.T) {
	dataDirectory := t.TempDir()
	outcomePath := filepath.Join(t.TempDir(), "outcome.txt")
	settings := `{"hooks":{"outcome":["printf '%s' {{.EmittedBytes}}'|'{{.EvaluatedBytes}} > ` + shellQuoteArgument(outcomePath) + `"]}}`
	if err := os.WriteFile(filepath.Join(dataDirectory, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	transform, proxy, _, _ := newHPatchTestTransform(
		t,
		newInProcessHPatchTranslator(dataDirectory),
	)
	reference, retained := proxy.retainShell(transform.shellDirectory, "call-shell", "printf ok\n")
	if !retained {
		t.Fatal("shell script was not retained")
	}
	emitted := "in " + reference + "\ntype 1:ef86 \"printf @shell/fixed\"\n"
	evaluated := "in call-shell\ntype 1:ef86 \"printf @shell/fixed\"\n"
	history, err := transform.translate("call-edit", emitted, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(transform.shellDirectory, "call-shell")
	if content, err := os.ReadFile(path); err != nil || string(content) != "printf @shell/fixed\n" {
		t.Fatalf("applied content = %q, %v", content, err)
	}
	if !history.applied || history.patch != "" || strings.Contains(history.carrierInput(), "apply_patch") || strings.Contains(history.carrierInput(), "exec_command") {
		t.Fatalf("retained edit used host patch carrier: %+v, %s", history, history.carrierInput())
	}
	got, err := os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d|%d", len(emitted), len(evaluated))
	if string(got) != want {
		t.Fatalf("outcome byte counts = %q, want %q", got, want)
	}
}

func TestHPatchTreatsShellArtifactLiteralAsContent(t *testing.T) {
	const script = "in /tmp/repro.txt\ntype 1:6db7 \"literal @shell/ marker\"\n"
	calls := 0
	translator := hpatchTranslatorFunc(func(_ context.Context, _ string, gotScript string) ([]byte, error) {
		calls++
		if gotScript != script {
			t.Fatalf("script = %q", gotScript)
		}
		return []byte(testTranslatedPatch), nil
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	history, err := transform.translate("call-edit", script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || history.applied {
		t.Fatalf("translations = %d, applied = %v", calls, history.applied)
	}
}

func TestShellResultMetadata(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	shell, ok := proxy.registry.contribution("shell")
	if !ok {
		t.Fatal("shell contribution is unavailable")
	}
	carrier, err := proxy.registry.execCarrierInput(
		shell,
		[]string{"bash", "printf ok"},
		"",
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
