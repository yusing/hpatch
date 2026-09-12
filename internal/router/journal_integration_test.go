package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJournalRouterToolContinuesWithoutClientDispatch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
			workspace := t.TempDir()
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
			request := serverRequest(t, func(fields map[string]any) { fields["stream"] = stream })
			call := map[string]any{"type": "function_call", "id": "item-journal", "call_id": "journal-call", "name": "journal", "arguments": `{"op":"add","text":"Verified the journal path"}`, "status": "completed"}
			first := mustTestJSON(t, map[string]any{"id": "response-journal", "status": "completed", "output": []any{call}})
			second := mustTestJSON(t, map[string]any{
				"id": "response-answer", "status": "completed",
				"output": []any{map[string]any{"type": "message", "id": "answer", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Provider essay must not appear"}}}},
				"usage":  map[string]any{"input_tokens": 20, "output_tokens": 5, "input_tokens_details": map[string]any{"cached_tokens": 12}, "output_tokens_details": map[string]any{"reasoning_tokens": 3}},
			})
			response1 := serverHTTPResponse(string(first))
			response2 := serverHTTPResponse(string(second))
			if stream {
				response1 = serverHTTPResponse(finalAnswerTestWire([][]byte{
					mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": call}),
					mustTestJSON(t, map[string]any{"type": "response.completed", "response": json.RawMessage(first)}),
				}))
				var object struct {
					Output []json.RawMessage `json:"output"`
				}
				if err := json.Unmarshal(second, &object); err != nil {
					t.Fatal(err)
				}
				response2 = serverHTTPResponse(finalAnswerTestWire([][]byte{
					mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": object.Output[0]}),
					mustTestJSON(t, map[string]any{"type": "response.completed", "response": json.RawMessage(second)}),
				}))
				response1.Header.Set("Content-Type", "text/event-stream")
				response2.Header.Set("Content-Type", "text/event-stream")
			}
			provider := &serverFakeProvider{results: []serverForwardResult{{response: response1}, {response: response2}}}
			var output bytes.Buffer
			issues := NewCriticalErrors()
			if err := executeRequest(t.Context(), t.Context(), request, headers, "session", provider, &output, issues, proxy, nil, nil); err != nil {
				t.Fatal(err)
			}
			if len(issues.entries) != 0 {
				t.Fatalf("successful continuation recorded failure notices: %+v", issues.entries)
			}
			if len(provider.forwarded) != 2 {
				t.Fatalf("provider requests: %d", len(provider.forwarded))
			}
			if !bytes.Contains(provider.forwarded[1], []byte("function_call_output")) || !bytes.Contains(provider.forwarded[1], []byte("j1")) {
				t.Fatal("journal result missing from model continuation")
			}
			if strings.Contains(output.String(), "Provider essay must not appear") {
				t.Fatalf("provider final leaked: %s", output.String())
			}
			if !strings.Contains(output.String(), "Verified the journal path") || !strings.Contains(output.String(), "Tokens:") {
				t.Fatalf("missing terminal record: %s", output.String())
			}
			items, err := proxy.journals.list(t.Context(), proxy.replayStore, workspace, "thread-1")
			if err != nil || len(items) != 1 || !items[0].Reported {
				t.Fatalf("delivery acknowledgement: %+v %v", items, err)
			}
		})
	}
}

func TestJournalKnownAncestryOnly(t *testing.T) {
	activity := newSubagentActivity()
	for _, node := range []struct {
		thread, parent, path string
		child                bool
	}{
		{"root", "", "/root", false},
		{"a", "root", "/root/a", true},
		{"b", "root", "/root/b", true},
		{"nested", "a", "/root/a/nested", true},
		{"other", "", "/root", false},
	} {
		if !activity.observe(node.thread, node.parent, node.path, node.child) {
			t.Fatal("identity rejected")
		}
	}
	if got, err := activity.journalThread("a", "/root"); err != nil || got != "root" {
		t.Fatalf("ancestor: %q %v", got, err)
	}
	if got, err := activity.journalThread("root", "/root/a/nested"); err != nil || got != "nested" {
		t.Fatalf("descendant: %q %v", got, err)
	}
	for _, query := range []struct{ caller, path string }{{"a", "/root/b"}, {"other", "/root/a"}, {"missing", "/root"}} {
		if _, err := activity.journalThread(query.caller, query.path); err == nil {
			t.Fatalf("unproven access: %+v", query)
		}
	}
}

func TestJournalCatalogStripsPlansButKeepsHistory(t *testing.T) {
	historical := json.RawMessage(`{"type":"function_call","call_id":"old","name":"update_plan","arguments":"{\"plan\":[]}"}`)
	fields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[{"type":"function","name":"update_plan"},{"type":"function","name":"lookup","strict":true,"parameters":{"type":"object","properties":{"query":{"type":"string"}}}}]`),
		"input": mustTestJSON(t, []any{
			historical,
			map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "namespace", "name": "functions", "tools": []any{map[string]any{"type": "function", "name": "update_plan"}, map[string]any{"type": "function", "name": "clock"}}}}},
		}),
	}
	catalog := decodeResponsesToolCatalog(fields)
	strict := mustTestJSON(t, catalog.top.tools[1])
	if err := stripStockPlanTools(fields, catalog); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(fields["tools"], []byte("update_plan")) || !bytes.Contains(fields["tools"], strict) {
		t.Fatalf("top catalog: %s", fields["tools"])
	}
	var input []json.RawMessage
	if err := json.Unmarshal(fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input[0], historical) || bytes.Contains(input[1], []byte("update_plan")) {
		t.Fatalf("history or additional tools: %s", fields["input"])
	}
}

func TestJournalBufferOverflowCannotReleaseSuccessfulAnswer(t *testing.T) {
	stream := finalAnswerStream{journal: true}
	events := finalAnswerTestEvents(t, "final_answer")
	if _, buffered := stream.observe(events[0]); !buffered {
		t.Fatal("answer not buffered")
	}
	stream.bytes = upstreamJSONBufferBytes
	visible, buffered := stream.observe(events[1])
	if !buffered || len(visible) != 0 || stream.bufferErr == nil {
		t.Fatal("overflow allowed a successful answer escape")
	}
	if released := stream.flush(); len(released) != 2 {
		t.Fatal("failure drain lost buffered output")
	}
}

func TestJournalNamedResultDiscoversDurableReplay(t *testing.T) {
	store, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	proxy.replayStore = store
	workspace := t.TempDir()
	callID := "journal-call"
	call := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "call_id": mustMarshalJSON(callID),
		"name": mustMarshalJSON(journalToolName), "arguments": mustMarshalJSON(`{"op":"list"}`),
	}
	history := mekugiHistory{toolName: journalHistoryTool, upstreamItem: call, carrierName: journalToolName, carrierKind: codeModeCarrierFunction}
	if err := proxy.replayStore.put(t.Context(), workspace, map[string]mekugiHistory{callID: history}); err != nil {
		t.Fatal(err)
	}
	result := journalClientResult(map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call_output"), "call_id": mustMarshalJSON(callID),
		"output": mustMarshalJSON(`{"ok":true,"items":[]}`),
	})
	request := serverRequest(t, func(fields map[string]any) { fields["input"] = []any{result} })
	visible, err := proxy.reconcileVisibleInput(t.Context(), &request, workspace, "resumed")
	if err != nil {
		t.Fatal(err)
	}
	if visible[callID].toolName != journalHistoryTool {
		t.Fatal("named output did not recover durable call identity")
	}
	if err := restoreJournalCalls(&request, visible); err != nil {
		t.Fatal(err)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &input); err != nil || len(input) != 2 {
		t.Fatalf("missing durable pair: %s %v", request.fields["input"], err)
	}
}

func TestJournalNamedResultRestoresAndRebasesCachedInput(t *testing.T) {
	call := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "call_id": mustMarshalJSON("journal-call"),
		"name": mustMarshalJSON(journalToolName), "arguments": mustMarshalJSON(`{"op":"list"}`),
	}
	result := journalClientResult(map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call_output"), "call_id": mustMarshalJSON("journal-call"),
		"output": mustMarshalJSON(`{"ok":true,"items":[]}`),
	})
	if _, exists := result["call_id"]; exists || jsonString(result, "name") != journalToolName || !strings.HasPrefix(jsonString(result, "id"), "fco_") {
		t.Fatalf("result cannot survive Codex normalization: %s", mustMarshalJSON(result))
	}
	ordinary := map[string]json.RawMessage{"type": mustMarshalJSON("function_call"), "call_id": mustMarshalJSON("ordinary")}
	request := serverRequest(t, func(fields map[string]any) {
		fields["input"] = []any{map[string]any{"role": "user", "content": "work"}, result, ordinary}
	})
	request.cachedInput = 3
	visible := map[string]mekugiHistory{"journal-call": {toolName: journalHistoryTool, upstreamItem: call}}
	if err := restoreJournalCalls(&request, visible); err != nil {
		t.Fatal(err)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 4 || jsonString(input[1], "type") != "function_call" || jsonString(input[2], "call_id") != "journal-call" || request.cachedInput != 0 || !request.rebaseInput {
		t.Fatalf("incorrect restored prefix: %s cached=%d rebase=%v", request.fields["input"], request.cachedInput, request.rebaseInput)
	}
	if err := restoreJournalCalls(&request, visible); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request.fields["input"], &input); err != nil || len(input) != 4 {
		t.Fatalf("replay duplicated call: %s %v", request.fields["input"], err)
	}
}

func TestJournalChildFlushHasIndependentRootCopyBudget(t *testing.T) {
	activity := newSubagentActivity()
	activity.observe("root", "", "/root", false)
	activity.observe("child", "root", "/root/child", true)
	body := "Journal `/root/child`\n- `j1` " + strings.Repeat("x", maxCommentaryPublicationBytes)
	activity.collect("child", "live", "commentary", "Live update")
	activity.collect("child", "flush", "journal_flush", body)
	messages := activity.drain("root", time.Now(), 0, maxJournalFlushBytes)
	if len(messages) != 1 || !strings.Contains(commentaryMessageText(messages[0]), body) {
		t.Fatal("child terminal flush was lost to the live progress budget")
	}
	if len(activity.drain("root", time.Now(), 0, maxJournalFlushBytes)) != 0 {
		t.Fatal("child flush repeated after root delivery")
	}
}

func TestJournalCapacityDoesNotRejectUnrelatedRequest(t *testing.T) {
	proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	for i := range maxJournalThreads {
		if err := proxy.journals.initialize(t.Context(), nil, "workspace", fmt.Sprint(i), "/root", ""); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	request := serverRequest(t, nil)
	headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(`{"id":"answer","status":"completed","output":[]}`)}}}
	var output bytes.Buffer
	if err := executeRequest(t.Context(), t.Context(), request, headers, "new", provider, &output, nil, proxy, nil, nil); err != nil {
		t.Fatalf("journal capacity blocked unrelated request: %v", err)
	}
}

func TestJournalTerminalRetentionFailureDoesNotSucceedSilently(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(fmt.Sprint(child), func(t *testing.T) {
			proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
			store, err := openMekugiReplayStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			proxy.replayStore = store
			store.maxCommentaryBytes = 1
			workspace := t.TempDir()
			request := serverRequest(t, nil)
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
			if child {
				headers.Set(codexTurnMetadataHeader, string(mustMarshalJSON(codexTurnMetadata{
					RequestKind: "turn", SubagentKind: "thread_spawn", AgentName: "/root/child", Directories: map[string]json.RawMessage{workspace: nil},
				})))
			}
			if err := proxy.journals.initialize(t.Context(), store, workspace, "thread-1", "/root", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := proxy.journals.apply(t.Context(), store, workspace, "thread-1", "add", []journalMutation{{Op: "add", Text: new("Must not be silently lost")}}); err != nil {
				t.Fatal(err)
			}
			provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(`{"id":"answer","status":"completed","output":[]}`)}}}
			var output bytes.Buffer
			if err := executeRequest(t.Context(), t.Context(), request, headers, "quota", provider, &output, nil, proxy, nil, nil); err == nil {
				t.Fatal("terminal succeeded after required journal retention failed")
			}
			items, err := proxy.journals.list(t.Context(), store, workspace, "thread-1")
			if err != nil || len(items) != 1 || items[0].Reported {
				t.Fatalf("failed retention marked item reported: %+v %v", items, err)
			}
		})
	}
}

func TestJournalToolReturnsBatchedIDs(t *testing.T) {
	proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	workspace := t.TempDir()
	request := serverRequest(t, nil)
	transform, err := proxy.prepareRequest(t.Context(), &request, "batch-ids", "thread-1", codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer transform.Close()
	for _, test := range []struct {
		call string
		args string
		want string
	}{
		{"add", `{"op":"add","text":"Main milestone","journal":[{"op":"add","text":"Batched milestone"}]}`, `{"ok":true,"id":"j2","journal_ids":["j1"]}`},
		{"list", `{"op":"list","journal":[{"op":"add","text":"Before listing"}]}`, ""},
	} {
		item := map[string]json.RawMessage{
			"type": mustMarshalJSON("function_call"), "name": mustMarshalJSON("journal"),
			"call_id": mustMarshalJSON(test.call), "arguments": mustMarshalJSON(test.args),
		}
		result, err := transform.executeJournalCall(item)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(jsonString(result, "output")), &got); err != nil {
			t.Fatal(err)
		}
		if test.want != "" {
			var want map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			if string(mustMarshalJSON(got)) != string(mustMarshalJSON(want)) {
				t.Fatalf("add result = %s", result["output"])
			}
		} else {
			var items []journalListItem
			if err := json.Unmarshal(got["items"], &items); err != nil || len(items) != 3 || string(got["journal_ids"]) != `["j3"]` {
				t.Fatalf("list result = %s, error = %v", result["output"], err)
			}
		}
	}
}

func TestJournalLiveSnapshotCacheRefreshesOnMutationAndTerminal(t *testing.T) {
	proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	replay, err := openMekugiReplayStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy.replayStore = replay
	workspace := t.TempDir()
	request := serverRequest(t, nil)
	transform, err := proxy.prepareRequest(t.Context(), &request, "live-cache", "thread-1", codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer transform.Close()
	if _, err := proxy.journals.apply(t.Context(), replay, workspace, "thread-1", "", []journalMutation{{Op: "add", Text: new("Silent milestone")}}); err != nil {
		t.Fatal(err)
	}
	messages, err := transform.prepareJournalDelivery(false)
	if err != nil || len(messages) != 0 || transform.journalQuietFile == nil {
		t.Fatalf("quiet snapshot not cached: %d %v", len(messages), err)
	}
	other := newJournalStore()
	if _, err := other.apply(t.Context(), replay, workspace, "thread-1", "", []journalMutation{{Op: "edit", ID: "j1", Text: new("Show revised milestone"), ReportNow: true}}); err != nil {
		t.Fatal(err)
	}
	messages, err = transform.prepareJournalDelivery(false)
	if err != nil || len(messages) != 1 || !strings.Contains(commentaryMessageText(messages[0]), "Show revised milestone") {
		t.Fatalf("external mutation missed: %v %v", messages, err)
	}
	transform.ReleaseDelivery()
	// Exhausted live delivery must also use the cache even with pending notices.
	transform.journalLiveBytes = maxCommentaryPublicationBytes
	messages, err = transform.prepareJournalDelivery(false)
	if err != nil || len(messages) != 0 || transform.journalQuietFile == nil {
		t.Fatalf("exhausted live snapshot not cached: %d %v", len(messages), err)
	}
	messages, err = transform.prepareJournalDelivery(true)
	if err != nil || len(messages) != 1 || !strings.Contains(commentaryMessageText(messages[0]), "Show revised milestone") {
		t.Fatalf("terminal failed to bypass live cache: %v %v", messages, err)
	}
	transform.ReleaseDelivery()
}

func TestJournalChildUsageWaitsForDeferredFlush(t *testing.T) {
	activity := newSubagentActivity()
	activity.observe("root", "", "/root", false)
	activity.observe("first", "root", "/root/first", true)
	activity.observe("second", "root", "/root/second", true)
	activity.collect("first", "first-flush", "journal_flush", "First flush")
	activity.collect("second", "second-flush", "journal_flush", "Second flush")
	activity.collect("second", "second-usage", "usage", "Tokens: second")
	// Leave enough terminal budget for only the first child, with live capacity
	// still available. The second table must wait for its own flush.
	firstSize := len("[`/root/first`] First flush")
	messages := activity.drain("root", time.Now(), maxCommentaryPublicationBytes, firstSize)
	if len(messages) != 1 || !strings.Contains(commentaryMessageText(messages[0]), "First flush") {
		t.Fatalf("table overtook a deferred flush: %v", messages)
	}
	messages = activity.drain("root", time.Now(), maxCommentaryPublicationBytes, maxJournalFlushBytes)
	if len(messages) != 2 || !strings.Contains(commentaryMessageText(messages[0]), "Second flush") ||
		!strings.Contains(commentaryMessageText(messages[1]), "Tokens: second") {
		t.Fatalf("deferred child ordering changed: %v", messages)
	}
}
