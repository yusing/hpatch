package router

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentCommentaryJSONIsVisibleAndRemovedFromReplay(t *testing.T) {
	responseText := "status done\nfact exact response\n"
	agentMessage := map[string]any{
		"type": "agent_message", "id": "amsg-result", "author": "/root/explorer", "recipient": "/root",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/explorer\nPayload:\n" + responseText,
		}},
	}
	transform, _, request := newSubagentCommentaryTestTransform(t, []any{agentMessage})

	spawnArguments := `{"task_name":"inspect","message":"encrypted-spawn-message","agent_type":" explorer ","model":"gpt-requested","reasoning_effort":"low"}`
	followupArguments := `{"target":"/root/explorer","message":"encrypted-follow-up-message"}`
	usage := map[string]any{
		"input_tokens": 120, "output_tokens": 30,
		"input_tokens_details":  map[string]any{"cached_tokens": 80},
		"output_tokens_details": map[string]any{"reasoning_tokens": 20},
	}
	payload := mustTestJSON(t, map[string]any{
		"id": "resp-json", "status": "completed", "usage": usage,
		"output": []any{
			map[string]any{
				"type": "function_call", "id": "item-spawn", "call_id": "call-spawn",
				"namespace": "collaboration", "name": "spawn_agent", "arguments": spawnArguments,
			},
			map[string]any{
				"type": "function_call", "id": "item-followup", "call_id": "call-followup",
				"namespace": "collaboration", "name": "followup_task", "arguments": followupArguments,
			},
			map[string]any{
				"type": "function_call", "id": "item-send", "call_id": "call-send",
				"namespace": "collaboration", "name": "send_message", "arguments": `{"target":"/root/explorer","message":"not requested"}`,
			},
		},
	})
	observeTestResponseUsage(t, transform, payload, false)
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
	if len(response.Output) != 6 {
		t.Fatalf("output = %s", transformed)
	}
	if text := commentaryText(t, response.Output[0]); text != "Response from /root/explorer:\n"+responseText {
		t.Fatalf("response commentary = %q", text)
	}
	wantSpawn := "Starting subagent.\nRole: explorer\nModel: gpt-requested\nReasoning effort: low"
	if text := commentaryText(t, response.Output[2]); text != wantSpawn {
		t.Fatalf("spawn commentary = %q", text)
	}
	if text := commentaryText(t, response.Output[1]); text != "Tokens: i=120, ci=80, o=30, r=20" {
		t.Fatalf("usage commentary = %q", text)
	}
	if jsonString(response.Output[3], "arguments") != spawnArguments ||
		jsonString(response.Output[4], "arguments") != followupArguments ||
		jsonString(response.Output[5], "name") != "send_message" {
		t.Fatalf("collaboration calls changed: %s", transformed)
	}
	if bytes.Contains(response.Output[2]["content"], []byte("encrypted")) {
		t.Fatalf("encrypted message reached spawn commentary: %s", response.Output[2]["content"])
	}

	var forwarded []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &forwarded); err != nil {
		t.Fatal(err)
	}
	if !containsAgentMessage(forwarded, "amsg-result") {
		t.Fatalf("subagent response was removed from model input: %s", request.fields["input"])
	}
	for _, item := range forwarded {
		if strings.HasPrefix(jsonString(item, "id"), subagentCommentaryMessagePrefix) {
			t.Fatalf("user-only commentary reached model input: %s", request.fields["input"])
		}
	}
}

func TestTokenUsageCommentaryUsesSharedObservationWithoutReplacingTerminalMessage(t *testing.T) {
	terminal := map[string]any{
		"type": "message", "id": "msg-final", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": "substantive result"}},
	}
	payload := mustTestJSON(t, map[string]any{
		"id": "resp-terminal", "status": "completed", "output": []any{terminal},
		"usage": map[string]any{
			"input_tokens": 20, "output_tokens": 5,
			"input_tokens_details":  map[string]any{"cached_tokens": 12},
			"output_tokens_details": map[string]any{"reasoning_tokens": 3},
		},
	})
	response, _, err := responseWithTokenUsageCommentary(payload, tokenCounts{
		InputTokens: 20, UncachedInputTokens: 8, OutputTokens: 5, ReasoningTokens: 3,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(response["output"], &output); err != nil || len(output) != 2 {
		t.Fatalf("output = %s, error = %v", response["output"], err)
	}
	if text := commentaryText(t, output[0]); text != "Tokens: i=20, ci=12, o=5, r=3" {
		t.Fatalf("usage commentary = %q", text)
	}
	if jsonString(output[1], "id") != "msg-final" {
		t.Fatalf("terminal message = %s", response["output"])
	}
}

func TestSubagentStreamingUsageDoesNotBecomeAStandaloneResult(t *testing.T) {
	metadata := codexTurnMetadata{SubagentKind: threadSpawnSubagentKind}
	transform, _, _ := newSubagentCommentaryTestTransformWithMetadata(t, nil, metadata)
	terminal := mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": "resp-child", "status": "completed",
			"output": []any{map[string]any{
				"type": "message", "id": "msg-child-final", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": "child result"}},
			}},
			"usage": map[string]any{
				"input_tokens": 20, "output_tokens": 5,
				"input_tokens_details":  map[string]any{"cached_tokens": 12},
				"output_tokens_details": map[string]any{"reasoning_tokens": 3},
			},
		},
	})
	observeTestResponseUsage(t, transform, terminal, true)
	events, err := transform.TransformSSE(terminal)
	if err != nil || len(events) != 1 {
		t.Fatalf("terminal events = %q, error = %v", events, err)
	}
	var envelope struct {
		Response struct {
			Output []map[string]json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(events[0], &envelope); err != nil || len(envelope.Response.Output) != 2 {
		t.Fatalf("terminal event = %s, error = %v", events[0], err)
	}
	if text := commentaryText(t, envelope.Response.Output[0]); text != "Tokens: i=20, ci=12, o=5, r=3" {
		t.Fatalf("usage commentary = %q", text)
	}
	if jsonString(envelope.Response.Output[1], "id") != "msg-child-final" {
		t.Fatalf("terminal message = %s", events[0])
	}
}

func TestSubagentResponseCommentaryDoesNotRepeat(t *testing.T) {
	responseText := "result"
	agentMessage := map[string]any{
		"type": "agent_message", "id": "amsg-result", "author": "/root/worker", "recipient": "/root",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": "Message Type: MESSAGE\nTask name: /root\nSender: /root/worker\nPayload:\n" + responseText,
		}},
	}
	generatedID := subagentCommentaryMessageID("response\x00amsg-result\x00/root/worker\x00" + responseText)
	generated := assistantCommentaryMessage(generatedID, "Response from /root/worker:\n"+responseText)
	input := []any{generated, agentMessage}
	transform, _, request := newSubagentCommentaryTestTransform(t, input)
	if len(transform.subagentDeferred) != 0 {
		t.Fatalf("deferred commentary = %#v", transform.subagentDeferred)
	}
	if bytes.Contains(request.fields["input"], []byte(generatedID)) {
		t.Fatalf("generated commentary reached model input: %s", request.fields["input"])
	}
}

func TestSubagentCommentaryBuffersStreamingCall(t *testing.T) {
	transform, _, _ := newSubagentCommentaryTestTransform(t, nil)
	arguments := `{"task_name":"inspect","message":"encrypted-spawn-message","agent_type":"explorer"}`
	item := map[string]any{
		"type": "function_call", "id": "item-spawn", "call_id": "call-spawn",
		"namespace": "collaboration", "name": "spawn_agent", "arguments": arguments,
	}
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "item-spawn", "call_id": "call-spawn",
			"namespace": "collaboration", "name": "spawn_agent", "arguments": "",
		},
	})
	if events, err := transform.TransformSSE(added); err != nil || len(events) != 0 {
		t.Fatalf("added events = %q, error %v", events, err)
	}
	delta := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": "item-spawn", "delta": arguments,
	})
	if events, err := transform.TransformSSE(delta); err != nil || len(events) != 1 || !bytes.Contains(events[0], []byte("response.in_progress")) {
		t.Fatalf("delta events = %q, error %v", events, err)
	}
	argumentsDone := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.done", "item_id": "item-spawn", "arguments": arguments,
	})
	if events, err := transform.TransformSSE(argumentsDone); err != nil || len(events) != 1 || !bytes.Contains(events[0], []byte("response.in_progress")) {
		t.Fatalf("arguments events = %q, error %v", events, err)
	}
	itemDone := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done", "output_index": 0, "item": item,
	})
	events, err := transform.TransformSSE(itemDone)
	if err != nil || len(events) != 4 {
		t.Fatalf("done events = %q, error %v", events, err)
	}
	if !bytes.Contains(events[0], []byte("Starting subagent.")) || bytes.Contains(events[0], []byte("encrypted-spawn-message")) || !bytes.Equal(events[1], added) ||
		!bytes.Equal(events[2], argumentsDone) || !bytes.Equal(events[3], itemDone) {
		t.Fatalf("done events = %q", events)
	}

	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": "resp-stream", "status": "completed", "output": []any{item},
			"usage": map[string]any{
				"input_tokens": 75, "output_tokens": 12,
				"input_tokens_details":  map[string]any{"cached_tokens": 50},
				"output_tokens_details": map[string]any{"reasoning_tokens": 9},
			},
		},
	})
	observeTestResponseUsage(t, transform, completed, true)
	events, err = transform.TransformSSE(completed)
	if err != nil || len(events) != 2 {
		t.Fatalf("completed events = %q, error %v", events, err)
	}
	if text := commentaryEventText(t, events[0]); text != "Tokens: i=75, ci=50, o=12, r=9" {
		t.Fatalf("usage commentary = %q", text)
	}
	var terminal struct {
		Response struct {
			Output []map[string]json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(events[1], &terminal) != nil || len(terminal.Response.Output) != 3 ||
		bytes.Count(events[1], []byte("Starting subagent.")) != 1 ||
		jsonString(terminal.Response.Output[2], "arguments") != arguments {
		t.Fatalf("completed event = %s", events[1])
	}
	if text := commentaryText(t, terminal.Response.Output[0]); text != "Tokens: i=75, ci=50, o=12, r=9" {
		t.Fatalf("terminal usage commentary = %q", text)
	}
}

func TestSubagentTokenUsageCommentaryOnFailedAndIncompleteStops(t *testing.T) {
	for _, status := range []string{"failed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			metadata := codexTurnMetadata{SubagentKind: "thread_spawn"}
			transform, _, _ := newSubagentCommentaryTestTransformWithMetadata(t, nil, metadata)
			terminal := mustTestJSON(t, map[string]any{
				"type": "response." + status,
				"response": map[string]any{
					"id": "resp-" + status, "status": status, "output": []any{},
					"usage": map[string]any{
						"input_tokens": 90, "output_tokens": 11,
						"input_tokens_details":  map[string]any{"cached_tokens": 60},
						"output_tokens_details": map[string]any{"reasoning_tokens": 7},
					},
				},
			})
			observeTestResponseUsage(t, transform, terminal, true)
			events, err := transform.TransformSSE(terminal)
			if err != nil || len(events) != 1 {
				t.Fatalf("terminal events = %q, error %v", events, err)
			}
			if !bytes.Contains(events[0], []byte(`"type":"response.`+status+`"`)) ||
				bytes.Count(events[0], []byte("Tokens: i=90")) != 1 {
				t.Fatalf("terminal event = %s", events[0])
			}
		})
	}
}

func observeTestResponseUsage(t *testing.T, transform *hpatchResponseTransform, payload []byte, streamEvent bool) {
	t.Helper()
	counts, observed := usageFromResponsePayload(payload, streamEvent)
	if !observed {
		t.Fatal("test response has no provider usage")
	}
	transform.observeResponseUsage(counts)
}

func TestSubagentCommentaryRejectsIncompleteCallBeforeHistoryCommit(t *testing.T) {
	transform, _, _ := newSubagentCommentaryTestTransform(t, nil)
	transform.subagentPending["item-spawn"] = subagentPendingCall{callID: "call-spawn"}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
			"output": []any{},
		},
	})

	events, err := transform.TransformSSE(completed)
	if err == nil || err.Error() != "upstream completed with an incomplete subagent call" || len(events) != 0 {
		t.Fatalf("events = %q, error = %v", events, err)
	}
	if transform.historyCommitted {
		t.Fatal("incomplete subagent call committed turn history")
	}
}

func TestSubagentCommentaryPreservesPendingHPatchEventRejection(t *testing.T) {
	transform, _, _ := newSubagentCommentaryTestTransform(t, nil)
	transform.pending["item-hpatch"] = hpatchPendingCall{callID: "call-hpatch"}

	for _, eventType := range []string{
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
	} {
		t.Run(eventType, func(t *testing.T) {
			payload := mustTestJSON(t, map[string]any{"type": eventType, "item_id": "item-hpatch"})
			events, err := transform.TransformSSE(payload)
			if err == nil || !strings.Contains(err.Error(), "unsupported hpatch-related stream event") || len(events) != 0 {
				t.Fatalf("events = %q, error = %v", events, err)
			}
		})
	}
}

func newSubagentCommentaryTestTransform(
	t *testing.T,
	conversation []any,
) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest) {
	t.Helper()
	return newSubagentCommentaryTestTransformWithMetadata(t, conversation, codexTurnMetadata{})
}

func newSubagentCommentaryTestTransformWithMetadata(
	t *testing.T,
	conversation []any,
	metadata codexTurnMetadata,
) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest) {
	t.Helper()
	additional := testCodeModeAdditionalTools(testCodeModeDescription)
	namespaces := additional["tools"].([]any)
	collaboration := namespaces[1].(map[string]any)
	collaboration["tools"] = []any{
		map[string]any{"type": "function", "name": "spawn_agent"},
		map[string]any{"type": "function", "name": "followup_task"},
		map[string]any{"type": "function", "name": "send_message"},
	}
	input := append([]any{additional, map[string]any{"role": "user", "content": "task"}}, conversation...)
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-parent", "reasoning": map[string]any{"effort": "medium"}, "input": input,
		"tools":       []any{map[string]any{"type": "function", "name": "lookup"}},
		"tool_choice": "auto", "parallel_tool_calls": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	translator := hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
		t.Fatal("unexpected hpatch translation")
		return nil, nil
	})
	proxy := newManagedHPatchProxy(t, translator)
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	workspace := t.TempDir()
	metadata.RequestKind = "turn"
	metadata.Directories = map[string]json.RawMessage{workspace: nil}
	transform, err := proxy.prepareRequest(t.Context(), &request, "session", "thread", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transform.Close)
	return transform, proxy, &request
}

func commentaryText(t *testing.T, item map[string]json.RawMessage) string {
	t.Helper()
	if jsonString(item, "type") != "message" || jsonString(item, "phase") != "commentary" {
		t.Fatalf("not commentary: %#v", item)
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(item["content"], &content); err != nil || len(content) != 1 {
		t.Fatalf("commentary content = %s, error %v", item["content"], err)
	}
	return jsonString(content[0], "text")
}

func commentaryEventText(t *testing.T, event []byte) string {
	t.Helper()
	var envelope struct {
		Item map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(event, &envelope); err != nil {
		t.Fatal(err)
	}
	return commentaryText(t, envelope.Item)
}

func containsAgentMessage(items []map[string]json.RawMessage, id string) bool {
	for _, item := range items {
		if jsonString(item, "type") == "agent_message" && jsonString(item, "id") == id {
			return true
		}
	}
	return false
}
