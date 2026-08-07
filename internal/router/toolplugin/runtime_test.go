package toolplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadBoundsPluginHostOutput(t *testing.T) {
	pluginDirectory := t.TempDir()
	declaration := `process.stdout.write("x".repeat(16 * 1024 * 1024 + 1));
export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "output.test",
  tools: [{
    specification: {type: "custom", name: "output_test", description: "test tool"},
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute() { return {stdout: "", exitCode: 0}; }
  }]
};
`
	if err := os.WriteFile(filepath.Join(pluginDirectory, "output.mjs"), []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
	if err == nil || !strings.Contains(err.Error(), "plugin runtime output exceeds") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestExecutionHostOutputBoundCoversJSONExpansion(t *testing.T) {
	const minimum = 6*maxHostOutputBytes + 1024
	if maxEncodedExecutionHostOutputBytes < minimum {
		t.Fatalf("execution output bound = %d, want at least %d", maxEncodedExecutionHostOutputBytes, minimum)
	}
}

func TestLoadTimesOutPluginValidation(t *testing.T) {
	pluginDirectory := t.TempDir()
	declaration := `await new Promise((resolve) => setTimeout(resolve, 60_000));
export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "timeout.test",
  tools: []
};
`
	if err := os.WriteFile(filepath.Join(pluginDirectory, "timeout.mjs"), []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
	if err == nil || !strings.Contains(err.Error(), "plugin validation exceeded") {
		t.Fatalf("Load() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("plugin validation took %s", elapsed)
	}
}
