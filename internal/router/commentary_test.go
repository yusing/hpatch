package router

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPrepareCommentaryToolsPreservesOwnedSchemas(t *testing.T) {
	fields := map[string]json.RawMessage{
		"tools": mustTestJSON(t, []any{
			map[string]any{
				"type": "function", "name": "lookup", "strict": false,
				"parameters": map[string]any{
					"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required": []string{"query"}, "additionalProperties": false,
				},
			},
			map[string]any{
				"type": "function", "name": "strict_lookup", "strict": true,
				"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			map[string]any{
				"type": "function", "name": "owned_commentary", "strict": false,
				"parameters": map[string]any{
					"type": "object", "properties": map[string]any{"commentary": map[string]any{"type": "boolean"}},
				},
			},
		}),
		"input": mustTestJSON(t, []any{map[string]any{
			"type": "additional_tools",
			"tools": []any{map[string]any{
				"type": "namespace", "name": "collaboration",
				"tools": []any{map[string]any{
					"type": "function", "name": "followup_task", "strict": false,
					"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			}},
		}}),
	}
	additionalTools := bytes.Clone(fields["input"])
	catalog, err := prepareCommentaryTools(fields)
	if err != nil {
		t.Fatal(err)
	}
	_, collaborationInstrumented := catalog[functionToolKey("collaboration", "followup_task")]
	if !catalog[functionToolKey("", "lookup")].explicit ||
		catalog[functionToolKey("", "strict_lookup")].explicit ||
		catalog[functionToolKey("", "owned_commentary")].explicit ||
		collaborationInstrumented {
		t.Fatalf("commentary catalog = %#v", catalog)
	}
	if !bytes.Equal(fields["input"], additionalTools) {
		t.Fatalf("additional_tools changed:\n got %s\nwant %s", fields["input"], additionalTools)
	}

	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	properties := func(index int) map[string]json.RawMessage {
		t.Helper()
		var parameters, result map[string]json.RawMessage
		if json.Unmarshal(tools[index]["parameters"], &parameters) != nil ||
			json.Unmarshal(parameters["properties"], &result) != nil {
			t.Fatalf("tool %d has malformed parameters", index)
		}
		return result
	}
	if _, ok := properties(0)[commentaryArgumentName]; !ok {
		t.Fatal("extensible tool did not receive commentary")
	}
	if _, ok := properties(1)[commentaryArgumentName]; ok {
		t.Fatal("strict tool schema changed")
	}
	if raw := properties(2)[commentaryArgumentName]; !bytes.Contains(raw, []byte(`"boolean"`)) {
		t.Fatalf("owned commentary changed: %s", raw)
	}
}

func TestStructuredCommentaryTransformsJSONAndReplay(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	transform.commentaryTools = commentaryToolCatalog{
		functionToolKey("functions", "write_stdin"): {
			qualifiedName: "functions.write_stdin", display: "write_stdin", explicit: true,
		},
	}
	originalArguments := `{"session_id":42,"chars":"y","commentary":"Confirming the prompt."}`
	payload := mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{map[string]any{
			"type": "function_call", "id": "item-write", "call_id": "call-write",
			"namespace": "functions", "name": "write_stdin", "arguments": originalArguments,
		}},
	})
	transformed, err := transform.TransformJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if json.Unmarshal(transformed, &response) != nil || len(response.Output) != 2 {
		t.Fatalf("transformed response = %s", transformed)
	}
	if jsonString(response.Output[0], "phase") != "commentary" ||
		!bytes.Contains(response.Output[0]["content"], []byte("Confirming the prompt.")) ||
		jsonString(response.Output[1], "arguments") != `{"chars":"y","session_id":42}` {
		t.Fatalf("transformed output = %s", transformed)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		response.Output[0], response.Output[1], map[string]any{
			"type": "function_call_output", "call_id": "call-write", "output": `{"ok":true}`,
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
		t.Fatal(err)
	}
	var replayed []map[string]json.RawMessage
	if json.Unmarshal(replay.fields["input"], &replayed) != nil || len(replayed) != 2 ||
		jsonString(replayed[0], "arguments") != originalArguments {
		t.Fatalf("replayed input = %s", replay.fields["input"])
	}
}

func TestStructuredCommentaryRejectsNonStringValues(t *testing.T) {
	catalog := commentaryToolCatalog{
		functionToolKey("functions", "lookup"): {
			qualifiedName: "functions.lookup", display: "lookup", explicit: true,
		},
	}
	for _, value := range []string{"null", "true", "42", `{}`, `[]`} {
		item := map[string]json.RawMessage{
			"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON("functions"),
			"name": mustMarshalJSON("lookup"), "arguments": mustMarshalJSON(`{"query":"x","commentary":` + value + `}`),
		}
		if _, matched, err := extractStructuredCommentary(item, catalog); err == nil || matched ||
			!bytes.Contains([]byte(err.Error()), []byte("functions.lookup commentary must be a string")) {
			t.Fatalf("commentary %s matched = %v, error = %v", value, matched, err)
		}
	}
}

func TestStructuredCommentaryBuffersStreamingArguments(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		functionToolKey("functions", "exec_command"): {
			qualifiedName: "functions.exec_command", display: "exec_command", explicit: true,
		},
	}
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": "",
		},
	})
	if events, err := transform.TransformSSE(added); err != nil || len(events) != 0 {
		t.Fatalf("added events = %q, error %v", events, err)
	}
	arguments := `{"cmd":"go test ./...","commentary":"Testing the project."}`
	argumentsDone := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.done", "item_id": "item-exec", "output_index": 0,
		"arguments": arguments,
	})
	if events, err := transform.TransformSSE(argumentsDone); err != nil || len(events) != 1 {
		t.Fatalf("arguments events = %q, error %v", events, err)
	}
	itemDone := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done", "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": arguments,
		},
	})
	events, err := transform.TransformSSE(itemDone)
	if err != nil || len(events) != 4 {
		t.Fatalf("done events = %q, error %v", events, err)
	}
	if !bytes.Contains(events[0], []byte("Testing the project.")) {
		t.Fatalf("commentary event = %s", events[0])
	}
	for _, event := range events[1:] {
		if bytes.Contains(event, []byte(commentaryArgumentName)) {
			t.Fatalf("router-owned argument leaked: %s", event)
		}
	}
}

func TestBufferedStructuredCommentaryOmitsNullCompletionMessage(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		functionToolKey("functions", "exec_command"): {
			qualifiedName: "functions.exec_command", display: "exec_command", explicit: true,
		},
	}
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": "",
		},
	})
	if events, err := transform.TransformSSE(added); err != nil || len(events) != 0 {
		t.Fatalf("added events = %q, error %v", events, err)
	}
	arguments := `{"cmd":"go test ./...","commentary":"Testing the project."}`
	argumentsDone := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.done", "item_id": "item-exec", "output_index": 0,
		"arguments": arguments,
	})
	if events, err := transform.TransformSSE(argumentsDone); err != nil || len(events) != 1 {
		t.Fatalf("arguments events = %q, error %v", events, err)
	}
	itemDone := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done", "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "renamed", "arguments": arguments,
		},
	})
	events, err := transform.TransformSSE(itemDone)
	if err != nil || len(events) != 3 {
		t.Fatalf("done events = %q, error %v", events, err)
	}
	wantTypes := []string{"response.output_item.added", "response.function_call_arguments.done", "response.output_item.done"}
	for index, event := range events {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(event, &envelope) != nil || jsonString(envelope, "type") != wantTypes[index] ||
			bytes.Contains(event, []byte(`"item":null`)) {
			t.Fatalf("event %d = %s", index, event)
		}
	}
}
