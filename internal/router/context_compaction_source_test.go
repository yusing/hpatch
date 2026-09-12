package router

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func compactionSourceTestRows(quotedPath string, count int) []string {
	rows := make([]string, count)
	for index := range rows {
		prefix := fmt.Sprintf("%d:%04x", index+1, index+1)
		if quotedPath != "" {
			prefix = fmt.Sprintf("%q:%s", quotedPath, prefix)
		}
		rows[index] = prefix + " source declaration with enough exact text to make source pruning profitable\n"
	}
	return rows
}

func compactionSourceTestRecent() []json.RawMessage {
	var recent []json.RawMessage
	for index := range compactionRecentOperations {
		id := fmt.Sprintf("recent-%d", index)
		recent = append(recent, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}
	return recent
}

func compactionSourceTestOutputText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("invalid result record")
	}
	var text string
	mapCompactionCompletedOutput(fields["output"], func(value string) string {
		if text != "" {
			t.Fatal("test result contains multiple output bodies")
		}
		text = value
		return value
	})
	if text == "" {
		t.Fatal("result has no completed output body")
	}
	return text
}

func TestCompactionSourcePrunesOnlyUnreferencedRows(t *testing.T) {
	rows := compactionSourceTestRows("internal/router/source.go", 18)
	source := "hgrep output follows\n" + strings.Join(rows, "") + "warning: keep this non-row diagnostic\n"
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "reasoning", "summary": []any{map[string]string{"type": "summary_text", "text": "Inspect the source before deciding."}}}),
		compactTestCall("source-old", "hgrep -n declaration internal/router/source.go"),
		compactTestOutput("source-old", source, 0),
		mustMarshalJSON(map[string]any{"type": "function_call", "name": "unknown", "call_id": "unknown-old", "arguments": "{}"}),
		compactTestOutput("unknown-old", "unknown companion\n", 0),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "Keep exact row 3:0003 and range 6:0006..8:0008 for the next edit."}),
	}
	items = append(items, compactionSourceTestRecent()...)

	before := string(mustMarshalJSON(items))
	got := reduceContextCompaction(items)
	if len(got) != len(items) {
		t.Fatal("source pruning removed timeline items")
	}
	text := compactionSourceTestOutputText(t, got[2])
	for _, want := range []string{
		"hgrep output follows",
		rows[2],
		rows[5],
		rows[6],
		rows[7],
		"warning: keep this non-row diagnostic",
		"[mekugi compaction: omitted",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("source pruning lost %q", want)
		}
	}
	for _, removed := range []string{rows[10], rows[17]} {
		if strings.Contains(text, removed) {
			t.Fatalf("unreferenced source row remained: %q", removed)
		}
	}
	if string(got[3]) != string(items[3]) || string(got[4]) != string(items[4]) {
		t.Fatal("unknown companion changed")
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("source pruning modified its input")
	}
	if again := reduceContextCompaction(got); string(mustMarshalJSON(again)) != string(mustMarshalJSON(got)) {
		t.Fatal("source pruning was not idempotent")
	}
}
func TestCompactionSourceKeepsProtectedOutputsByteExact(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	tests := []struct {
		name   string
		result json.RawMessage
		extra  []json.RawMessage
		recent bool
	}{
		{name: "live", result: mustMarshalJSON(map[string]any{
			"type": "function_call_output", "call_id": "source-old",
			"output": string(mustMarshalJSON(map[string]any{"output": rows, "exit_code": 0, "session_id": 42})),
		})},
		{name: "whole call reference", result: compactTestOutput("source-old", rows, 0), extra: []json.RawMessage{
			mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "Use complete output from source-old."}),
		}},
		{name: "visible line reference", result: compactTestOutput("source-old", rows, 0), extra: []json.RawMessage{
			mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "!V=old,2,4\n"}),
		}},
		{name: "recent", result: compactTestOutput("source-old", rows, 0), recent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := []json.RawMessage{compactTestCall("source-old", "hread source.go"), test.result}
			items = append(items, test.extra...)
			if !test.recent {
				items = append(items, compactionSourceTestRecent()...)
			}
			got := reduceContextCompactionSource(items, items)
			if string(got[1]) != string(test.result) {
				t.Fatal("protected source output changed")
			}
		})
	}
}

func TestCompactionSourceKeepsTracebackSuffix(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	traceback := "Traceback (most recent call last):\n  File \"check.py\", line 4, in <module>\nValueError: important failure\n"
	items := []json.RawMessage{
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", rows+traceback, 0),
	}
	items = append(items, compactionSourceTestRecent()...)

	got := reduceContextCompactionSource(items, items)
	if !strings.Contains(compactionSourceTestOutputText(t, got[1]), traceback) {
		t.Fatal("Python traceback suffix was not preserved byte-exact")
	}
}

func TestCompactionSourceUsesCompletedCodeModeBody(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "custom_tool_call", "name": "exec", "call_id": "source-old",
			"input": "const result = await tools.some_runtime_call({}); text(result)",
		}),
		compactCodeModeOutput("source-old", rows),
	}
	items = append(items, compactionSourceTestRecent()...)

	got := reduceContextCompaction(items)
	text := compactionSourceTestOutputText(t, got[1])
	if !strings.Contains(text, "[mekugi compaction: omitted") || strings.Contains(text, "10:000a source declaration") {
		t.Fatal("completed Code Mode source body was not pruned")
	}
}

func TestCompactionSourceDecodesReferenceLiteralsConservatively(t *testing.T) {
	rows := compactionSourceTestRows("", 18)
	base := []json.RawMessage{
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", strings.Join(rows, ""), 0),
	}
	base = append(base, compactionSourceTestRecent()...)

	escaped := slices.Clone(base)
	escaped = append(escaped[:2], append([]json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": `const target = "3\u003a0003";`,
		}),
	}, escaped[2:]...)...)
	got := reduceContextCompactionSource(escaped, escaped)
	text := compactionSourceTestOutputText(t, got[1])
	if !strings.Contains(text, rows[2]) || strings.Contains(text, rows[10]) {
		t.Fatal("escaped JavaScript row reference was not decoded")
	}

	codePoint := slices.Clone(base)
	codePoint = append(codePoint[:2], append([]json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": `const target = "3\u{3a}0003";`,
		}),
	}, codePoint[2:]...)...)
	got = reduceContextCompactionSource(codePoint, codePoint)
	text = compactionSourceTestOutputText(t, got[1])
	if !strings.Contains(text, rows[2]) || strings.Contains(text, rows[10]) {
		t.Fatal("JavaScript code-point row reference was not decoded")
	}
}
func TestCompactionSourceReducesSuccessfulOutputInBlockedGroup(t *testing.T) {
	rows := compactionSourceTestRows("", 18)
	live := mustMarshalJSON(map[string]any{
		"type": "function_call_output", "call_id": "live-old",
		"output": string(mustMarshalJSON(map[string]any{
			"output": "still running\n", "exit_code": 0, "session_id": 42,
		})),
	})
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "reasoning", "summary": []any{map[string]string{"type": "summary_text", "text": "Keep the blocked group native."}},
			"encrypted_content": "opaque",
		}),
		compactTestCall("finished-old", "make inspect"),
		compactTestOutput("finished-old", strings.Repeat("unmarked historical detail\n", 100), 0),
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", strings.Join(rows, ""), 0),
		compactTestCall("failed-old", "make inspect"),
		compactTestOutput("failed-old", "important failure\n", 1),
		compactTestCall("live-old", "make inspect"),
		live,
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": "The next edit still requires 3:0003.",
		}),
	}
	items = append(items, compactionSourceTestRecent()...)
	before := string(mustMarshalJSON(items))

	got := reduceContextCompaction(items)
	finished := compactionSourceTestOutputText(t, got[2])
	if string(got[2]) == string(items[2]) || strings.Contains(finished, "unmarked historical detail\n") ||
		!strings.Contains(finished, "output details omitted; unavailable") {
		t.Fatal("successful unreferenced output in blocked group was not reduced")
	}
	source := compactionSourceTestOutputText(t, got[4])
	if !strings.Contains(source, rows[2]) || strings.Contains(source, rows[10]) {
		t.Fatal("referenced source row was not preserved while unreferenced rows were reduced")
	}
	for _, index := range []int{0, 1, 3, 5, 6, 7, 8, 9} {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("blocked-group item %d changed", index)
		}
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("output-only reduction mutated its input")
	}
}

func TestCompactionSourcePreservesReferencedPartialRow(t *testing.T) {
	body := strings.Repeat("unmarked historical detail\n", 100) +
		"diagnostic mentions partial target 3:0003 without a verified source line\n"
	items := []json.RawMessage{
		compactTestCall("finished-old", "make inspect"),
		compactTestOutput("finished-old", body, 0),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "content": "Keep 3:0003 for the next edit.",
		}),
	}
	items = append(items, compactionSourceTestRecent()...)
	got := reduceContextCompactionSource(items, items)
	if string(got[1]) != string(items[1]) {
		t.Fatal("successful output containing a referenced partial row was reduced")
	}
}

func TestCompactionSourceNeverRestoresRetiredHistory(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	original := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "reasoning", "summary": []any{map[string]string{"type": "summary_text", "text": "Inspect before editing."}},
			"encrypted_content": "opaque",
		}),
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", rows, 0),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "!V=old,2,4\n"}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Continue."}),
	}
	original = append(original, compactionSourceTestRecent()...)
	upstream := retireCompactionOperations(reduceRepeatedCompactionRows(slices.Clone(original), contextCompactionReferencedResults(original)))
	if string(upstream[0]) == string(original[0]) || string(upstream[1]) == string(original[1]) || string(upstream[2]) == string(original[2]) {
		t.Fatal("fixture reasoning group was not fully retired")
	}
	before := string(mustMarshalJSON(upstream))
	got := reduceContextCompactionSource(original, upstream)
	if string(mustMarshalJSON(got)) != before {
		t.Fatal("source postpass resurrected already-retired history")
	}
	if string(mustMarshalJSON(upstream)) != before {
		t.Fatal("source postpass mutated the retained input")
	}
}

func TestCompactionSourceDoesNotMutateRetainedInput(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	original := []json.RawMessage{
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", rows, 0),
	}
	original = append(original, compactionSourceTestRecent()...)
	retained := slices.Clone(original)
	before := string(mustMarshalJSON(retained))
	wantResult := string(retained[1])
	got := reduceContextCompactionSource(original, retained)
	if string(got[1]) == wantResult {
		t.Fatal("fixture source output was not reduced")
	}
	if len(mustMarshalJSON(got)) > len(mustMarshalJSON(retained)) {
		t.Fatal("source postpass increased serialized history")
	}
	if string(mustMarshalJSON(retained)) != before {
		t.Fatal("source postpass mutated the retained input")
	}
}

func TestCompactionSourceGeneratedCarrierReferences(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	input := `const result = await tools.exec_command({"cmd":"hread source.go"}); text(JSON.stringify(Object.assign({}, result, {"retained":true,"script_ref":"@shell/retained"})));`
	call := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "source-old", "input": input,
	})
	var callFields map[string]json.RawMessage
	_ = json.Unmarshal(call, &callFields)
	if operation, ok := compactionOperationCall(callFields); !ok || string(operation.arguments) != `{"cmd":"hread source.go"}` {
		t.Fatal("fixture generated carrier was not decoded")
	}
	base := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "reasoning", "summary": []any{map[string]string{"type": "summary_text", "text": "Inspect source."}},
		}),
		call,
		compactCodeModeOutput("source-old", rows),
		mustMarshalJSON(map[string]any{"type": "function_call", "name": "unknown", "call_id": "unknown-old", "arguments": "{}"}),
		compactTestOutput("unknown-old", "unknown companion\n", 0),
	}
	base = append(base, compactionSourceTestRecent()...)

	got := reduceContextCompaction(base)
	text := compactionSourceTestOutputText(t, got[2])
	if string(got[2]) == string(base[2]) || strings.Contains(text, "10:000a source declaration") ||
		!strings.Contains(text, "[mekugi:") {
		t.Fatal("generated carrier self metadata pinned its own source output")
	}

	external := slices.Clone(base)
	external = append(external[:5], append([]json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "Use the complete @shell/retained result."}),
	}, external[5:]...)...)
	got = reduceContextCompaction(external)
	if string(got[2]) != string(external[2]) {
		t.Fatal("external script reference did not pin complete source output")
	}
}

func TestCompactionSourceFailedPatchRetainsOriginalReferences(t *testing.T) {
	sourceRows := compactionSourceTestRows("", 18)
	patch := "*** Begin Patch\n*** Update File: source.go\n@@\n-" + strings.TrimSuffix(sourceRows[2], "\n") + "\n+replacement\n*** End Patch\n"
	report := "in source.go\nfiles add=0 update=1 move=0 delete=0\n"
	patchInput := mekugiApplyExecMarker + "await tools.apply_patch(" + string(mustMarshalJSON(patch)) + ");\ntext(" + string(mustMarshalJSON(report)) + ");"
	patchCall := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "patch-consumer", "input": patchInput,
	})
	base := []json.RawMessage{
		compactTestCall("source-old", "hread source.go"),
		compactTestOutput("source-old", strings.Join(sourceRows, ""), 0),
		patchCall,
	}
	base = append(base, compactionSourceTestRecent()...)

	failed := append(slices.Clone(base[:3]),
		mustMarshalJSON(map[string]any{"type": "custom_tool_call_output", "call_id": "patch-consumer", "output": "Script failed"}))
	failed = append(failed, base[3:]...)
	got := reduceContextCompactionSource(failed, failed)
	text := compactionSourceTestOutputText(t, got[1])
	if !strings.Contains(text, sourceRows[2]) || strings.Contains(text, sourceRows[10]) {
		t.Fatal("failed translated patch did not retain references from its original body")
	}

	success := append(slices.Clone(base[:3]), mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "patch-consumer",
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": report},
		},
	}))
	success = append(success, base[3:]...)
	got = reduceContextCompactionSource(success, success)
	if strings.Contains(compactionSourceTestOutputText(t, got[1]), sourceRows[2]) {
		t.Fatal("successful translated patch body was treated as a retained reference consumer")
	}
}

func TestCompactionSourceKeepsAmbiguousIdentities(t *testing.T) {
	rows := strings.Join(compactionSourceTestRows("", 18), "")
	call := compactTestCall("source-old", "hread source.go")
	result := compactTestOutput("source-old", rows, 0)
	tests := []struct {
		name  string
		items []json.RawMessage
		index int
	}{
		{name: "duplicate", items: []json.RawMessage{call, call, result}, index: 2},
		{name: "unmatched", items: []json.RawMessage{result}, index: 0},
		{name: "mismatched", items: []json.RawMessage{
			mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "shell", "call_id": "source-old", "input": "hread source.go"}),
			result,
		}, index: 1},
		{name: "crossing", items: []json.RawMessage{
			call,
			compactTestCall("other-old", "pwd"),
			result,
			compactTestOutput("other-old", "/workspace\n", 0),
		}, index: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.items = append(test.items, compactionSourceTestRecent()...)
			want := test.items[test.index]
			got := reduceContextCompactionSource(test.items, test.items)
			if string(got[test.index]) != string(want) {
				t.Fatal("ambiguous call/result identity was reduced")
			}
		})
	}
}
