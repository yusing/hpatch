package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactionImageOnlyPreservesSurroundingHistory(t *testing.T) {
	for _, kind := range []string{"user", "assistant", "agent_message", "function_call_output", "custom_tool_call_output"} {
		t.Run(kind, func(t *testing.T) {
			fields := map[string]any{"type": "message", "role": kind, "id": "picture-message",
				"metadata": map[string]string{"trace": "unchanged"}}
			key, textType := "content", "input_text"
			if kind == "assistant" {
				textType = "output_text"
			} else if kind == "agent_message" {
				fields["type"] = kind
				delete(fields, "role")
				fields["author"], fields["recipient"] = "reviewer", "main"
			}
			reasoning := mustMarshalJSON(map[string]any{"type": "reasoning", "id": "opaque-reasoning", "encrypted_content": "opaque", "summary": []any{}})
			input := []json.RawMessage{reasoning}
			if strings.HasSuffix(kind, "_output") {
				key, fields["type"], fields["call_id"] = "output", kind, "picture-call"
				delete(fields, "role")
				call := map[string]any{"type": strings.TrimSuffix(kind, "_output"), "name": "unfamiliar_view", "call_id": "picture-call"}
				if kind == "function_call_output" {
					call["arguments"] = "{}"
				} else {
					call["input"] = "inspect"
				}
				input = append(input, mustMarshalJSON(call))
			}
			left := mustMarshalJSON(map[string]string{"type": textType, "text": "Exact request before the picture.", "annotation": "unchanged"})
			right := mustMarshalJSON(map[string]string{"type": textType, "text": "Exact correction after the picture."})
			unknown := mustMarshalJSON(map[string]string{"type": "future_part", "value": "unchanged"})
			image := mustMarshalJSON(map[string]string{"type": "input_image", "image_url": "data:image/png;base64,cGljdHVyZQ==", "detail": "high"})
			fields[key] = []json.RawMessage{left, image, right, unknown}
			original := mustMarshalJSON(fields)
			input = append(input, original)
			fields[key] = []json.RawMessage{left, mustMarshalJSON(map[string]string{"type": textType, "text": "[Image]"}), right, unknown}
			want := append([]json.RawMessage(nil), input...)
			want[len(want)-1] = mustMarshalJSON(fields)
			baseline := mustMarshalJSON(input)
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			parsed, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "loopback", "input": input}))
			if err != nil {
				t.Fatal(err)
			}
			// Ordinary requests are not compaction: their fresh images stay native.
			if capsule, err := compactor.prepare(t.Context(), &parsed, http.Header{}, false); err != nil || len(capsule) != 0 ||
				!bytes.Equal(parsed.fields["input"], baseline) {
				t.Fatal("ordinary request changed a fresh image")
			}
			capsule, err := compactor.prepare(t.Context(), &parsed, http.Header{}, true)
			if err != nil {
				t.Fatal(err)
			}
			carried := []json.RawMessage{capsule}
			if compactionCarriedMessage(original) {
				carried = []json.RawMessage{original, capsule}
			}
			restored, err := compactor.restore(t.Context(), carried)
			if err != nil || !bytes.Equal(mustMarshalJSON(restored), mustMarshalJSON(want)) {
				t.Fatalf("image-only compaction changed surrounding history: %v", err)
			}
			fresh := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []json.RawMessage{mustMarshalJSON(map[string]string{"type": "input_text", "text": "A fresh picture."}), image}})
			continued, err := parseResponsesRequest(mustMarshalJSON(map[string]any{"model": "loopback", "input": []json.RawMessage{capsule, fresh}}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := compactor.prepare(t.Context(), &continued, http.Header{}, false); err != nil {
				t.Fatal(err)
			}
			var items []json.RawMessage
			_ = json.Unmarshal(continued.fields["input"], &items)
			if count, _ := compactionImageUsage(items); count != 1 || !bytes.Equal(items[len(items)-1], fresh) {
				t.Fatal("restoration stripped a fresh image or resurrected a historical one")
			}
			second, err := compactor.prepare(t.Context(), &continued, http.Header{}, true)
			if err != nil {
				t.Fatal(err)
			}
			carried = []json.RawMessage{fresh, second}
			if compactionCarriedMessage(original) {
				carried = append([]json.RawMessage{original}, carried...)
			}
			again, err := compactor.restore(t.Context(), carried)
			if err != nil || !bytes.Equal(mustMarshalJSON(again), continued.fields["input"]) {
				t.Fatalf("repeated compaction resurrected carried image data: %v", err)
			}
			if !bytes.Equal(mustMarshalJSON(input), baseline) {
				t.Fatal("image replacement mutated source history")
			}
		})
	}
}
