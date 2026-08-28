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

	spawnArguments := `{"task_name":"inspect","message":"first line\nsecond line","agent_type":"explorer","model":"gpt-requested","reasoning_effort":"low"}`
	followupArguments := `{"target":"/root/explorer","message":"check the exact edge case"}`
	payload := mustTestJSON(t, map[string]any{
		"status": "completed",
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
	texts := []string{
		commentaryText(t, response.Output[0]),
		commentaryText(t, response.Output[1]),
		commentaryText(t, response.Output[3]),
	}
	if texts[0] != "Response from /root/explorer:\n"+responseText {
		t.Fatalf("response commentary = %q", texts[0])
	}
	wantSpawn := "Starting subagent.\nRole: explorer\nModel: gpt-requested\nReasoning effort: low\nMessage:\nfirst line\nsecond line"
	if texts[1] != wantSpawn {
		t.Fatalf("spawn commentary = %q", texts[1])
	}
	if texts[2] != "Follow-up to /root/explorer:\ncheck the exact edge case" {
		t.Fatalf("follow-up commentary = %q", texts[2])
	}
	if jsonString(response.Output[2], "arguments") != spawnArguments ||
		jsonString(response.Output[4], "arguments") != followupArguments ||
		jsonString(response.Output[5], "name") != "send_message" {
		t.Fatalf("collaboration calls changed: %s", transformed)
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
	generated := subagentCommentaryMessage(generatedID, "Response from /root/worker:\n"+responseText)
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
	arguments := `{"task_name":"inspect","message":"inspect exactly","agent_type":"explorer"}`
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
	if !bytes.Contains(events[0], []byte("Starting subagent.")) || !bytes.Equal(events[1], added) ||
		!bytes.Equal(events[2], argumentsDone) || !bytes.Equal(events[3], itemDone) {
		t.Fatalf("done events = %q", events)
	}

	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": []any{item}},
	})
	events, err = transform.TransformSSE(completed)
	if err != nil || len(events) != 1 {
		t.Fatalf("completed events = %q, error %v", events, err)
	}
	var terminal struct {
		Response struct {
			Output []map[string]json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(events[0], &terminal) != nil || len(terminal.Response.Output) != 2 ||
		bytes.Count(events[0], []byte("Starting subagent.")) != 1 ||
		jsonString(terminal.Response.Output[1], "arguments") != arguments {
		t.Fatalf("completed event = %s", events[0])
	}
}

func newSubagentCommentaryTestTransform(
	t *testing.T,
	conversation []any,
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
	workspace := t.TempDir()
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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

func containsAgentMessage(items []map[string]json.RawMessage, id string) bool {
	for _, item := range items {
		if jsonString(item, "type") == "agent_message" && jsonString(item, "id") == id {
			return true
		}
	}
	return false
}
