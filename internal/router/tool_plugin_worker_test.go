package router

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newToolPluginTestRegistry(t *testing.T) (*toolRegistry, string) {
	t.Helper()
	dataDirectory := t.TempDir()
	pluginDirectory := filepath.Join(dataDirectory, "plugins")
	if err := os.Mkdir(pluginDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	liveModule := filepath.Join(pluginDirectory, "proxy.mjs")
	if err := os.WriteFile(liveModule, []byte(testToolPluginDeclaration), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	return registry, liveModule
}

func TestToolPluginWorkerRunsPinnedImplementationInCodexContext(t *testing.T) {
	registry, liveModule := newToolPluginTestRegistry(t)
	wrapper, ok := registry.wrapper("plugin_tool")
	if !ok {
		t.Fatal("plugin wrapper is unavailable")
	}
	if err := os.WriteFile(liveModule, []byte("throw new Error('live module changed');\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HPATCH_PLUGIN_TEST", "inherited")

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(
		t.Context(),
		wrapper,
		[]string{"one", "two words"},
		&stdout,
		&stderr,
	)
	if !handled || exitCode != 7 {
		t.Fatalf("worker handled %t, exit code %d, stderr %q", handled, exitCode, stderr.String())
	}
	wantStdout := strings.Join([]string{cwd, "inherited", "one", "two words"}, "|")
	if stdout.String() != wantStdout || stderr.String() != "fixture stderr" {
		t.Fatalf("worker stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestToolPluginWorkerRejectsSnapshotMismatch(t *testing.T) {
	registry, _ := newToolPluginTestRegistry(t)
	wrapper, ok := registry.wrapper("plugin_tool")
	if !ok {
		t.Fatal("plugin wrapper is unavailable")
	}
	hostPath := filepath.Join(registry.RuntimeRoot, "host.mjs")
	if err := os.WriteFile(hostPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := readToolWorkerManifest(filepath.Join(registry.SnapshotDir, toolPluginManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	manifest.RegistryID, err = toolRegistryIdentity(manifest, registry.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeToolWorkerManifest(registry.SnapshotDir, manifest); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(t.Context(), wrapper, nil, &bytes.Buffer{}, &stderr)
	if !handled || exitCode != 1 || !strings.Contains(stderr.String(), "registry identity mismatch") {
		t.Fatalf("worker handled %t, exit code %d, stderr %q", handled, exitCode, stderr.String())
	}
}
func TestToolPluginWorkerRejectsMissingManifest(t *testing.T) {
	registry, _ := newToolPluginTestRegistry(t)
	wrapper, ok := registry.wrapper("plugin_tool")
	if !ok {
		t.Fatal("plugin wrapper is unavailable")
	}
	if err := os.Remove(filepath.Join(registry.SnapshotDir, toolPluginManifestFilename)); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(t.Context(), wrapper, nil, &bytes.Buffer{}, &stderr)
	if !handled || exitCode != 1 || !strings.Contains(stderr.String(), "open tool worker manifest") {
		t.Fatalf("worker handled %t, exit code %d, stderr %q", handled, exitCode, stderr.String())
	}
}
