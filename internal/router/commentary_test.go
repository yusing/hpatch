package router

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPrepareCommentaryToolsAddsOptionalFieldAndPreservesStrictSchema(t *testing.T) {
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
				"parameters": map[string]any{
					"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required": []string{"query"}, "additionalProperties": false,
				},
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
					"type": "function", "name": "wait_agent", "strict": false,
					"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			}},
		}}),
	}

	catalog, err := prepareCommentaryTools(fields)
	if err != nil {
		t.Fatal(err)
	}
	if !catalog[commentaryToolKey("", "lookup")].explicit {
		t.Fatal("non-strict lookup does not support explicit commentary")
	}
	if catalog[commentaryToolKey("", "strict_lookup")].explicit {
		t.Fatal("strict lookup unexpectedly supports explicit commentary")
	}
	if catalog[commentaryToolKey("", "owned_commentary")].explicit {
		t.Fatal("tool-owned commentary was hijacked")
	}
	if !catalog[commentaryToolKey("collaboration", "wait_agent")].explicit {
		t.Fatal("namespaced wait_agent does not support explicit commentary")
	}

	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	propertiesOf := func(tool map[string]json.RawMessage) map[string]json.RawMessage {
		t.Helper()
		var parameters map[string]json.RawMessage
		if err := json.Unmarshal(tool["parameters"], &parameters); err != nil {
			t.Fatal(err)
		}
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(parameters["properties"], &properties); err != nil {
			t.Fatal(err)
		}
		return properties
	}
	if _, exists := propertiesOf(tools[0])[commentaryArgumentName]; !exists {
		t.Fatal("optional commentary property was not added")
	}
	if _, exists := propertiesOf(tools[1])[commentaryArgumentName]; exists {
		t.Fatal("strict schema was changed")
	}
	if raw := propertiesOf(tools[2])[commentaryArgumentName]; !bytes.Contains(raw, []byte(`"boolean"`)) {
		t.Fatalf("tool-owned commentary schema changed: %s", raw)
	}
}

func TestExtractStructuredCommentaryUsesExplicitAndDynamicDefaults(t *testing.T) {
	catalog := commentaryToolCatalog{
		commentaryToolKey("functions", "write_stdin"): {
			namespace: "functions", name: "write_stdin", display: "write_stdin", explicit: true,
		},
		commentaryToolKey("app", "deploy"): {
			namespace: "app", name: "deploy", display: "Deployment", explicit: false,
		},
	}

	explicitItem := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON("functions"),
		"name":      mustMarshalJSON("write_stdin"),
		"arguments": mustMarshalJSON(`{"chars":"y","commentary":"Confirming the prompt."}`),
	}
	explicit, ok, err := extractStructuredCommentary(explicitItem, catalog)
	if err != nil || !ok {
		t.Fatalf("explicit extraction = %+v, %t, %v", explicit, ok, err)
	}
	if explicit.text != "Confirming the prompt." || explicit.arguments != `{"chars":"y"}` {
		t.Fatalf("explicit extraction = %+v", explicit)
	}

	defaultItem := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON("functions"),
		"name": mustMarshalJSON("write_stdin"), "arguments": mustMarshalJSON(`{"chars":""}`),
	}
	generated, ok, err := extractStructuredCommentary(defaultItem, catalog)
	if err != nil || !ok || generated.text != "Waiting for command output." {
		t.Fatalf("generated extraction = %+v, %t, %v", generated, ok, err)
	}

	dynamicItem := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON("app"),
		"name": mustMarshalJSON("deploy"), "arguments": mustMarshalJSON(`{}`),
	}
	dynamic, ok, err := extractStructuredCommentary(dynamicItem, catalog)
	if err != nil || !ok || dynamic.text != "Using Deployment." {
		t.Fatalf("dynamic extraction = %+v, %t, %v", dynamic, ok, err)
	}
}

func TestPrepareCommentaryToolsExcludesUserMessaging(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{map[string]any{
			"type": "additional_tools",
			"tools": []any{map[string]any{
				"type": "namespace", "name": "functions",
				"tools": []any{map[string]any{
					"type": "function", "name": "send_user_message_async", "strict": false,
					"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			}},
		}}),
	}
	catalog, err := prepareCommentaryTools(fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 0 {
		t.Fatalf("excluded catalog = %#v", catalog)
	}
}

func TestStructuredCommentaryTransformsJSONAndCollapsesReplay(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "write_stdin"): {
			namespace: "functions", name: "write_stdin", display: "write_stdin", explicit: true,
		},
	}
	originalArguments := `{"session_id":42,"chars":"y","commentary":"Confirming the prompt."}`
	originalCall := map[string]any{
		"type": "function_call", "id": "item-write", "call_id": "call-write",
		"namespace": "functions", "name": "write_stdin", "arguments": originalArguments,
	}
	payload := mustTestJSON(t, map[string]any{
		"id": "response-1", "status": "completed", "output": []any{originalCall},
	})
	transformed, err := transform.TransformJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(transformed, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || jsonString(response.Output[0], "type") != "message" ||
		jsonString(response.Output[0], "phase") != "commentary" {
		t.Fatalf("transformed output = %s", transformed)
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(response.Output[0]["content"], &content); err != nil || len(content) != 1 ||
		jsonString(content[0], "text") != "Confirming the prompt." {
		t.Fatalf("commentary content = %s, error %v", response.Output[0]["content"], err)
	}
	if got := jsonString(response.Output[1], "arguments"); got != `{"chars":"y","session_id":42}` {
		t.Fatalf("stripped arguments = %s", got)
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
	if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 || jsonString(replayed[0], "arguments") != originalArguments ||
		jsonString(replayed[0], "name") != "write_stdin" {
		t.Fatalf("replayed input = %s", replay.fields["input"])
	}
}

func TestStructuredCommentaryPrecedesStreamingFunctionCall(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("collaboration", "wait_agent"): {
			namespace: "collaboration", name: "wait_agent", display: "wait_agent", explicit: true,
		},
	}
	payload := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"type": "function_call", "id": "item-wait", "call_id": "call-wait",
			"namespace": "collaboration", "name": "wait_agent",
			"arguments": `{"timeout_ms":300000,"commentary":"Waiting for the focused review."}`,
		},
	})
	events, err := transform.TransformSSE(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || !bytes.Contains(events[0], []byte(`"phase":"commentary"`)) ||
		!bytes.Contains(events[0], []byte("Waiting for the focused review.")) ||
		bytes.Contains(events[1], []byte(commentaryArgumentName)) {
		t.Fatalf("stream events = %q", events)
	}
}

func TestStructuredCommentaryBuffersStreamingArguments(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": "",
		},
	})
	if events, err := transform.TransformSSE(added); err != nil || len(events) != 0 {
		t.Fatalf("added events = %q, error %v", events, err)
	}
	delta := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": "item-exec",
		"delta": `{"cmd":"go test`,
	})
	if events, err := transform.TransformSSE(delta); err != nil || len(events) != 1 ||
		!bytes.Contains(events[0], []byte("response.in_progress")) {
		t.Fatalf("delta events = %q, error %v", events, err)
	}
	arguments := `{"cmd":"go test ./...","commentary":"Testing the project."}`
	done := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.done", "item_id": "item-exec", "arguments": arguments,
	})
	events, err := transform.TransformSSE(done)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !bytes.Contains(events[0], []byte("response.in_progress")) ||
		bytes.Contains(events[0], []byte("Testing the project.")) ||
		bytes.Contains(events[0], []byte("response.output_item.added")) {
		t.Fatalf("done events = %q", events)
	}
	itemDone := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": arguments,
		},
	})
	events, err = transform.TransformSSE(itemDone)
	if err != nil || len(events) != 4 ||
		!bytes.Contains(events[0], []byte("Testing the project.")) ||
		!bytes.Contains(events[1], []byte("response.output_item.added")) ||
		!bytes.Contains(events[2], []byte(`\"cmd\":\"go test ./...\"`)) ||
		bytes.Contains(events[1], []byte(commentaryArgumentName)) ||
		bytes.Contains(events[2], []byte(commentaryArgumentName)) ||
		bytes.Contains(events[3], []byte(commentaryArgumentName)) {
		t.Fatalf("item done events = %q, error %v", events, err)
	}
	if len(transform.pending) != 0 {
		t.Fatalf("pending calls = %#v", transform.pending)
	}
}

func TestStructuredCommentaryRejectsInconsistentStreamingIdentity(t *testing.T) {
	newTransform := func(t *testing.T) *hpatchResponseTransform {
		t.Helper()
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		transform.commentaryTools = commentaryToolCatalog{
			commentaryToolKey("functions", "exec_command"): {
				namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
			},
		}
		added := mustTestJSON(t, map[string]any{
			"type": "response.output_item.added",
			"item": map[string]any{
				"type": "function_call", "id": "item-exec", "call_id": "call-exec",
				"namespace": "functions", "name": "exec_command", "arguments": "",
			},
		})
		if events, err := transform.TransformSSE(added); err != nil || len(events) != 0 {
			t.Fatalf("added events = %q, error %v", events, err)
		}
		return transform
	}

	t.Run("repeated arguments completion", func(t *testing.T) {
		transform := newTransform(t)
		done := mustTestJSON(t, map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "item-exec",
			"arguments": `{"cmd":"true"}`,
		})
		if visible, err := transform.TransformSSE(done); err != nil || len(visible) != 1 ||
			!bytes.Contains(visible[0], []byte("response.in_progress")) {
			t.Fatalf("arguments completion visible = %q, error %v", visible, err)
		}
		if _, err := transform.TransformSSE(done); err == nil {
			t.Fatal("repeated arguments completion was accepted")
		}
	})

	t.Run("changed terminal identity", func(t *testing.T) {
		transform := newTransform(t)
		done := mustTestJSON(t, map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "item-exec",
			"arguments": `{"cmd":"true"}`,
		})
		if _, err := transform.TransformSSE(done); err != nil {
			t.Fatal(err)
		}
		itemDone := mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "function_call", "id": "item-exec", "call_id": "changed",
				"namespace": "functions", "name": "exec_command", "arguments": `{"cmd":"true"}`,
			},
		})
		if visible, err := transform.TransformSSE(itemDone); err == nil || visible != nil {
			t.Fatalf("changed terminal call ID visible = %q, error %v", visible, err)
		}
	})

	t.Run("changed terminal item ID", func(t *testing.T) {
		transform := newTransform(t)
		done := mustTestJSON(t, map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "item-exec",
			"arguments": `{"cmd":"true"}`,
		})
		if visible, err := transform.TransformSSE(done); err != nil || len(visible) != 1 ||
			!bytes.Contains(visible[0], []byte("response.in_progress")) {
			t.Fatalf("arguments completion visible = %q, error %v", visible, err)
		}
		itemDone := mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "function_call", "id": "changed-item", "call_id": "call-exec",
				"namespace": "functions", "name": "exec_command", "arguments": `{"cmd":"true"}`,
			},
		})
		if visible, err := transform.TransformSSE(itemDone); err == nil || visible != nil {
			t.Fatalf("changed terminal item ID visible = %q, error %v", visible, err)
		}
	})

	t.Run("changed terminal item and call IDs", func(t *testing.T) {
		transform := newTransform(t)
		done := mustTestJSON(t, map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "item-exec",
			"arguments": `{"cmd":"true"}`,
		})
		if visible, err := transform.TransformSSE(done); err != nil || len(visible) != 1 ||
			!bytes.Contains(visible[0], []byte("response.in_progress")) {
			t.Fatalf("arguments completion visible = %q, error %v", visible, err)
		}
		itemDone := mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "function_call", "id": "changed-item", "call_id": "changed-call",
				"namespace": "functions", "name": "exec_command", "arguments": `{"cmd":"true"}`,
			},
		})
		if visible, err := transform.TransformSSE(itemDone); err == nil || visible != nil {
			t.Fatalf("changed terminal identity visible = %q, error %v", visible, err)
		}
	})

	t.Run("changed terminal arguments", func(t *testing.T) {
		transform := newTransform(t)
		done := mustTestJSON(t, map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "item-exec",
			"arguments": `{"cmd":"true"}`,
		})
		if visible, err := transform.TransformSSE(done); err != nil || len(visible) != 1 ||
			!bytes.Contains(visible[0], []byte("response.in_progress")) {
			t.Fatalf("arguments completion visible = %q, error %v", visible, err)
		}
		itemDone := mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "function_call", "id": "item-exec", "call_id": "call-exec",
				"namespace": "functions", "name": "exec_command", "arguments": `{"cmd":"false"}`,
			},
		})
		if visible, err := transform.TransformSSE(itemDone); err == nil || visible != nil {
			t.Fatalf("changed terminal arguments visible = %q, error %v", visible, err)
		}
	})
}

func TestStructuredCommentaryFromTerminalJSONRetainsExactReplay(t *testing.T) {
	for _, status := range []string{"failed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
			transform.commentaryTools = commentaryToolCatalog{
				commentaryToolKey("functions", "exec_command"): {
					namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
				},
			}
			originalArguments := `{"cmd":"go test ./...","commentary":"Testing the project."}`
			transformed, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": status,
				"output": []any{map[string]any{
					"type": "function_call", "id": "item-exec", "call_id": "call-exec",
					"namespace": "functions", "name": "exec_command", "arguments": originalArguments,
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Output []map[string]json.RawMessage `json:"output"`
			}
			if json.Unmarshal(transformed, &response) != nil || len(response.Output) != 2 {
				t.Fatalf("transformed response = %s", transformed)
			}
			replay := parsedResponsesRequest{fields: map[string]json.RawMessage{
				"input": mustMarshalJSON(response.Output),
			}}
			if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
				t.Fatal(err)
			}
			var replayed []map[string]json.RawMessage
			if json.Unmarshal(replay.fields["input"], &replayed) != nil || len(replayed) != 1 ||
				jsonString(replayed[0], "arguments") != originalArguments {
				t.Fatalf("%s replay = %s", status, replay.fields["input"])
			}
		})
	}
}
