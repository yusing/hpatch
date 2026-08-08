package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerFrontendSymlinkLifecycle(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "hpatch-router")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	name := "plugin_tool"
	link := filepath.Join(directory, name)
	wrapper := filepath.Join(t.TempDir(), name)

	if err := os.WriteFile(link, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorkerFrontendSymlink(executable, wrapper, directory, name); err == nil ||
		!strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("unrelated frontend error = %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorkerFrontendSymlink(executable, wrapper, directory, name); err != nil {
		t.Fatalf("replace legacy frontend: %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != wrapper {
		t.Fatalf("legacy frontend target = %q, want %q: %v", target, wrapper, err)
	}

	otherWrapper := filepath.Join(t.TempDir(), name)
	if err := removeWorkerFrontendSymlink(link, otherWrapper); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != wrapper {
		t.Fatalf("foreign frontend changed to %q: %v", target, err)
	}
	if err := removeWorkerFrontendSymlink(link, wrapper); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("owned frontend remains: %v", err)
	}

	staleDirectory := filepath.Join(
		t.TempDir(),
		"hpatch-router-tools-fixture-"+strings.Repeat("a", 64),
	)
	if err := os.Mkdir(staleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	staleWrapper := filepath.Join(staleDirectory, name)
	if err := os.Symlink(executable, staleWrapper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(staleWrapper, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureWorkerFrontendSymlink(executable, wrapper, directory, name); err != nil {
		t.Fatalf("replace stale frontend: %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != wrapper {
		t.Fatalf("stale frontend target = %q, want %q: %v", target, wrapper, err)
	}
}

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

	t.Run("missing directory loads embedded built-ins", func(t *testing.T) {
		registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := registry.SnapshotDir
		if registry.NodeExecutable == "" || len(registry.ordered) != 4 {
			t.Fatalf("registry = %+v", registry)
		}
		if err := registry.installFrontends(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"hread", "hgrep", "shell"} {
			_, ok := registry.contribution(name)
			if !ok {
				t.Fatalf("built-in %q is unavailable", name)
			}
			wrapper, ok := registry.wrapper(name)
			if !ok || filepath.Dir(wrapper) != snapshot {
				t.Fatalf("wrapper %q = %q, available %t", name, wrapper, ok)
			}
			frontend, ok := registry.frontends[name]
			if !ok {
				t.Fatalf("frontend %q is unavailable", name)
			}
			target, err := os.Readlink(frontend)
			if err != nil || target != wrapper {
				t.Fatalf("frontend %q targets %q, want %q: %v", frontend, target, wrapper, err)
			}
		}
		specifications, err := registry.specifications()
		if err != nil {
			t.Fatal(err)
		}
		if len(specifications) != 2 ||
			jsonString(specifications[0], "name") != hpatchToolName ||
			jsonString(specifications[1], "name") != "shell" {
			t.Fatalf("model-visible specifications = %#v", specifications)
		}
		instructions, err := registry.baseInstructions()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(instructions, "Use `hread` through `shell`") ||
			!strings.Contains(instructions, "Use `hgrep` through `shell`") {
			t.Fatalf("base instructions = %q", instructions)
		}
		second, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription)
		if err != nil {
			t.Fatal(err)
		}
		if err := second.installFrontends(); err == nil ||
			!strings.Contains(err.Error(), "another hpatch-router process owns") {
			t.Fatalf("concurrent frontend installation error = %v", err)
		}
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
		if err := registry.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
			t.Fatalf("snapshot remains after close: %v", err)
		}
		for name, frontend := range registry.frontends {
			if _, err := os.Lstat(frontend); !os.IsNotExist(err) {
				t.Fatalf("frontend %q remains after close: %v", name, err)
			}
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
		if len(registry.ordered) != 6 ||
			registry.ordered[4].PluginID != "alpha.plugin" ||
			registry.ordered[5].PluginID != "zeta.plugin" {
			t.Fatalf("registration order = %+v", registry.ordered)
		}
		for _, name := range []string{"hread", "hgrep", "shell", "alpha_tool", "zeta_tool"} {
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
		snapshotModule := filepath.Join(snapshot, "runtime", "plugins", "user", "alpha.js")
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
		if manifest.RegistryID != registry.ID || len(manifest.Tools) != 6 {
			t.Fatalf("manifest = %+v, registry ID %q", manifest, registry.ID)
		}
		if err := registry.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("retired configured shell does not shadow the built-in", func(t *testing.T) {
		dataDirectory := t.TempDir()
		pluginDirectory := filepath.Join(dataDirectory, "plugins")
		if err := os.Mkdir(pluginDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		legacyDeclaration := strings.Replace(
			declaration(retiredConfiguredShellPluginID, "shell", ""),
			"  }]\n};\n",
			`  }, {
    specification: {type: "custom", name: "legacy_extra", description: "test tool"},
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute(argv) { return {stdout: argv.join(" "), exitCode: 0}; }
  }]
};
`,
			1,
		)
		writePlugin(t, pluginDirectory, "shell.mjs", legacyDeclaration)

		registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := registry.Close(); err != nil {
				t.Error(err)
			}
		})
		shell, shellOK := registry.contribution("shell")
		extra, extraOK := registry.contribution("legacy_extra")
		if !shellOK || shell.PluginID != "builtin.shell" ||
			!extraOK || extra.PluginID != retiredConfiguredShellPluginID ||
			len(registry.ordered) != 5 {
			t.Fatalf("registry retired the wrong contributions: %+v", registry.ordered)
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
		writePlugin(t, pluginDirectory, "shell.mjs", declaration("shell.plugin", "eval", ""))
		registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription)
		if registry != nil || err == nil {
			t.Fatalf("registry = %+v, error = %v", registry, err)
		}
		diagnostic := err.Error()
		for _, fragment := range []string{
			"bad.mjs: default export",
			`plugin identity "duplicate.plugin"`,
			`tool name "hpatch"`,
			"collides with a shell keyword or built-in",
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
