package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func journalFinishResponse(t *testing.T, stream bool, status, snapshot string, calls ...any) *http.Response {
	t.Helper()
	terminal := map[string]any{"id": "finish-response", "status": status}
	switch snapshot {
	case "full", "snapshot-only":
		terminal["output"] = calls
	case "empty":
		terminal["output"] = []any{}
	}
	if !stream {
		return serverHTTPResponse(string(mustTestJSON(t, terminal)))
	}
	var events [][]byte
	for _, call := range calls {
		if snapshot == "snapshot-only" {
			break
		}
		events = append(events, mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": call}))
	}
	events = append(events, mustTestJSON(t, map[string]any{"type": "response." + status, "response": terminal}))
	response := serverHTTPResponse(finalAnswerTestWire(events))
	response.Header.Set("Content-Type", "text/event-stream")
	return response
}

func journalFinishCall(arguments string) map[string]any {
	return map[string]any{"type": "function_call", "id": "finish-item", "call_id": "finish-call", "namespace": "functions", "name": "journal", "arguments": arguments, "status": "completed"}
}

func journalFinishClientOutput(t *testing.T, stream bool, wire []byte) []map[string]json.RawMessage {
	t.Helper()
	body := wire
	if stream {
		body = nil
		for line := range strings.SplitSeq(string(wire), "\n") {
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok || data == "[DONE]" {
				continue
			}
			var event map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatal(err)
			}
			switch jsonString(event, "type") {
			case "response.completed", "response.failed", "response.incomplete":
				body = event["response"]
			}
		}
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode client terminal: %v; wire=%s", err, wire)
	}
	return response.Output
}

func TestJournalFinishEndsWithoutProviderContinuation(t *testing.T) {
	for _, mode := range []string{"json", "sse-full", "sse-empty", "sse-absent", "sse-snapshot-only"} {
		for _, batched := range []bool{false, true} {
			name := mode + map[bool]string{false: "/existing", true: "/batched"}[batched]
			t.Run(name, func(t *testing.T) {
				stream := mode != "json"
				snapshot := strings.TrimPrefix(mode, "sse-")
				if !stream {
					snapshot = "full"
				}
				proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
				proxy.journals = newJournalStore()
				var err error
				proxy.replayStore, err = openMekugiReplayStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				workspace := t.TempDir()
				if err := proxy.journals.initialize(t.Context(), proxy.replayStore, workspace, "thread-1", "/root", ""); err != nil {
					t.Fatal(err)
				}
				arguments := `{"op":"finish"}`
				if batched {
					arguments = `{"op":"finish","journal":[{"op":"add","text":"Completed the assigned milestone"}]}`
				} else if _, err := proxy.journals.apply(t.Context(), proxy.replayStore, workspace, "thread-1", "seed", []journalMutation{{Op: "add", Text: new("Completed the assigned milestone")}}); err != nil {
					t.Fatal(err)
				}
				provider := &serverFakeProvider{results: []serverForwardResult{{response: journalFinishResponse(t, stream, "completed", snapshot, journalFinishCall(arguments))}}}
				request := serverRequest(t, func(fields map[string]any) { fields["stream"] = stream })
				var output bytes.Buffer
				if err := executeRequest(t.Context(), t.Context(), request, serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}), "session", provider, &output, NewCriticalErrors(), proxy, nil, nil); err != nil {
					t.Fatal(err)
				}
				if len(provider.forwarded) != 1 {
					t.Fatalf("finish issued %d provider requests; want 1", len(provider.forwarded))
				}
				if !strings.Contains(output.String(), "Journal flush") || !strings.Contains(output.String(), "Completed the assigned milestone") {
					t.Fatalf("missing journal flush: %s", output.Bytes())
				}
				items, err := newJournalStore().list(t.Context(), proxy.replayStore, workspace, "thread-1")
				if err != nil || len(items) != 1 || !items[0].Reported || !items[0].Flushed {
					t.Fatalf("durable flush acknowledgement: %+v, %v", items, err)
				}
				found := false
				for _, item := range journalFinishClientOutput(t, stream, output.Bytes()) {
					if jsonString(item, "type") == "function_call" {
						t.Fatalf("router finish escaped as client-dispatched call: %s", mustTestJSON(t, item))
					}
					if journalResultCallID(item) == "finish-call" {
						var result struct {
							OK              bool     `json:"ok"`
							FinishRequested bool     `json:"finish_requested"`
							JournalIDs      []string `json:"journal_ids"`
						}
						if err := json.Unmarshal([]byte(jsonString(item, "output")), &result); err != nil || !result.OK || !result.FinishRequested {
							t.Fatalf("finish result: %s, %v", mustTestJSON(t, item), err)
						}
						if batched && (len(result.JournalIDs) != 1 || result.JournalIDs[0] != "j1") {
							t.Fatalf("missing assigned mutation ID: %+v", result)
						}
						found = true
					}
				}
				if !found {
					t.Fatalf("terminal lost retained finish result: %s", output.Bytes())
				}
			})
		}
	}
}

func TestJournalFinishDoesNotHidePendingCallsOrFlushFailures(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scenario := range []string{"mixed", "failed", "incomplete", "invalid-id", "invalid-text", "invalid-agent", "invalid-report-now"} {
			t.Run(map[bool]string{false: "json/", true: "sse/"}[stream]+scenario, func(t *testing.T) {
				proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
				workspace := t.TempDir()
				arguments := `{"op":"finish"}`
				invalid := strings.HasPrefix(scenario, "invalid-")
				if invalid {
					arguments = map[string]string{"invalid-id": `{"op":"finish","id":"j1"}`, "invalid-text": `{"op":"finish","text":"not allowed"}`, "invalid-agent": `{"op":"finish","agent":"/root"}`, "invalid-report-now": `{"op":"finish","report_now":true}`}[scenario]
				}
				// Seed through a separate router-owned call in the same response.
				seed := map[string]any{"type": "function_call", "id": "seed-item", "call_id": "seed-call", "name": "journal", "arguments": `{"op":"add","text":"Unflushed milestone"}`, "status": "completed"}
				pending := map[string]any{"type": "function_call", "id": "lookup-item", "call_id": "lookup-call", "name": "lookup", "arguments": `{}`, "status": "completed"}
				calls := []any{seed, journalFinishCall(arguments)}
				status := "completed"
				if scenario == "mixed" {
					calls = append(calls, pending)
				} else if scenario == "failed" || scenario == "incomplete" {
					status = scenario
				}
				snapshot := "full"
				if stream {
					snapshot = "empty"
				}
				provider := &serverFakeProvider{results: []serverForwardResult{{response: journalFinishResponse(t, stream, status, snapshot, calls...)}}}
				if invalid {
					provider.results = append(provider.results, serverForwardResult{response: journalFinishResponse(t, stream, "completed", snapshot, pending)})
				}
				request := serverRequest(t, func(fields map[string]any) { fields["stream"] = stream })
				var output bytes.Buffer
				err := executeRequest(t.Context(), t.Context(), request, serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}), "session", provider, &output, NewCriticalErrors(), proxy, nil, nil)
				if status == "completed" && err != nil {
					t.Fatal(err)
				}
				wantRequests := 1
				if invalid {
					wantRequests = 2
					if len(provider.forwarded) == 2 && !bytes.Contains(provider.forwarded[1], []byte(`\"ok\":false`)) {
						t.Fatalf("invalid finish error missing from continuation: %s", provider.forwarded[1])
					}
				}
				if len(provider.forwarded) != wantRequests {
					t.Fatalf("provider requests: %d; want %d", len(provider.forwarded), wantRequests)
				}
				if strings.Contains(output.String(), "Journal flush") {
					t.Fatalf("ineligible response flushed: %s", output.Bytes())
				}
				items, err := proxy.journals.list(t.Context(), proxy.replayStore, workspace, "thread-1")
				if err != nil || len(items) != 1 || items[0].Flushed {
					t.Fatalf("journal state: %+v, %v", items, err)
				}
				if scenario == "mixed" || invalid {
					found := false
					for _, item := range journalFinishClientOutput(t, stream, output.Bytes()) {
						found = found || (jsonString(item, "type") == "function_call" && jsonString(item, "call_id") == "lookup-call")
					}
					if !found {
						t.Fatalf("pending client call lost: %s", output.Bytes())
					}
				}
			})
		}
	}
}

func TestJournalFinishReplayDoesNotFinishLaterCRUD(t *testing.T) {
	workspace, storeDirectory := t.TempDir(), t.TempDir()
	proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	var err error
	proxy.replayStore, err = openMekugiReplayStore(storeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
	provider := &serverFakeProvider{results: []serverForwardResult{{response: journalFinishResponse(t, false, "completed", "full", journalFinishCall(`{"op":"finish","journal":[{"op":"add","text":"First task completed"}]}`))}}}
	var completed bytes.Buffer
	if err := executeRequest(t.Context(), t.Context(), serverRequest(t, nil), headers, "original-session", provider, &completed, NewCriticalErrors(), proxy, nil, nil); err != nil {
		t.Fatal(err)
	}
	history := journalFinishClientOutput(t, false, completed.Bytes())
	resumed := newManagedMekugiProxy(t, testTranslator(t, new(int)))
	resumed.replayStore, err = openMekugiReplayStore(storeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	request := serverRequest(t, func(fields map[string]any) {
		input := fields["input"].([]any)
		for _, item := range history {
			input = append(input, item)
		}
		fields["input"] = append(input, map[string]any{"role": "user", "content": "Start another task"})
	})
	add := map[string]any{"type": "function_call", "id": "new-item", "call_id": "new-call", "name": "journal", "arguments": `{"op":"add","text":"Second task in progress"}`, "status": "completed"}
	pending := map[string]any{"type": "function_call", "id": "lookup-item", "call_id": "lookup-call", "name": "lookup", "arguments": `{}`, "status": "completed"}
	provider = &serverFakeProvider{results: []serverForwardResult{
		{response: journalFinishResponse(t, false, "completed", "full", add)},
		{response: journalFinishResponse(t, false, "completed", "full", pending)},
	}}
	var output bytes.Buffer
	if err := executeRequest(t.Context(), t.Context(), request, headers, "fresh-session", provider, &output, NewCriticalErrors(), resumed, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 2 {
		t.Fatalf("replayed finish terminated ordinary CRUD; provider requests=%d", len(provider.forwarded))
	}
	if !bytes.Contains(provider.forwarded[0], []byte(`\"op\":\"finish\"`)) {
		t.Fatal("fresh router did not restore the durable finish call")
	}
	if strings.Contains(output.String(), "Journal flush") || !strings.Contains(output.String(), "lookup-call") {
		t.Fatalf("replayed finish affected current delivery: %s", output.Bytes())
	}
	items, err := newJournalStore().list(t.Context(), resumed.replayStore, workspace, "thread-1")
	if err != nil || len(items) != 2 || !items[0].Flushed || items[1].Flushed {
		t.Fatalf("replay changed journal delivery state: %+v, %v", items, err)
	}
}
