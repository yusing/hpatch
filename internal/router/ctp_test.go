package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

func newTestCTPCodec(t *testing.T) *ctpCodec {
	t.Helper()
	codec, err := newCTPCodec()
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func ctpRequestTokenCount(t *testing.T, codec *ctpCodec, fields map[string]json.RawMessage) int {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	count, err := codec.count(body)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCTPRepeatedSubstringGoldenRequest(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "preserve this exact repeated substring because it is useful inside ordinary message text"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model":        "gpt-5.6-sol",
		"instructions": "keep the original instruction priority\n",
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": "developer prefix: " + strings.Repeat(repeated+" / ", 8)},
			map[string]any{"type": "message", "role": "user", "content": "user prefix: " + strings.Repeat(repeated+" | ", 8)},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nativeFields := cloneRawFields(request.fields)
	nativeTokens := ctpRequestTokenCount(t, codec, nativeFields)
	transform, decision, metrics, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	if decision != ctpAdmissionAdmitted || transform == nil || len(transform.requestDefinitions) == 0 {
		t.Fatal("profitable repeated-substring request was not encoded")
	}
	compactTokens := ctpRequestTokenCount(t, codec, request.fields)
	if compactTokens >= nativeTokens {
		t.Fatalf("compact tokens = %d, native = %d", compactTokens, nativeTokens)
	}
	if metrics.Representation.NativeTokens != uint64(nativeTokens) ||
		metrics.Representation.CompactTokens != uint64(compactTokens) ||
		metrics.Representation.NativeBytes == 0 || metrics.Representation.CompactBytes == 0 ||
		metrics.Definitions != uint64(len(transform.requestDefinitions)) || metrics.DictionaryBytes == 0 {
		t.Fatalf("request metrics = %#v", metrics)
	}

	var instructions string
	if err := json.Unmarshal(request.fields["instructions"], &instructions); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(instructions, "keep the original instruction priority\n!ctp1 D\n") ||
		!strings.Contains(instructions, "0=") {
		t.Fatalf("compact instructions = %q", instructions)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(input[0], "content"); !strings.HasPrefix(got, ctpReferenceTag) || !strings.Contains(got, "@{0}") {
		t.Fatalf("compact message = %q", got)
	}
}

func TestCTPRequestDictionaryIgnoresAppendedModelHistory(t *testing.T) {
	codec := newTestCTPCodec(t)
	stable := "stable readable repeated diagnostic phrase with exact bytes and clear boundaries\n"
	added := "new history-only phrase whose repetitions must not change the request dictionary\n"
	baseInput := []any{
		map[string]any{"type": "message", "role": "user", "content": strings.Repeat(stable, 16)},
	}
	prepare := func(input []any) (string, json.RawMessage) {
		t.Helper()
		request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
			"model":        "gpt-5.6-sol",
			"instructions": "persistent CTP guidance",
			"input":        input,
			"tools": []any{map[string]any{
				"type": "function", "name": "probe", "description": strings.Repeat(stable, 16),
			}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		transform, decision, _, err := codec.prepareRequest(&request)
		if err != nil {
			t.Fatal(err)
		}
		if transform == nil || decision != ctpAdmissionAdmitted {
			t.Fatalf("CTP transform = %#v, decision = %v", transform, decision)
		}
		return jsonString(request.fields, "instructions"), request.fields["input"]
	}

	firstInstructions, firstInput := prepare(baseInput)
	extendedInput := append(slices.Clone(baseInput),
		map[string]any{"type": "message", "role": "assistant", "content": strings.Repeat(added, 32)},
		map[string]any{
			"type": "function_call_output", "call_id": "call-1",
			"output": strings.Repeat(added, 32) + strings.Repeat(stable, 16),
		},
	)
	secondInstructions, secondInput := prepare(extendedInput)
	if firstInstructions != secondInstructions {
		t.Fatal("appended model history changed the request dictionary")
	}

	var firstItems, secondItems []json.RawMessage
	if err := json.Unmarshal(firstInput, &firstItems); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondInput, &secondItems); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstItems[0], secondItems[0]) {
		t.Fatal("appended model history changed the compact request prefix")
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(secondItems[2], &output); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(output, "output"); !strings.HasPrefix(got, ctpReferenceTag) || !strings.Contains(got, "@{") {
		t.Fatalf("appended history did not reuse stable dictionary = %q", got)
	}
}

func TestCTPAdmissionIgnoresNewlyProfitableAppendedHistory(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "this marginal exact substring is just long enough\n"
	baseInput := []any{
		map[string]any{"type": "message", "role": "developer", "content": strings.Repeat(repeated, 3)},
		map[string]any{"type": "message", "role": "user", "content": strings.Repeat(repeated, 3)},
		map[string]any{"type": "message", "role": "user", "content": ctpReferenceTag + "literal @{0}"},
	}
	prepare := func(input []any) ctpAdmissionDecision {
		t.Helper()
		request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
			"model": "gpt-5.6-sol", "instructions": "persistent model guidance", "input": input,
		}))
		if err != nil {
			t.Fatal(err)
		}
		nativeFields := cloneRawFields(request.fields)
		transform, decision, _, err := codec.prepareRequest(&request)
		if err != nil {
			t.Fatal(err)
		}
		if transform != nil || !rawFieldsEqual(request.fields, nativeFields) {
			t.Fatal("unprofitable request changed fields")
		}
		return decision
	}
	if decision := prepare(baseInput); decision != ctpAdmissionUnprofitable {
		t.Fatalf("base admission = %v, want unprofitable", decision)
	}
	extendedInput := append(slices.Clone(baseInput), map[string]any{
		"type": "message", "role": "assistant", "content": strings.Repeat(repeated, 64),
	})
	if decision := prepare(extendedInput); decision != ctpAdmissionUnprofitable {
		t.Fatalf("extended admission = %v, want stable unprofitable", decision)
	}
}

func TestCTPAdmissionIgnoresNewlyUnprofitableAppendedHistory(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "stable exact request phrase that remains profitable and readable\n"
	baseInput := []any{
		map[string]any{"type": "message", "role": "developer", "content": strings.Repeat(repeated, 12)},
		map[string]any{"type": "message", "role": "user", "content": strings.Repeat(repeated, 12)},
	}
	prepare := func(input []any) (string, int, int) {
		t.Helper()
		request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
			"model": "gpt-5.6-sol", "instructions": "persistent model guidance", "input": input,
		}))
		if err != nil {
			t.Fatal(err)
		}
		nativeTokens := ctpRequestTokenCount(t, codec, request.fields)
		transform, decision, _, err := codec.prepareRequest(&request)
		if err != nil {
			t.Fatal(err)
		}
		if transform == nil || decision != ctpAdmissionAdmitted {
			t.Fatalf("CTP transform = %#v, admission = %v", transform, decision)
		}
		return jsonString(request.fields, "instructions"), nativeTokens, ctpRequestTokenCount(t, codec, request.fields)
	}

	baseInstructions, _, _ := prepare(baseInput)
	extendedInput := slices.Clone(baseInput)
	for index := range 400 {
		extendedInput = append(extendedInput, map[string]any{
			"type": "message", "role": "assistant", "content": ctpReferenceTag + "literal-" + base36(index),
		})
	}
	extendedInstructions, nativeTokens, compactTokens := prepare(extendedInput)
	if extendedInstructions != baseInstructions {
		t.Fatal("newly unprofitable history changed the admitted dictionary")
	}
	if compactTokens < nativeTokens {
		t.Fatalf("extended compact tokens = %d, native = %d; test did not exercise whole-request loss", compactTokens, nativeTokens)
	}
}

func TestCTPStableProjectionRetainsLateDeveloperCarrier(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "stable tool description text repeated for cache-safe admission\n"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "message", "role": "assistant", "content": strings.Repeat("excluded history\n", 32)},
			map[string]any{"type": "message", "role": "developer", "content": "persistent model guidance"},
		},
		"tools": []any{map[string]any{
			"type": "function", "name": "probe", "description": strings.Repeat(repeated, 16),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	transform, decision, _, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	if transform == nil || decision != ctpAdmissionAdmitted {
		t.Fatalf("late developer carrier transform = %#v, admission = %v", transform, decision)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	if carrier := jsonString(input[1], "content"); !strings.Contains(carrier, ctpDictionaryTag) {
		t.Fatalf("late developer carrier = %q", carrier)
	}
}

func TestCTPDeveloperMessageInstructionCarrier(t *testing.T) {
	codec := newTestCTPCodec(t)
	const instructions = "persistent CTP interpretation remains native"
	repeated := "preserve this repeated request line exactly because it is profitable to reference\n"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{
				"type": "additional_tools", "role": "developer",
				"tools": []any{map[string]any{"type": "custom", "name": "exec", "description": repeated}},
			},
			map[string]any{
				"type": "message", "role": "developer",
				"content": []any{map[string]any{"type": "input_text", "text": instructions}},
			},
			map[string]any{"type": "message", "role": "user", "content": strings.Repeat(repeated, 12)},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nativeTokens := ctpRequestTokenCount(t, codec, request.fields)
	transform, decision, _, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	if decision != ctpAdmissionAdmitted || transform == nil {
		t.Fatalf("developer-carried CTP transform = %#v, decision = %v", transform, decision)
	}
	if _, ok := request.fields["instructions"]; ok {
		t.Fatal("developer-carried CTP added top-level instructions")
	}
	compactTokens := ctpRequestTokenCount(t, codec, request.fields)
	if compactTokens >= nativeTokens {
		t.Fatalf("compact tokens = %d, native = %d", compactTokens, nativeTokens)
	}
	var input []map[string]any
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	content := input[1]["content"].([]any)
	carrier := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(carrier, instructions+"\n!ctp1 D\n") ||
		strings.HasPrefix(carrier, ctpReferenceTag) {
		t.Fatalf("developer instruction carrier = %q", carrier)
	}
	if got := input[2]["content"].(string); !strings.HasPrefix(got, ctpReferenceTag) || !strings.Contains(got, "@{0}") {
		t.Fatalf("compact user message = %q", got)
	}
}

func TestCTPEmptyTopLevelUsesDeveloperMessageCarrier(t *testing.T) {
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"instructions": "",
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": "persistent CTP interpretation"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	view, err := decodeCTPRequestView(request.fields)
	if err != nil {
		t.Fatal(err)
	}
	if view.carrier != ctpCarrierDeveloperMessage {
		t.Fatalf("instruction carrier = %v, want developer message", view.carrier)
	}
}

func TestCTPDoesNotAliasTools(t *testing.T) {
	codec := newTestCTPCodec(t)
	const nativeName = "repository_semantic_cross_reference_search"
	items := []any{}
	for index := range 8 {
		items = append(items, map[string]any{
			"type": "function_call", "name": nativeName, "call_id": "call-" + base36(index), "arguments": "{}",
		})
	}
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-5.6-sol",
		"input": items,
		"tools": []any{map[string]any{
			"type": "function", "name": nativeName, "description": "Search known repository symbols.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		"tool_choice": map[string]any{"type": "function", "name": nativeName},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nativeFields := cloneRawFields(request.fields)
	transform, decision, _, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	if transform != nil {
		t.Fatalf("tool-name-only CTP transform = %#v", transform)
	}
	if decision != ctpAdmissionMissingCarrier {
		t.Fatalf("tool-name-only CTP decision = %v", decision)
	}
	if !rawFieldsEqual(request.fields, nativeFields) {
		t.Fatal("tool names changed without an admitted definition encoding")
	}
}

func TestCTPNativeFallbackGoldenRequest(t *testing.T) {
	codec := newTestCTPCodec(t)
	request, err := parseResponsesRequest([]byte(`{"model":"gpt-5.6-sol","instructions":"persistent model guidance","input":"short unique prompt"}`))
	if err != nil {
		t.Fatal(err)
	}
	nativeFields := cloneRawFields(request.fields)
	nativeTokens := ctpRequestTokenCount(t, codec, nativeFields)
	transform, decision, _, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	compactTokens := ctpRequestTokenCount(t, codec, request.fields)
	if nativeTokens != 23 || compactTokens != 23 {
		t.Fatalf("golden token counts native=%d compact=%d, want 23/23", nativeTokens, compactTokens)
	}
	if transform != nil {
		t.Fatalf("unprofitable request transform = %#v", transform)
	}
	if decision != ctpAdmissionNoDefinitions {
		t.Fatalf("fallback decision = %v", decision)
	}
	if !rawFieldsEqual(request.fields, nativeFields) {
		t.Fatalf("fallback fields changed: %#v", request.fields)
	}
}

func TestCTPReservedPrefixesStayNativeWithoutDefinitions(t *testing.T) {
	codec := newTestCTPCodec(t)
	for name, value := range map[string]string{
		"reference":  ctpReferenceTag + "literal @{0}",
		"literal":    ctpLiteralTag + "literal body",
		"dictionary": ctpDictionaryTag + "literal body",
	} {
		t.Run(name, func(t *testing.T) {
			request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
				"instructions": "persistent model guidance",
				"input":        value,
			}))
			if err != nil {
				t.Fatal(err)
			}
			nativeFields := cloneRawFields(request.fields)
			transform, decision, _, err := codec.prepareRequest(&request)
			if err != nil {
				t.Fatal(err)
			}
			if transform != nil || decision != ctpAdmissionNoDefinitions {
				t.Fatalf("inactive CTP transform = %#v, decision = %v", transform, decision)
			}
			if !rawFieldsEqual(request.fields, nativeFields) {
				t.Fatalf("inactive CTP changed reserved literal: %#v", request.fields)
			}
		})
	}
}

func TestCTPUnprofitableDictionaryFallback(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "this marginal exact substring is just long enough\n"
	for name, reserved := range map[string]string{
		"reference":  ctpReferenceTag + "literal @{0}",
		"literal":    ctpLiteralTag + "literal body",
		"dictionary": ctpDictionaryTag + "literal body",
	} {
		t.Run(name, func(t *testing.T) {
			request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
				"model":        "gpt-5.6-sol",
				"instructions": "persistent model guidance",
				"input": []any{
					map[string]any{"type": "message", "role": "developer", "content": strings.Repeat(repeated, 3)},
					map[string]any{"type": "message", "role": "user", "content": strings.Repeat(repeated, 3)},
					map[string]any{"type": "message", "role": "user", "content": reserved},
				},
			}))
			if err != nil {
				t.Fatal(err)
			}
			nativeFields := cloneRawFields(request.fields)
			transform, decision, _, err := codec.prepareRequest(&request)
			if err != nil {
				t.Fatal(err)
			}
			if transform != nil || decision != ctpAdmissionUnprofitable {
				t.Fatalf("marginal CTP transform = %#v, decision = %v", transform, decision)
			}
			if !rawFieldsEqual(request.fields, nativeFields) {
				t.Fatalf("unprofitable dictionary changed request: %#v", request.fields)
			}
		})
	}
}

func TestCTPStringRoundTripAndMalformedOutput(t *testing.T) {
	definitions := map[string]string{
		"0": "exact\r\nbytes without a final newline",
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "literal", input: "ordinary @{0} is literal without a tag", want: "ordinary @{0} is literal without a tag"},
		{name: "reference and ordinary at", input: ctpReferenceTag + "mail alice@example.com @{0}", want: "mail alice@example.com exact\r\nbytes without a final newline"},
		{name: "escaped reference lookalike", input: ctpReferenceTag + "literal @@{0}", want: "literal @{0}"},
		{name: "reserved reference literal", input: ctpLiteralTag + ctpReferenceTag + "@{bad}", want: ctpReferenceTag + "@{bad}"},
		{name: "reserved literal literal", input: ctpLiteralTag + ctpLiteralTag + "body", want: ctpLiteralTag + "body"},
		{name: "lookalike", input: "!ctp1 X\n@{BAD}", want: "!ctp1 X\n@{BAD}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeCTPString(
				test.input, cloneCTPDefinitions(definitions), upstreamJSONBufferBytes,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("decoded = %q, want %q", got, test.want)
			}
		})
	}
	for _, malformed := range []string{
		ctpReferenceTag + "@{missing}",
		ctpDictionaryTag + "0=\"replacement\"\nEND\n" + ctpReferenceTag + "@{0}",
		ctpDictionaryTag + "new=not-json\nEND\n" + ctpReferenceTag + "@{new}",
	} {
		if _, err := decodeCTPString(
			malformed, cloneCTPDefinitions(definitions), upstreamJSONBufferBytes,
		); err == nil {
			t.Fatalf("malformed value decoded: %q", malformed)
		}
	}
}

func TestCTPInputEscapesOnlyReferenceLookalikes(t *testing.T) {
	const repeated = "exact repeated substring"
	definitions := []ctpDefinition{{id: "0", value: repeated}}
	value := "alice@example.com literal @{0}; " + repeated + " and " + repeated
	encoded, references := newCTPStringEncoder(definitions).encode(value)
	if references["0"] != 2 || !strings.Contains(encoded, "alice@example.com") ||
		!strings.Contains(encoded, "@@{0}") || !strings.Contains(encoded, "@{0}") {
		t.Fatalf("encoded input = %q, references = %#v", encoded, references)
	}
	decoded, err := decodeCTPString(encoded, map[string]string{"0": repeated}, upstreamJSONBufferBytes)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != value {
		t.Fatalf("decoded input = %q, want %q", decoded, value)
	}
}

func TestCTPAssistantExtendsRequestDictionary(t *testing.T) {
	transform := &ctpResponseTransform{
		requestDefinitions: map[string]string{"0": "inherited request text"},
	}
	extension := ctpDictionaryTag + "1=\"novel repeated output @{0}\"\n" + ctpDictionaryEnd +
		ctpReferenceTag + "@{0} then @{1}"
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": extension},
				map[string]any{"type": "output_text", "text": ctpReferenceTag + "again @{1}"},
			},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var visible struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response, &visible); err != nil {
		t.Fatal(err)
	}
	if got := visible.Output[0].Content[0].Text; got != "inherited request text then novel repeated output @{0}" {
		t.Fatalf("extended assistant text = %q", got)
	}
	if got := visible.Output[0].Content[1].Text; got != "again novel repeated output @{0}" {
		t.Fatalf("later assistant reference = %q", got)
	}
	if _, exists := transform.requestDefinitions["1"]; exists {
		t.Fatal("response-local definition escaped into request scope")
	}

	_, err = transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": ctpReferenceTag + "@{1}"},
				map[string]any{"type": "output_text", "text": extension},
			},
		}},
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown reference") {
		t.Fatalf("forward reference error = %v", err)
	}
}

func TestCTPResponseDictionaryMetrics(t *testing.T) {
	dictionary := ctpDictionaryTag + "1=\"first\"\n2=\"second\"\n" + ctpDictionaryEnd
	definitions, dictionaryBytes := ctpResponseDictionaryMetrics(dictionary + ctpReferenceTag + "@{1}")
	if definitions != 2 || dictionaryBytes != uint64(len(dictionary)) {
		t.Fatalf("dictionary metrics = %d definitions, %d bytes", definitions, dictionaryBytes)
	}
	if definitions, dictionaryBytes := ctpResponseDictionaryMetrics(ctpReferenceTag + "@{0}"); definitions != 0 || dictionaryBytes != 0 {
		t.Fatalf("reference-only dictionary metrics = %d definitions, %d bytes", definitions, dictionaryBytes)
	}
}

func TestCTPDecodeFailureMetrics(t *testing.T) {
	store := newMetricsStore("")
	transform := &ctpResponseTransform{
		requestDefinitions: map[string]string{"0": "known"},
		recordDecode: func(duration time.Duration, failed bool) {
			store.recordCTPDecode("session", duration, failed)
		},
	}
	_, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"output": []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": ctpReferenceTag + "@{missing}"}},
		}},
	}))
	if err == nil {
		t.Fatal("unknown response reference was accepted")
	}
	metrics := store.snapshot().CTP.Codec
	if metrics.DecodeOperations != 1 || metrics.DecodeFailures != 1 || metrics.DecodeNanoseconds == 0 {
		t.Fatalf("decode metrics = %#v", metrics)
	}
}

func TestCTPRestoresEchoedInstructionsAndAssistantText(t *testing.T) {
	codec := newTestCTPCodec(t)
	store := newMetricsStore("")
	compactText := ctpReferenceTag + "@{0} @ done"
	transform := &ctpResponseTransform{
		requestDefinitions:     map[string]string{"0": "exact reused text"},
		originalInstructions:   json.RawMessage(`"original instructions"`),
		compactInstructions:    "CTP/1 prelude and compact instructions",
		hasCompactInstructions: true,
		tokens:                 codec.tokens,
		recordOutput: func(representation ctpRepresentationMetrics, definitions, dictionaryBytes uint64) {
			store.recordCTPOutput("", 0, representation, definitions, dictionaryBytes)
		},
	}
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status":       "completed",
		"instructions": transform.compactInstructions,
		"output": []any{map[string]any{
			"type": "message", "id": "message-1", "role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": compactText},
				map[string]any{"type": "output_image", "image_url": "unchanged"},
			},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var visible map[string]json.RawMessage
	if err := json.Unmarshal(response, &visible); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(visible, "instructions"); got != "original instructions" {
		t.Fatalf("restored instructions = %q", got)
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(visible["output"], &output); err != nil {
		t.Fatal(err)
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(output[0]["content"], &content); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(content[0], "text"); got != "exact reused text @ done" {
		t.Fatalf("restored assistant text = %q", got)
	}
	if got := jsonString(content[1], "image_url"); got != "unchanged" {
		t.Fatalf("non-text content = %q", got)
	}
	compactTokens, compactErr := codec.tokens.Count(compactText)
	nativeTokens, nativeErr := codec.tokens.Count("exact reused text @ done")
	ctp := store.snapshot().CTP
	if compactErr != nil || nativeErr != nil || ctp.AssistantTexts != 1 ||
		ctp.Output != (ctpCompressionTokens{NativeTokens: uint64(nativeTokens), CompactTokens: uint64(compactTokens)}) {
		t.Fatalf("JSON CTP output metrics = %#v, count errors %v/%v", ctp, compactErr, nativeErr)
	}

	created, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"status": "in_progress", "instructions": transform.compactInstructions, "output": []any{},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || !bytes.Contains(created[0], []byte(`"instructions":"original instructions"`)) {
		t.Fatalf("restored lifecycle response = %s", created)
	}
}

func TestCTPStructuredToolOutputTextEncoding(t *testing.T) {
	codec := newTestCTPCodec(t)
	repeated := "repeated structured tool output text that is long enough for a profitable exact definition\n"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"instructions": "preserve instruction priority",
		"input": []any{map[string]any{
			"type": "function_call_output", "call_id": "call-1",
			"output": []any{
				map[string]any{"type": "input_text", "text": strings.Repeat(repeated, 8)},
				map[string]any{"type": "input_text", "text": strings.Repeat(repeated, 8)},
				map[string]any{"type": "input_image", "image_url": "unchanged"},
			},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	transform, decision, _, err := codec.prepareRequest(&request)
	if err != nil {
		t.Fatal(err)
	}
	if decision != ctpAdmissionAdmitted || transform == nil || len(transform.requestDefinitions) == 0 {
		t.Fatal("profitable structured output was not encoded")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(items[0]["output"], &output); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(output[0], "text"); !strings.HasPrefix(got, ctpReferenceTag) {
		t.Fatalf("structured text = %q", got)
	}
	if got := jsonString(output[2], "image_url"); got != "unchanged" {
		t.Fatalf("structured image = %q", got)
	}

	fallback, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"instructions": "preserve instruction priority",
		"input": []any{map[string]any{
			"type": "function_call_output", "call_id": "call-2",
			"output": []any{
				map[string]any{"type": "input_text", "text": "unique short output"},
				map[string]any{"type": "input_image", "image_url": "unchanged"},
			},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	original := cloneRawFields(fallback.fields)
	if transform, decision, _, err := codec.prepareRequest(&fallback); err != nil || transform != nil || decision != ctpAdmissionNoDefinitions {
		t.Fatalf("structured fallback transform = %#v, decision = %v, error = %v", transform, decision, err)
	}
	if !rawFieldsEqual(fallback.fields, original) {
		t.Fatal("structured fallback changed native fields")
	}
}

func TestCTPDecodedStringBudget(t *testing.T) {
	definitions := map[string]string{"0": "12345678"}
	got, err := decodeCTPString(ctpReferenceTag+"@{0}", definitions, 8)
	if err != nil || got != "12345678" {
		t.Fatalf("at-budget decode = %q, %v", got, err)
	}
	for _, value := range []string{
		ctpReferenceTag + "@{0}x",
		ctpLiteralTag + "123456789",
		"123456789",
	} {
		if _, err := decodeCTPString(value, definitions, 8); err == nil || !strings.Contains(err.Error(), "buffer budget") {
			t.Fatalf("over-budget decode error for %q = %v", value, err)
		}
	}
}

func TestCTPOutputDecodingIsLimitedToAssistantText(t *testing.T) {
	transform := &ctpResponseTransform{
		requestDefinitions: map[string]string{"0": "new created.txt\n"},
	}
	compact := ctpReferenceTag + "@{0}type \"payload@value\"\n"
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{
				"type": "custom_tool_call", "id": "item-1", "call_id": "call-1",
				"name": "hpatch", "input": compact,
			},
			map[string]any{
				"type": "function_call", "id": "item-2", "call_id": "call-2",
				"name": "shell", "arguments": compact,
			},
			map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": compact}},
			},
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "output_text", "text": compact}},
			},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var visible struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &visible); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(visible.Output[0], "input"); got != compact {
		t.Fatalf("custom-tool input was decoded = %q", got)
	}
	if got := jsonString(visible.Output[1], "arguments"); got != compact {
		t.Fatalf("function arguments were decoded = %q", got)
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(visible.Output[2]["content"], &content); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(content[0], "text"); got != "new created.txt\ntype \"payload@value\"\n" {
		t.Fatalf("assistant text = %q", got)
	}
	if err := json.Unmarshal(visible.Output[3]["content"], &content); err != nil {
		t.Fatal(err)
	}
	if got := jsonString(content[0], "text"); got != compact {
		t.Fatalf("non-assistant message text was decoded = %q", got)
	}

	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"type": "custom_tool_call", "id": "item-1", "call_id": "call-1", "name": "hpatch", "input": compact,
		},
	})
	streamed, err := transform.TransformSSE(added)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || !bytes.Contains(streamed[0], mustTestJSON(t, compact)) {
		t.Fatalf("custom-tool item was decoded = %s", streamed)
	}
	done := mustTestJSON(t, map[string]any{
		"type": "response.custom_tool_call_input.done", "item_id": "item-1",
		"input": compact,
	})
	streamed, err = transform.TransformSSE(done)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || !bytes.Contains(streamed[0], mustTestJSON(t, compact)) {
		t.Fatalf("terminal custom-tool input was decoded = %s", streamed)
	}
	argumentsDone := mustTestJSON(t, map[string]any{
		"type": "response.function_call_arguments.done", "item_id": "item-2",
		"arguments": compact,
	})
	streamed, err = transform.TransformSSE(argumentsDone)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || !bytes.Contains(streamed[0], mustTestJSON(t, compact)) {
		t.Fatalf("terminal function arguments were decoded = %s", streamed)
	}
	textDone := mustTestJSON(t, map[string]any{
		"type": "response.output_text.done", "item_id": "message-1", "content_index": 0,
		"text": ctpReferenceTag + "@{0}@assistant",
	})
	streamed, err = transform.TransformSSE(textDone)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || !bytes.Contains(streamed[0], []byte(`new created.txt\n@assistant`)) {
		t.Fatalf("restored text event = %s", streamed)
	}
}

func TestCTPStreamingCompressionMetricsObserveTerminalText(t *testing.T) {
	codec := newTestCTPCodec(t)
	store := newMetricsStore("")
	transform := &ctpResponseTransform{
		requestDefinitions: map[string]string{"0": "exact reused assistant text"},
		tokens:             codec.tokens,
		recordOutput: func(representation ctpRepresentationMetrics, definitions, dictionaryBytes uint64) {
			store.recordCTPOutput("", 0, representation, definitions, dictionaryBytes)
		},
	}
	compact := ctpReferenceTag + "@{0}"
	native := "exact reused assistant text"
	message := map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": compact}},
	}
	events := [][]byte{
		mustTestJSON(t, map[string]any{
			"type": "response.output_text.done", "item_id": "message-1", "content_index": 0, "text": compact,
		}),
		mustTestJSON(t, map[string]any{
			"type": "response.content_part.done", "item_id": "message-1", "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": compact},
		}),
		mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": message}),
		mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "custom_tool_call", "id": "tool-1", "name": "hpatch", "input": compact,
			},
		}),
		mustTestJSON(t, map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"status": "completed", "output": []any{}},
		}),
	}
	if _, err := transform.TransformSSE(events[0]); err != nil {
		t.Fatal(err)
	}
	compactTokens, err := codec.tokens.Count(compact)
	if err != nil {
		t.Fatal(err)
	}
	nativeTokens, err := codec.tokens.Count(native)
	if err != nil {
		t.Fatal(err)
	}
	want := ctpCompressionMetrics{
		AssistantTexts: 1,
		Output: ctpCompressionTokens{
			NativeTokens: uint64(nativeTokens), CompactTokens: uint64(compactTokens),
		},
		OutputBytes: ctpCompressionBytes{
			NativeBytes: uint64(len(native)), CompactBytes: uint64(len(compact)),
		},
	}
	if got := store.snapshot().CTP; got != want {
		t.Fatalf("terminal-text CTP metrics = %#v, want %#v", got, want)
	}
	for _, event := range events[1:] {
		if _, err := transform.TransformSSE(event); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.snapshot().CTP; got != want {
		t.Fatalf("duplicated streaming projections changed CTP metrics = %#v, want %#v", got, want)
	}
}

func TestCTPStreamingAssistantDictionaryExtension(t *testing.T) {
	transform := &ctpResponseTransform{
		requestDefinitions: map[string]string{"0": "inherited"},
	}
	extension := ctpDictionaryTag + "1=\"response-local\"\n" + ctpDictionaryEnd +
		ctpReferenceTag + "@{0} @{1}"
	item := map[string]any{"type": "message", "id": "message-1", "role": "assistant", "content": []any{}}
	if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.output_item.added",
		"item": item,
	})); err != nil {
		t.Fatal(err)
	}
	first, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type":    "response.output_text.done",
		"item_id": "message-1", "content_index": 0,
		"text": extension,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || !bytes.Contains(first[0], []byte(`"text":"inherited response-local"`)) {
		t.Fatalf("first terminal text = %s", first)
	}
	partDone, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type":    "response.content_part.done",
		"item_id": "message-1", "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": extension},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(partDone) != 1 || !bytes.Contains(partDone[0], []byte(`"text":"inherited response-local"`)) {
		t.Fatalf("completed first content part = %s", partDone)
	}
	second, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type":    "response.output_text.done",
		"item_id": "message-1", "content_index": 1,
		"text": ctpReferenceTag + "@{1}",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || !bytes.Contains(second[0], []byte(`"text":"response-local"`)) {
		t.Fatalf("later terminal text = %s", second)
	}

	message := map[string]any{
		"type": "message", "id": "message-1", "role": "assistant",
		"content": []any{
			map[string]any{"type": "output_text", "text": extension},
			map[string]any{"type": "output_text", "text": ctpReferenceTag + "@{1}"},
		},
	}
	itemDone, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": message,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(itemDone) != 1 || !bytes.Contains(itemDone[0], []byte("inherited response-local")) {
		t.Fatalf("completed output item = %s", itemDone)
	}
	completed, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
			"output": []any{message},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || bytes.Contains(completed[0], []byte("already exists")) ||
		!bytes.Contains(completed[0], []byte("inherited response-local")) {
		t.Fatalf("completed response = %s", completed)
	}
}

func TestCTPResponseTransformerCompositionOrder(t *testing.T) {
	var calls []string
	first := &recordingResponseTransformer{name: "ctp", calls: &calls, suffix: "-native"}
	second := &recordingResponseTransformer{name: "hpatch", calls: &calls, suffix: "-carrier"}
	chain := composeResponseTransformers(first, second)
	jsonPayload, err := chain.TransformJSON([]byte("response"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(jsonPayload); got != "response-native-carrier" {
		t.Fatalf("JSON composition = %q", got)
	}
	ssePayloads, err := chain.TransformSSE([]byte("event"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ssePayloads) != 1 || string(ssePayloads[0]) != "event-native-carrier" {
		t.Fatalf("SSE composition = %q", ssePayloads)
	}
	if err := chain.Finish(true); err != nil {
		t.Fatal(err)
	}
	want := []string{"ctp:json", "hpatch:json", "ctp:sse", "hpatch:sse", "ctp:finish", "hpatch:finish"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("composition calls = %q, want %q", calls, want)
	}
}

func TestCTPExecuteRequestKeepsHPatchCallsNative(t *testing.T) {
	workspace := t.TempDir()
	codec := newTestCTPCodec(t)
	provider := serverProviderFunc(func(_ context.Context, _ context.Context, body []byte, _ http.Header, _ string) (*http.Response, error) {
		var request map[string]json.RawMessage
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if instructions := jsonString(request, "instructions"); !strings.Contains(instructions, "\n!ctp1 D\n") {
			t.Fatalf("request did not admit CTP input encoding: %s", body)
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(request["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		toolFound := false
		for _, tool := range tools {
			if jsonString(tool, "description") == testHPatchToolDescription {
				toolFound = true
				if name := jsonString(tool, "name"); name != hpatchToolName {
					t.Fatalf("forwarded hpatch tool name = %q", name)
				}
			}
		}
		if !toolFound {
			t.Fatalf("forwarded hpatch tool missing: %s", body)
		}
		var input []map[string]json.RawMessage
		if err := json.Unmarshal(request["input"], &input); err != nil {
			t.Fatal(err)
		}
		var additionalTools []map[string]json.RawMessage
		if err := json.Unmarshal(input[0]["tools"], &additionalTools); err != nil {
			t.Fatal(err)
		}
		if jsonString(additionalTools[0], "name") != "exec" || jsonString(additionalTools[1], "name") != "wait" {
			t.Fatalf("Code Mode nested tools were aliased: %#v", additionalTools)
		}
		return serverHTTPResponse(string(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": []any{map[string]any{
				"type": "custom_tool_call", "id": "item-H", "call_id": "call-H",
				"name": hpatchToolName, "input": testHPatchScript,
			}},
		}))), nil
	})
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	proxy.modelInstructions = codexinstructions.Instructions()
	metrics := newMetricsStore("")
	var output bytes.Buffer
	repeated := "repeat this request context line enough times to admit CTP input encoding\n"
	err := executeRequest(
		t.Context(), t.Context(), serverRequest(t, func(request map[string]any) {
			request["instructions"] = stockModelInstructionsForTest("", "")
			request["input"] = append(request["input"].([]any),
				map[string]any{"type": "message", "role": "developer", "content": strings.Repeat(repeated, 8)},
				map[string]any{"type": "message", "role": "user", "content": strings.Repeat(repeated, 8)},
			)
		}),
		serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}),
		"ctp-composition", provider, &output, newDiagnostics(io.Discard), time.Now,
		proxy, codec, metrics,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"name":"exec"`)) ||
		!bytes.Contains(output.Bytes(), []byte("*** Begin Patch")) {
		t.Fatalf("visible translated response = %s", output.Bytes())
	}
	ctp := metrics.snapshot().CTP
	if ctp.ConsideredRequests != 1 || ctp.EncodedRequests != 1 || ctp.Input.NativeTokens <= ctp.Input.CompactTokens {
		t.Fatalf("CTP input metrics = %#v", ctp)
	}
	if ctp.AssistantTexts != 0 || ctp.Output != (ctpCompressionTokens{}) {
		t.Fatalf("tool-only response contributed CTP output metrics = %#v", ctp)
	}
}

type recordingResponseTransformer struct {
	name   string
	calls  *[]string
	suffix string
}

func (t *recordingResponseTransformer) TransformJSON(payload []byte) ([]byte, error) {
	*t.calls = append(*t.calls, t.name+":json")
	return append(payload, t.suffix...), nil
}

func (t *recordingResponseTransformer) TransformSSE(payload []byte) ([][]byte, error) {
	*t.calls = append(*t.calls, t.name+":sse")
	return [][]byte{append(payload, t.suffix...)}, nil
}

func (t *recordingResponseTransformer) Finish(bool) error {
	*t.calls = append(*t.calls, t.name+":finish")
	return nil
}

func cloneRawFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		cloned[name] = bytes.Clone(value)
	}
	return cloned
}

func rawFieldsEqual(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if !bytes.Equal(value, right[name]) {
			return false
		}
	}
	return true
}
