package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func compactionBudgetPressureHistory() []json.RawMessage {
	input := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "developer", "content": "Change only the router; retain exact evidence."}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Continue from protected_call. Do not repeat its operation."}),
		mustMarshalJSON(map[string]any{"type": "reasoning", "encrypted_content": "opaque-provider-state", "summary": []any{map[string]string{"type": "summary_text", "text": "The failure is unresolved; inspect it before proceeding."}}}),
	}
	for index := range 4 {
		id := fmt.Sprintf("bulk_%d", index)
		input = append(input, compactTestCall(id, fmt.Sprintf("cat old-%d.log", index)),
			compactTestOutput(id, strings.Repeat("payload ", 25_000), 0))
	}
	input = append(input,
		compactTestCall("protected_call", "cat evidence.log"), compactTestOutput("protected_call", strings.Repeat("exact evidence ", 500), 0),
		compactTestCall("failed_call", "go test ./..."), compactTestOutput("failed_call", "FAIL: assertion remains unresolved\nexpected alpha\nactual beta\nmultiline diagnostic detail\n", 1),
		compactTestCall("live_call", "long-running-command"), mustMarshalJSON(map[string]any{
			"type": "function_call_output", "call_id": "live_call",
			"output": string(mustMarshalJSON(map[string]any{"output": "still running", "exit_code": nil, "session_id": 1234})),
		}),
		compactTestCall("frontier_call", "pwd"), compactTestOutput("frontier_call", "/workspace/current\n", 0))
	return input
}

func TestCompactionBudgetNativeOutputFirstAndRestoration(t *testing.T) {
	input := compactionBudgetPressureHistory()
	original := mustMarshalJSON(input)
	baseline := reduceContextCompaction(input)
	baselineTokens, ok := compactionVisibleStringTokens(baseline...)
	if !ok || baselineTokens <= compactionTargetTokens+compactionOvershootTokens {
		t.Fatalf("fixture must exceed the ceiling with the fixed frontier: %d", baselineTokens)
	}
	retained, report, err := selectCompactionWorkingSet(t.Context(), input,
		compactionTargetTokens, compactionOvershootTokens, reduceContextCompactionWithPlan, compactionVisibleStringTokens)
	if err != nil || report.after > compactionTargetTokens || report.plan != (compactionRetentionPlan{8, 1}) {
		t.Fatalf("output-only pressure plan = %+v: %v", report, err)
	}
	if !bytes.Equal(original, mustMarshalJSON(input)) {
		t.Fatal("candidate evaluation mutated the original evidence")
	}
	if len(retained) != len(input) {
		t.Fatal("output-only plan retired native operation or reasoning items")
	}
	for index := range input {
		// Only the four unreferenced, completed bulk result bodies may change.
		if index >= 4 && index <= 10 && index%2 == 0 {
			if bytes.Equal(retained[index], input[index]) || strings.Contains(string(retained[index]), "payload payload") {
				t.Fatalf("historical bulk result %d survived", index)
			}
			continue
		}
		if !bytes.Equal(retained[index], input[index]) {
			t.Fatalf("authority, reference, invocation, reasoning, failure, live state or frontier changed at %d", index)
		}
	}
	measured, ok := compactionVisibleStringTokens(retained...)
	if !ok || measured != report.after {
		t.Fatalf("budget report differs from native replay: %d != %d", measured, report.after)
	}

	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.seal(t.Context(), retained)
	if err != nil {
		t.Fatal(err)
	}
	// A different instance must restore selected native history, not the
	// pre-compaction bulk. Fresh suffix authority must survive unchanged.
	suffix := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Now inspect the unresolved failure."})
	resumed := &contextCompactor{keyPath: compactor.keyPath}
	restored, err := resumed.restore(t.Context(), []json.RawMessage{capsule, suffix})
	want := append(slices.Clone(retained), suffix)
	if err != nil || !bytes.Equal(mustMarshalJSON(restored), mustMarshalJSON(want)) {
		t.Fatalf("budgeted history was not restored exactly: %v", err)
	}
	// A repeated compaction may decline to reduce this small window further;
	// it must not relax the normal frontier just to manufacture progress.
	again, againReport, err := selectCompactionWorkingSet(t.Context(), restored,
		compactionTargetTokens, compactionOvershootTokens, reduceContextCompactionWithPlan, compactionVisibleStringTokens)
	if err != nil {
		if !strings.Contains(err.Error(), "no supported token reduction") {
			t.Fatalf("repeated compaction failed unexpectedly: %v", err)
		}
	} else if againReport.plan != (compactionRetentionPlan{8, 8}) ||
		!bytes.Equal(mustMarshalJSON(again), mustMarshalJSON(reduceContextCompaction(restored))) {
		t.Fatal("repeated compaction escalated an already-small working set")
	}
}

func TestCompactionBudgetHTTPAdmission(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, test := range []struct {
			name      string
			authority int
			status    int
		}{
			{"user-over-ceiling", 90_000, http.StatusOK},
			{"user-within-overshoot", 60_000, http.StatusOK},
		} {
			t.Run(fmt.Sprintf("%s/v2=%t", test.name, v2), func(t *testing.T) {
				input := append([]json.RawMessage{mustMarshalJSON(map[string]any{
					"type": "message", "role": "user", "content": "Continue the router task.\n" + strings.Repeat("detail ", test.authority) + "\nDo not deploy.",
				})}, compactHTTPHistory()...)
				path := "/v1/responses/compact"
				if v2 {
					path = "/v1/responses"
					input = append(input, mustMarshalJSON(map[string]any{"type": "compaction_trigger"}))
				}
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(mustMarshalJSON(map[string]any{
					"model": "gpt-5", "input": input, "stream": v2,
				})))
				if v2 {
					request.Header.Set(codexTurnMetadataHeader, `{"request_kind":"compaction","compaction":{"implementation":"responses_compaction_v2"}}`)
				}
				compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
				response := httptest.NewRecorder()
				compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Fatal("budget admission called a provider")
				}))(response, request)
				if response.Code != test.status {
					t.Fatalf("status = %d, want %d: %.300s", response.Code, test.status, response.Body.String())
				}
				var compacted struct {
					Output []json.RawMessage `json:"output"`
				}
				if v2 {
					for line := range strings.SplitSeq(response.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var event struct {
							Type     string `json:"type"`
							Response struct {
								Output []json.RawMessage `json:"output"`
							} `json:"response"`
						}
						if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "response.completed" {
							compacted.Output = event.Response.Output
						}
					}
				} else if err := json.Unmarshal(response.Body.Bytes(), &compacted); err != nil {
					t.Fatal(err)
				}
				if len(compacted.Output) == 0 {
					t.Fatal("successful admission emitted no capsule")
				}
				restored, err := compactor.restore(t.Context(), compacted.Output)
				if err != nil {
					t.Fatal(err)
				}
				tokens, ok := compactionVisibleStringTokens(restored...)
				if !ok || tokens > compactionTargetTokens+compactionOvershootTokens {
					t.Fatalf("overshoot not measured at native replay: %d", tokens)
				}
				if test.authority > compactionTargetTokens+compactionOvershootTokens {
					visible := mustMarshalJSON(restored)
					if tokens > compactionTargetTokens || !bytes.Contains(visible, []byte("mekugi repetition:")) ||
						!bytes.Contains(visible, []byte("Continue the router task.")) || !bytes.Contains(visible, []byte("Do not deploy.")) {
						t.Fatal("oversized user message lost instructions or retained repetitive bulk")
					}
				} else if tokens <= compactionTargetTokens || !bytes.Equal(restored[0], input[0]) {
					t.Fatal("within-allowance user message was changed")
				}
			})
		}
	}
}

func TestCompactionBudgetV2TriggerIsNotSavings(t *testing.T) {
	for _, input := range [][]json.RawMessage{
		{mustMarshalJSON(map[string]any{"type": "compaction_trigger"})},
		{mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Protected request"}), mustMarshalJSON(map[string]any{"type": "compaction_trigger"})},
	} {
		compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(mustMarshalJSON(map[string]any{"model": "gpt-5", "stream": true, "input": input})))
		request.Header.Set(codexTurnMetadataHeader, `{"request_kind":"compaction","compaction":{"implementation":"responses_compaction_v2"}}`)
		response := httptest.NewRecorder()
		compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("provider called") }))(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("trigger removal manufactured successful compaction: %d", response.Code)
		}
	}
}

func TestCompactionBudgetFrontierFloor(t *testing.T) {
	input := []json.RawMessage{
		compactTestCall("old", "cat old.log"), compactTestOutput("old", strings.Repeat("historical ", 1000), 0),
		compactTestCall("new", "cat new.log"), compactTestOutput("new", strings.Repeat("current ", 1000), 0),
	}
	for _, recent := range []int{-1, 0, 1} {
		for _, retained := range [][]json.RawMessage{
			retireCompactionOperationsWithFrontier(input, recent),
			reduceContextCompactionSourceWithFrontier(input, input, recent),
		} {
			if !bytes.Equal(retained[2], input[2]) || !bytes.Equal(retained[3], input[3]) {
				t.Fatal("budget pressure made the newest invocation or result eligible")
			}
		}
	}
}
