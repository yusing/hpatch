package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

const (
	ctpReferenceTag      = "!ctp1 R\n"
	ctpLiteralTag        = "!ctp1 L\n"
	ctpMinimumSegmentLen = 24
)

type ctpCodec struct {
	tokens tokenizer.Codec
}

type ctpAdmissionDecision uint8

const (
	ctpAdmissionDisabled ctpAdmissionDecision = iota
	ctpAdmissionMissingCarrier
	ctpAdmissionNoDefinitions
	ctpAdmissionUnprofitable
	ctpAdmissionAdmitted
)

type ctpInstructionCarrier uint8

const (
	ctpCarrierNone ctpInstructionCarrier = iota
	ctpCarrierTopLevel
	ctpCarrierDeveloperMessage
)

type ctpRequestView struct {
	carrier      ctpInstructionCarrier
	instructions string
	input        any
	hasInput     bool
	tools        any
	hasTools     bool
}

type ctpDefinition struct {
	id    string
	value string
}

type ctpResponseTransform struct {
	definitions            map[string]string
	originalInstructions   json.RawMessage
	compactInstructions    string
	hasCompactInstructions bool
	tokens                 tokenizer.Codec
	inputNativeTokens      uint64
	inputCompactTokens     uint64
	recordOutput           func(nativeTokens, compactTokens uint64)
}

func newCTPCodec() (*ctpCodec, error) {
	tokens, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return nil, fmt.Errorf("load GPT-5 tokenizer for CTP/1: %w", err)
	}
	return &ctpCodec{tokens: tokens}, nil
}

func (c *ctpCodec) prepareRequest(request *parsedResponsesRequest) (*ctpResponseTransform, ctpAdmissionDecision, error) {
	if c == nil {
		return nil, ctpAdmissionDisabled, nil
	}
	nativeBody, err := json.Marshal(request.fields)
	if err != nil {
		return nil, ctpAdmissionDisabled, fmt.Errorf("encode native CTP comparison request: %w", err)
	}
	nativeTokens, err := c.count(nativeBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}
	view, err := decodeCTPRequestView(request.fields)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}
	if view.carrier == ctpCarrierNone {
		return nil, ctpAdmissionMissingCarrier, nil
	}

	definitions, err := c.requestDefinitions(&view)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}

	if len(definitions) == 0 {
		return nil, ctpAdmissionNoDefinitions, nil
	}
	compactFields, err := transformCTPRequest(request.fields, &view, definitions)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}
	compactBody, err := json.Marshal(compactFields)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}
	compactTokens, err := c.count(compactBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, err
	}
	if compactTokens >= nativeTokens {
		return nil, ctpAdmissionUnprofitable, nil
	}

	originalInstructions := bytes.Clone(request.fields["instructions"])
	var compactInstructions string
	hasCompactInstructions := json.Unmarshal(compactFields["instructions"], &compactInstructions) == nil
	request.fields = compactFields
	definitionValues := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		definitionValues[definition.id] = definition.value
	}
	return &ctpResponseTransform{
		definitions:            definitionValues,
		originalInstructions:   originalInstructions,
		compactInstructions:    compactInstructions,
		hasCompactInstructions: hasCompactInstructions,
		tokens:                 c.tokens,
		inputNativeTokens:      uint64(nativeTokens),
		inputCompactTokens:     uint64(compactTokens),
	}, ctpAdmissionAdmitted, nil
}

func (c *ctpCodec) count(value []byte) (int, error) {
	count, err := c.tokens.Count(string(value))
	if err != nil {
		return 0, fmt.Errorf("estimate CTP/1 request tokens: %w", err)
	}
	if count < 0 {
		return 0, errors.New("estimate CTP/1 request tokens: negative count")
	}
	return count, nil
}

type ctpSegmentCandidate struct {
	value  string
	saving int
}

func (c *ctpCodec) requestDefinitions(view *ctpRequestView) ([]ctpDefinition, error) {
	values := collectCTPRequestStrings(view)
	type segmentCount struct {
		occurrences int
		strings     int
	}
	counts := make(map[string]segmentCount)
	for _, value := range values {
		seen := make(map[string]struct{})
		for _, segment := range splitCTPPhysicalSegments(value) {
			if len(segment) < ctpMinimumSegmentLen {
				continue
			}
			count := counts[segment]
			count.occurrences++
			if _, ok := seen[segment]; !ok {
				count.strings++
				seen[segment] = struct{}{}
			}
			counts[segment] = count
		}
	}

	candidates := make([]ctpSegmentCandidate, 0, len(counts))
	refTokens, tagTokens := -1, -1
	for value, count := range counts {
		if count.occurrences < 2 {
			continue
		}
		plainTokens, err := c.tokens.Count(value)
		if err != nil {
			return nil, fmt.Errorf("estimate CTP/1 definition source: %w", err)
		}
		quoted, _ := json.Marshal(value)
		encodedTokens, err := c.tokens.Count("D|0|" + string(quoted) + "\n")
		if err != nil {
			return nil, fmt.Errorf("estimate CTP/1 definition row: %w", err)
		}
		if refTokens < 0 {
			refTokens, err = c.tokens.Count("@0;")
			if err != nil {
				return nil, fmt.Errorf("estimate CTP/1 reference: %w", err)
			}
			tagTokens, err = c.tokens.Count(ctpReferenceTag)
			if err != nil {
				return nil, fmt.Errorf("estimate CTP/1 reference tag: %w", err)
			}
		}
		saving := plainTokens*count.occurrences - encodedTokens - refTokens*count.occurrences - tagTokens*count.strings
		if saving > 0 {
			candidates = append(candidates, ctpSegmentCandidate{value: value, saving: saving})
		}
	}
	slices.SortFunc(candidates, func(left, right ctpSegmentCandidate) int {
		if left.saving != right.saving {
			return right.saving - left.saving
		}
		if len(left.value) != len(right.value) {
			return len(right.value) - len(left.value)
		}
		return strings.Compare(left.value, right.value)
	})
	definitions := make([]ctpDefinition, len(candidates))
	for index, candidate := range candidates {
		definitions[index] = ctpDefinition{id: base36(index), value: candidate.value}
	}
	return definitions, nil
}

func splitCTPPhysicalSegments(value string) []string {
	segments := strings.SplitAfter(value, "\n")
	if len(segments) != 0 && segments[len(segments)-1] == "" {
		segments = segments[:len(segments)-1]
	}
	return segments
}

func base36(value int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if value == 0 {
		return "0"
	}
	var encoded [16]byte
	index := len(encoded)
	for value > 0 {
		index--
		encoded[index] = digits[value%len(digits)]
		value /= len(digits)
	}
	return string(encoded[index:])
}

func collectCTPRequestStrings(view *ctpRequestView) []string {
	var values []string
	collect := func(value string) string {
		values = append(values, value)
		return value
	}
	if view.hasInput {
		value := view.input
		if text, ok := value.(string); ok {
			collect(text)
		} else {
			var preserveDeveloper func(string) string
			if view.carrier == ctpCarrierDeveloperMessage {
				preserveDeveloper = func(value string) string { return value }
			}
			transformCTPInput(value, collect, preserveDeveloper)
		}
	}
	if view.hasTools {
		transformCTPTools(view.tools, collect)
	}
	return values
}

func transformCTPRequest(
	fields map[string]json.RawMessage,
	view *ctpRequestView,
	definitions []ctpDefinition,
) (map[string]json.RawMessage, error) {
	transformed := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		transformed[name] = bytes.Clone(value)
	}
	definitionByValue := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		definitionByValue[definition.value] = definition.id
	}
	dictionary := renderCTPDictionary(definitions)
	encodeString := func(value string) string {
		return encodeCTPString(value, definitionByValue)
	}
	if view.carrier == ctpCarrierTopLevel {
		transformed["instructions"] = mustMarshalJSON(appendCTPDictionary(view.instructions, dictionary))
	}
	if view.hasInput {
		if text, ok := view.input.(string); ok {
			view.input = encodeString(text)
		} else {
			var appendDeveloper func(string) string
			if view.carrier == ctpCarrierDeveloperMessage {
				appendDeveloper = func(value string) string {
					return appendCTPDictionary(value, dictionary)
				}
			}
			if found := transformCTPInput(view.input, encodeString, appendDeveloper); appendDeveloper != nil && !found {
				return nil, errors.New("locate CTP/1 developer instruction carrier")
			}
		}
		encoded, err := json.Marshal(view.input)
		if err != nil {
			return nil, fmt.Errorf("encode CTP/1 input: %w", err)
		}
		transformed["input"] = encoded
	}
	if view.hasTools {
		transformCTPTools(view.tools, encodeString)
		encoded, err := json.Marshal(view.tools)
		if err != nil {
			return nil, fmt.Errorf("encode CTP/1 tools: %w", err)
		}
		transformed["tools"] = encoded
	}
	return transformed, nil
}

func decodeCTPRequestView(fields map[string]json.RawMessage) (ctpRequestView, error) {
	var view ctpRequestView
	var instructions string
	if json.Unmarshal(fields["instructions"], &instructions) == nil && strings.TrimSpace(instructions) != "" {
		view.carrier = ctpCarrierTopLevel
		view.instructions = instructions
	}
	// Codex can carry model_instructions_file as its first textual developer message instead of
	// serializing top-level instructions. Keep that message native and append only dictionary data.
	if raw, ok := fields["input"]; ok {
		input, err := decodeCTPJSON(raw)
		if err != nil {
			return ctpRequestView{}, fmt.Errorf("decode CTP/1 input: %w", err)
		}
		view.input = input
		view.hasInput = true
		if view.carrier == ctpCarrierNone && transformCTPInput(input, nil, func(value string) string { return value }) {
			view.carrier = ctpCarrierDeveloperMessage
		}
	}
	if view.carrier == ctpCarrierNone {
		return view, nil
	}
	if raw, ok := fields["tools"]; ok {
		tools, err := decodeCTPJSON(raw)
		if err != nil {
			return ctpRequestView{}, fmt.Errorf("decode CTP/1 tools: %w", err)
		}
		view.tools = tools
		view.hasTools = true
	}
	return view, nil
}

func appendCTPDictionary(value, dictionary string) string {
	if value != "" && !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	return value + dictionary
}

func decodeCTPJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func transformCTPInput(value any, transformString, transformFirstDeveloper func(string) string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	developerTransformed := false
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := object["type"].(string)
		switch typeName {
		case "additional_tools":
			transformCTPTools(object["tools"], transformString)
		case "message":
			role, _ := object["role"].(string)
			if !developerTransformed && role == "developer" &&
				transformCTPLastTextContent(object, transformFirstDeveloper) {
				developerTransformed = true
				continue
			}
			transformCTPContent(object, "content", transformString)
		case "custom_tool_call":
			transformCTPStringField(object, "input", transformString)
		case "custom_tool_call_output", "function_call_output":
			transformCTPContent(object, "output", transformString)
		case "function_call":
			transformCTPStringField(object, "arguments", transformString)
		}
	}
	return developerTransformed
}

func transformCTPLastTextContent(object map[string]any, transform func(string) string) bool {
	if transform == nil {
		return false
	}
	value, ok := object["content"]
	if !ok {
		return false
	}
	if text, ok := value.(string); ok {
		object["content"] = transform(text)
		return true
	}
	parts, ok := value.([]any)
	if !ok {
		return false
	}
	for index := len(parts) - 1; index >= 0; index-- {
		content, ok := parts[index].(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := content["type"].(string)
		text, ok := content["text"].(string)
		if ok && (typeName == "input_text" || typeName == "text") {
			content["text"] = transform(text)
			return true
		}
	}
	return false
}

func transformCTPContent(object map[string]any, field string, transformString func(string) string) {
	if transformString == nil {
		return
	}
	value, ok := object[field]
	if !ok {
		return
	}
	if text, ok := value.(string); ok {
		object[field] = transformString(text)
		return
	}
	parts, ok := value.([]any)
	if !ok {
		return
	}
	for _, part := range parts {
		content, ok := part.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := content["type"].(string)
		if typeName == "input_text" || typeName == "output_text" || typeName == "text" {
			transformCTPStringField(content, "text", transformString)
		}
	}
}

func transformCTPTools(value any, transformString func(string) string) {
	items, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		transformCTPStringField(object, "description", transformString)
		if nested, ok := object["tools"]; ok {
			transformCTPTools(nested, transformString)
		}
	}
}

func transformCTPStringField(object map[string]any, field string, transform func(string) string) {
	if transform == nil {
		return
	}
	value, ok := object[field].(string)
	if ok {
		object[field] = transform(value)
	}
}

func encodeCTPString(value string, definitions map[string]string) string {
	if len(definitions) == 0 {
		return value
	}
	var body strings.Builder
	referenced := false
	for _, segment := range splitCTPPhysicalSegments(value) {
		if id, ok := definitions[segment]; ok {
			body.WriteByte('@')
			body.WriteString(id)
			body.WriteByte(';')
			referenced = true
			continue
		}
		body.WriteString(strings.ReplaceAll(segment, "@", "@@"))
	}
	if referenced {
		return ctpReferenceTag + body.String()
	}
	if strings.HasPrefix(value, ctpReferenceTag) || strings.HasPrefix(value, ctpLiteralTag) {
		return ctpLiteralTag + value
	}
	return value
}

func renderCTPDictionary(definitions []ctpDefinition) string {
	var dictionary strings.Builder
	dictionary.WriteString("CTP/1\nT|D|id|value\n")
	for _, definition := range definitions {
		encoded, _ := json.Marshal(definition.value)
		fmt.Fprintf(&dictionary, "D|%s|%s\n", definition.id, encoded)
	}
	dictionary.WriteString("END\n")
	return dictionary.String()
}

func (t *ctpResponseTransform) TransformJSON(payload []byte) ([]byte, error) {
	return t.transformJSON(payload, true)
}

func (t *ctpResponseTransform) transformJSON(payload []byte, observeOutput bool) ([]byte, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(payload, &response); err != nil || response == nil {
		return nil, errors.New("decode CTP/1 response")
	}
	if raw, ok := response["output"]; ok {
		var output []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &output); err != nil {
			return nil, errors.New("decode CTP/1 response output")
		}
		for _, item := range output {
			if err := t.transformOutputItem(item, observeOutput); err != nil {
				return nil, err
			}
		}
		response["output"] = mustMarshalJSON(output)
	}
	t.restoreInstructions(response)
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(encoded) > upstreamJSONBufferBytes {
		return nil, errors.New("decoded CTP/1 response exceeds the router buffer budget")
	}
	return encoded, nil
}

func (t *ctpResponseTransform) TransformSSE(payload []byte) ([][]byte, error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(payload, &event); err != nil || event == nil {
		return [][]byte{payload}, nil
	}
	typeName := jsonString(event, "type")
	switch typeName {
	case "response.output_item.added", "response.output_item.done":
		var item map[string]json.RawMessage
		if json.Unmarshal(event["item"], &item) == nil && item != nil {
			if err := t.transformOutputItem(item, false); err != nil {
				return nil, err
			}
			event["item"] = mustMarshalJSON(item)
		}
	case "response.output_text.done":
		compact := jsonString(event, "text")
		decoded, err := t.decodeString(compact)
		if err != nil {
			return nil, err
		}
		event["text"] = mustMarshalJSON(decoded)
	case "response.content_part.added", "response.content_part.done":
		var part map[string]json.RawMessage
		if json.Unmarshal(event["part"], &part) == nil && part != nil {
			if err := t.transformTextPart(part); err != nil {
				return nil, err
			}
			event["part"] = mustMarshalJSON(part)
		}
	}
	if rawResponse, ok := event["response"]; ok {
		trimmed := bytes.TrimSpace(rawResponse)
		if len(trimmed) != 0 && trimmed[0] == '{' {
			decoded, err := t.transformJSON(rawResponse, typeName == "response.completed")
			if err != nil {
				return nil, err
			}
			event["response"] = decoded
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(encoded) > upstreamJSONBufferBytes {
		return nil, errors.New("decoded CTP/1 stream event exceeds the router buffer budget")
	}
	return [][]byte{encoded}, nil
}

func (t *ctpResponseTransform) Finish(bool) error {
	return nil
}

func (t *ctpResponseTransform) transformOutputItem(item map[string]json.RawMessage, observeOutput bool) error {
	typeName := jsonString(item, "type")
	switch typeName {
	case "message":
		if jsonString(item, "role") != "assistant" {
			return nil
		}
		if raw, ok := item["content"]; ok {
			decoded, err := t.transformMessageContent(raw, observeOutput)
			if err != nil {
				return err
			}
			item["content"] = decoded
		}
	}
	return nil
}

func (t *ctpResponseTransform) transformMessageContent(raw json.RawMessage, observeOutput bool) (json.RawMessage, error) {
	value, err := decodeCTPJSON(raw)
	if err != nil {
		return nil, errors.New("decode CTP/1 message content")
	}
	if text, ok := value.(string); ok {
		decoded, err := t.decodeString(text)
		if err != nil {
			return nil, err
		}
		if observeOutput {
			t.observeAssistantText(text, decoded)
		}
		return mustMarshalJSON(decoded), nil
	}
	parts, ok := value.([]any)
	if !ok {
		return raw, nil
	}
	for _, value := range parts {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := part["type"].(string)
		if typeName != "output_text" && typeName != "text" {
			continue
		}
		text, ok := part["text"].(string)
		if !ok {
			continue
		}
		decoded, err := t.decodeString(text)
		if err != nil {
			return nil, err
		}
		part["text"] = decoded
		if observeOutput {
			t.observeAssistantText(text, decoded)
		}
	}
	return mustMarshalJSON(parts), nil
}

func (t *ctpResponseTransform) transformTextPart(part map[string]json.RawMessage) error {
	typeName := jsonString(part, "type")
	if typeName != "output_text" && typeName != "text" {
		return nil
	}
	compact := jsonString(part, "text")
	decoded, err := t.decodeString(compact)
	if err != nil {
		return err
	}
	part["text"] = mustMarshalJSON(decoded)
	return nil
}

func (t *ctpResponseTransform) observeAssistantText(compact, native string) {
	if t.recordOutput == nil || t.tokens == nil {
		return
	}
	compactTokens, compactErr := t.tokens.Count(compact)
	nativeTokens, nativeErr := t.tokens.Count(native)
	if compactErr != nil || nativeErr != nil || compactTokens < 0 || nativeTokens < 0 {
		return
	}
	t.recordOutput(uint64(nativeTokens), uint64(compactTokens))
}

func (t *ctpResponseTransform) restoreInstructions(response map[string]json.RawMessage) {
	if !t.hasCompactInstructions || len(t.originalInstructions) == 0 {
		return
	}
	var echoed string
	if json.Unmarshal(response["instructions"], &echoed) == nil && echoed == t.compactInstructions {
		response["instructions"] = bytes.Clone(t.originalInstructions)
	}
}

func (t *ctpResponseTransform) decodeString(value string) (string, error) {
	return decodeCTPString(value, t.definitions, upstreamJSONBufferBytes)
}

func decodeCTPString(value string, definitions map[string]string, limit int) (string, error) {
	if strings.HasPrefix(value, ctpLiteralTag) {
		literal := value[len(ctpLiteralTag):]
		if len(literal) > limit {
			return "", errors.New("decoded CTP/1 string exceeds the router buffer budget")
		}
		return literal, nil
	}
	if !strings.HasPrefix(value, ctpReferenceTag) {
		if len(value) > limit {
			return "", errors.New("decoded CTP/1 string exceeds the router buffer budget")
		}
		return value, nil
	}
	encoded := value[len(ctpReferenceTag):]
	var decoded strings.Builder
	for len(encoded) != 0 {
		at := strings.IndexByte(encoded, '@')
		if at < 0 {
			if err := appendCTPDecoded(&decoded, encoded, limit); err != nil {
				return "", err
			}
			break
		}
		if err := appendCTPDecoded(&decoded, encoded[:at], limit); err != nil {
			return "", err
		}
		encoded = encoded[at+1:]
		if strings.HasPrefix(encoded, "@") {
			if err := appendCTPDecoded(&decoded, "@", limit); err != nil {
				return "", err
			}
			encoded = encoded[1:]
			continue
		}
		end := strings.IndexByte(encoded, ';')
		if end <= 0 {
			return "", errors.New("decode CTP/1 string: malformed reference")
		}
		id := encoded[:end]
		if !validCTPReferenceID(id) {
			return "", errors.New("decode CTP/1 string: malformed reference identifier")
		}
		definition, ok := definitions[id]
		if !ok {
			return "", fmt.Errorf("decode CTP/1 string: unknown reference %q", id)
		}
		if err := appendCTPDecoded(&decoded, definition, limit); err != nil {
			return "", err
		}
		encoded = encoded[end+1:]
	}
	return decoded.String(), nil
}

func appendCTPDecoded(output *strings.Builder, value string, limit int) error {
	if limit < output.Len() || len(value) > limit-output.Len() {
		return errors.New("decoded CTP/1 string exceeds the router buffer budget")
	}
	output.WriteString(value)
	return nil
}

func validCTPReferenceID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'z' {
				return false
			}
		}
	}
	return true
}
