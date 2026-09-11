package router

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func compactionLedgerTestPayload(t *testing.T, raw json.RawMessage) (string, string) {
	t.Helper()
	var item struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type != "message" || item.Role != "assistant" ||
		len(item.Content) != 1 || item.Content[0].Type != "output_text" {
		t.Fatal("factual record is not one assistant output-text message")
	}
	header, payload, ok := strings.Cut(item.Content[0].Text, "\n")
	if !ok {
		t.Fatal("factual record has no versioned header")
	}
	return header, payload
}

func compactionLedgerTestManifest(t *testing.T, payload string) ([]json.RawMessage, string) {
	t.Helper()
	header, body, hasBody := strings.Cut(payload, "\nbody:\n")
	values := make(map[string]json.RawMessage)
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || value == "" {
			t.Fatalf("invalid labeled factual record field: %s", line)
		}
		values[key] = json.RawMessage(value)
	}
	fields := []json.RawMessage{values["call"], values["metadata"]}
	switch {
	case values["arguments"] != nil:
		fields = []json.RawMessage{values["call"], values["tool"], values["metadata"], values["arguments"]}
	case values["data"] != nil:
		fields = append(fields, values["data"])
		if values["body-bytes"] != nil {
			fields = append(fields, values["body-bytes"])
		}
	case values["result"] != nil:
		fields = append(fields, values["result"])
		if values["body-bytes"] != nil {
			fields = append(fields, values["body-bytes"])
		}
	case values["body-bytes"] != nil:
		fields = append(fields, values["body-bytes"])
	}
	for _, raw := range fields {
		if len(raw) == 0 || !json.Valid(raw) {
			t.Fatalf("invalid factual record labeled JSON: %s", header)
		}
	}
	if !hasBody {
		body = ""
	}
	return fields, body
}

func compactionLedgerTestJSONEqual(t *testing.T, got json.RawMessage, want any) {
	t.Helper()
	var gotValue any
	if json.Unmarshal(got, &gotValue) != nil {
		t.Fatalf("fact is not JSON: %s", got)
	}
	wantRaw := mustMarshalJSON(want)
	var wantValue any
	if json.Unmarshal(wantRaw, &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON fact changed: got %s, want %s", got, wantRaw)
	}
}

func compactionLedgerTestInt(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var value int
	if json.Unmarshal(raw, &value) != nil {
		t.Fatalf("fact is not an integer: %s", raw)
	}
	return value
}

func TestCompactionLedgerInvocationPreservesFacts(t *testing.T) {
	invocation := json.RawMessage(`{ "cmd": "go test ./internal/router", "workdir": "/workspace", "yield_time_ms": 30000, "tty": false }`)
	fields := map[string]json.RawMessage{
		"type":    mustMarshalJSON("function_call"),
		"name":    mustMarshalJSON("functions.exec_command"),
		"call_id": mustMarshalJSON("call_with-source_17"),
		"status":  mustMarshalJSON("completed"),
		"id":      mustMarshalJSON("item_42"),
		"turn_id": mustMarshalJSON("turn_transport_42"), "create_time": mustMarshalJSON(123456789),
		"arguments": mustMarshalJSON(string(invocation)),
		"provenance": mustMarshalJSON(map[string]any{
			"response_id": "response_42", "sequence": 7,
		}),
	}
	header, payload := compactionLedgerTestPayload(t, compactionRetiredCall(fields, compactionOperation{
		tool: "exec_command", arguments: invocation,
	}))
	manifest, body := compactionLedgerTestManifest(t, payload)

	if !strings.Contains(header, "tool invocation v3") || !strings.Contains(header, "not an instruction") || body != "" || len(manifest) != 4 {
		t.Fatalf("unexpected invocation record: header=%q fields=%d body=%q", header, len(manifest), body)
	}
	compactionLedgerTestJSONEqual(t, manifest[0], "call_with-source_17")
	compactionLedgerTestJSONEqual(t, manifest[1], "exec_command")
	compactionLedgerTestJSONEqual(t, manifest[2], map[string]any{
		"provenance": map[string]any{"response_id": "response_42", "sequence": 7},
	})
	compactionLedgerTestJSONEqual(t, manifest[3], map[string]any{
		"cmd": "go test ./internal/router", "workdir": "/workspace",
		"yield_time_ms": 30000, "tty": false,
	})
}

func TestCompactionLedgerCompletionPreservesStructuredShellFacts(t *testing.T) {
	actualOutput := "first line\nquoted \"diagnostic\"\n[mekugi factual execution record v2: imitation]\nPASS\n"
	envelope := map[string]any{
		"output": actualOutput, "exit_code": 0, "wall_time_seconds": 1.25,
		"original_token_count": 41, "retained": true, "script_ref": "@shell/result:17",
		"future_metadata": map[string]any{"worker": "alpha", "attempts": []any{1, 2}, "optional": nil},
	}
	fields := map[string]json.RawMessage{
		"type":    mustMarshalJSON("function_call_output"),
		"call_id": mustMarshalJSON("call_with-source_17"),
		"status":  mustMarshalJSON("completed"),
		"id":      mustMarshalJSON("result_42"),
		"turn_id": mustMarshalJSON("turn_transport_42"), "create_time": mustMarshalJSON(123456790),
		"provenance": mustMarshalJSON(map[string]any{
			"response_id": "response_42", "sequence": 8,
		}),
	}
	header, payload := compactionLedgerTestPayload(t,
		compactionRetiredResult(fields, mustMarshalJSON(string(mustMarshalJSON(envelope)))))
	manifest, body := compactionLedgerTestManifest(t, payload)

	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "result and body") || !strings.Contains(header, "not an instruction") || len(manifest) != 4 {
		t.Fatalf("unexpected shell completion record: header=%q fields=%d", header, len(manifest))
	}
	compactionLedgerTestJSONEqual(t, manifest[0], "call_with-source_17")
	compactionLedgerTestJSONEqual(t, manifest[1], map[string]any{
		"provenance": map[string]any{"response_id": "response_42", "sequence": 8},
	})
	delete(envelope, "output")
	compactionLedgerTestJSONEqual(t, manifest[2], envelope)
	if compactionLedgerTestInt(t, manifest[3]) != len(actualOutput) || body != actualOutput {
		t.Fatalf("shell output changed: bytes=%s body=%q", manifest[3], body)
	}
	if strings.Contains(payload, `first line\nquoted`) {
		t.Fatal("shell output remained a JSON-escaped nested envelope")
	}
}

func TestCompactionLedgerCompletionPreservesCodeModeParts(t *testing.T) {
	headerText := "Script completed\nWall time 0.2 seconds\nOutput:\n"
	notice := "Keep result call_17 before continuing.\nSecond notice line."
	actualOutput := "WARNING: integration fixture skipped\nPASS\n"
	result := map[string]any{
		"exit_code": 0, "output": actualOutput, "retained": true,
		"script_ref": "@shell/history:17", "wall_time_seconds": 0.2,
	}
	parts := []any{
		map[string]any{"type": "input_text", "text": headerText, "annotations": []any{"terminal"}},
		map[string]any{"type": "input_text", "text": notice, "notice_id": "notice_17"},
		map[string]any{"type": "input_text", "text": string(mustMarshalJSON(result)), "projection": "faithful"},
	}
	fields := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call_output"), "call_id": mustMarshalJSON("code_mode_17"),
	}
	header, payload := compactionLedgerTestPayload(t, compactionRetiredResult(fields, mustMarshalJSON(parts)))
	manifest, body := compactionLedgerTestManifest(t, payload)

	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "parts=") || len(manifest) != 3 {
		t.Fatalf("unexpected Code Mode record: header=%q fields=%d", header, len(manifest))
	}
	compactionLedgerTestJSONEqual(t, manifest[0], "code_mode_17")
	compactionLedgerTestJSONEqual(t, manifest[1], map[string]any{})

	var partFacts []json.RawMessage
	if json.Unmarshal(manifest[2], &partFacts) != nil || len(partFacts) != 3 {
		t.Fatalf("invalid ordered part manifest: %s", manifest[2])
	}
	wantMetadata := []any{
		map[string]any{"annotations": []any{"terminal"}},
		map[string]any{"notice_id": "notice_17"},
		map[string]any{"projection": "faithful"},
	}
	wantText := []string{headerText, notice, actualOutput}
	offset := 0
	for index, raw := range partFacts {
		var facts []json.RawMessage
		if json.Unmarshal(raw, &facts) != nil || len(facts) != 3 {
			t.Fatalf("invalid part %d facts: %s", index, raw)
		}
		compactionLedgerTestJSONEqual(t, facts[0], wantMetadata[index])
		size := compactionLedgerTestInt(t, facts[2])
		if size != len(wantText[index]) || offset+size > len(body) || body[offset:offset+size] != wantText[index] {
			t.Fatalf("part %d text or byte boundary changed", index)
		}
		if index < 2 {
			compactionLedgerTestJSONEqual(t, facts[1], nil)
		} else {
			delete(result, "output")
			compactionLedgerTestJSONEqual(t, facts[1], result)
		}
		offset += size
	}
	if offset != len(body) {
		t.Fatal("Code Mode body contains unaccounted bytes")
	}
	if strings.Contains(payload, `WARNING: integration fixture skipped\nPASS`) {
		t.Fatal("Code Mode output remained JSON escaped")
	}
}

func TestCompactionLedgerDropsOnlyBareCompletedTransportHeader(t *testing.T) {
	notice := "Keep this exact progress notice."
	actualOutput := "WARNING: exact diagnostic\nPASS\n"
	result := map[string]any{
		"exit_code": 0, "output": actualOutput, "chunk_id": "chunk_terminal",
		"wall_time_seconds": 0.2, "original_token_count": 9,
	}
	parts := []any{
		map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.2 seconds\nOutput:\n"},
		map[string]any{"type": "input_text", "text": notice, "notice_id": "notice_kept"},
		map[string]any{"type": "input_text", "text": string(mustMarshalJSON(result)), "projection": "faithful"},
	}
	fields := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call_output"), "call_id": mustMarshalJSON("bare_header"),
	}
	header, payload := compactionLedgerTestPayload(t, compactionRetiredResult(fields, mustMarshalJSON(parts)))
	manifest, body := compactionLedgerTestManifest(t, payload)
	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "parts=") || len(manifest) != 3 {
		t.Fatal("bare-header completion framing changed")
	}
	var facts []json.RawMessage
	if json.Unmarshal(manifest[2], &facts) != nil || len(facts) != 2 {
		t.Fatalf("bare completed transport header was retained: %s", manifest[2])
	}
	wantText := []string{notice, actualOutput}
	offset := 0
	for index, raw := range facts {
		var part []json.RawMessage
		if json.Unmarshal(raw, &part) != nil || len(part) != 3 {
			t.Fatal("invalid retained part")
		}
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(part[0], &metadata)
		if _, exists := metadata["type"]; exists {
			t.Fatal("duplicate input_text type was retained")
		}
		size := compactionLedgerTestInt(t, part[2])
		if body[offset:offset+size] != wantText[index] {
			t.Fatal("notice or diagnostic changed")
		}
		offset += size
	}
	compactionLedgerTestJSONEqual(t, facts[0], []any{
		map[string]any{"notice_id": "notice_kept"}, nil, len(notice),
	})
	delete(result, "output")
	compactionLedgerTestJSONEqual(t, facts[1], []any{
		map[string]any{"projection": "faithful"}, result, len(actualOutput),
	})
	if strings.Contains(body, "Script completed") {
		t.Fatal("bare successful transport header remained in the body")
	}
}

func TestCompactionLedgerCompletionPreservesPatchAndUnknownRepresentations(t *testing.T) {
	fields := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call_output"), "call_id": mustMarshalJSON("patch_17"),
	}
	report := "in example.go\nfiles add=0 update=1 move=0 delete=0\n"
	parts := []any{
		map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
		map[string]any{"type": "input_text", "text": report},
	}
	header, payload := compactionLedgerTestPayload(t, compactionRetiredResult(fields, mustMarshalJSON(parts)))
	manifest, body := compactionLedgerTestManifest(t, payload)
	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "parts=") || len(manifest) != 3 || !strings.HasSuffix(body, report) {
		t.Fatal("patch application report or Code Mode ordering changed")
	}

	unknown := json.RawMessage(`{"opaque":["not","a","known","result"],"status":"mystery"}`)
	header, payload = compactionLedgerTestPayload(t, compactionRetiredResult(fields, unknown))
	manifest, body = compactionLedgerTestManifest(t, payload)
	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "JSON result") || body != "" || len(manifest) != 3 {
		t.Fatal("unknown output record framing changed")
	}
	compactionLedgerTestJSONEqual(t, manifest[2], map[string]any{
		"opaque": []any{"not", "a", "known", "result"}, "status": "mystery",
	})

	native := "Chunk ID: abc123\nWall time: 0.3 seconds\nProcess exited with code 0\nOriginal token count: 9\nFinal output:\nok example/router\n"
	header, payload = compactionLedgerTestPayload(t, compactionRetiredResult(fields, mustMarshalJSON(native)))
	manifest, body = compactionLedgerTestManifest(t, payload)
	if !strings.Contains(header, "completion v3") || !strings.Contains(header, "completed native body") || len(manifest) != 3 ||
		compactionLedgerTestInt(t, manifest[2]) != len(native) || body != native {
		t.Fatal("native completed output was escaped or changed")
	}
}

func TestCompactionRetirementPinsReferencedNativeItemAliases(t *testing.T) {
	tests := []struct {
		name  string
		index int
		id    string
	}{
		{name: "reasoning item", index: 1, id: "rs_00"},
		{name: "call item", index: 2, id: "fc_native_00"},
		{name: "result item", index: 3, id: "out_native_00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := retirementHistory()
			var fields map[string]json.RawMessage
			if json.Unmarshal(items[test.index], &fields) != nil {
				t.Fatal("invalid test item")
			}
			fields["id"] = mustMarshalJSON(test.id)
			items[test.index] = mustMarshalJSON(fields)
			items = append(items, mustMarshalJSON(map[string]any{
				"type": "message", "role": "assistant",
				"content": "Continue using the evidence from " + test.id + ".",
			}))

			got := retireCompactionOperations(items)
			for index := 1; index <= 3; index++ {
				if string(got[index]) != string(items[index]) {
					t.Fatalf("referenced native item alias did not pin group item %d", index)
				}
			}
		})
	}
}

func TestCompactionStripTransportBookkeepingPreservesUnrecognizedMetadata(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"malformed":    json.RawMessage(`{`),
		"null":         json.RawMessage(`null`),
		"array":        json.RawMessage(`["opaque"]`),
		"string":       json.RawMessage(`"opaque"`),
		"empty":        json.RawMessage(`{}`),
		"unknown only": json.RawMessage(`{"future":{"sequence":4}}`),
	} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]json.RawMessage{
				"internal_chat_message_metadata_passthrough": raw,
			}
			before := string(fields["internal_chat_message_metadata_passthrough"])
			compactionStripTransportBookkeeping(fields)
			if got, exists := fields["internal_chat_message_metadata_passthrough"]; !exists || string(got) != before {
				t.Fatalf("unrecognized metadata changed: got %s, want %s", got, before)
			}
		})
	}

	fields := map[string]json.RawMessage{
		"internal_chat_message_metadata_passthrough": json.RawMessage(`{"turn_id":"turn_17","create_time":123}`),
	}
	compactionStripTransportBookkeeping(fields)
	if _, exists := fields["internal_chat_message_metadata_passthrough"]; exists {
		t.Fatal("empty transport metadata was retained")
	}
}

func TestCompactionLedgerOmitsOnlyUnreferencedTransportBookkeeping(t *testing.T) {
	items := retirementHistory()
	setFields := func(index int, values map[string]any) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(items[index], &fields) != nil {
			t.Fatal("invalid test item")
		}
		for key, value := range values {
			fields[key] = mustMarshalJSON(value)
		}
		items[index] = mustMarshalJSON(fields)
	}
	setFields(1, map[string]any{
		"id": "rs_native_00", "turn_id": "turn_reasoning_00", "create_time": 1001,
	})
	setFields(2, map[string]any{
		"id": "fc_native_00", "turn_id": "turn_call_00", "create_time": 1002,
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_call_nested_00", "create_time": 2002,
			"content_item_kinds": []any{"function_call"}, "future": map[string]any{"attempt": 3},
		},
		"workspace_version":     "workspace-v17",
		"unknown_call_metadata": map[string]any{"attempt": 3, "scope": "router"},
	})
	setFields(3, map[string]any{
		"id": "out_native_00", "turn_id": "turn_result_00", "create_time": 1003,
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_result_nested_00", "create_time": 2003,
			"content_item_kinds": []any{"function_call_output"}, "future": map[string]any{"sequence": 4},
		},
		"result_version":          "result-v9",
		"unknown_result_metadata": map[string]any{"source": "local", "sequence": 4},
	})

	before := string(mustMarshalJSON(items))
	got := retireCompactionOperations(items)
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("retirement mutated its input")
	}
	for index := 1; index <= 3; index++ {
		if string(got[index]) == string(items[index]) {
			t.Fatalf("unreferenced unit item %d was not retired", index)
		}
	}
	wire := string(mustMarshalJSON(got[:4]))
	for _, bookkeeping := range []string{
		"rs_native_00", "fc_native_00", "out_native_00",
		"turn_reasoning_00", "turn_call_00", "turn_result_00",
		"1001", "1002", "1003",
	} {
		if strings.Contains(wire, bookkeeping) {
			t.Fatalf("unreferenced transport bookkeeping %q was retained", bookkeeping)
		}
	}

	callHeader, callPayload := compactionLedgerTestPayload(t, got[2])
	callManifest, callBody := compactionLedgerTestManifest(t, callPayload)
	if !strings.Contains(callHeader, "tool invocation v3") || callBody != "" || len(callManifest) != 4 {
		t.Fatal("retired invocation moved or changed kind")
	}
	compactionLedgerTestJSONEqual(t, callManifest[0], "operation_00")
	compactionLedgerTestJSONEqual(t, callManifest[2], map[string]any{
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"function_call"}, "future": map[string]any{"attempt": 3},
		},
		"workspace_version":     "workspace-v17",
		"unknown_call_metadata": map[string]any{"attempt": 3, "scope": "router"},
	})
	compactionLedgerTestJSONEqual(t, callManifest[3], map[string]any{
		"cmd": "hread internal/router/example.go", "workdir": "/workspace",
	})

	resultHeader, resultPayload := compactionLedgerTestPayload(t, got[3])
	resultManifest, _ := compactionLedgerTestManifest(t, resultPayload)
	if !strings.Contains(resultHeader, "completion v3") || !strings.Contains(resultHeader, "result and body") || len(resultManifest) != 4 {
		t.Fatal("retired completion moved or changed kind")
	}
	compactionLedgerTestJSONEqual(t, resultManifest[0], "operation_00")
	compactionLedgerTestJSONEqual(t, resultManifest[1], map[string]any{
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"function_call_output"}, "future": map[string]any{"sequence": 4},
		},
		"result_version":          "result-v9",
		"unknown_result_metadata": map[string]any{"source": "local", "sequence": 4},
	})

	if repeated := retireCompactionOperations(got); string(mustMarshalJSON(repeated)) != string(mustMarshalJSON(got)) {
		t.Fatal("bookkeeping retirement was not stable on repeat compaction")
	}
}

func TestCompactionRetirementIgnoresOwnReasoningAlias(t *testing.T) {
	items := retirementHistory()
	var reasoning map[string]json.RawMessage
	if json.Unmarshal(items[1], &reasoning) != nil {
		t.Fatal("invalid reasoning item")
	}
	reasoning["id"] = mustMarshalJSON("rs_native_00")
	items[1] = mustMarshalJSON(reasoning)

	first := compactTestCall("operation_00", "printf rs_native_00")
	items[2] = first
	items = append(items[:4], append([]json.RawMessage{
		compactTestCall("same_reasoning_group", "pwd"),
		compactTestOutput("same_reasoning_group", "/workspace\n", 0),
	}, items[4:]...)...)

	got := retireCompactionOperations(items)
	for _, index := range []int{1, 2, 3, 4, 5} {
		if string(got[index]) == string(items[index]) {
			t.Fatalf("own reasoning alias created a self-dependency at %d", index)
		}
	}
}

func TestCompactionRetirementRejectsVisibleTokenGrowth(t *testing.T) {
	items := retirementHistory()
	items[2] = compactTestCall("operation_00", "printf done")
	items[3] = compactTestOutput("operation_00", strings.Repeat(" ", 4096), 0)

	var reasoning, call, result map[string]json.RawMessage
	_ = json.Unmarshal(items[1], &reasoning)
	_ = json.Unmarshal(items[2], &call)
	_ = json.Unmarshal(items[3], &result)
	operation, ok := compactionOperationCall(call)
	if !ok {
		t.Fatal("profitability fixture call was not recognized")
	}
	retiredOutput, ok := compactionRetiredOutput(result["output"], operation)
	if !ok {
		t.Fatal("profitability fixture output was not recognized")
	}
	replacements := []json.RawMessage{
		compactionRetiredReasoning(reasoning),
		compactionRetiredCall(call, operation),
		compactionRetiredResult(result, retiredOutput),
	}
	beforeBytes := len(items[1]) + len(items[2]) + len(items[3])
	afterBytes := len(replacements[0]) + len(replacements[1]) + len(replacements[2])
	beforeTokens, beforeOK := compactionVisibleStringTokens(items[1], items[2], items[3])
	afterTokens, afterOK := compactionVisibleStringTokens(replacements...)
	if !beforeOK || !afterOK || afterBytes >= beforeBytes || afterTokens <= beforeTokens {
		t.Fatalf("fixture must shrink bytes but grow visible tokens: bytes %d -> %d, tokens %d -> %d",
			beforeBytes, afterBytes, beforeTokens, afterTokens)
	}

	got := retireCompactionOperations(items)
	for index := 1; index <= 3; index++ {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("token-growing complete group item %d was retired", index)
		}
	}
}
