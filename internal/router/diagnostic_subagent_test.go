package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubagentDiagnosticPlaybackAndReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, mode := range []string{"stream", "deferred", "", "wait"} {
			t.Run(fmt.Sprintf("%s/stream=%v", mode, stream), func(t *testing.T) {
				proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
				provider := new(serverFakeProvider)
				headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
				input := []any{map[string]any{"role": "user", "content": strings.TrimSpace(":hpatch_diag subagent_commentary " + mode)}}
				for step := range 2 {
					request := serverRequest(t, func(fields map[string]any) {
						fields["input"], fields["stream"], fields["tools"] = input, stream, testNativeResponsesTools()
					})
					visible := httptest.NewRecorder()
					if err := executeRequest(t.Context(), t.Context(), request, headers, "diag-subagent", provider, visible, nil, proxy, nil, nil); err != nil {
						t.Fatal(err)
					}
					if len(provider.forwarded) != 0 {
						t.Fatal("provider contacted")
					}
					items := diagnosticResponseItems(t, visible, stream)
					var call map[string]json.RawMessage
					for _, item := range items {
						input = append(input, item)
						if jsonString(item, "type") == "function_call" {
							call = item
						}
					}
					body := visible.Body.String()
					if mode == "wait" {
						if !strings.Contains(body, "unavailable") || call != nil {
							t.Fatal(body)
						}
						break
					}
					if step == 0 && mode != "deferred" && (!strings.Contains(body, "[`/root/diagnostic_alpha`]") || !strings.Contains(body, "[`/root/diagnostic_beta`]")) {
						t.Fatal("attribution missing", body)
					}
					if step == 1 && (!strings.Contains(body, "since the last update") || !strings.Contains(body, "substantive answer unchanged")) {
						t.Fatal("deferred/result missing", body)
					}
					if call == nil {
						break
					}
					if step != 0 {
						t.Fatal("repeated continuation")
					}
					input = append(input, map[string]any{"type": "function_call_output", "call_id": jsonString(call, "call_id"), "output": "Chunk ID: x\nWall time: 0.1 seconds\nProcess exited with code 0\nOutput:\n"})
				}
				input = append(input, map[string]any{"role": "user", "content": "ordinary turn"})
				provider.results = []serverForwardResult{{response: serverHTTPResponse(`{"status":"completed","output":[]}`)}}
				request := serverRequest(t, func(fields map[string]any) { fields["input"], fields["tools"] = input, testNativeResponsesTools() })
				if err := executeRequest(t.Context(), t.Context(), request, headers, "diag-subagent", provider, io.Discard, nil, proxy, nil, nil); err != nil {
					t.Fatal(err)
				}
				if len(provider.forwarded) != 1 || bytes.Contains(provider.forwarded[0], []byte("Diagnostic fixture")) || bytes.Contains(provider.forwarded[0], []byte("hpatch_diag")) {
					t.Fatalf("diagnostic replay leak: %q", provider.forwarded)
				}
			})
		}
	}
}

func TestSubagentDiagnosticNativeWaitContinuation(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	provider := new(serverFakeProvider)
	headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
	tools := append(testNativeResponsesTools(), map[string]any{"type": "namespace", "name": "collaboration", "tools": []any{
		map[string]any{"type": "function", "name": "wait_agent", "parameters": map[string]any{"type": "object", "properties": map[string]any{
			"timeout_ms": map[string]any{"type": "number", "description": "Timeout in milliseconds. Defaults to 30000, min 10000, max 3600000."},
		}}},
	}})
	input := []any{map[string]any{"role": "user", "content": ":hpatch_diag subagent_commentary wait"}}
	for step := range 2 {
		request := serverRequest(t, func(fields map[string]any) { fields["input"], fields["tools"], fields["stream"] = input, tools, true })
		visible := httptest.NewRecorder()
		if err := executeRequest(t.Context(), t.Context(), request, headers, "wait-session", provider, visible, nil, proxy, nil, nil); err != nil {
			t.Fatal(err)
		}
		items := diagnosticResponseItems(t, visible, true)
		var call map[string]json.RawMessage
		for _, item := range items {
			input = append(input, item)
			if jsonString(item, "type") == "function_call" {
				call = item
			}
		}
		if step == 0 {
			if call == nil || jsonString(call, "name") != "wait_agent" || jsonString(call, "arguments") != `{"timeout_ms":10000}` {
				t.Fatal("native wait missing", visible.Body.String())
			}
			if !strings.Contains(visible.Body.String(), "Activity offered after the native") {
				t.Fatal("late stream fixture missing")
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": jsonString(call, "call_id"), "output": `{"message":"No activity","timed_out":true}`})
		} else if call != nil || !strings.Contains(visible.Body.String(), "returned through the native host") {
			t.Fatal(visible.Body.String())
		}
	}
	if len(provider.forwarded) != 0 {
		t.Fatal("provider contacted")
	}
}

func TestSubagentDiagnosticCancellationDoesNotPublish(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	transform, _ := prepareActivityTest(t, proxy, "r", "r", "", "/root", nil)
	ctx, cancel := context.WithCancel(t.Context())
	transform.ctx = ctx
	cancel()
	transform.diagnosticActivity(diagnosticCallPrefix+"canceled", "alpha", "one", "operation", "canceled fixture")
	if len(proxy.activity.drain("r", transform.activityStarted, maxCommentaryPublicationBytes)) != 0 {
		t.Fatal("canceled fixture published")
	}
	body := &diagnosticActivityBody{ctx: ctx, frames: [][]byte{[]byte("data: {}\n\n")}}
	if _, err := io.ReadAll(body); err != context.Canceled {
		t.Fatal(err)
	}
}
