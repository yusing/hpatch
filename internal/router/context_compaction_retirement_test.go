package router

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func retirementHistory() []json.RawMessage {
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Do not commit. Keep the original API. This task is still open."}),
	}
	for index := range 24 {
		id := fmt.Sprintf("operation_%02d", index)
		items = append(items,
			mustMarshalJSON(map[string]any{"type": "reasoning", "id": fmt.Sprintf("rs_%02d", index), "summary": []any{map[string]any{"type": "summary_text", "text": "Preserve the API contract."}}, "encrypted_content": "opaque-state"}),
			compactTestCall(id, "hread internal/router/example.go"),
			compactTestOutput(id, strings.Repeat(fmt.Sprintf("historical-body-%02d\n", index), 300), 0))
	}
	return items
}

func TestCompactionRetiresFinishedOperationsInOpenTask(t *testing.T) {
	items := retirementHistory()
	before := string(mustMarshalJSON(items))
	got := retireCompactionOperations(items)
	if string(got[0]) != string(items[0]) {
		t.Fatal("user authority changed")
	}
	wire := string(mustMarshalJSON(got))
	if strings.Contains(wire, "historical-body-00") || !strings.Contains(wire, "historical-body-17") {
		t.Fatal("old completed output was not retired, or recent output was lost")
	}
	if !strings.Contains(wire, "hread internal/router/example.go") || !strings.Contains(wire, "operation_00") || !strings.Contains(wire, "Preserve the API contract.") {
		t.Fatal("execution facts or visible reasoning were lost")
	}
	if len(wire) >= len(before)/2 {
		t.Fatal("finished-operation retirement did not provide material relief")
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("input was mutated")
	}
	if string(mustMarshalJSON(retireCompactionOperations(got))) != wire {
		t.Fatal("repeated retirement changed the retained ledger")
	}
}

func TestCompactionRetirementStaticCodeModeCarriers(t *testing.T) {
	arguments := `{"cmd":"hread example.go","login":false}`
	good := `const result = await tools.exec_command(` + arguments + `); text(JSON.stringify(Object.assign({}, result, {"retained":true,"script_ref":"@shell/history"})));`
	for _, source := range []string{good, `text(await tools.exec_command(` + arguments + `));`} {
		operation, ok := compactionCodeModeOperation(source)
		if !ok || operation.tool != "exec_command" || string(operation.arguments) != arguments {
			t.Fatal("static result-preserving carrier was not recognized")
		}
		items := retirementHistory()
		items[2] = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "operation_00", "input": source})
		items[3] = compactCodeModeOutput("operation_00", strings.Repeat("unmarked old read detail\n", 500))
		got := retireCompactionOperations(items)
		if strings.Contains(string(got[3]), "unmarked old read detail") {
			t.Fatal("finished Code Mode output was not retired")
		}
	}
	for _, source := range []string{
		strings.Replace(good, `"retained":true`, `"exit_code":0`, 1),
		`const result = await tools.exec_command(buildArguments()); text(result);`,
		`const result = await tools.exec_command(` + arguments + `); text({"exit_code":0,"output":"fake"});`,
		`const result = await tools.exec_command(` + arguments + `); text(result); await tools.other();`,
		`if (false) { text(await tools.exec_command(` + arguments + `)); }`,
	} {
		if _, ok := compactionCodeModeOperation(source); ok {
			t.Fatal("dynamic or status-forging carrier was accepted")
		}
	}
}

func TestCompactionRetirementPreservesCarrierNotice(t *testing.T) {
	notice := "Keep the evidence at 17:abcd for the next edit."
	source := `const result = await tools.exec_command({"cmd":"hread example.go"}); text(` +
		string(mustMarshalJSON(notice)) + `); text(JSON.stringify(result));`
	items := retirementHistory()
	items[2] = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "operation_00", "input": source})
	items[3] = mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00",
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": notice},
			map[string]any{"type": "input_text", "text": string(mustMarshalJSON(map[string]any{
				"exit_code": 0, "output": strings.Repeat("old unmarked detail\n", 500),
			}))},
		},
	})
	items[6] = compactTestOutput("operation_01", "17:abcd referenced source\n"+strings.Repeat("needed evidence\n", 300), 0)
	got := retireCompactionOperations(items)
	if string(got[3]) == string(items[3]) || strings.Contains(string(got[3]), "old unmarked detail") {
		t.Fatal("static carrier with a literal notice was not retired")
	}
	wire := string(mustMarshalJSON(got))
	if !strings.Contains(string(got[3]), notice) || !strings.Contains(wire, "17:abcd referenced source") ||
		strings.Contains(wire, "needed evidence") {
		t.Fatal("carrier notice or row-aware factual evidence was lost")
	}
	mismatch := append([]json.RawMessage(nil), items...)
	mismatch[3] = json.RawMessage(strings.Replace(string(items[3]), notice, "different notice", 1))
	got = retireCompactionOperations(mismatch)
	if string(got[2]) != string(mismatch[2]) || string(got[3]) != string(mismatch[3]) {
		t.Fatal("mismatched notice/result projection was retired")
	}
	failed := append([]json.RawMessage(nil), items...)
	escapedSource := strings.Replace(source, "17:abcd", `17\u003aabcd`, 1)
	failed[2] = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "operation_00", "input": escapedSource})
	failed[3] = mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00", "output": "Script failed",
	})
	got = retireCompactionOperations(failed)
	wire = string(mustMarshalJSON(got))
	if string(got[2]) != string(failed[2]) || string(got[3]) != string(failed[3]) ||
		!strings.Contains(wire, "17:abcd referenced source") {
		t.Fatal("an unfinished carrier or its row-aware factual evidence was lost")
	}
}

func TestCompactionOperationRejectsMalformedShellInput(t *testing.T) {
	for _, fields := range []map[string]json.RawMessage{
		{"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell")},
		{"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"), "input": json.RawMessage("null")},
		{"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"), "input": json.RawMessage("17")},
		{"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"), "input": json.RawMessage(`{"cmd":"pwd"}`)},
	} {
		if _, ok := compactionOperationCall(fields); ok {
			t.Fatalf("malformed shell input was accepted: %s", mustMarshalJSON(fields))
		}
	}
	valid := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"), "input": mustMarshalJSON(""),
	}
	if _, ok := compactionOperationCall(valid); !ok {
		t.Fatal("valid empty shell input was rejected")
	}
}

func TestCompactionRetiresAppliedPatchBodyNotFailedPatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: example.go\n+" + strings.Repeat("// old implementation detail\n+", 500) + "\n*** End Patch\n"
	report := "in example.go\nfiles add=1 update=0 move=0 delete=0\n"
	source := mekugiApplyExecMarker + "await tools.apply_patch(" + string(mustMarshalJSON(patch)) + ");\ntext(" + string(mustMarshalJSON(report)) + ");"
	result := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00",
		"output": []any{map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": report}},
	})
	items := retirementHistory()
	items[2] = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "operation_00", "input": source})
	items[3] = result
	got := retireCompactionOperations(items)
	if strings.Contains(string(got[2]), "old implementation detail") || !strings.Contains(string(got[2]), "example.go") || !strings.Contains(string(got[3]), "files add=1") {
		t.Fatal("applied edit was not replaced with truthful target/outcome records")
	}
	items[3] = json.RawMessage(strings.Replace(string(result), "Script completed", "Script failed", 1))
	got = retireCompactionOperations(items)
	if string(got[2]) != string(items[2]) || string(got[3]) != string(items[3]) {
		t.Fatal("failed patch carrier was retired")
	}
}

func TestCompactionRetiredPatchReportKeepsApplicationFactsAndReferencedRows(t *testing.T) {
	var rows strings.Builder
	for row := 1; row <= 40; row++ {
		fmt.Fprintf(&rows, "%d:%04x source declaration with error word that is not a runtime diagnostic %s\n",
			row, row, strings.Repeat("detail ", 20))
	}
	report := "in internal/router/example.go\nfiles add=0 update=1 move=0 delete=0\n" + rows.String() +
		"Done!\nWARNING: retained application qualification\n"
	patch := "*** Begin Patch\n*** Update File: internal/router/example.go\n@@\n-old\n+new\n*** End Patch\n"
	source := mekugiApplyExecMarker + "await tools.apply_patch(" + string(mustMarshalJSON(patch)) + ");\ntext(" + string(mustMarshalJSON(report)) + ");"
	result := mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": "operation_00",
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": report},
		},
	})
	items := retirementHistory()
	items[2] = mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "operation_00", "input": source})
	items[3] = result
	items = append(items, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "content": "Keep exact patch evidence row 17:0011.",
	}))

	got := retireCompactionOperations(items)
	wire := string(mustMarshalJSON(got[1:4]))
	for _, fact := range []string{
		"internal/router/example.go", "Update File", "files add=0 update=1 move=0 delete=0",
		"Done!", "WARNING: retained application qualification", "17:0011",
	} {
		if !strings.Contains(wire, fact) {
			t.Fatalf("patch retirement lost application fact %q", fact)
		}
	}
	if strings.Contains(wire, "18:0012") || strings.Count(wire, "error word") != 1 {
		t.Fatal("unreferenced verified source rows survived because their source text resembled diagnostics")
	}
}

func TestCompactionRetirementMeasuresCompleteGroup(t *testing.T) {
	items := retirementHistory()
	// A cheap successful companion must not pin the much larger completed read.
	items = append(items[:4], append([]json.RawMessage{
		compactTestCall("small_companion", "pwd"), compactTestOutput("small_companion", "/workspace\n", 0),
	}, items[4:]...)...)
	got := retireCompactionOperations(items)
	if strings.Contains(string(got[3]), "historical-body-00") {
		t.Fatal("a small completed companion pinned the entire eligible group")
	}
	for _, index := range []int{1, 2, 3, 4, 5} {
		if string(got[index]) == string(items[index]) {
			t.Fatalf("eligible reasoning/tool group was only partially retired at %d", index)
		}
	}
	if !strings.Contains(string(got[4]), "pwd") || !strings.Contains(string(got[5]), "exit_code") {
		t.Fatal("small companion lost its invocation or observed completion")
	}
}

func TestCompactionRetirementRetiresCompletedFailedReasoningGroups(t *testing.T) {
	items := retirementHistory()
	// One reasoning response issued a successful and a terminal failed call.
	items = append(items[:4], append([]json.RawMessage{
		compactTestCall("group_failure", "go test ./..."), compactTestOutput("group_failure", "unresolved failure", 1),
	}, items[4:]...)...)
	got := retireCompactionOperations(items)
	for index := 1; index <= 5; index++ {
		if string(got[index]) == string(items[index]) {
			t.Fatal("complete reasoning/tool group with terminal failure stayed native")
		}
	}
	if wire := string(mustMarshalJSON(got[1:6])); !strings.Contains(wire, "group_failure") ||
		!strings.Contains(wire, "unresolved failure") || !strings.Contains(wire, `\"exit_code\":1`) {
		t.Fatal("failed operation facts were lost from retired group")
	}
}
func TestCompactionRetirementPinsFailureLiveAndReferencedEvidence(t *testing.T) {
	items := retirementHistory()
	items[3] = compactTestOutput("operation_00",
		strings.Join(compactionSourceTestRows("", 200), "")+"unique unresolved failure\n", 1)
	items[6] = mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "operation_01",
		"output": string(mustMarshalJSON(map[string]any{"exit_code": 0, "session_id": 42, "output": "still running"}))})
	items = append(items, mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "The evidence in operation_02 is needed for the remaining investigation."}))
	got := retireCompactionOperations(items)
	for _, index := range []int{1, 2, 3} {
		if string(got[index]) == string(items[index]) {
			t.Fatalf("terminal failed operation item %d stayed native", index)
		}
	}
	if wire := string(mustMarshalJSON(got[1:4])); !strings.Contains(wire, "unique unresolved failure") ||
		!strings.Contains(wire, `\"exit_code\":1`) {
		t.Fatal("terminal failure facts disappeared")
	}
	for _, index := range []int{4, 5, 6, 7, 8, 9} {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("protected operation/associated reasoning at %d changed", index)
		}
	}
	if !strings.Contains(string(mustMarshalJSON(got)), "historical-body-02") {
		t.Fatal("explicitly referenced evidence disappeared")
	}
}

func TestCompactionRetirementPreservesDiagnosticsAndUnknownLifecycle(t *testing.T) {
	items := retirementHistory()
	items[3] = compactTestOutput("operation_00", strings.Repeat("routine output\n", 300)+"WARNING: coverage excludes integration tests\nok example/router 0.2s\n", 0)
	items[6] = mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "operation_01", "output": "unknown completion format"})
	got := retireCompactionOperations(items)
	wire := string(mustMarshalJSON(got))
	if !strings.Contains(wire, "WARNING: coverage excludes integration tests") || !strings.Contains(wire, "ok example/router 0.2s") {
		t.Fatal("diagnostic or validation scope was lost")
	}
	if string(got[5]) != string(items[5]) || string(got[6]) != string(items[6]) {
		t.Fatal("unknown completion state was retired")
	}
}

func TestCompactionRetirementPreservesAmbiguousCalls(t *testing.T) {
	items := retirementHistory()
	items = append([]json.RawMessage{compactTestCall("operation_00", "pwd")}, items...)
	got := retireCompactionOperations(items)
	for _, index := range []int{0, 2, 3, 4} {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("ambiguous operation at %d changed", index)
		}
	}
}

func TestCompactionRetirementPinsReplacementNoteTargets(t *testing.T) {
	note := func(target string) string {
		return fmt.Sprintf("[mekugi compaction: 3 source rows (1:0001 through 3:0003) retained verbatim in later tool result %q]\n", target)
	}
	assertNative := func(t *testing.T, got, want []json.RawMessage) {
		t.Helper()
		for _, index := range []int{1, 2, 3, 4, 5, 6} {
			if string(got[index]) != string(want[index]) {
				t.Fatalf("replacement-note closure lost native item %d", index)
			}
		}
	}

	t.Run("failed consumer keeps its target", func(t *testing.T) {
		items := retirementHistory()
		items[3] = compactTestOutput("operation_00", note("operation_01"), 1)
		items[6] = compactTestOutput("operation_01", strings.Repeat("later source evidence\n", 500), 0)
		assertNative(t, retireCompactionOperations(items), items)
		assertNative(t, reduceContextCompaction(items), items)
	})

	t.Run("consumer pinned later restores its original note", func(t *testing.T) {
		items := retirementHistory()
		items[3] = compactTestOutput("operation_00", note("operation_01")+strings.Repeat("pinned consumer detail\n", 500), 0)
		items[6] = compactTestOutput("operation_01", strings.Repeat("later source evidence\n", 500), 0)
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "content": "Keep operation_00.",
		}))
		assertNative(t, retireCompactionOperations(items), items)
		assertNative(t, reduceContextCompaction(items), items)
	})

	t.Run("input notes pin both sides of a replacement cycle", func(t *testing.T) {
		items := retirementHistory()
		items[3] = compactTestOutput("operation_00", note("operation_01")+strings.Repeat("cycle detail\n", 500), 0)
		items[6] = compactTestOutput("operation_01", note("operation_00")+strings.Repeat("cycle detail\n", 500), 0)
		assertNative(t, retireCompactionOperations(items), items)
		assertNative(t, reduceContextCompaction(items), items)
	})
}

func TestCompactionRetirementPinsRetainedScriptProducer(t *testing.T) {
	items := retirementHistory()
	items[3] = mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "operation_00",
		"output": string(mustMarshalJSON(map[string]any{"exit_code": 0, "script_ref": "@shell/source-reference",
			"output": strings.Repeat("old source\n", 500)}))})
	items = append(items, compactTestCall("pending", "hread @shell/source-reference"))
	got := retireCompactionOperations(items)
	if string(got[2]) != string(items[2]) || string(got[3]) != string(items[3]) {
		t.Fatal("retained-script reference lost its producer")
	}
}

func TestCompactionRetirementPinsDecodedNestedReferences(t *testing.T) {
	assertPinned := func(t *testing.T, items []json.RawMessage) {
		t.Helper()
		got := retireCompactionOperations(items)
		for _, index := range []int{1, 2, 3} {
			if string(got[index]) != string(items[index]) {
				t.Fatalf("referenced operation evidence at %d changed", index)
			}
		}
	}

	t.Run("call ID with newline and escaped unicode", func(t *testing.T) {
		items := retirementHistory()
		items = append(items, json.RawMessage(`{"type":"message","role":"assistant","content":"continued evidence:\noperation_\u0030\u0030"}`))
		assertPinned(t, items)
	})

	t.Run("verified row in nested JSON", func(t *testing.T) {
		items := retirementHistory()
		referencedLine := `{\"result\":{\"row\":\"17\u003aabcd\",\"detail\":\"exact\"}}`
		items[3] = compactTestOutput("operation_00",
			referencedLine+"\n"+strings.Repeat("old detail\n", 500), 0)
		arguments := `{"cmd":"{\"target\":\"17\\u003aabcd\"}","workdir":"/workspace"}`
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "function_call", "name": "exec_command", "call_id": "pending_row", "arguments": arguments,
		}))
		got := retireCompactionOperations(items)
		for _, index := range []int{1, 2, 3} {
			if string(got[index]) == string(items[index]) {
				t.Fatal("decoded verified-row reference pinned the native reasoning group")
			}
		}
		var completion map[string]json.RawMessage
		var content []map[string]json.RawMessage
		_ = json.Unmarshal(got[3], &completion)
		_ = json.Unmarshal(completion["content"], &content)
		completionText := jsonString(content[0], "text")
		if !strings.Contains(completionText, referencedLine) {
			t.Fatal("decoded verified-row reference was lost from factual completion")
		}
		if strings.Contains(completionText, "old detail") {
			t.Fatal("factual completion retained unreferenced output")
		}
	})

	t.Run("percent encoded row remains exact", func(t *testing.T) {
		items := retirementHistory()
		referencedLine := `{"row":"17%3aabcd","detail":"exact"}`
		items[3] = compactTestOutput("operation_00",
			referencedLine+"\n"+strings.Repeat("old detail\n", 500), 0)
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "content": "Retain 17:abcd.",
		}))
		got := retireCompactionOperations(items)
		for _, index := range []int{1, 2, 3} {
			if string(got[index]) == string(items[index]) {
				t.Fatal("percent-encoded row pinned the native reasoning group")
			}
		}
		var completion map[string]json.RawMessage
		var content []map[string]json.RawMessage
		_ = json.Unmarshal(got[3], &completion)
		_ = json.Unmarshal(completion["content"], &content)
		completionText := jsonString(content[0], "text")
		if !strings.Contains(completionText, referencedLine) {
			t.Fatal("percent-encoded row was lost from factual completion")
		}
		if strings.Contains(completionText, "old detail") {
			t.Fatal("factual completion retained unreferenced output")
		}
	})

	t.Run("retained script in nested JSON", func(t *testing.T) {
		items := retirementHistory()
		items[3] = compactTestOutput("operation_00",
			`{"result":{"script_ref":"@shell\u002fsource-reference","detail":"`+strings.Repeat("old detail ", 500)+`"}}`, 0)
		arguments := `{"cmd":"{\"script_ref\":\"@shell\\u002fsource-reference\"}","workdir":"/workspace"}`
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "function_call", "name": "exec_command", "call_id": "pending_script", "arguments": arguments,
		}))
		assertPinned(t, items)
	})
}

func TestCompactionRetirementCarriesReferencedRowsInFactualCompletion(t *testing.T) {
	items := retirementHistory()
	var body strings.Builder
	for row := 1; row <= 20; row++ {
		fmt.Fprintf(&body, "%d:%04x source declaration %s\n", row, row, strings.Repeat("detail ", 20))
	}
	partial := "context line contains partial row 9:0009 with exact surrounding evidence\n"
	body.WriteString(partial)
	items[3] = compactTestOutput("operation_00", body.String(), 0)
	items = append(items, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant",
		"content": "Retain 2:0002, range 5:0005..7:0007, and partial 9:0009.",
	}))
	before := string(mustMarshalJSON(items))

	got := reduceContextCompaction(items)
	completionText := string(mustMarshalJSON(got))
	if !strings.Contains(completionText, "historical facts v4") {
		t.Fatal("row-only evidence reference pinned the native reasoning group")
	}
	for _, retained := range []string{"2:0002", "5:0005", "6:0006", "7:0007", partial} {
		if !strings.Contains(completionText, strings.TrimSpace(retained)) {
			t.Fatalf("factual completion lost referenced evidence %q", retained)
		}
	}
	if strings.Contains(completionText, "10:000a source declaration") {
		t.Fatal("factual completion retained unreferenced source evidence")
	}
	if len(mustMarshalJSON(got)) > len(mustMarshalJSON(items)) {
		t.Fatal("row-aware retirement increased retained history")
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("row-aware retirement mutated its input")
	}
	if again := reduceContextCompaction(got); string(mustMarshalJSON(again)) != string(mustMarshalJSON(got)) {
		t.Fatal("row-aware factual completion was not stable across repeated compaction")
	}

	whole := retirementHistory()
	whole = append(whole, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "content": "Keep complete output from operation_00.",
	}))
	kept := reduceContextCompaction(whole)
	for _, index := range []int{1, 2, 3} {
		if string(kept[index]) != string(whole[index]) {
			t.Fatal("whole-output reference no longer pins native operation")
		}
	}
}

func TestCompactionRetirementPreservesFullPythonTraceback(t *testing.T) {
	items := retirementHistory()
	traceback := strings.Repeat("routine before\n", 300) +
		"Traceback (most recent call last):\n" +
		"  File \"first.py\", line 10, in <module>\n" +
		"    outer()\n" +
		"  File \"worker.py\", line 20, in outer\n" +
		"    inner()\n" +
		"  File \"worker.py\", line 30, in inner\n" +
		"    raise ValueError(\"masked failure\")\n" +
		"ValueError: masked failure\n" +
		"first line of a multiline exception message\n" +
		"second line of the exception message\n" +
		"important remediation note\n" +
		strings.Repeat("routine after\n", 300)
	items[3] = compactTestOutput("operation_00", traceback, 0)

	wire := string(mustMarshalJSON(retireCompactionOperations(items)))
	for _, evidence := range []string{
		"Traceback (most recent call last):",
		"first.py",
		"worker.py",
		"raise ValueError",
		"ValueError: masked failure",
		"first line of a multiline exception message",
		"second line of the exception message",
		"important remediation note",
	} {
		if !strings.Contains(wire, evidence) {
			t.Fatalf("Python traceback evidence %q was lost", evidence)
		}
	}
	if strings.Count(wire, "routine before") > 2 {
		t.Fatal("bulk unmarked output before the traceback was not retired")
	}
	if strings.Count(wire, "routine after") != 300 {
		t.Fatal("the suffix after the traceback was not preserved conservatively")
	}
}

func TestCompactionRetirementDoesNotTreatVerifiedSearchSourceAsDiagnostics(t *testing.T) {
	var output strings.Builder
	for row := 1; row <= 100; row++ {
		fmt.Fprintf(&output, "\"path with spaces/example.go\":%d:abcd func example() error { return nil }\n", row)
	}
	output.WriteString("WARNING: integration coverage is incomplete\nok example/router 0.1s\n")
	retired := compactionRetiredText(output.String())
	if strings.Contains(retired, `example.go":1:abcd`) {
		t.Fatal("an error type inside verified source was retained as a runtime diagnostic")
	}
	if !strings.Contains(retired, "WARNING: integration coverage is incomplete") ||
		!strings.Contains(retired, "ok example/router 0.1s") {
		t.Fatal("actual diagnostic or test outcome was lost")
	}
}
