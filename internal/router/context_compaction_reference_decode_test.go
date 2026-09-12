package router

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestCompactionReferenceDecoderAcceptsSupportedPercentColon(t *testing.T) {
	var visited []string
	unsafeEncoding := false
	compactionSourceVisitDecodedReferences("unrelated%3acolon", func(text string) {
		visited = append(visited, text)
	}, &unsafeEncoding)

	if unsafeEncoding {
		t.Fatal("supported percent-encoded colon was marked globally unsafe")
	}
	if !slices.Contains(visited, "unrelated:colon") {
		t.Fatalf("decoded visits = %q, want decoded percent-colon form", visited)
	}
}

func TestCompactionReferenceDecoderVisitsRawAndOneDecodedLayer(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "percent", text: `17%3Aabcd`, want: `17:abcd`},
		{name: "HTML decimal", text: `17&#58;abcd`, want: `17:abcd`},
		{name: "HTML hexadecimal", text: `17&#x3A;abcd`, want: `17:abcd`},
		{name: "HTML named", text: `17&colon;abcd`, want: `17:abcd`},
		{name: "JavaScript fixed unicode", text: `"turn\u005fkeep"`, want: `"turn_keep"`},
		{name: "JavaScript code point", text: "`17\\u{3a}abcd`", want: "`17:abcd`"},
		{name: "JavaScript code point leading zeros", text: "`17\\u{00000003a}abcd`", want: "`17:abcd`"},
		{name: "JavaScript hexadecimal", text: `'@shell\x2fresult'`, want: `'@shell/result'`},
		{name: "mixed direct encodings", text: `"turn\u{3a}keep%5fexact"`, want: `"turn:keep_exact"`},
		{name: "surrogate pair", text: `"face\uD83D\uDE00"`, want: `"face😀"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var visited []string
			unsafeEncoding := false
			compactionSourceVisitDecodedReferences(test.text, func(text string) {
				visited = append(visited, text)
			}, &unsafeEncoding)
			if unsafeEncoding {
				t.Fatal("supported encoding was marked globally unsafe")
			}
			if len(visited) < 2 || visited[0] != test.text || !slices.Contains(visited, test.want) {
				t.Fatalf("decoded visits = %q, want raw %q then decoded %q", visited, test.text, test.want)
			}
		})
	}

	var visited []string
	unsafeEncoding := false
	compactionSourceVisitDecodedReferences(`17%2558abcd`, func(text string) {
		visited = append(visited, text)
	}, &unsafeEncoding)
	if unsafeEncoding || slices.Contains(visited, `17:abcd`) || !slices.Contains(visited, `17%58abcd`) {
		t.Fatalf("nested encoding was interpreted more than once: unsafe=%t visits=%q", unsafeEncoding, visited)
	}
}

func TestCompactionReferenceDecoderMarksMalformedEncodingsUnsafe(t *testing.T) {
	for _, text := range []string{
		`"17\u03Zabcd"`,
		`"17\u{110000}abcd"`,
		`"17\x3Zabcd"`,
	} {
		t.Run(text, func(t *testing.T) {
			unsafeEncoding := false
			compactionSourceVisitDecodedReferences(text, func(string) {}, &unsafeEncoding)
			if !unsafeEncoding {
				t.Fatal("malformed or ambiguous encoding was not marked unsafe")
			}
		})
	}
}

func TestCompactionReferenceDecoderLeavesIncompleteUntypedEncodingsRaw(t *testing.T) {
	for _, input := range []string{
		`17%3`,
		`17%3Zabcd`,
		`17%u003Aabcd`,
		`17&#58abcd`,
		`17&#x3Aabcd`,
		`17&colon`,
	} {
		t.Run(input, func(t *testing.T) {
			unsafeEncoding := false
			var visited []string
			compactionSourceVisitDecodedReferences(input, func(text string) {
				visited = append(visited, text)
			}, &unsafeEncoding)
			if unsafeEncoding || len(visited) != 1 || visited[0] != input {
				t.Fatalf("untyped incomplete encoding was interpreted: unsafe=%t visits=%q", unsafeEncoding, visited)
			}
		})
	}
}

func TestCompactionReferenceDecoderDoesNotGuessOrdinaryBackslashText(t *testing.T) {
	input := `The files remain under C:\users\xray, fmt uses %d, and work is 100% complete.`
	unsafeEncoding := false
	compactionSourceVisitDecodedReferences(input, func(string) {}, &unsafeEncoding)
	if unsafeEncoding {
		t.Fatal("ordinary backslash or percent text was treated as a malformed encoding")
	}
}

func TestCompactionReferenceDecoderAcceptsPercentEncodedBinaryBytes(t *testing.T) {
	input := "unrelated%FFbinary operation%5F00"
	unsafeEncoding := false
	var visited []string
	compactionSourceVisitDecodedReferences(input, func(text string) {
		visited = append(visited, text)
	}, &unsafeEncoding)
	if unsafeEncoding {
		t.Fatal("syntactically valid percent-encoded binary byte was marked globally unsafe")
	}
	want := "unrelated\xffbinary operation_00"
	if !slices.Contains(visited, want) {
		t.Fatalf("decoded visits = %q, want byte-preserving form %q", visited, want)
	}
}

func TestCompactionReferenceDecoderPreservesEncodedRowWithoutGlobalPin(t *testing.T) {
	firstRows := compactionSourceTestRows("", 18)
	secondRows := compactionSourceTestRows("other.go", 18)
	items := []json.RawMessage{
		compactTestCall("source-first", "hread first.go"),
		compactTestOutput("source-first", strings.Join(firstRows, ""), 0),
		compactTestCall("source-second", "hread second.go"),
		compactTestOutput("source-second", strings.Join(secondRows, ""), 0),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": `Keep exact row 3%3A0003 beside unrelated binary %FF and state%3Aready.`,
		}),
	}
	items = append(items, compactionSourceTestRecent()...)

	got := reduceContextCompactionSource(items, items)
	for index, rows := range map[int][]string{1: firstRows, 3: secondRows} {
		text := compactionSourceTestOutputText(t, got[index])
		if !strings.Contains(text, rows[2]) {
			t.Fatalf("encoded row reference was not retained in result %d", index)
		}
		if string(got[index]) == string(items[index]) || strings.Contains(text, rows[10]) {
			t.Fatalf("supported encoded text globally pinned result %d", index)
		}
	}
}

func TestCompactionReferenceDecoderPreservesEncodedScriptReference(t *testing.T) {
	items := retirementHistory()
	items[3] = mustMarshalJSON(map[string]any{
		"type": "function_call_output", "call_id": "operation_00",
		"output": string(mustMarshalJSON(map[string]any{
			"exit_code": 0, "script_ref": "@shell/result:17",
			"output": strings.Repeat("old source\n", 500),
		})),
	})
	items = append(items, mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant",
		"content": "Use the result named `@shell/result%3A17`.",
	}))

	got := retireCompactionOperations(items)
	for _, index := range []int{1, 2, 3} {
		if string(got[index]) != string(items[index]) {
			t.Fatalf("encoded script reference did not preserve producer item %d", index)
		}
	}
}

func TestCompactionReferenceDecoderPreservesEncodedMetadataReference(t *testing.T) {
	item := compactionMetadataTestItem("message", "metadata_encoded", "turn:keep")
	reference := mustMarshalJSON(map[string]any{
		"type": "message", "id": "metadata_reference", "role": "assistant",
		"content": `const turn = "turn&#x3a;keep"; const state = "ready%3Atrue";`,
	})
	got := reduceContextCompactionMetadata(compactionMetadataTestHistory(item, reference))
	var fields, metadata map[string]json.RawMessage
	if json.Unmarshal(got[0], &fields) != nil || json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata) != nil {
		t.Fatal("metadata fixture was not retained as JSON")
	}
	if _, exists := metadata["turn_id"]; !exists {
		t.Fatal("HTML-encoded turn reference was removed")
	}
	if _, exists := metadata["create_time"]; exists {
		t.Fatal("unrelated supported encoded text globally pinned metadata")
	}
}

func TestCompactionReferenceDecoderPreservesEncodedAssistantID(t *testing.T) {
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": "assistant_keep",
			"content": "The completed result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": "assistant_drop",
			"content": "Another completed result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "user",
			"content": "Continue from `assistant\\u{5f}keep`; status%3Aready.",
		}),
	}
	for index := range 12 {
		id := fmt.Sprintf("reference_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}

	got := reduceContextCompactionNarration(items)
	if !strings.Contains(string(got[0]), `"id":"assistant_keep"`) {
		t.Fatal("escaped assistant ID reference was removed")
	}
	if strings.Contains(string(got[1]), `"id":"assistant_drop"`) {
		t.Fatal("unrelated supported encoded text globally pinned assistant IDs")
	}

	items[2] = mustMarshalJSON(map[string]any{
		"type": "message", "role": "user", "content": `malformed "assistant\u{110000}"`,
	})
	got = reduceContextCompactionNarration(items)
	if !strings.Contains(string(got[1]), `"id":"assistant_drop"`) {
		t.Fatal("malformed encoding did not conservatively preserve assistant IDs")
	}
}

func TestCompactionReferenceDecoderBinaryPercentDoesNotPinAssistantIDs(t *testing.T) {
	const keepID = "123e4567-e89b-12d3-a456-426614174000"
	const dropID = "123e4567-e89b-12d3-a456-426614174001"
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": keepID,
			"content": "The referenced result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": dropID,
			"content": "The unrelated result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "user",
			"content": "Use 123e4567%2De89b%2D12d3%2Da456%2D426614174000; binary=%FF.",
		}),
	}
	for index := range 12 {
		id := fmt.Sprintf("binary_reference_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}

	got := reduceContextCompactionNarration(items)
	if !strings.Contains(string(got[0]), `"id":"`+keepID+`"`) {
		t.Fatal("encoded UUID reference beside a binary percent byte was removed")
	}
	if strings.Contains(string(got[1]), `"id":"`+dropID+`"`) {
		t.Fatal("unrelated binary percent byte globally pinned assistant UUIDs")
	}
}

func TestCompactionReferenceDecoderPrintfTextDoesNotPinAssistantIDs(t *testing.T) {
	const keepID = "assistant_literal_keep"
	const dropID = "assistant_printf_drop"
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": keepID,
			"content": "The referenced result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant", "id": dropID,
			"content": "The unrelated result is recorded.",
		}),
		mustMarshalJSON(map[string]any{
			"type": "message", "role": "user",
			"content": `Keep assistant_literal_keep; fmt.Sprintf("%d %2d %3.2f %% %3Z", n).`,
		}),
	}
	for index := range 12 {
		id := fmt.Sprintf("printf_reference_%02d", index)
		items = append(items, compactTestCall(id, `printf "%d %2d %3.2f %%\n" 1 2 3`), compactTestOutput(id, "%d %2d %3.2f %%\n", 0))
	}

	got := reduceContextCompactionNarration(items)
	if !strings.Contains(string(got[0]), `"id":"`+keepID+`"`) {
		t.Fatal("raw literal assistant reference beside printf formats was removed")
	}
	if strings.Contains(string(got[1]), `"id":"`+dropID+`"`) {
		t.Fatal("ordinary printf formats globally pinned assistant IDs")
	}
}

func TestCompactionReferenceDecoderDoesNotEvaluateTemplateInterpolation(t *testing.T) {
	input := "const row = `17\\u{3a}abcd${tools.exec_command({cmd: 'false'})}`;"
	unsafeEncoding := false
	var decoded string
	compactionSourceVisitDecodedReferences(input, func(text string) {
		if text != input {
			decoded = text
		}
	}, &unsafeEncoding)
	if unsafeEncoding || !strings.Contains(decoded, "17:abcd") ||
		!strings.Contains(decoded, "${tools.exec_command({cmd: 'false'})}") {
		t.Fatalf("static decoding changed template interpolation: unsafe=%t decoded=%q", unsafeEncoding, decoded)
	}
}
