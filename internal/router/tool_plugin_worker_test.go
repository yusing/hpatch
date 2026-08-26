package router

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yusing/hpatch"
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
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, false)
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
		os.Stdin,
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
	manifest, err := readToolWorkerManifest(filepath.Join(registry.SnapshotDir, toolPluginManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	gain, err := hpatch.LoadGainMetrics(manifest.MetricsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(gain.ToolInputs) != 1 || gain.ToolInputs[0].StockTokens == 0 ||
		gain.ToolInputs[0].CurrentTokens == gain.ToolInputs[0].StockTokens {
		t.Fatalf("worker stock-result metrics = %+v", gain.ToolInputs)
	}
}

func TestToolPluginWorkerResolvesBasenameFromPath(t *testing.T) {
	registry, _ := newToolPluginTestRegistry(t)
	if err := registry.installFrontends(); err != nil {
		t.Fatal(err)
	}
	second, _ := newToolPluginTestRegistry(t)
	if err := second.installFrontends(); err == nil ||
		!strings.Contains(err.Error(), "another hpatch-router process owns the tool frontends") {
		t.Fatalf("second configured registry frontend installation error = %v", err)
	}
	frontend, ok := registry.frontends["plugin_tool"]
	if !ok {
		t.Fatal("plugin frontend is unavailable")
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PATH", filepath.Dir(frontend))
	t.Setenv("HPATCH_PLUGIN_TEST", "inherited")

	var stdout, stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(
		t.Context(),
		filepath.Base(frontend),
		[]string{"one", "two words"},
		os.Stdin,
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

func TestBuiltinToolWorkersRunGeneratedTypeScriptImplementations(t *testing.T) {
	dataDirectory := t.TempDir()
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	workspace := t.TempDir()
	t.Chdir(workspace)
	if err := os.WriteFile("file.txt", []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goSource := "package sample\n\nfunc Alpha() {}\n"
	goPath := filepath.Join(workspace, "file.go")
	if err := os.WriteFile(goPath, []byte(goSource), 0o600); err != nil {
		t.Fatal(err)
	}
	nameOffset := strings.Index(goSource, "Alpha")
	goplsOutput := fmt.Sprintf(
		`{"span":{"uri":%q,"start":{"line":3,"column":6,"offset":%d},"end":{"line":3,"column":11,"offset":%d}},"description":"func Alpha()"}`+"\n",
		(&url.URL{Scheme: "file", Path: filepath.ToSlash(goPath)}).String(),
		nameOffset,
		nameOffset+len("Alpha"),
	)
	goplsPath := filepath.Join(workspace, "gopls")
	if err := os.WriteFile(goplsPath, fmt.Appendf(nil, "#!/bin/sh\ncat <<'EOF'\n%sEOF\n", goplsOutput), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", workspace+string(os.PathListSeparator)+os.Getenv("PATH"))
	alphaHash := sha256.Sum256([]byte("func Alpha() {}"))

	for _, test := range []struct {
		name       string
		arguments  []string
		wantOutput string
	}{
		{name: "hread", arguments: []string{"file.txt", "0:1"}, wantOutput: "1:8ed3 alpha\n"},
		{name: "hgrep", arguments: []string{"-F", "alpha", "file.txt"}, wantOutput: "\"file.txt\":1:8ed3 alpha\n"},
		{name: "hsymbol", arguments: []string{"def", "file.go", fmt.Sprintf("3:%x", alphaHash[:2]), "Alpha"}, wantOutput: strconv.Quote("file.go") + ":3:" + fmt.Sprintf("%x", alphaHash[:2]) + " func Alpha() {}\n"},
		{name: "inspect_file", arguments: []string{"file.txt"}, wantOutput: "{\"ok\":true,\"data\":{\"path\":\"file.txt\",\"kind\":\"none\",\"language\":null,\"size_bytes\":11,\"line_count\":null,\"parse_complete\":true,\"outline\":[]},\"truncated\":false,\"truncation\":null}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if wrapper, ok := registry.wrapper(test.name); ok {
				t.Fatalf("%s unexpectedly has wrapper %q", test.name, wrapper)
			}
			var stdout, stderr bytes.Buffer
			handled, exitCode := RunToolPluginWorker(
				t.Context(),
				registry.shellRuntime,
				[]string{"bash", workerCommand(test.name, test.arguments)},
				os.Stdin,
				&stdout,
				&stderr,
			)
			if !handled || exitCode != 0 || stdout.String() != test.wantOutput || stderr.Len() != 0 {
				t.Fatalf(
					"%s worker handled %t, exit %d, stdout %q, stderr %q",
					test.name,
					handled,
					exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
	gain, err := hpatch.LoadGainMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(gain.ToolInputs) != 5 {
		t.Fatalf("built-in input metrics = %+v", gain)
	}
	for _, row := range gain.ToolInputs {
		if row.ToolName == "shell" {
			if row.Executions != 4 || row.CurrentTokens != row.StockTokens {
				t.Fatalf("shell input metric row = %+v", row)
			}
			continue
		}
		if row.ToolName == "inspect_file" {
			if row.CurrentTokens != row.StockTokens || row.Reduction != "0.0" {
				t.Fatalf("inspect_file input metric row = %+v", row)
			}
			continue
		}
		if row.ToolName == "hsymbol" {
			if row.CurrentTokens >= row.StockTokens || row.Reduction == "0.0" {
				t.Fatalf("hsymbol input metric row = %+v", row)
			}
			continue
		}
		if row.CurrentTokens <= row.StockTokens || row.Reduction == "0.0" {
			t.Fatalf("built-in input metric row = %+v", row)
		}
	}
	if gain.AllToolInputs.CurrentTokens == 0 || gain.AllToolInputs.StockTokens == 0 {
		t.Fatalf("all built-in input metrics = %+v", gain.AllToolInputs)
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
	handled, exitCode := RunToolPluginWorker(t.Context(), wrapper, nil, os.Stdin, &bytes.Buffer{}, &stderr)
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
	handled, exitCode := RunToolPluginWorker(t.Context(), wrapper, nil, os.Stdin, &bytes.Buffer{}, &stderr)
	if !handled || exitCode != 1 || !strings.Contains(stderr.String(), "open tool worker manifest") {
		t.Fatalf("worker handled %t, exit code %d, stderr %q", handled, exitCode, stderr.String())
	}
}
