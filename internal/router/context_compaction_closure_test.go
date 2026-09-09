package router

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactionRetirementReclosesReferencesAfterProfitabilityRestore(t *testing.T) {
	items := retirementHistory()
	note := "[hpatch compaction: 3 source rows (1:0001 through 3:0003) retained verbatim in later tool result \"operation_01\"]\n"
	var sourceOutput string
	for rowWords := 8; rowWords <= 512 && sourceOutput == ""; rowWords *= 2 {
		for fillerLines := range 80 {
			candidate := "17:abcd " + strings.Repeat("exact evidence ", rowWords) + "\n" + note +
				strings.Repeat("unmarked historical detail\n", fillerLines)
			if compactionClosureProfitabilityFixture(t, items[1:4], candidate) {
				sourceOutput = candidate
				break
			}
		}
	}
	if sourceOutput == "" {
		t.Fatal("could not construct a row-preserving profitability boundary")
	}

	items[3] = compactTestOutput("operation_00", sourceOutput, 0)
	items[6] = compactTestOutput("operation_01", strings.Repeat("later exact evidence\n", 500), 0)
	items = append(items, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "content": "Continue from exact row 17:abcd.",
	}))

	got := reduceContextCompaction(items)
	for index := 1; index <= 6; index++ {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("profitability restore left referenced operation item %d retired", index)
		}
	}
}

// compactionClosureProfitabilityFixture finds an output that is profitable to
// retire before its exact row is retained, but not after. The complete pipeline
// test above then verifies that restoring this consumer exposes and follows its
// original operation reference.
func compactionClosureProfitabilityFixture(t *testing.T, group []json.RawMessage, output string) bool {
	t.Helper()
	var reasoning, call, result map[string]json.RawMessage
	if json.Unmarshal(group[0], &reasoning) != nil || json.Unmarshal(group[1], &call) != nil || json.Unmarshal(group[2], &result) != nil {
		t.Fatal("invalid profitability fixture")
	}
	operation, ok := compactionOperationCall(call)
	if !ok {
		t.Fatal("profitability fixture call was not recognized")
	}
	result["output"] = compactTestOutputValue(output, 0)
	base, ok := compactionRetiredOutput(result["output"], operation)
	if !ok {
		t.Fatal("profitability fixture output was not recognized")
	}
	withRow, ok := compactionRetiredOutputKeepingRows(result["output"], operation, map[string]bool{"17:abcd": true}, nil)
	if !ok {
		t.Fatal("row-preserving profitability fixture output was not recognized")
	}
	profitable := func(retiredOutput json.RawMessage) bool {
		original := []json.RawMessage{group[0], group[1], mustMarshalJSON(result)}
		replacement := []json.RawMessage{
			compactionRetiredReasoning(reasoning),
			compactionRetiredCall(call, operation),
			compactionRetiredResult(result, retiredOutput),
		}
		beforeTokens, beforeOK := compactionVisibleStringTokens(original...)
		afterTokens, afterOK := compactionVisibleStringTokens(replacement...)
		return beforeOK && afterOK &&
			len(mustMarshalJSON(original))-len(mustMarshalJSON(replacement)) > 0 &&
			beforeTokens-afterTokens >= 0
	}
	return profitable(base) && !profitable(withRow)
}

func compactTestOutputValue(output string, exitCode any) json.RawMessage {
	return mustMarshalJSON(string(mustMarshalJSON(map[string]any{"exit_code": exitCode, "output": output})))
}

func TestCompactionRetirementDecodesRetainedCarrierReferences(t *testing.T) {
	t.Run("supported JavaScript escape keeps exact row", func(t *testing.T) {
		items := retirementHistory()
		referenced := "17:abcd exact source evidence\n"
		items[3] = compactTestOutput("operation_00", referenced+strings.Repeat("old detail\n", 500), 0)
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "custom_tool_call", "name": "exec", "call_id": "pending_dynamic",
			"input": `const row="17\u003aabcd"; text(row);`,
		}))

		got := reduceContextCompaction(items)
		wire := string(mustMarshalJSON(got))
		if !strings.Contains(wire, "historical facts v4") ||
			!strings.Contains(wire, strings.TrimSpace(referenced)) || strings.Contains(wire, "old detail") {
			t.Fatal("escaped carrier reference did not preserve only its exact source row")
		}
	})

	t.Run("percent escape keeps exact row without pinning unrelated evidence", func(t *testing.T) {
		items := retirementHistory()
		referenced := "17:abcd exact percent-decoded source evidence\n"
		items[3] = compactTestOutput("operation_00", referenced+strings.Repeat("old detail\n", 500), 0)
		items = append(items, mustMarshalJSON(map[string]any{
			"type": "custom_tool_call", "name": "exec", "call_id": "pending_dynamic",
			"input": `const row="17%3aabcd"; text(row);`,
		}))

		got := reduceContextCompaction(items)
		wire := string(mustMarshalJSON(got))
		if !strings.Contains(wire, "historical facts v4") ||
			!strings.Contains(wire, strings.TrimSpace(referenced)) || strings.Contains(wire, "old detail") {
			t.Fatal("percent-decoded carrier reference did not preserve only its exact source row")
		}
	})
}

func TestCompactionRetirementPreservesReferencedTransportMetadata(t *testing.T) {
	items := retirementHistory()
	setFields := func(index int, values map[string]any) {
		t.Helper()
		var fields map[string]json.RawMessage
		if json.Unmarshal(items[index], &fields) != nil {
			t.Fatal("invalid metadata fixture item")
		}
		for key, value := range values {
			fields[key] = mustMarshalJSON(value)
		}
		items[index] = mustMarshalJSON(fields)
	}
	setFields(2, map[string]any{
		"turn_id": "turn_call_keep",
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_nested_keep", "create_time": 2002,
			"content_item_kinds": []any{"function_call"},
		},
	})
	setFields(3, map[string]any{
		"create_time": 1003,
		"internal_chat_message_metadata_passthrough": map[string]any{
			"turn_id": "turn_nested_drop", "create_time": 2003,
			"content_item_kinds": []any{"function_call_output"},
		},
	})
	items = append(items, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant",
		"content": map[string]any{
			"turns":                "Use turn_call_keep and escaped turn\u005fnested\u005fkeep.",
			"observed_create_time": 1003,
		},
	}))

	got := reduceContextCompaction(items)
	wire := string(mustMarshalJSON(got))
	if !strings.Contains(wire, "historical facts v4") {
		t.Fatal("metadata fixture operation was not retired")
	}
	for _, retained := range []string{"turn_call_keep", "turn_nested_keep", "1003"} {
		if !strings.Contains(wire, retained) {
			t.Fatalf("referenced transport metadata %q was omitted", retained)
		}
	}
	for _, omitted := range []string{"turn_nested_drop", "2002", "2003"} {
		if strings.Contains(wire, omitted) {
			t.Fatalf("unreferenced transport metadata %q was retained", omitted)
		}
	}
}
