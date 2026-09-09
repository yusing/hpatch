package router

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func compactionMetadataTestItem(kind, id, turnID string) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": kind, "id": id, "role": "user", "phase": "analysis",
		"content":        []any{map[string]any{"type": "input_text", "text": "full original text"}},
		"opaque_payload": map[string]any{"digest": "kept", "sequence": 7},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": turnID, "create_time": 123456789,
			"content_item_kinds": []string{"user.text"},
			"unknown":            map[string]any{"worker": "alpha", "attempt": 3},
		},
	})
}

func compactionMetadataTestHistory(prefix ...json.RawMessage) []json.RawMessage {
	items := append([]json.RawMessage(nil), prefix...)
	for index := range compactionRecentOperations + 1 {
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "function_call", "call_id": fmt.Sprintf("recent_%02d", index),
			"name": "unknown", "arguments": "{}",
		}))
	}
	return items
}

func TestCompactionMetadataCleansRecognizedOlderNativeItems(t *testing.T) {
	for _, kind := range []string{
		"message", "reasoning", "function_call", "custom_tool_call",
		"function_call_output", "custom_tool_call_output", "agent_message",
	} {
		t.Run(kind, func(t *testing.T) {
			item := compactionMetadataTestItem(kind, "stable_"+kind, "turn_old_"+kind)
			input := compactionMetadataTestHistory(item)
			before := string(mustMarshalJSON(input))
			got := reduceContextCompactionMetadata(input)
			if string(mustMarshalJSON(input)) != before {
				t.Fatal("metadata cleanup mutated its input")
			}
			if string(got[0]) == string(item) {
				t.Fatal("eligible transport metadata was not removed")
			}

			var fields map[string]json.RawMessage
			if json.Unmarshal(got[0], &fields) != nil {
				t.Fatal("cleaned item is invalid")
			}
			for _, key := range []string{"id", "role", "phase", "content", "opaque_payload"} {
				var original map[string]json.RawMessage
				_ = json.Unmarshal(item, &original)
				if string(fields[key]) != string(original[key]) {
					t.Fatalf("protected field %q changed", key)
				}
			}
			var metadata map[string]json.RawMessage
			if json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata) != nil {
				t.Fatal("retained metadata is invalid")
			}
			if _, exists := metadata["turn_id"]; exists {
				t.Fatal("turn_id was retained")
			}
			if _, exists := metadata["create_time"]; exists {
				t.Fatal("create_time was retained")
			}
			compactionLedgerTestJSONEqual(t, metadata["content_item_kinds"], []string{"user.text"})
			compactionLedgerTestJSONEqual(t, metadata["unknown"], map[string]any{"worker": "alpha", "attempt": 3})
			if repeated := reduceContextCompactionMetadata(got); string(mustMarshalJSON(repeated)) != string(mustMarshalJSON(got)) {
				t.Fatal("metadata cleanup is not idempotent")
			}
		})
	}
}

func TestCompactionMetadataIntegratedAtFinalReduction(t *testing.T) {
	item := compactionMetadataTestItem("message", "integrated_message", "turn_integrated")
	got := reduceContextCompaction(compactionMetadataTestHistory(item))
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(got[0], &fields)
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata)
	if _, exists := metadata["turn_id"]; exists {
		t.Fatal("final compaction pipeline retained redundant transport metadata")
	}
}

func TestCompactionMetadataPrecedesAssistantIDRemoval(t *testing.T) {
	oldAssistant := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "id": "old_assistant_transport",
		"content": "An older factual observation.",
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_assistant_drop", "create_time": 123456789,
		},
	})
	user := mustMarshalJSON(map[string]any{
		"type": "message", "role": "user", "id": "user_identity_keep",
		"content": "Keep referenced turn_user_keep and all authority unchanged.",
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_user_keep", "create_time": 123456790,
		},
	})
	agent := mustMarshalJSON(map[string]any{
		"type": "agent_message", "id": "agent_identity_keep", "author": "/root/worker", "recipient": "/root",
		"content": "Completed evidence.",
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_agent_drop", "create_time": 123456791,
		},
	})
	got := reduceContextCompaction(compactionMetadataTestHistory(oldAssistant, user, agent))
	if len(got) == 0 {
		t.Fatal("compaction removed the fixture")
	}
	var assistantFields map[string]json.RawMessage
	if json.Unmarshal(got[0], &assistantFields) != nil {
		t.Fatal("old assistant item is invalid")
	}
	if _, exists := assistantFields["id"]; exists {
		t.Fatal("unreferenced old ordinary-assistant transport ID survived")
	}
	if _, exists := assistantFields["internal_chat_message_metadata_passthrough"]; exists {
		t.Fatal("assistant ID removal prevented prior transport metadata cleanup")
	}

	var userFields, agentFields map[string]json.RawMessage
	_ = json.Unmarshal(got[1], &userFields)
	_ = json.Unmarshal(got[2], &agentFields)
	if jsonString(userFields, "id") != "user_identity_keep" || jsonString(agentFields, "id") != "agent_identity_keep" {
		t.Fatal("authority or V2-eligible agent identity changed")
	}
	var userMetadata map[string]json.RawMessage
	_ = json.Unmarshal(userFields["internal_chat_message_metadata_passthrough"], &userMetadata)
	if jsonString(userMetadata, "turn_id") != "turn_user_keep" {
		t.Fatal("referenced authority metadata was not retained")
	}
}

func TestCompactionMetadataPreservesIneligibleItems(t *testing.T) {
	t.Run("no id", func(t *testing.T) {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(compactionMetadataTestItem("message", "remove", "turn_no_id"), &fields)
		delete(fields, "id")
		item := mustMarshalJSON(fields)
		got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item))
		if string(got[0]) != string(item) {
			t.Fatal("no-ID item changed")
		}
	})
	t.Run("invalid id shapes", func(t *testing.T) {
		for name, rawID := range map[string]json.RawMessage{
			"empty":  mustMarshalJSON(""),
			"number": mustMarshalJSON(17),
			"null":   mustMarshalJSON(nil),
		} {
			t.Run(name, func(t *testing.T) {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(compactionMetadataTestItem("message", "replace", "turn_invalid_id"), &fields)
				fields["id"] = rawID
				item := mustMarshalJSON(fields)
				got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item))
				if string(got[0]) != string(item) {
					t.Fatal("invalid top-level ID was accepted")
				}
			})
		}
	})
	t.Run("duplicate id", func(t *testing.T) {
		first := compactionMetadataTestItem("message", "duplicate", "turn_first")
		second := compactionMetadataTestItem("reasoning", "duplicate", "turn_second")
		got := reduceContextCompactionMetadata(compactionMetadataTestHistory(first, second))
		if string(got[0]) != string(first) || string(got[1]) != string(second) {
			t.Fatal("duplicate top-level ID was accepted")
		}
	})
	t.Run("unknown kind", func(t *testing.T) {
		item := compactionMetadataTestItem("future_item", "future_1", "turn_future")
		got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item))
		if string(got[0]) != string(item) {
			t.Fatal("unknown item kind changed")
		}
	})
	t.Run("malformed metadata", func(t *testing.T) {
		for name, metadata := range map[string]any{
			"null":       nil,
			"array":      []any{"opaque"},
			"wrong turn": map[string]any{"turn_id": 17, "content_item_kinds": []string{"user.text"}},
			"wrong time": map[string]any{"create_time": "yesterday", "content_item_kinds": []string{"user.text"}},
		} {
			t.Run(name, func(t *testing.T) {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(compactionMetadataTestItem("message", "malformed_"+name, "turn"), &fields)
				fields["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(metadata)
				item := mustMarshalJSON(fields)
				got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item))
				if string(got[0]) != string(item) {
					t.Fatal("malformed metadata changed")
				}
			})
		}
	})
	t.Run("recent frontier", func(t *testing.T) {
		item := compactionMetadataTestItem("message", "recent_message", "turn_recent")
		input := append(compactionMetadataTestHistory(), item)
		got := reduceContextCompactionMetadata(input)
		if string(got[len(got)-1]) != string(item) {
			t.Fatal("recent item changed")
		}
	})
	t.Run("empty metadata removed", func(t *testing.T) {
		item := mustMarshalJSON(map[string]any{
			"type": "message", "id": "empty_metadata", "role": "assistant", "content": "kept",
			"internal_chat_message_metadata_passthrough": map[string]any{
				"turn_id": "turn_empty", "create_time": 123,
			},
		})
		got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item))
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(got[0], &fields)
		if _, exists := fields["internal_chat_message_metadata_passthrough"]; exists {
			t.Fatal("empty transport metadata object was retained")
		}
	})
}

func TestCompactionMetadataPreservesReferencedValues(t *testing.T) {
	item := compactionMetadataTestItem("message", "referenced_message", "turn_keep")
	reference := mustMarshalJSON(map[string]any{
		"type": "message", "id": "reference_message", "role": "assistant",
		"content": "Continue with transport evidence turn_keep.",
	})
	got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item, reference))
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(got[0], &fields)
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata)
	compactionLedgerTestJSONEqual(t, metadata["turn_id"], "turn_keep")
	if _, exists := metadata["create_time"]; exists {
		t.Fatal("unreferenced create_time was retained with referenced turn_id")
	}
}
func TestCompactionMetadataPreservesEncodedAndNumericReferences(t *testing.T) {
	tests := []struct {
		name           string
		content        any
		wantTurnID     bool
		wantCreateTime bool
	}{
		{name: "escaped turn ID", content: `text("turn\u005fkeep")`, wantTurnID: true},
		{name: "numeric create time", content: map[string]any{"observed": 123456789}, wantCreateTime: true},
		{name: "unrelated decoded code point", content: `text("unrelated\u{3a}escape")`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := compactionMetadataTestItem("message", "encoded_reference_item", "turn_keep")
			reference := mustMarshalJSON(map[string]any{
				"type": "message", "id": "encoded_reference", "role": "assistant", "content": test.content,
			})
			got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item, reference))
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(got[0], &fields)
			var metadata map[string]json.RawMessage
			_ = json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata)
			if _, exists := metadata["turn_id"]; exists != test.wantTurnID {
				t.Fatalf("turn_id presence = %t, want %t", exists, test.wantTurnID)
			}
			if _, exists := metadata["create_time"]; exists != test.wantCreateTime {
				t.Fatalf("create_time presence = %t, want %t", exists, test.wantCreateTime)
			}
		})
	}
}

func TestCompactionMetadataRestoresSameIDCarriedMessages(t *testing.T) {
	historical := mustMarshalJSON(map[string]any{
		"type": "message", "role": "developer",
		"content": []any{map[string]any{"type": "input_text", "text": "Historical policy"}},
	})
	original := compactionMetadataTestItem("message", "message_same_id", "turn_transport")
	history := compactionMetadataTestHistory(historical, original)
	cleaned := reduceContextCompactionMetadata(history)
	if string(cleaned[1]) == string(original) {
		t.Fatal("restoration fixture did not remove transport metadata")
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.seal(t.Context(), cleaned)
	if err != nil {
		t.Fatal(err)
	}
	fresh := mustMarshalJSON(map[string]any{
		"type": "message", "role": "developer",
		"content": []any{map[string]any{"type": "input_text", "text": "Fresh policy"}},
	})

	var truncatedFields map[string]json.RawMessage
	_ = json.Unmarshal(original, &truncatedFields)
	truncatedFields["content"] = mustMarshalJSON([]any{
		map[string]any{"type": "input_text", "text": "full…1 tokens truncated…text"},
	})
	truncated := mustMarshalJSON(truncatedFields)

	for _, test := range []struct {
		name    string
		carried json.RawMessage
	}{
		{name: "legacy carried full", carried: original},
		{name: "V2 carried truncated", carried: truncated},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := compactor.restore(t.Context(), []json.RawMessage{fresh, test.carried, capsule})
			want := append([]json.RawMessage{cleaned[0], fresh}, cleaned[1:]...)
			if err != nil || contextCompactionCanonicalJSON(mustMarshalJSON(got)) != contextCompactionCanonicalJSON(mustMarshalJSON(want)) {
				t.Fatalf("same-ID carried message was not restored in canonical order: %v", err)
			}
			if strings.Contains(string(mustMarshalJSON(got)), "turn_transport") {
				t.Fatal("removed transport metadata reappeared")
			}
		})
	}
}
