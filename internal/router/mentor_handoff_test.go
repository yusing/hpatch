package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func mentorTestHeaders(t *testing.T, threadID string) http.Header {
	t.Helper()
	metadata, err := json.Marshal(codexTurnMetadata{RequestKind: "turn", SubagentKind: threadSpawnSubagentKind})
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set(openAISubagentHeader, threadSpawnSubagent)
	headers.Set(codexTurnMetadataHeader, string(metadata))
	if threadID != "" {
		headers.Set(threadIDHeader, threadID)
	}
	return headers
}

func mentorTestRequest(t *testing.T, model string) parsedResponsesRequest {
	t.Helper()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": model,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "keep exact history"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mentorTestItems(t *testing.T, itemTypes ...string) []json.RawMessage {
	t.Helper()
	items := make([]json.RawMessage, 0, len(itemTypes))
	for index, itemType := range itemTypes {
		item := map[string]any{"type": itemType, "id": fmt.Sprintf("item-%d", index)}
		if itemType == "message" {
			item["role"] = "assistant"
		}
		items = append(items, mustTestJSON(t, item))
	}
	return items
}

func TestMentorHandoffUsesCanonicalThreadSpawnMarker(t *testing.T) {
	mentor := newMentorHandoff()
	tests := []struct {
		name    string
		model   string
		headers http.Header
		want    bool
		wantErr string
	}{
		{name: "thread spawn", model: "gpt-5.6-luna", headers: mentorTestHeaders(t, "child"), want: true},
		{name: "second eligible model", model: "gpt-5.6-terra", headers: mentorTestHeaders(t, "child-terra"), want: true},
		{name: "ordinary session", model: "gpt-5.6-luna", headers: http.Header{}},
		{name: "ordinary fork metadata", model: "gpt-5.6-luna", headers: serverMetadataHeaders(t, "turn", nil)},
		{name: "leader model", model: mentorLeaderModel, headers: mentorTestHeaders(t, "leader")},
		{name: "unknown lower model", model: "gpt-test", headers: mentorTestHeaders(t, "unknown")},
		{name: "marker without metadata", model: "gpt-5.6-luna", headers: http.Header{openAISubagentHeader: []string{threadSpawnSubagent}}, wantErr: "canonical thread-spawn metadata"},
		{name: "marker without thread", model: "gpt-5.6-luna", headers: mentorTestHeaders(t, ""), wantErr: "Codex thread ID"},
		{name: "duplicate marker", model: "gpt-5.6-luna", headers: http.Header{openAISubagentHeader: []string{threadSpawnSubagent, threadSpawnSubagent}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mentorTestRequest(t, test.model)
			metadata, valid := decodeCodexTurnMetadata(test.headers)
			handoff, err := mentor.prepare(test.headers, metadata, valid, &request)
			if test.wantErr != "" {
				if err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.wantErr)) {
					t.Fatalf("error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (handoff != nil) != test.want {
				t.Fatalf("handoff = %v, want active %t", handoff != nil, test.want)
			}
			if !test.want {
				if got := request.model(); got != test.model {
					t.Fatalf("model = %q, want unchanged %q", got, test.model)
				}
				return
			}
			if got := request.modelDescription(); got != mentorLeaderModel+" "+mentorLeaderEffort {
				t.Fatalf("leader request = %q", got)
			}
			var reasoning map[string]json.RawMessage
			if err := json.Unmarshal(request.fields["reasoning"], &reasoning); err != nil {
				t.Fatal(err)
			}
			if string(reasoning["summary"]) != `"auto"` {
				t.Fatalf("reasoning siblings = %s", request.fields["reasoning"])
			}
		})
	}
}

func TestMentorHandoffCompletesAtEachBound(t *testing.T) {
	tests := []struct {
		name       string
		responses  [][]json.RawMessage
		inputUsage []uint64
	}{
		{name: "tool calls", responses: [][]json.RawMessage{mentorTestItems(t, "custom_tool_call", "function_call"), mentorTestItems(t, "custom_tool_call"), nil}, inputUsage: []uint64{10_000, 10_000, 10_000}},
		{name: "messages", responses: [][]json.RawMessage{mentorTestItems(t, "message"), mentorTestItems(t, "message")}, inputUsage: []uint64{10_000, 10_000}},
		{name: "input tokens with overshoot", responses: [][]json.RawMessage{nil, nil, nil}, inputUsage: []uint64{30_000, 25_000, 50_001}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mentor := newMentorHandoff()
			headers := mentorTestHeaders(t, "child")
			metadata, valid := decodeCodexTurnMetadata(headers)
			for index, items := range test.responses {
				request := mentorTestRequest(t, "gpt-5.6-luna")
				handoff, err := mentor.prepare(headers, metadata, valid, &request)
				if err != nil {
					t.Fatal(err)
				}
				if handoff == nil {
					t.Fatalf("response %d was handed off early", index+1)
				}
				handoff.observation.observeItems(items)
				progress := handoff.record(test.inputUsage[index], true)
				if got, want := progress.complete, index == len(test.responses)-1; got != want {
					t.Fatalf("response %d complete = %t, want %t", index+1, got, want)
				}
				if test.name == "tool calls" && index == 1 && !progress.awaitingToolResult {
					t.Fatal("tool-call threshold did not retain the mentor for its result")
				}
				if test.name == "input tokens with overshoot" && progress.latestInputTokens != test.inputUsage[index] {
					t.Fatalf("response %d input tokens = %d, want latest usage %d", index+1, progress.latestInputTokens, test.inputUsage[index])
				}
			}
			request := mentorTestRequest(t, "gpt-5.6-luna")
			handoff, err := mentor.prepare(headers, metadata, valid, &request)
			if err != nil {
				t.Fatal(err)
			}
			if handoff != nil || request.modelDescription() != "gpt-5.6-luna medium" {
				t.Fatalf("post-handoff request = %q, active = %t", request.modelDescription(), handoff != nil)
			}
		})
	}
}

func TestMentorHandoffWaitsForCompletedToolResultResponse(t *testing.T) {
	mentor := newMentorHandoff()
	headers := mentorTestHeaders(t, "child")
	metadata, valid := decodeCodexTurnMetadata(headers)
	record := func(items []json.RawMessage, completed bool) mentorProgress {
		t.Helper()
		request := mentorTestRequest(t, "gpt-5.6-luna")
		handoff, err := mentor.prepare(headers, metadata, valid, &request)
		if err != nil {
			t.Fatal(err)
		}
		if handoff == nil {
			t.Fatal("mentor handed off before a completed result-consuming response")
		}
		handoff.observation.observeItems(items)
		return handoff.record(1_000, completed)
	}

	if progress := record(mentorTestItems(t, "custom_tool_call", "custom_tool_call", "custom_tool_call"), true); !progress.awaitingToolResult || progress.complete {
		t.Fatalf("tool threshold progress = %#v", progress)
	}
	if progress := record(nil, false); !progress.awaitingToolResult || progress.complete {
		t.Fatalf("failed result-consuming response progress = %#v", progress)
	}
	if progress := record(nil, true); progress.awaitingToolResult || !progress.complete {
		t.Fatalf("completed result-consuming response progress = %#v", progress)
	}
}

func TestMentorResponseObservationDoesNotDoubleCountStreamingItems(t *testing.T) {
	item := mentorTestItems(t, "custom_tool_call")[0]
	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"output": []json.RawMessage{item}},
	})
	done := mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item})
	var observation mentorResponseObservation
	if _, err := observation.TransformSSE(done); err != nil {
		t.Fatal(err)
	}
	if _, err := observation.TransformSSE(completed); err != nil {
		t.Fatal(err)
	}
	if observation.toolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", observation.toolCalls)
	}
	visible, err := observation.TransformSSE([]byte("[DONE]"))
	if err != nil || len(visible) != 1 || string(visible[0]) != "[DONE]" {
		t.Fatalf("DONE passthrough = %q, %v", visible, err)
	}
}

func TestExecuteRequestMentorHandoffPreservesHistoryAndRestoresRequestedModel(t *testing.T) {
	response := func(itemTypes ...string) *http.Response {
		return serverHTTPResponse(string(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": mentorTestItems(t, itemTypes...),
			"usage":  map[string]any{"input_tokens": 10_000},
		})))
	}
	provider := &serverFakeProvider{results: []serverForwardResult{
		{response: response("custom_tool_call", "function_call")},
		{response: response("custom_tool_call")},
		{response: response("message")},
		{response: response("message")},
	}}
	mentor := newMentorHandoff()
	headers := mentorTestHeaders(t, "child")
	for range 4 {
		request := mentorTestRequest(t, "gpt-5.6-luna")
		if err := executeRequest(
			t.Context(), t.Context(), request, headers, "session", provider, io.Discard,
			newDiagnostics(io.Discard), time.Now, nil, nil, mentor,
		); err != nil {
			t.Fatal(err)
		}
	}
	if len(provider.forwarded) != 4 {
		t.Fatalf("upstream requests = %d, want 4", len(provider.forwarded))
	}
	wantModels := []string{mentorLeaderModel, mentorLeaderModel, mentorLeaderModel, "gpt-5.6-luna"}
	for index, body := range provider.forwarded {
		request, err := parseResponsesRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		if got := request.model(); got != wantModels[index] {
			t.Errorf("request %d model = %q, want %q", index+1, got, wantModels[index])
		}
		if !bytes.Contains(request.fields["input"], []byte("keep exact history")) {
			t.Errorf("request %d lost inherited input: %s", index+1, request.fields["input"])
		}
	}
}

func TestExecuteRequestMentorHandoffCountsFailedResponseInput(t *testing.T) {
	failed := serverHTTPResponse(string(mustTestJSON(t, map[string]any{
		"status": "failed",
		"usage":  map[string]any{"input_tokens": 55_000},
	})))
	completed := serverHTTPResponse(string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage":  map[string]any{"input_tokens": 1},
	})))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: failed}, {response: completed}}}
	mentor := newMentorHandoff()
	headers := mentorTestHeaders(t, "child")
	for range 2 {
		request := mentorTestRequest(t, "gpt-5.6-luna")
		if err := executeRequest(
			t.Context(), t.Context(), request, headers, "session", provider, io.Discard,
			newDiagnostics(io.Discard), time.Now, nil, nil, mentor,
		); err != nil {
			t.Fatal(err)
		}
	}
	first, err := parseResponsesRequest(provider.forwarded[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseResponsesRequest(provider.forwarded[1])
	if err != nil {
		t.Fatal(err)
	}
	if first.model() != mentorLeaderModel || second.model() != "gpt-5.6-luna" {
		t.Fatalf("models after failed-response budget = %q, %q", first.model(), second.model())
	}
}

func TestMentorHandoffRejectsInvalidReasoningWithoutSharingSessionCapacity(t *testing.T) {
	mentor := newMentorHandoff()
	headers := mentorTestHeaders(t, "child")
	metadata, valid := decodeCodexTurnMetadata(headers)
	request := mentorTestRequest(t, "gpt-5.6-luna")
	request.fields["reasoning"] = json.RawMessage(`"invalid"`)
	if _, err := mentor.prepare(headers, metadata, valid, &request); err == nil {
		t.Fatal("invalid reasoning was accepted")
	}

	mentor = newMentorHandoff()
	for index := range maxSessionHistories {
		mentor.sessions[fmt.Sprintf("active-%d", index)] = mentorSession{}
	}
	request = mentorTestRequest(t, "gpt-5.6-luna")
	prepared, err := mentor.prepare(headers, metadata, valid, &request)
	if err != nil || prepared == nil {
		t.Fatalf("new child at session history limit = %#v, %v", prepared, err)
	}
}
