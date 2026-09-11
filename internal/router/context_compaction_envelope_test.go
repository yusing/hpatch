package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCompactionEnvelopeSurvivesRestartAndRejectsDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compaction.key")
	first := &contextCompactor{keyPath: path}
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Preserve this requirement."}),
		compactTestCall("last", "go test ./..."),
		compactTestOutput("last", "unfinished diagnostics", nil),
		mustMarshalJSON(map[string]any{"type": "reasoning", "encrypted_content": "provider-owned-opaque-state"}),
	}
	sealed, err := first.seal(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "Preserve this requirement") || strings.Contains(string(sealed), "unfinished diagnostics") {
		t.Fatal("envelope contains plaintext history")
	}
	restarted := &contextCompactor{keyPath: path}
	restored, local, err := restarted.open(t.Context(), sealed)
	if err != nil || !local || string(mustMarshalJSON(restored)) != string(mustMarshalJSON(items)) {
		t.Fatalf("restart round trip failed: local=%v, err=%v", local, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions: %v, %v", info, err)
	}
	var damaged map[string]json.RawMessage
	if err := json.Unmarshal(sealed, &damaged); err != nil {
		t.Fatal(err)
	}
	encoded := jsonString(damaged, "encrypted_content")
	position := len(encoded) / 2
	replacement := byte('A')
	if encoded[position] == replacement {
		replacement = 'B'
	}
	damaged["encrypted_content"] = mustMarshalJSON(encoded[:position] + string(replacement) + encoded[position+1:])
	if _, local, err := restarted.open(t.Context(), mustMarshalJSON(damaged)); !local || err == nil {
		t.Fatal("damaged local envelope was accepted or treated as provider state")
	}
	damaged["encrypted_content"] = mustMarshalJSON("provider-looking-value")
	if _, local, err := restarted.open(t.Context(), mustMarshalJSON(damaged)); !local || err == nil {
		t.Fatal("local item with a damaged prefix could escape to the provider")
	}
	if _, local, err := (&contextCompactor{keyPath: filepath.Join(t.TempDir(), "absent.key")}).open(t.Context(), sealed); !local || err == nil {
		t.Fatal("missing key did not fail closed")
	}
}
func TestCompactionEnvelopeConcurrentKeyCreationAndProviderIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compaction.key")
	items := []json.RawMessage{mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Keep me."})}
	sealed := make([]json.RawMessage, 8)
	var workers sync.WaitGroup
	for index := range sealed {
		workers.Go(func() {
			compactor := &contextCompactor{keyPath: path}
			var err error
			sealed[index], err = compactor.seal(t.Context(), items)
			if err != nil {
				t.Error(err)
				return
			}
			if _, _, err := compactor.open(t.Context(), sealed[index]); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()

	verifier := &contextCompactor{keyPath: path}
	for index, envelope := range sealed {
		if len(envelope) == 0 {
			continue
		}
		if _, _, err := verifier.open(t.Context(), envelope); err != nil {
			t.Errorf("worker %d used a different installation key: %v", index, err)
		}
	}

	provider := mustMarshalJSON(map[string]any{"type": "compaction", "encrypted_content": "provider-owned"})
	if _, local, err := (&contextCompactor{}).open(t.Context(), provider); local || err != nil {
		t.Fatal("provider-owned compaction was interpreted locally")
	}
}
