package toolplugin

import (
	"encoding/json"
	"fmt"
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
    maxInputBytes: 1,
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

func grammarPluginDeclaration(t *testing.T, syntax, definition string) string {
	t.Helper()
	format, err := json.Marshal(map[string]string{
		"type":       "grammar",
		"syntax":     syntax,
		"definition": definition,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "grammar.test",
  tools: [{
    specification: {type: "custom", name: "grammar_test", description: "test tool", format: %s},
    maxInputBytes: 4096,
    parse(input) { return input; },
    argv(input) { return [input]; },
    translate(_input, api) { return api.exec(); },
    execute() { return {stdout: "", exitCode: 0}; }
  }]
};
`, format)
}

func TestLoadValidatesProviderGrammarSubset(t *testing.T) {
	valid := []struct {
		name       string
		syntax     string
		definition string
	}{
		{
			name:       "lark",
			syntax:     "lark",
			definition: "start: (\n  WORD\n  | \"ok\"\n)\n%import common.WORD\n%import common.WS\n%ignore WS",
		},
		{
			name:       "regex",
			syntax:     "regex",
			definition: `^(?:[a-z]+|[0-9]{1,3})$`,
		},
	}
	for _, test := range valid {
		t.Run("valid "+test.name, func(t *testing.T) {
			pluginDirectory := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(pluginDirectory, "grammar.mjs"),
				[]byte(grammarPluginDeclaration(t, test.syntax, test.definition)),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			snapshot, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
			if err != nil || len(snapshot.Diagnostics) != 0 {
				t.Fatalf("valid %s grammar rejected: diagnostics %v, error %v", test.name, snapshot.Diagnostics, err)
			}
		})
	}

	invalid := []struct {
		name       string
		syntax     string
		definition string
	}{
		{name: "invalid token", syntax: "lark", definition: "start: @"},
		{name: "priority", syntax: "lark", definition: `start.2: "ok"`},
		{name: "template", syntax: "lark", definition: "template{x}: x\nstart: template{\"ok\"}"},
		{name: "non-common import", syntax: "lark", definition: "%import other.WORD\nstart: WORD"},
		{name: "declare", syntax: "lark", definition: "%declare WORD\nstart: WORD"},
		{name: "duplicate rule", syntax: "lark", definition: "start: \"a\"\nstart: \"b\""},
		{name: "duplicate terminal", syntax: "lark", definition: "TOKEN: \"a\"\nTOKEN: \"b\"\nstart: TOKEN"},
		{name: "newline", syntax: "regex", definition: "one\ntwo"},
		{name: "lookaround", syntax: "regex", definition: "(?=ok)ok"},
		{name: "lazy quantifier", syntax: "regex", definition: "ok+?"},
		{name: "backreference", syntax: "regex", definition: `(ok)\1`},
	}
	for _, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			pluginDirectory := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(pluginDirectory, "grammar.mjs"),
				[]byte(grammarPluginDeclaration(t, test.syntax, test.definition)),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			snapshot, err := Load(t.Context(), pluginDirectory, filepath.Join(t.TempDir(), "snapshot"))
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Diagnostics) == 0 {
				t.Fatalf("invalid %s grammar accepted: %q", test.syntax, test.definition)
			}
		})
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
