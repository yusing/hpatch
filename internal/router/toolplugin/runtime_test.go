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

func TestLoadProvidesSharedCoreToConfiguredPlugin(t *testing.T) {
	pluginDirectory := t.TempDir()
	declaration := `import {hashLine, parseRowReference} from "hpatch:core/v1";
const row = parseRowReference("12:abcd");
export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "shared-core.test",
  tools: [{
    specification: {
      type: "custom",
      name: "shared_core_test",
      description: hashLine("hello") + ":" + row.line + ":" + row.hash,
    },
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute() { return {stdout: "", exitCode: 0}; }
  }]
};
`
	if err := os.WriteFile(filepath.Join(pluginDirectory, "shared-core.mjs"), []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range snapshot.Plugins {
		if plugin.ID != "shared-core.test" {
			continue
		}
		if len(plugin.Tools) != 1 || !strings.Contains(string(plugin.Tools[0].Specification), `"description":"2cf2:12:abcd"`) {
			t.Fatalf("shared-core plugin = %+v", plugin)
		}
		return
	}
	t.Fatal("configured shared-core plugin was not loaded")
}

func TestLoadRejectsUnknownSharedCoreVersion(t *testing.T) {
	pluginDirectory := t.TempDir()
	declaration := `import "hpatch:core/v2";
export default {apiVersion: "hpatch-tool-plugin/v1", id: "future.test", tools: []};
`
	if err := os.WriteFile(filepath.Join(pluginDirectory, "future.mjs"), []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(snapshot.Diagnostics, "\n"), "hpatch plugin module is not supported: hpatch:core/v2") {
		t.Fatalf("Load() diagnostics = %v", snapshot.Diagnostics)
	}
}

func TestExecutionHostOutputBoundCoversJSONExpansion(t *testing.T) {
	const minimum = 6*ExecutionOutputBudgetBytes + 1024
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
