package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolRegistryStartup(t *testing.T) {
	declaration := func(pluginID, toolName, format string) string {
		t.Helper()
		formatField := ""
		if format != "" {
			formatField = ", format: " + format
		}
		return fmt.Sprintf(`export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: %q,
  tools: [{
    specification: {type: "custom", name: %q, description: "test tool"%s},
    maxInputBytes: 4096,
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute(argv) { return {stdout: argv.join(" "), exitCode: 0}; }
  }]
};
`, pluginID, toolName, formatField)
	}
	writePlugin := func(t *testing.T, directory, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("missing directory preserves builtins without Node", func(t *testing.T) {
		registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := registry.SnapshotDir
		if registry.NodeExecutable != "" || len(registry.ordered) != 3 {
			t.Fatalf("registry = %+v", registry)
		}
		for _, name := range []string{hreadToolName, hgrepToolName} {
			wrapper, ok := registry.wrapper(name)
			if !ok || filepath.Dir(wrapper) != snapshot {
				t.Fatalf("wrapper %q = %q, available %t", name, wrapper, ok)
			}
		}
		if err := registry.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
			t.Fatalf("snapshot remains after close: %v", err)
		}
	})

	t.Run("lexical declarations are pinned and wrapped", func(t *testing.T) {
		dataDirectory := t.TempDir()
		pluginDirectory := filepath.Join(dataDirectory, "plugins")
		if err := os.Mkdir(pluginDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		writePlugin(t, pluginDirectory, "zeta.mjs", declaration(
			"zeta.plugin",
			"zeta_tool",
			`{type: "grammar", syntax: "regex", definition: "^[a-z]+$"}`,
		))
		writePlugin(t, pluginDirectory, "alpha.js", declaration(
			"alpha.plugin",
			"alpha_tool",
			"{type: \"grammar\", syntax: \"lark\", definition: \"start: \\\"ok\\\"\"}",
		))
		if err := os.Symlink(filepath.Join(pluginDirectory, "alpha.js"), filepath.Join(pluginDirectory, "ignored.mjs")); err != nil {
			t.Fatal(err)
		}

		registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := registry.SnapshotDir
		if len(registry.ordered) != 5 ||
			registry.ordered[3].PluginID != "alpha.plugin" ||
			registry.ordered[4].PluginID != "zeta.plugin" {
			t.Fatalf("registration order = %+v", registry.ordered)
		}
		for _, name := range []string{hreadToolName, hgrepToolName, "alpha_tool", "zeta_tool"} {
			wrapper, ok := registry.wrapper(name)
			if !ok {
				t.Fatalf("wrapper %q is unavailable", name)
			}
			target, err := os.Readlink(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			if target == "" || filepath.Base(wrapper) != name {
				t.Fatalf("wrapper %q targets %q", wrapper, target)
			}
		}
		if _, ok := registry.wrapper(hpatchToolName); ok {
			t.Fatal("hpatch unexpectedly has an executor wrapper")
		}
		snapshotModule := filepath.Join(snapshot, "runtime", "plugins", "alpha.js")
		pinned, err := os.ReadFile(snapshotModule)
		if err != nil {
			t.Fatal(err)
		}
		writePlugin(t, pluginDirectory, "alpha.js", "throw new Error('changed live declaration');\n")
		after, err := os.ReadFile(snapshotModule)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(pinned) {
			t.Fatal("snapshot changed with live declaration")
		}
		var manifest toolWorkerManifest
		encodedManifest, err := os.ReadFile(filepath.Join(snapshot, toolPluginManifestFilename))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encodedManifest, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.RegistryID != registry.ID || len(manifest.Tools) != 5 {
			t.Fatalf("manifest = %+v, registry ID %q", manifest, registry.ID)
		}
		if err := registry.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("independent declaration and ownership errors aggregate", func(t *testing.T) {
		dataDirectory := t.TempDir()
		pluginDirectory := filepath.Join(dataDirectory, "plugins")
		if err := os.Mkdir(pluginDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		writePlugin(t, pluginDirectory, "bad.mjs", "export default {apiVersion: 'wrong'};\n")
		writePlugin(t, pluginDirectory, "duplicate-a.mjs", declaration("duplicate.plugin", hpatchToolName, ""))
		writePlugin(t, pluginDirectory, "duplicate-b.mjs", declaration("duplicate.plugin", "other_tool", ""))
		registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription)
		if registry != nil || err == nil {
			t.Fatalf("registry = %+v, error = %v", registry, err)
		}
		diagnostic := err.Error()
		for _, fragment := range []string{
			"bad.mjs: default export",
			`plugin identity "duplicate.plugin"`,
			`tool name "hpatch"`,
		} {
			if !strings.Contains(diagnostic, fragment) {
				t.Fatalf("startup diagnostic lacks %q:\n%s", fragment, diagnostic)
			}
		}
	})

	t.Run("invalid registry fails the real entry point before listening", func(t *testing.T) {
		configRoot := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", configRoot)
		pluginDirectory := filepath.Join(configRoot, "hpatch", "plugins")
		if err := os.MkdirAll(pluginDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		writePlugin(t, pluginDirectory, "invalid.mjs", "export default null;\n")
		var stderr strings.Builder
		err := Run(t.Context(), []string{"--listen", "127.0.0.1:0"}, &stderr)
		if err == nil || !strings.Contains(err.Error(), "initialize tool registry") {
			t.Fatalf("Run() error = %v", err)
		}
		if strings.Contains(stderr.String(), "listening") {
			t.Fatalf("router listened with invalid registry: %s", stderr.String())
		}
		if _, err := os.Stat(filepath.Join(configRoot, "hpatch", "metrics.bin")); !os.IsNotExist(err) {
			t.Fatalf("invalid startup changed durable metrics: %v", err)
		}
	})
}
