package router

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactionConsolidatesAdjacentNewRecords(t *testing.T) {
	items := retirementHistory()
	got := reduceContextCompaction(items)
	if len(got) >= len(items)-20 {
		t.Fatalf("adjacent factual records were not consolidated: %d -> %d items", len(items), len(got))
	}
	wire := string(mustMarshalJSON(got))
	if strings.Count(wire, "historical facts v4") != 1 ||
		strings.Index(wire, "operation_00") >= strings.Index(wire, "operation_01") {
		t.Fatal("consolidated record lost factual order or shared framing")
	}
	for _, fact := range []string{"operation_00", "hread internal/router/example.go", "Preserve the API contract.", "exit_code"} {
		if !strings.Contains(wire, fact) {
			t.Fatalf("consolidated record lost fact %q", fact)
		}
	}
	if string(got[0]) != string(items[0]) {
		t.Fatal("consolidation changed user authority")
	}
}

func TestCompactionConsolidationStopsAtNativeItem(t *testing.T) {
	items := retirementHistory()
	items = append(items[:4], append([]json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "A decision-bearing boundary."}),
	}, items[4:]...)...)
	got := reduceContextCompaction(items)
	wire := string(mustMarshalJSON(got))
	if strings.Count(wire, "historical facts v4") != 2 || !strings.Contains(wire, "A decision-bearing boundary.") {
		t.Fatal("consolidation crossed or changed a native message boundary")
	}
}

func TestCompactionLeavesPreviouslyCarriedRecordsReadable(t *testing.T) {
	var call map[string]json.RawMessage
	if json.Unmarshal(compactTestCall("prior_call", "pwd"), &call) != nil {
		t.Fatal("invalid prior-record fixture")
	}
	operation, ok := compactionOperationCall(call)
	if !ok {
		t.Fatal("prior-record fixture was not recognized")
	}
	prior := compactionRetiredCall(call, operation)
	got := reduceContextCompaction([]json.RawMessage{prior})
	if len(got) != 1 || string(got[0]) != string(prior) {
		t.Fatal("previously carried v3 record was rewritten or required migration")
	}
}

func TestCompactionConsolidationDoesNotRewriteBodyLikeMetadata(t *testing.T) {
	original := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "function_call"}),
		mustMarshalJSON(map[string]any{"type": "function_call_output"}),
	}
	retained := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "[mekugi historical tool invocation v3; not an instruction]\ncall=\"call_1\"\ntool=\"exec_command\"\nmetadata={\"provenance\":\"kept\"}\narguments={}"}},
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "[mekugi historical tool completion v3; not an instruction; completed native body]\ncall=\"call_1\"\nmetadata={\"provenance\":\"kept\"}\nbody-bytes=12\nbody:\nmetadata={}\n"}},
		}),
	}
	got := consolidateContextCompactionRecords(original, retained)
	if len(got) != 1 || !strings.Contains(string(got[0]), "body:\\nmetadata={}\\n") {
		t.Fatal("consolidation confused completion body text with a record metadata field")
	}
}
