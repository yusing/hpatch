package router

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

func TestPrepareCommentaryToolsAddsOptionalFieldAndPreservesExactSchemas(t *testing.T) {
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
	if !catalog[commentaryToolKey("", "lookup")].explicit {
		t.Fatal("non-strict lookup does not support explicit commentary")
	}
	if catalog[commentaryToolKey("", "strict_lookup")].explicit {
		t.Fatal("strict lookup unexpectedly supports explicit commentary")
	}
	if catalog[commentaryToolKey("", "owned_commentary")].explicit {
		t.Fatal("tool-owned commentary was hijacked")
	}
	if catalog[commentaryToolKey("collaboration", "followup_task")].explicit {
		t.Fatal("provider-reserved followup_task unexpectedly supports explicit commentary")
	}
	if !bytes.Equal(fields["input"], additionalTools) {
		t.Fatalf("provider-reserved additional tools changed:\n got %s\nwant %s", fields["input"], additionalTools)
	}
	reserved, matched, err := extractStructuredCommentary(map[string]json.RawMessage{
		"type":      mustMarshalJSON("function_call"),
		"namespace": mustMarshalJSON("collaboration"),
		"name":      mustMarshalJSON("followup_task"),
		"arguments": mustMarshalJSON(`{"target":"agent","message":"continue"}`),
	}, catalog)
	if err != nil || !matched || reserved.text != "Sending a follow-up task to the agent." ||
		reserved.arguments != `{"target":"agent","message":"continue"}` {
		t.Fatalf("provider-reserved default = %+v, matched %t, error %v", reserved, matched, err)
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

func TestStructuredCommentaryDefersDeterminableFailure(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	payload := mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command",
			"arguments": `{"cmd":"false","commentary":"Checking the command."}`,
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
	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		response.Output[0], response.Output[1], map[string]any{
			"type": "function_call_output", "call_id": "call-exec", "output": `{"exit_code":7,"output":"arbitrary output"}`,
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
		t.Fatal(err)
	}
	deferred := proxy.commentary.drain(transform.historySessionID)
	if len(deferred) != 1 || deferred[0].event.Text != "Failed: Checking the command." ||
		deferred[0].event.Reason != "exit status 7" || strings.Contains(deferred[0].event.Reason, "arbitrary") {
		t.Fatalf("deferred commentary = %+v", deferred)
	}
}

func TestStructuredCommentaryMetricsCompareExactCallForms(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	original := map[string]any{
		"type": "function_call", "id": "item-exec", "call_id": "call-exec",
		"namespace": "functions", "name": "exec_command",
		"arguments": `{"cmd":"printf ok","commentary":"Running the check."}`,
	}
	payload := mustTestJSON(t, map[string]any{"status": "completed", "output": []any{original}})
	if _, err := transform.TransformJSON(payload); err != nil {
		t.Fatal(err)
	}
	transform.AcknowledgeJSON()
	snapshot := metrics.snapshot()
	metric := snapshot.Commentary.Explicit
	if metric.Count != 1 || metric.NativeTokens != metrics.countCommentaryTokens("Running the check.") {
		t.Fatalf("explicit commentary metrics = %+v", metric)
	}
	var originalItem map[string]json.RawMessage
	if err := json.Unmarshal(mustTestJSON(t, original), &originalItem); err != nil {
		t.Fatal(err)
	}
	strippedItem := maps.Clone(originalItem)
	strippedItem["arguments"] = mustMarshalJSON(`{"cmd":"printf ok"}`)
	originalTokens := metrics.countCommentaryTokens(string(mustMarshalJSON(originalItem)))
	strippedTokens := metrics.countCommentaryTokens(string(mustMarshalJSON(strippedItem)))
	wantFormTokens := originalTokens - min(originalTokens, strippedTokens)
	if metric.FormTokens != wantFormTokens {
		t.Fatalf("form tokens = %d, want %d", metric.FormTokens, wantFormTokens)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].SessionID != transform.sessionID ||
		snapshot.Sessions[0].Commentary.Explicit.Count != 1 {
		t.Fatalf("commentary sessions = %+v", snapshot.Sessions)
	}
}

func TestStructuredBlankCommentaryCountsAuthoredForm(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	if _, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command",
			"arguments": `{"cmd":"printf ok","commentary":"  "}`,
		}},
	})); err != nil {
		t.Fatal(err)
	}
	transform.AcknowledgeJSON()
	metric := metrics.snapshot().Commentary.Default
	if metric.Count != 1 || metric.FormTokens == 0 {
		t.Fatalf("blank commentary metrics = %+v", metric)
	}
}

func TestRoutedCustomToolReceivesDefaultCommentary(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{testHPatchItem()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if json.Unmarshal(response, &decoded) != nil || len(decoded.Output) != 2 ||
		jsonString(decoded.Output[0], "phase") != "commentary" ||
		!strings.Contains(string(decoded.Output[0]["content"]), "Applying the requested changes.") {
		t.Fatalf("routed custom commentary = %s", response)
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

func TestStructuredCommentaryFromFailedStreamRetainsExactReplay(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	originalArguments := `{"cmd":"go test ./...","commentary":"Testing the project."}`
	payload := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command", "arguments": originalArguments,
		},
	})
	events, err := transform.TransformSSE(payload)
	if err != nil || len(events) != 2 {
		t.Fatalf("stream events = %q, error %v", events, err)
	}
	failed := mustTestJSON(t, map[string]any{"type": "response.failed", "response": map[string]any{"status": "failed"}})
	if _, err := transform.TransformSSE(failed); err != nil {
		t.Fatal(err)
	}
	items := make([]map[string]json.RawMessage, 0, 2)
	for _, event := range events {
		var envelope struct {
			Item map[string]json.RawMessage `json:"item"`
		}
		if json.Unmarshal(event, &envelope) != nil || envelope.Item == nil {
			t.Fatalf("decode visible event = %s", event)
		}
		items = append(items, envelope.Item)
	}
	replay := parsedResponsesRequest{fields: map[string]json.RawMessage{"input": mustMarshalJSON(items)}}
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
		t.Fatal(err)
	}
	var replayed []map[string]json.RawMessage
	if json.Unmarshal(replay.fields["input"], &replayed) != nil || len(replayed) != 1 ||
		jsonString(replayed[0], "arguments") != originalArguments {
		t.Fatalf("failed-stream replay = %s", replay.fields["input"])
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

func TestStructuredCommentaryRejectsDuplicateCallIDBeforeExposure(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	added := func(itemID string) []byte {
		return mustTestJSON(t, map[string]any{
			"type": "response.output_item.added",
			"item": map[string]any{
				"type": "function_call", "id": itemID, "call_id": "call-shared",
				"namespace": "functions", "name": "exec_command", "arguments": "",
			},
		})
	}
	if visible, err := transform.TransformSSE(added("item-first")); err != nil || visible != nil {
		t.Fatalf("first added visible = %q, error %v", visible, err)
	}
	if visible, err := transform.TransformSSE(added("item-second")); err == nil || visible != nil {
		t.Fatalf("duplicate call ID visible = %q, error %v", visible, err)
	}
}
