package toolplugin

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Exercise cold Node/WASM startup through the same bounded invocation used by
// routed shell calls, without executing the supplied script or calling a model.
func TestTranslateBuiltinShellColdStartup(t *testing.T) {
	snapshot, err := Load(t.Context(), "", filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range snapshot.Plugins {
		for index, tool := range plugin.Tools {
			var spec struct{ Name string }
			if err := json.Unmarshal(tool.Specification, &spec); err != nil {
				t.Fatal(err)
			}
			if spec.Name != "shell" {
				continue
			}
			for _, script := range []string{
				"printf 'ok\\n'",
				"rg -n 'RangeStream|MaxRequestBytes' server/etcdserver/v3_server.go\nsed -n '1,160p' server/etcdserver/txn/range.go",
				"echo first\necho second\necho third\necho fourth\n",
			} {
				started := time.Now()
				result, err := Translate(t.Context(), snapshot.NodeExecutable, snapshot.Root, plugin.Module, index, script, "")
				if err != nil {
					t.Fatal(err)
				}
				if result.Rejected || result.Carrier.Kind != "exec" || !slices.Equal(result.Arguments, []string{"bash", script}) {
					t.Fatalf("unexpected shell translation: %+v", result)
				}
				t.Logf("cold translation completed in %s", time.Since(started))
			}
			return
		}
	}
	t.Fatal("built-in shell was not registered")
}
