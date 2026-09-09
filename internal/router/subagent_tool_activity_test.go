package router

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentToolActivityJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
			root, _ := prepareActivityTest(t, proxy, "root", "r", "", "/root", nil)
			child, _ := prepareActivityTest(t, proxy, "child", "c", "r", "/root/worker", nil)
			calls := []map[string]any{
				{"type": "function_call", "id": "first", "call_id": "first", "namespace": "functions", "name": "lookup", "arguments": "{\"query\":\"hello\"}"},
				{"type": "custom_tool_call", "id": "second", "call_id": "second", "name": "external", "input": "first line\n" + strings.Repeat("界", 300)},
				{"type": "function_call", "id": "third", "call_id": "third", "namespace": "collaboration", "name": "send_message", "arguments": "opaque message"},
				{"type": "shell_call", "id": "shell", "status": "completed", "action": map[string]any{"commands": []string{"echo a", "  echo b"}}},
				{"type": "local_shell_call", "id": "exec", "status": "completed", "action": map[string]any{"command": []string{"bash", "-lc", "cat a"}}},
				{"type": "web_search_call", "id": "web", "status": "completed", "action": map[string]any{"type": "search", "query": "Go parser"}},
			}
			payload := mustTestJSON(t, map[string]any{"status": "completed", "output": calls})
			if stream {
				for _, call := range calls {
					for _, eventType := range []string{"response.output_item.added", "response.output_item.done"} {
						event := mustTestJSON(t, map[string]any{"type": eventType, "item": call})
						events, err := child.TransformSSE(event)
						if err != nil || len(events) != 1 || !bytes.Equal(events[0], event) {
							t.Fatalf("child call changed: %s, %v", events, err)
						}
					}
				}
				event := mustTestJSON(t, map[string]any{"type": "response.completed", "response": json.RawMessage(payload)})
				if _, err := child.TransformSSE(event); err != nil {
					t.Fatal(err)
				}
			} else if output, err := child.TransformJSON(payload); err != nil || !bytes.Equal(output, payload) {
				t.Fatalf("child output changed: %s, %v", output, err)
			}
			visible, err := root.TransformJSON([]byte(`{"status":"completed","output":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Output []map[string]json.RawMessage }
			if err := json.Unmarshal(visible, &response); err != nil || len(response.Output) != 2 {
				t.Fatalf("distinct calls or terminal deduplication: %s, %v", visible, err)
			}
			// Start metadata precedes the child's distinct tool calls.
			response.Output = response.Output[1:]
			got := commentaryText(t, response.Output[0])
			want := "In `/root/worker`\n\n- Tool call: `functions.lookup`\n  `{\"query\":\"hello\"}`" +
				"\n\n- Tool call: `external`\n  ```\n  first line\n  " + strings.Repeat("界", 300) + "\n  ```" +
				"\n\n- Tool call: `collaboration.send_message`" +
				"\n\n- Run\n  ```\n  echo a\n    echo b\n  ```" +
				"\n\n- Read `a`\n\n- Search web\n  `Go parser`"
			if got != want {
				t.Fatalf("grouped display: got %q, want %q", got, want)
			}
			_, replay := prepareActivityTest(t, proxy, "replay", "c", "r", "/root/worker", []any{response.Output[0], calls[0]})
			if bytes.Contains(replay.fields["input"], response.Output[0]["id"]) || !bytes.Contains(replay.fields["input"], mustTestJSON(t, calls[0])) {
				t.Fatalf("call or user-only replay changed: %s", replay.fields["input"])
			}
		})
	}
}

func TestSubagentTranslatedEditActivityJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			calls := 0
			proxy := newManagedHPatchProxy(t, testTranslator(t, &calls))
			root, _ := prepareActivityTest(t, proxy, "root", "r", "", "/root", nil)
			child, _ := prepareActivityTest(t, proxy, "child", "c", "r", "/root/worker", nil)
			call := testHPatchItem()
			original := mustTestJSON(t, call)
			child.directory = t.TempDir()

			if stream {
				if _, err := child.TransformSSE(mustTestJSON(t, map[string]any{
					"type": "response.output_item.done", "item": call,
				})); err != nil {
					t.Fatal(err)
				}
			} else {
				payload := mustTestJSON(t, map[string]any{"status": "completed", "output": []any{call}})
				if _, err := child.TransformJSON(payload); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatalf("display translated or executed the edit again: %d translations", calls)
			}
			if got := mustTestJSON(t, call); !bytes.Equal(got, original) {
				t.Fatalf("activity changed the original call: %s", got)
			}

			visible, err := root.TransformJSON([]byte(`{"status":"completed","output":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Output []map[string]json.RawMessage }
			if err := json.Unmarshal(visible, &response); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range response.Output {
				text := strings.ReplaceAll(commentaryText(t, item), "\n  ", "\n")
				if strings.Contains(text, "```diff\n"+testTranslatedPatch+"```") {
					found = true
				}
			}
			if !found {
				t.Fatalf("translated edit activity missing: %s", visible)
			}
			if calls != 1 {
				t.Fatalf("root delivery retranslated the edit: %d translations", calls)
			}
		})
	}
}

func TestSubagentToolActivityRejectsPartialCalls(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	root, _ := prepareActivityTest(t, proxy, "root", "r", "", "/root", nil)
	child, _ := prepareActivityTest(t, proxy, "child", "c", "r", "/root/worker", nil)
	for _, status := range []string{"in_progress", "incomplete"} {
		call := map[string]json.RawMessage{"type": mustTestJSON(t, "function_call"), "id": mustTestJSON(t, status), "status": mustTestJSON(t, status)}
		child.collectSubagentToolCall(call)
	}
	if got := proxy.activity.drain("r", root.activityStarted, maxCommentaryPublicationBytes); len(got) != 1 || !strings.Contains(commentaryText(t, got[0]), "Started.") {
		t.Fatal("partial calls projected")
	}
}

func TestCommentaryCodeEscapesBackticks(t *testing.T) {
	if got := attributedCommentary("/root/a`b", "Working."); got != "[`` /root/a`b ``] Working." {
		t.Fatalf("code span: %s", got)
	}
	text := attributedCommentary("/root/a`b", "Working.")
	if attributedCommentary("/root/a`b", text) != text {
		t.Fatal("attribution duplicated")
	}
}
