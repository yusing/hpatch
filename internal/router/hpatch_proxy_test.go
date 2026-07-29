package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yusing/hpatch"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	testTranslatedPatch = "*** Begin Patch\n*** Add File: created.txt\n+payload\n*** End Patch\n"
	testHPatchScript    = "new created.txt\ntype \"payload\"\n"
	testHPatchReport    = "in created.txt 1:8\n1 payload\n"
)

const testHPatchToolDescription = "fixture hpatch description\nwith exact trailing newline\n"

const testCodeModeDescription = "Run JavaScript.\n\n### `apply_patch`\nThe default editor.\n\nexec tool declaration:\n```ts\ndeclare const tools: { apply_patch(input: string): Promise<unknown>; };\n```\n\n### `create_goal`\nCreate a goal."

type hpatchTranslatorFunc func(context.Context, routingWorkspace, string) ([]byte, error)

func (f hpatchTranslatorFunc) Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error) {
	patch, err := f(ctx, workspace, script)
	return hpatchTranslationResult{patch: patch, report: testHPatchReport}, err
}

func (hpatchTranslatorFunc) ToolDescription() string {
	return testHPatchToolDescription
}

func (hpatchTranslatorFunc) RecordMetrics(context.Context, hpatchMetricRecord) error {
	return nil
}

func newHPatchTestTransform(t *testing.T, translator hpatchTranslator) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest, string) {
	t.Helper()
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{
				"type":   "additional_tools",
				"role":   "developer",
				"future": map[string]any{"kept": true},
				"tools": []any{map[string]any{
					"type":        "custom",
					"name":        "exec",
					"description": testCodeModeDescription,
					"format":      map[string]any{"type": "text"},
					"future":      true,
				}},
			},
			map[string]any{"role": "user", "content": "task", "future": true},
		},
		"tools":               []any{map[string]any{"type": "function", "name": "lookup", "future": true}},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"future_request":      map[string]any{"kept": true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHPatchProxy(translator)
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "session-1", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	if transform == nil {
		t.Fatal("prepareRequest returned no transform")
	}
	t.Cleanup(transform.Close)
	return transform, proxy, &request, workspace
}

func testTranslator(t *testing.T, calls *int) hpatchTranslator {
	t.Helper()
	return hpatchTranslatorFunc(func(_ context.Context, workspace routingWorkspace, script string) ([]byte, error) {
		*calls++
		if workspace.canonical == "" || workspace.root == nil {
			t.Fatal("translator received no trusted workspace")
		}
		if script != testHPatchScript {
			t.Fatalf("script = %q", script)
		}
		return []byte(testTranslatedPatch), nil
	})
}

func testHPatchItem() map[string]any {
	return map[string]any{
		"type":    "custom_tool_call",
		"id":      "item-H",
		"call_id": "call-H",
		"name":    hpatchToolName,
		"input":   testHPatchScript,
		"status":  "completed",
		"future":  map[string]any{"kept": true},
	}
}

func TestHPatchPrepareRequestExposesOnlyStandaloneHPatch(t *testing.T) {
	transform, _, request, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	if !transform.originalToolsPresent || len(transform.originalTools) == 0 {
		t.Fatal("original top-level tools were not retained")
	}
	var topTools []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["tools"], &topTools); err != nil {
		t.Fatal(err)
	}
	if len(topTools) != 2 || jsonString(topTools[0], "name") != "lookup" || jsonString(topTools[1], "name") != hpatchToolName {
		t.Fatalf("top-level tools = %#v", topTools)
	}
	if jsonString(topTools[1], "type") != "custom" {
		t.Fatalf("standalone hpatch definition = %#v", topTools[1])
	}
	var format struct {
		Type       string `json:"type"`
		Syntax     string `json:"syntax"`
		Definition string `json:"definition"`
	}
	if err := json.Unmarshal(topTools[1]["format"], &format); err != nil {
		t.Fatal(err)
	}
	if format.Type != "grammar" || format.Syntax != "lark" || format.Definition != hpatch.ToolGrammar() {
		t.Fatalf("standalone hpatch format = %#v", topTools[1])
	}
	// The exposed description is hpatch's own help plus the correction protocol,
	// which only the proxy implements.
	exposed := jsonString(topTools[1], "description")
	if !strings.HasPrefix(exposed, testHPatchToolDescription) {
		t.Fatalf("standalone hpatch description = %q, want hpatch tool help first", exposed)
	}
	if !strings.Contains(exposed, "INDEX: COMMAND") {
		t.Fatalf("standalone hpatch description omits the correction protocol: %q", exposed)
	}
	if !strings.Contains(exposed, "type <<PATCH consumes") {
		t.Fatalf("standalone hpatch description omits correction heredocs: %q", exposed)
	}
	for _, operation := range []string{"-INDEX", "+INDEX: COMMAND", "INDEX+: COMMAND"} {
		if !strings.Contains(exposed, operation) {
			t.Fatalf("standalone hpatch description omits %q: %q", operation, exposed)
		}
	}
	if strings.Contains(exposed, "workspace_id") {
		t.Fatalf("standalone hpatch description retains workspace selection: %q", exposed)
	}
	if string(topTools[0]["future"]) != "true" {
		t.Fatalf("unrelated top-level tool changed: %#v", topTools[0])
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || jsonString(items[0], "type") != "additional_tools" || string(items[1]["future"]) != "true" {
		t.Fatalf("rewritten input items = %#v", items)
	}
	var additionalTools []map[string]json.RawMessage
	if err := json.Unmarshal(items[0]["tools"], &additionalTools); err != nil {
		t.Fatal(err)
	}
	if len(additionalTools) != 1 || jsonString(additionalTools[0], "name") != "exec" || string(additionalTools[0]["future"]) != "true" {
		t.Fatalf("additional tools changed unexpectedly: %#v", additionalTools)
	}
	description := jsonString(additionalTools[0], "description")
	if strings.Contains(description, codeModeApplyPatchHeading) || strings.Contains(description, "tools.apply_patch") || !strings.Contains(description, "### `create_goal`") {
		t.Fatalf("native apply_patch was not hidden exactly: %q", description)
	}
	if !bytes.Contains(items[0]["future"], []byte(`"kept":true`)) || !bytes.Contains(request.fields["future_request"], []byte(`"kept":true`)) {
		t.Fatalf("future fields were not preserved: %#v", request.fields)
	}
	if string(request.fields["parallel_tool_calls"]) != "false" {
		t.Fatalf("parallel_tool_calls = %s", request.fields["parallel_tool_calls"])
	}
}

func TestHPatchAdditionalToolsReplacementRejectsDuplicateAndConflictingOwners(t *testing.T) {
	additional := func(name string) map[string]any {
		return map[string]any{
			"type":  "additional_tools",
			"role":  "developer",
			"tools": []any{map[string]any{"type": "custom", "name": name, "description": testCodeModeDescription}},
		}
	}
	tests := []struct {
		name  string
		input []any
		tools []any
	}{
		{
			name:  "duplicate additional items",
			input: []any{additional("exec"), additional("functions.exec")},
		},
		{
			name:  "standalone native collision",
			input: []any{additional("exec")},
			tools: []any{map[string]any{"type": "custom", "name": applyPatchToolName}},
		},
		{
			name:  "top-level exec collision",
			input: []any{additional("exec")},
			tools: []any{map[string]any{"type": "custom", "name": "functions.exec", "description": testCodeModeDescription}},
		},
		{
			name:  "existing hpatch collision",
			input: []any{additional("exec")},
			tools: []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		},
		{
			name: "direct nested apply_patch collision",
			input: []any{map[string]any{
				"type": "additional_tools",
				"tools": []any{
					map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription},
					map[string]any{"type": "custom", "name": applyPatchToolName},
				},
			}},
		},
		{
			name:  "direct nested hpatch collision in another item",
			input: []any{additional("exec"), map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "custom", "name": hpatchToolName}}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{
				"input": mustTestJSON(t, test.input),
				"tools": mustTestJSON(t, test.tools),
			}
			beforeInput := bytes.Clone(fields["input"])
			beforeTools := bytes.Clone(fields["tools"])
			_, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, testHPatchToolDescription)
			if err == nil || replaced {
				t.Fatalf("replacement = %v, error %v", replaced, err)
			}
			if !bytes.Equal(fields["input"], beforeInput) || !bytes.Equal(fields["tools"], beforeTools) {
				t.Fatalf("rejected request mutated: %#v", fields)
			}
		})
	}
}

func TestHPatchAdditionalToolsReplacementLeavesUnsupportedAndMalformedRequestsUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		input      json.RawMessage
		tools      json.RawMessage
		toolChoice json.RawMessage
	}{
		{
			name:  "direct apply_patch only",
			input: mustTestJSON(t, []any{map[string]any{"role": "user", "content": "task"}}),
			tools: mustTestJSON(t, []any{map[string]any{"type": "custom", "name": applyPatchToolName}}),
		},
		{
			name:  "top-level exec only",
			input: mustTestJSON(t, []any{map[string]any{"role": "user", "content": "task"}}),
			tools: mustTestJSON(t, []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}}),
		},
		{
			name:  "malformed additional tools",
			input: json.RawMessage(`[{"type":"additional_tools","tools":{}}]`),
			tools: mustTestJSON(t, []any{map[string]any{"type": "function", "name": "lookup"}}),
		},
		{
			name:  "unrelated heading collision",
			input: mustTestJSON(t, []any{map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "custom", "name": "exec", "description": "### `apply_patch`\ndocumentation only"}}}}),
			tools: mustTestJSON(t, []any{}),
		},
		{
			name:       "restricted exec choice",
			input:      mustTestJSON(t, []any{map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}}}}),
			tools:      mustTestJSON(t, []any{}),
			toolChoice: mustTestJSON(t, map[string]any{"type": "custom", "name": "exec"}),
		},
		{
			name:  "unknown future input shape",
			input: json.RawMessage(`{"future":true}`),
			tools: mustTestJSON(t, []any{}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{"input": bytes.Clone(test.input), "tools": bytes.Clone(test.tools)}
			if test.toolChoice != nil {
				fields["tool_choice"] = bytes.Clone(test.toolChoice)
			}
			beforeInput := bytes.Clone(fields["input"])
			beforeTools := bytes.Clone(fields["tools"])
			beforeChoice := bytes.Clone(fields["tool_choice"])
			_, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, testHPatchToolDescription)
			if err != nil || replaced {
				t.Fatalf("replacement = %v, error %v", replaced, err)
			}
			if !bytes.Equal(fields["input"], beforeInput) || !bytes.Equal(fields["tools"], beforeTools) || !bytes.Equal(fields["tool_choice"], beforeChoice) {
				t.Fatalf("unsupported request mutated: %#v", fields)
			}
		})
	}
}

func TestHPatchReplacementRetainsCodeModeOwnerName(t *testing.T) {
	for _, name := range []string{"exec", "functions.exec"} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]json.RawMessage{
				"input": mustTestJSON(t, []any{map[string]any{
					"type":  "additional_tools",
					"tools": []any{map[string]any{"type": "custom", "name": name, "description": testCodeModeDescription}},
				}}),
				"tools": mustTestJSON(t, []any{}),
			}
			_, got, replaced, err := replaceAdditionalToolsApplyPatch(fields, testHPatchToolDescription)
			if err != nil || !replaced || got != name {
				t.Fatalf("owner = %q, replaced %v, error %v", got, replaced, err)
			}
		})
	}
}

func TestHPatchPrepareRequestLeavesIneligibleRequestUnchanged(t *testing.T) {
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{map[string]any{
			"type":  "additional_tools",
			"tools": []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}},
		}},
		"tools": []any{map[string]any{"type": "function", "name": "lookup"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	beforeInput := bytes.Clone(request.fields["input"])
	beforeTools := bytes.Clone(request.fields["tools"])
	proxy := newHPatchProxy(testTranslator(t, new(int)))
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "", metadata, true)
	if err == nil || transform != nil || !strings.Contains(err.Error(), "valid session ID") || !bytes.Equal(beforeInput, request.fields["input"]) || !bytes.Equal(beforeTools, request.fields["tools"]) {
		t.Fatalf("ineligible request = transform %v, error %v, fields %#v", transform, err, request.fields)
	}
}

func TestHPatchIneligibleContinuationDoesNotRestoreHistory(t *testing.T) {
	proxy := newHPatchProxy(testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": {script: testHPatchScript, patch: testTranslatedPatch}}); err != nil {
		t.Fatal(err)
	}
	request, err := parseResponsesRequest([]byte(`{"input":[{"type":"custom_tool_call","name":"apply_patch","call_id":"call-H","input":` + jsonQuoted(testTranslatedPatch) + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(request.fields["input"])
	transform, err := proxy.prepareRequest(t.Context(), &request, "session", codexTurnMetadata{}, false)
	if err == nil || transform != nil || !strings.Contains(err.Error(), "valid turn metadata") || !bytes.Equal(before, request.fields["input"]) {
		t.Fatalf("ineligible continuation = transform %v, error %v, input %s", transform, err, request.fields["input"])
	}
}

func TestHPatchJSONWrapsPatchAndImmediateReportInCodeModeExec(t *testing.T) {
	calls := 0
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, &calls))
	originalItem := mustTestJSON(t, testHPatchItem())
	payload := mustTestJSON(t, map[string]any{
		"status":      "completed",
		"output":      []any{json.RawMessage(originalItem), map[string]any{"type": "message", "future": true}},
		"tools":       []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		"tool_choice": map[string]any{"type": "custom", "name": hpatchToolName},
		"future":      map[string]any{"kept": true},
	})
	visible, err := transform.TransformJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("translations = %d", calls)
	}
	var response struct {
		Output     []json.RawMessage `json:"output"`
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tools) != 1 || !bytes.Contains(response.Tools[0], []byte(`"name":"lookup"`)) || string(response.ToolChoice) != `"auto"` {
		t.Fatalf("restored response contract = %s", visible)
	}
	var carrier struct {
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Input  string          `json:"input"`
		Future json.RawMessage `json:"future"`
	}
	if err := json.Unmarshal(response.Output[0], &carrier); err != nil {
		t.Fatal(err)
	}
	wantInput := hpatchApplyExecInput(testTranslatedPatch, testHPatchReport)
	if carrier.CallID != "call-H" || carrier.Name != "exec" || carrier.Input != wantInput || string(carrier.Future) != `{"kept":true}` {
		t.Fatalf("translated call = %s", response.Output[0])
	}

	replay, err := parseResponsesRequest([]byte(`{"input":[` + string(response.Output[0]) + `,{"type":"custom_tool_call_output","call_id":"call-H","output":` + jsonQuoted(testHPatchReport) + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.restoreInputPrefix(&replay, "session-1"); err != nil {
		t.Fatal(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(items[0], originalItem) {
		t.Fatalf("reconstructed prefix item = %s, want %s", items[0], originalItem)
	}
	if !bytes.Contains(items[1], []byte(`"output":`+jsonQuoted(testHPatchReport))) {
		t.Fatalf("immediate hpatch report changed during replay: %s", items[1])
	}
}

func TestHPatchReplayPreservesImmediateApplyFailure(t *testing.T) {
	proxy := newHPatchProxy(testTranslator(t, new(int)))
	history := hpatchHistory{script: testHPatchScript, patch: testTranslatedPatch, carrierName: "exec", report: testHPatchReport}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": history}); err != nil {
		t.Fatal(err)
	}
	const applyFailure = "Failed to find expected lines in created.txt:\nmissing\n"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call-H", "input": history.carrierInput()},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call-H", "output": applyFailure},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.restoreInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(request.fields["input"], []byte(jsonQuoted(applyFailure))) || bytes.Contains(request.fields["input"], []byte(jsonQuoted(testHPatchReport))) {
		t.Fatalf("apply failure changed during replay: %s", request.fields["input"])
	}
}

func TestHPatchReplayRejectsChangedExecCarrierAndIgnoresUnrelatedCalls(t *testing.T) {
	proxy := newHPatchProxy(testTranslator(t, new(int)))
	history := hpatchHistory{script: testHPatchScript, patch: testTranslatedPatch, carrierName: "exec", report: testHPatchReport}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": history}); err != nil {
		t.Fatal(err)
	}
	changed, _ := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "call-H", "input": "changed",
	}}}))
	if err := proxy.restoreInputPrefix(&changed, "session"); err == nil {
		t.Fatal("changed replay was accepted")
	}
	unrelated, _ := parseResponsesRequest([]byte(`{"input":[{"type":"custom_tool_call","name":"exec","call_id":"native","input":"unchanged"}]}`))
	before := bytes.Clone(unrelated.fields["input"])
	if err := proxy.restoreInputPrefix(&unrelated, "session"); err != nil || !bytes.Equal(before, unrelated.fields["input"]) {
		t.Fatalf("unrelated call changed to %s, error %v", unrelated.fields["input"], err)
	}
}

func TestHPatchReportSeparatesHookWarning(t *testing.T) {
	if got := hpatchReport("in file.txt 1:1", "hpatch: warning: hook failed\n"); got != "in file.txt 1:1\nhpatch: warning: hook failed\n" {
		t.Fatalf("hpatchReport() = %q", got)
	}
}

func TestHPatchExecInputQuotesPatchReportAndDiagnostic(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: quoted.txt\n+` ${value} \\\"\n*** End Patch\n"
	report := "in quoted.txt 1:14\n1 ` ${value} \\\"\n"
	input := hpatchApplyExecInput(patch, report)
	if !strings.HasPrefix(input, hpatchApplyExecMarker) || !strings.Contains(input, strconv.Quote(patch)) || !strings.Contains(input, strconv.Quote(report)) {
		t.Fatalf("unsafe or incomplete apply wrapper: %q", input)
	}
	diagnostic := "selector `x` rejected: ${value} " + string([]byte{'\\'})
	if got := hpatchDiagnosticExecInput(diagnostic); got != "text("+strconv.Quote(diagnostic)+");" {
		t.Fatalf("diagnostic wrapper = %q", got)
	}
}

func TestHPatchStreamingReplacesLifecycleWithoutChangingCallID(t *testing.T) {
	calls := 0
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, &calls))
	item := testHPatchItem()
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""

	visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
	if err != nil || visible != nil {
		t.Fatalf("buffered added = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.delta", "item_id": "item-H", "delta": "secret"}))
	if err != nil || visible != nil {
		t.Fatalf("delta = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript}))
	if err != nil || len(visible) != 2 || !bytes.Contains(visible[0], []byte(`"name":"exec"`)) || !bytes.Contains(visible[0], []byte(`"call_id":"call-H"`)) || !bytes.Contains(visible[1], []byte(jsonQuoted(hpatchApplyExecInput(testTranslatedPatch, testHPatchReport)))) {
		t.Fatalf("input.done = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
	if err != nil || len(visible) != 1 || !bytes.Contains(visible[0], []byte(`"call_id":"call-H"`)) {
		t.Fatalf("item.done = %q, error %v", visible, err)
	}
	completed := map[string]any{"status": "completed", "output": []any{item}}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.completed", "response": completed}))
	if err != nil || len(visible) != 1 || calls != 1 {
		t.Fatalf("completed = %q, translations %d, error %v", visible, calls, err)
	}
}

func TestHPatchCorrectionRetainsCorrelationAndIncrementsAttempt(t *testing.T) {
	calls := 0
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		return []byte(testTranslatedPatch), nil
	}))
	first, err := transform.translate("call-1", testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.correlationID != "call-1" || first.attempt != 1 {
		t.Fatalf("first attempt metadata = %+v", first)
	}
	second, err := transform.translate("call-2", "2: type \"changed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.translationError != "" || second.correlationID != "call-1" || second.attempt != 2 {
		t.Fatalf("correction metadata = %+v", second)
	}
	if _, ok := proxy.history("session-1", "call-1"); ok {
		t.Fatal("local history committed before response completion")
	}
}

func TestHPatchCompactCorrectionRebuildsScriptBeforeTranslation(t *testing.T) {
	base := "new file.txt\ntype \"old\"\nrm\n"
	payload := "-2\n+2: type <<BODY\nnew\nBODY\n2+: copy\n"
	want := "new file.txt\ntype <<BODY\nnew\nBODY\ncopy\nrm\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		evaluated = script
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	result, err := transform.translate("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.translationError != "" || calls != 2 {
		t.Fatalf("correction result = %+v, calls %d", result, calls)
	}
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchMalformedDeletionPreservesCorrectableHistory(t *testing.T) {
	base := "new file.txt\ntype \"old\"\nrm\n"
	want := "new file.txt\ntype \"fixed\"\nrm\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		evaluated = script
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	malformed, err := transform.translate("call-2", "-2: rm\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !malformed.unevaluated || !strings.Contains(malformed.translationError, "is not `INDEX: COMMAND`") || calls != 1 {
		t.Fatalf("malformed correction = %+v, calls %d", malformed, calls)
	}
	corrected, err := transform.translate("call-3", "2: type \"fixed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.translationError != "" || corrected.correlationID != "call-1" || calls != 2 {
		t.Fatalf("corrected result = %+v, calls %d", corrected, calls)
	}
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchTranslationFailureReturnsImmediateDiagnosticExec(t *testing.T) {
	transform, proxy, _, workspace := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("selector is not unique")
	}))
	originalItem := mustTestJSON(t, testHPatchItem())
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{"type": "message", "future": "preserved", "metadata": map[string]any{"name": "apply_patch"}},
			json.RawMessage(originalItem),
		},
		"future": map[string]any{"kept": true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status string                       `json:"status"`
		Error  json.RawMessage              `json:"error"`
		Output []map[string]json.RawMessage `json:"output"`
		Future json.RawMessage              `json:"future"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "completed" || len(response.Error) != 0 || len(response.Output) != 2 {
		t.Fatalf("translation rejection carrier = %s", visible)
	}
	carrier := response.Output[1]
	history, remembered := proxy.history("session-1", "call-H")
	if !remembered {
		t.Fatal("translation rejection carrier was not remembered")
	}
	if jsonString(carrier, "name") != "exec" || jsonString(carrier, "input") != history.carrierInput() || jsonString(carrier, "call_id") != "call-H" || !strings.Contains(history.carrierInput(), "selector is not unique") {
		t.Fatalf("diagnostic exec carrier = %s", visible)
	}
	if jsonString(response.Output[0], "future") != "preserved" || string(response.Future) != `{"kept":true}` || !bytes.Contains(response.Output[0]["metadata"], []byte(`"name":"apply_patch"`)) {
		t.Fatalf("unrelated response data changed: %s", visible)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{
			map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}}},
			carrier,
			map[string]any{"type": "custom_tool_call_output", "call_id": "call-H", "output": history.translationError, "future": true},
			map[string]any{"type": "custom_tool_call_output", "call_id": "other", "output": "keep"},
		},
		"tools": []any{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	continuation, err := proxy.prepareRequest(t.Context(), &replay, "session-1", metadata, true)
	if err != nil || continuation == nil {
		t.Fatalf("prepare rejection continuation = transform %v, error %v", continuation, err)
	}
	defer continuation.Close()
	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 4 || jsonString(replayed[1], "name") != hpatchToolName || jsonString(replayed[1], "input") != testHPatchScript || string(replayed[2]["future"]) != "true" || jsonString(replayed[2], "output") != history.translationError || jsonString(replayed[3], "output") != "keep" {
		t.Fatalf("restored hpatch rejection = %s", replay.fields["input"])
	}
}

func TestHPatchStreamingTranslationFailureCompletesDiagnosticExecLifecycle(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("parent directory does not exist")
	}))
	item := testHPatchItem()
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""
	completed := map[string]any{
		"status":      "completed",
		"output":      []any{item},
		"future":      map[string]any{"kept": true},
		"tools":       []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		"tool_choice": hpatchToolName,
	}
	body := "event: response.output_item.added\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})) + "\n\n" +
		"event: response.custom_tool_call_input.done\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript})) + "\n\n" +
		"event: response.future\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.future", "future": "future-preserved"})) + "\n\n" +
		"event: response.output_item.done\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item})) + "\n\n" +
		"event: response.completed\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.completed", "response": completed, "future": "outer-preserved"})) + "\n\n"
	var output bytes.Buffer
	terminal, err := copySSETransformed(&output, strings.NewReader(body), transform, nil)
	if err != nil || terminal != responseTerminalCompleted {
		t.Fatalf("translation rejection stream = terminal %v, error %v, output %q", terminal, err, output.String())
	}
	visible := output.String()
	for _, required := range []string{"event: response.output_item.added", "event: response.custom_tool_call_input.done", "event: response.output_item.done", "event: response.completed", `"name":"exec"`, "text(\\\"parent directory does not exist", `"future":{"kept":true}`, `"future":"outer-preserved"`, "future-preserved", `"name":"lookup"`, `"tool_choice":"auto"`} {
		if !strings.Contains(visible, required) {
			t.Fatalf("translation carrier missing %q: %q", required, visible)
		}
	}
	for _, forbidden := range []string{"response.failed", "stream disconnected"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("translation carrier exposed %q: %q", forbidden, visible)
		}
	}
	if _, remembered := proxy.history("session-1", "call-H"); !remembered {
		t.Fatal("streaming translation rejection carrier was not remembered")
	}
}

func TestHPatchStreamingTranslationFailureRejectsMalformedTerminal(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("selector is not unique")
	}))
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil || visible != nil {
		t.Fatalf("buffer hpatch call = %q, error %v", visible, err)
	}
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript})); err != nil || len(visible) != 2 || !bytes.Contains(visible[1], []byte("selector is not unique")) {
		t.Fatalf("release hpatch rejection carrier = %q, error %v", visible, err)
	}
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": testHPatchItem()})); err != nil || len(visible) != 1 {
		t.Fatalf("complete hpatch rejection carrier = %q, error %v", visible, err)
	}
	visible, err := transform.TransformSSE([]byte(`{"type":"response.completed","response":null}`))
	if err == nil || visible != nil || !strings.Contains(err.Error(), "decode hpatch-enabled response") {
		t.Fatalf("malformed terminal = %q, error %v", visible, err)
	}
}

func TestHPatchMalformedCallStillFailsRequest(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	item := testHPatchItem()
	item["type"] = "message"
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{item}}))
	if err == nil || visible != nil {
		t.Fatalf("malformed hpatch call = visible %q, error %v", visible, err)
	}
	if _, remembered := proxy.history("session-1", "call-H"); remembered {
		t.Fatal("malformed hpatch call created history")
	}
}

func TestHPatchTranslationCancellationRemainsRequestCancellation(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(ctx context.Context, _ routingWorkspace, _ string) ([]byte, error) {
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transform.ctx = ctx
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}}))
	if !errors.Is(err, context.Canceled) || visible != nil {
		t.Fatalf("canceled translation = visible %q, error %v", visible, err)
	}
	if _, remembered := proxy.history("session-1", "call-H"); remembered {
		t.Fatal("canceled translation created rejection history")
	}
}

func TestHPatchBoundsTranslationAndHistory(t *testing.T) {
	t.Run("translation output capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			return make([]byte, maxHPatchPatchBytes+1), nil
		}))
		payload := mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}})
		visible, err := transform.TransformJSON(payload)
		if err == nil || visible != nil || !strings.Contains(err.Error(), "translation exceeds") {
			t.Fatalf("oversized translation = visible %q, error %v", visible, err)
		}
		if _, remembered := proxy.history("session-1", "call-H"); remembered {
			t.Fatal("oversized translation created rejection history")
		}
	})

	t.Run("script capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("translator called for oversized script")
			return nil, nil
		}))
		item := testHPatchItem()
		item["input"] = strings.Repeat("x", maxHPatchScriptBytes+1)
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{item}}))
		if err == nil || visible != nil || !strings.Contains(err.Error(), "script exceeds") {
			t.Fatalf("oversized script = visible bytes %d, error %v", len(visible), err)
		}
		if _, remembered := proxy.history("session-1", "call-H"); remembered {
			t.Fatal("oversized script created rejection history")
		}
	})

	t.Run("translator capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			return nil, fmt.Errorf("%w: diagnostic overflow", errHPatchCapacity)
		}))
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}}))
		if !errors.Is(err, errHPatchCapacity) || visible != nil {
			t.Fatalf("translator capacity = visible %q, error %v", visible, err)
		}
		if _, remembered := proxy.history("session-1", "call-H"); remembered {
			t.Fatal("translator capacity created rejection history")
		}
	})

	t.Run("global history", func(t *testing.T) {
		proxy := newHPatchProxy(testTranslator(t, new(int)))
		proxy.historyBytes = maxHPatchHistoryGlobalBytes
		if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call": {script: "x", patch: "y"}}); err == nil {
			t.Fatal("history exceeded global capacity")
		}
		if len(proxy.sessions) != 0 {
			t.Fatalf("failed history reservation created %d sessions", len(proxy.sessions))
		}
	})

	t.Run("batch history is atomic", func(t *testing.T) {
		proxy := newHPatchProxy(testTranslator(t, new(int)))
		proxy.historyBytes = maxHPatchHistoryGlobalBytes - 1
		err := proxy.rememberBatch("session", map[string]hpatchHistory{
			"call-first":  {script: "x", patch: "y"},
			"call-second": {script: "x", patch: "y"},
		})
		if err == nil {
			t.Fatal("history batch exceeded global capacity")
		}
		if len(proxy.sessions) != 0 {
			t.Fatalf("failed history batch created %d sessions", len(proxy.sessions))
		}
	})
}

func TestHPatchBoundsPendingStreamCallsAndRejectsRelatedFutureEvents(t *testing.T) {
	t.Run("duplicate item", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		payload := mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})
		if _, err := transform.TransformSSE(payload); err != nil {
			t.Fatal(err)
		}
		if visible, err := transform.TransformSSE(payload); err == nil || visible != nil {
			t.Fatalf("duplicate item = visible %q, error %v", visible, err)
		}
	})

	t.Run("pending count", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		for index := range maxHPatchPendingCalls + 1 {
			added := testHPatchItem()
			added["status"] = "in_progress"
			added["input"] = ""
			added["id"] = fmt.Sprintf("item-%d", index)
			added["call_id"] = fmt.Sprintf("call-%d", index)
			visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
			if index < maxHPatchPendingCalls {
				if err != nil || visible != nil {
					t.Fatalf("pending item %d = visible %q, error %v", index, visible, err)
				}
				continue
			}
			if err == nil || visible != nil {
				t.Fatalf("excess pending item = visible %q, error %v", visible, err)
			}
		}
	})

	t.Run("malformed pending event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		malformed := []byte(`{"type":1,"item_id":"item-H","delta":"secret script"}`)
		if visible, err := transform.TransformSSE(malformed); err == nil || visible != nil {
			t.Fatalf("malformed pending event = visible %q, error %v", visible, err)
		}
	})

	t.Run("malformed unrelated event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		malformed := []byte(`{"type":1,"future":"kept"}`)
		visible, err := transform.TransformSSE(malformed)
		if err != nil || len(visible) != 1 || !bytes.Equal(visible[0], malformed) {
			t.Fatalf("malformed unrelated event = visible %q, error %v", visible, err)
		}
	})

	t.Run("incomplete terminal event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		completed := mustTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}}})
		if visible, err := transform.TransformSSE(completed); err == nil || visible != nil {
			t.Fatalf("incomplete terminal event = visible %q, error %v", visible, err)
		}
	})

	t.Run("related future event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		future := mustTestJSON(t, map[string]any{"type": "response.future", "call_id": "call-H", "input": "secret"})
		if visible, err := transform.TransformSSE(future); err == nil || visible != nil {
			t.Fatalf("related future event = visible %q, error %v", visible, err)
		}
	})

	t.Run("unrelated future event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		future := mustTestJSON(t, map[string]any{"type": "response.future", "call_id": "other", "name": "other"})
		visible, err := transform.TransformSSE(future)
		if err != nil || len(visible) != 1 || !bytes.Equal(visible[0], future) {
			t.Fatalf("unrelated future event = visible %q, error %v", visible, err)
		}
	})
}

func TestInProcessHPatchToolDescription(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	translator, err := newInProcessHPatchTranslator()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := translator.ToolDescription(), hpatch.ToolDescription(); got != want {
		t.Fatalf("installed tool description differs from authoritative description:\n got %q\nwant %q", got, want)
	}
}

func TestInProcessHPatchTranslatorIsRootScopedAndDoesNotMutateWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	translator, err := newInProcessHPatchTranslator()
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := t.TempDir()
	path := filepath.Join(workspacePath, "existing.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, ok := usableRoutingWorkspace(map[string]json.RawMessage{workspacePath: nil})
	if !ok {
		t.Fatal("workspace unavailable")
	}
	defer workspace.close()

	translated, err := translator.Translate(t.Context(), workspace, "in "+path+"\ntsel 1 \"first\"\ntype \"first\\ninserted\"\ntsel 3 \"third\"\ntype \"THIRD\"\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"*** Update File: existing.txt", "+inserted", "+THIRD"} {
		if !bytes.Contains(translated.patch, []byte(required)) {
			t.Fatalf("translation does not contain %q: %s", required, translated.patch)
		}
	}
	if !strings.HasPrefix(translated.report, "in existing.txt ") {
		t.Fatalf("translation report = %q", translated.report)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first\nsecond\nthird\n" {
		t.Fatalf("translation mutated workspace to %q", content)
	}

	conflict := "in " + path + "\nrsel 1:2\ntype \"replacement\"\nrsel 2:3\ntype \"overlap\"\n"
	if translated, err := translator.Translate(t.Context(), workspace, conflict); err == nil || !strings.Contains(translated.diagnostic, "selection conflicts with edit") {
		t.Fatalf("overlapping baseline translation error = %v", err)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := translator.Translate(t.Context(), workspace, "in "+outsidePath+"\n"); err == nil {
		t.Fatal("translation accepted an absolute path outside the trusted root")
	}
}

func mustTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func jsonQuoted(value string) string {
	return strconv.Quote(value)
}
