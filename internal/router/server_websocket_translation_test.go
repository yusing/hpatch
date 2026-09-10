package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestResponsesWebSocketIncrementalTranslationAndVisibleSources(t *testing.T) {
	proxy := newToolPluginTestProxy(t)
	proxy.customizedInstructions = true
	proxy.compactModelProtocol = true
	proxy.activity.copies["router-only"] = struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	headers := codexAuthHeaders()
	headers.Set(sessionIDHeader, "socket-session")
	for name, values := range serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil}) {
		headers[name] = values
	}
	source := "a native output line that remains available on the same connection\n"
	conn := testResponsesSocket(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.CloseNow()
		first := socketRead(t, ctx, upstream)
		if !strings.Contains(string(first["tools"]), `"plugin_tool"`) || strings.Contains(string(first["input"]), "router-only") {
			t.Errorf("request projection missing: %s", mustMarshalJSON(first))
		}
		item := map[string]string{"type": "custom_tool_call", "id": "item", "call_id": "plugin-call", "name": "plugin_tool", "input": "function", "status": "completed"}
		socketWrite(t, ctx, upstream, socketEvent("response.created", "first"))
		socketWrite(t, ctx, upstream, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		socketWrite(t, ctx, upstream, map[string]any{"type": "response.completed", "response": map[string]any{"id": "first", "status": "completed", "output": []any{item}}})
		next := socketRead(t, ctx, upstream)
		var input []map[string]json.RawMessage
		_ = json.Unmarshal(next["input"], &input)
		if jsonString(next, "previous_response_id") != "first" || len(input) != 1 || jsonString(input[0], "type") != "custom_tool_call_output" || jsonString(input[0], "output") != "result" {
			t.Errorf("incremental carrier restoration or prefix filtering failed: %s", mustMarshalJSON(next))
		}
		socketWrite(t, ctx, upstream, socketEvent("response.created", "second"))
		message := map[string]any{"type": "message", "id": "answer", "role": "assistant", "status": "completed", "content": []any{
			map[string]any{"type": "output_text", "text": "!V=source,1,1\n", "annotations": []any{}},
		}}
		socketWrite(t, ctx, upstream, map[string]any{"type": "response.completed", "response": map[string]any{"id": "second", "status": "completed", "output": []any{message}}})
		_, _, _ = upstream.Read(ctx)
	}), proxy, mustCTP2Codec(t), headers)
	socketWrite(t, ctx, conn, map[string]any{
		"type": "response.create", "model": "gpt-test", "instructions": "Follow the task.",
		"input": []any{
			testCodeModeAdditionalTools(testCodeModeDescription),
			map[string]any{"type": "message", "id": "router-only", "role": "assistant", "content": "router-only"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "call_source", "output": source},
			map[string]string{"role": "user", "content": "task"},
		},
		"tools": []any{map[string]string{"type": "function", "name": "lookup"}}, "tool_choice": "auto",
	})
	var terminal map[string]json.RawMessage
	for {
		event := socketRead(t, ctx, conn)
		if jsonString(event, "type") == "error" {
			t.Fatalf("translation error: %s", mustMarshalJSON(event))
		}
		if jsonString(event, "type") == "response.completed" {
			terminal = event
			break
		}
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	_ = json.Unmarshal(terminal["response"], &response)
	if len(response.Output) == 0 || jsonString(response.Output[0], "type") != "function_call" || jsonString(response.Output[0], "name") != "lookup" {
		t.Fatalf("plugin call not restored: %s", terminal["response"])
	}
	socketWrite(t, ctx, conn, map[string]any{"type": "response.create", "model": "gpt-test", "instructions": "Follow the task.", "tools": []any{map[string]string{"type": "function", "name": "lookup"}}, "tool_choice": "auto", "previous_response_id": "first", "input": []any{
		map[string]string{"type": "function_call_output", "call_id": "plugin-call", "output": "result"},
	}})
	for {
		event := socketRead(t, ctx, conn)
		if jsonString(event, "type") == "error" {
			t.Fatalf("continuation error: %s", mustMarshalJSON(event))
		}
		if jsonString(event, "type") == "response.completed" {
			if strings.Contains(string(event["response"]), "!V=") || !strings.Contains(string(event["response"]), strings.TrimSpace(source)) {
				t.Fatalf("cached CTP source was not restored: %s", event["response"])
			}
			break
		}
	}
}
