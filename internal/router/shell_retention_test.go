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
	"testing/synctest"
	"time"
)

func newShellStorageTestProxy(t *testing.T) (*hpatchProxy, string) {
	t.Helper()
	proxy := &hpatchProxy{
		shellDirectory: t.TempDir(),
		shellSessions:  make(map[string]*shellSession),
		registry:       &toolRegistry{shellRuntime: "/unused-test-worker"},
	}
	t.Cleanup(func() { _ = proxy.Close() })
	directory, err := proxy.storeShellRuntime("thread-id")
	if err != nil {
		t.Fatal(err)
	}
	return proxy, directory
}

func TestShellRetentionRejectsUnsafeCallIDsAndExistingFiles(t *testing.T) {
	proxy, directory := newShellStorageTestProxy(t)
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", ".runtime", "../outside", "a/b", `a\b`, "bad\x00id", outside, "linked"} {
		if ref, retained := proxy.retainShell(directory, id, "overwritten"); retained || ref != "" {
			t.Fatalf("retained unsafe ID %q as %q", id, ref)
		}
	}
	if _, retained := proxy.retainShell(directory, "normal", "first"); !retained {
		t.Fatal("ordinary retention failed")
	}
	if _, retained := proxy.retainShell(directory, "normal", "second"); retained {
		t.Fatal("duplicate ID overwrote retained content")
	}
	for path, want := range map[string]string{outside: "untouched", filepath.Join(directory, "normal"): "first"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("content = %q, %v, want %q", got, err, want)
		}
	}
	if err := os.Rename(filepath.Join(directory, "normal"), filepath.Join(directory, "moved")); err != nil {
		t.Fatal(err)
	}
	if _, retained := proxy.retainShell(directory, "normal", "replacement"); retained {
		t.Fatal("reused an ID while its original expiry callback is pending")
	}
	if _, err := os.Readlink(filepath.Join(filepath.Dir(directory), ".runtime")); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedShellResolverConfinesReferences(t *testing.T) {
	proxy, directory := newShellStorageTestProxy(t)
	const body = "#!python3\nprint('retained')\n"
	if _, ok := proxy.retainShell(directory, "original", body); !ok {
		t.Fatal("retention failed")
	}
	if _, ok := proxy.retainShell(directory, "nested", "#!script=@shell/original"); !ok {
		t.Fatal("nested retention failed")
	}
	for _, input := range []string{body, "#!script=@shell/original", "#!script=@shell/nested"} {
		got, err := proxy.resolveShellInput(directory, input)
		if err != nil || got != body {
			t.Fatalf("resolve %q = %q, %v", input, got, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, ok := proxy.retainShell(directory, "cycle", "#!script=@shell/cycle"); !ok {
		t.Fatal("cycle fixture retention failed")
	}
	for _, reference := range []string{outside, "relative", "@shell/", "@shell/../outside", "@shell/../.runtime", "@shell/.runtime", "@shell/linked", "@shell/cycle", "@shell/missing", `@shell/a\b`} {
		if got, err := proxy.resolveShellInput(directory, "#!script="+reference); err == nil || got != "" {
			t.Fatalf("accepted %q: %q, %v", reference, got, err)
		}
	}
}

func TestShellRetentionExpiryAndCleanupStayInPinnedStorage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proxy, directory := newShellStorageTestProxy(t)
		if _, ok := proxy.retainShell(directory, "call-id", "retained"); !ok {
			t.Fatal("retention failed")
		}
		thread := filepath.Dir(directory)
		moved := thread + "-moved"
		if err := os.Rename(thread, moved); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.Mkdir(filepath.Join(outside, "scripts"), 0o700); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(outside, "scripts", "call-id")
		if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, thread); err != nil {
			t.Fatal(err)
		}
		time.Sleep(shellArtifactTTL + time.Second)
		synctest.Wait()
		if _, err := os.Stat(filepath.Join(moved, "scripts", "call-id")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("original artifact survived expiry: %v", err)
		}
		if err := proxy.Close(); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched" {
			t.Fatalf("escaped expiry or cleanup: %q, %v", got, err)
		}
	})
}

func TestShellRetentionShutdownPreservesReplacementDirectory(t *testing.T) {
	proxy, directory := newShellStorageTestProxy(t)
	if _, ok := proxy.retainShell(directory, "call-id", "private script"); !ok {
		t.Fatal("retention failed")
	}
	thread := filepath.Dir(directory)
	moved := thread + "-moved"
	if err := os.Rename(thread, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(thread, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(thread, "replacement")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched" {
		t.Fatalf("replacement directory changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(moved, "scripts", "call-id")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original script survived shutdown before expiry: %v", err)
	}
}

func TestShellRerunPreservesOriginalHistoryAndRetainsResolvedBody(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	const body = "#!python3\nprint('retained')\n"
	if _, ok := proxy.retainShell(transform.shellDirectory, "original", body); !ok {
		t.Fatal("retention failed")
	}
	const input = "#!script=@shell/original"
	history, err := transform.translateTool("shell", "rerun", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if history.translationError != "" || history.script != input || !strings.Contains(history.carrierInput(), "retained") {
		t.Fatalf("rerun history: %+v", history)
	}
	if got, err := os.ReadFile(filepath.Join(transform.shellDirectory, "rerun")); err != nil || string(got) != body {
		t.Fatalf("retained rerun = %q, %v", got, err)
	}
	for index, reference := range []string{"/etc/passwd", "@shell/../.runtime", "@shell/missing"} {
		history, err := transform.translateTool("shell", fmt.Sprintf("rejected-%d", index), "#!script="+reference, nil)
		if err != nil || history.translationError == "" {
			t.Fatalf("reference %q did not produce a tool rejection: %+v, %v", reference, history, err)
		}
	}
}

func TestRetainedShellEditsCannotReachOutsideScripts(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, newInProcessHPatchTranslator(t.TempDir()))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("printf ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(transform.shellDirectory, "linked")); err != nil {
		t.Fatal(err)
	}
	for index, reference := range []string{"@shell/../.runtime", "@shell/.runtime", "@shell/linked", "@shell/" + outside} {
		history, err := transform.translate(fmt.Sprintf("call-edit-%d", index), "in "+reference+"\ntype 1:ef86 \"changed\"\n", nil)
		if err != nil || history.translationError == "" || history.applied {
			t.Fatalf("accepted retained escape %q: %+v, %v", reference, history, err)
		}
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "printf ok\n" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if got, err := os.Readlink(filepath.Join(filepath.Dir(transform.shellDirectory), ".runtime")); err != nil || got != proxy.registry.shellRuntime {
		t.Fatalf("runtime launcher changed: %q, %v", got, err)
	}
}

func TestShellRetentionLifecycle(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "shell")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := &hpatchProxy{shellDirectory: directory, shellSessions: make(map[string]*shellSession), registry: &toolRegistry{shellRuntime: "/unused-test-worker"}}
	const sessionID = "019fe9b0-c75b-7f92-9ce0-1580bca5e4ab"
	sessionDirectory, err := proxy.storeShellRuntime(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
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
	carrier, err := proxy.registry.execCarrierPayload(
		codeModeCarrierCustom,
		shell,
		"printf ok",
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
