package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
)

const (
	ctpReferenceTag  = "!ctp1 R\n"
	ctpLiteralTag    = "!ctp1 L\n"
	ctpDictionaryTag = "!ctp1 D\n"
	ctpDictionaryEnd = "END\n"
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
	requestDefinitions     map[string]string
	streamDefinitions      map[string]string
	streamItemDefinitions  map[string]map[string]string
	originalInstructions   json.RawMessage
	compactInstructions    string
	hasCompactInstructions bool
	tokens                 tokenizer.Codec
	recordOutput           func(ctpRepresentationMetrics, uint64, uint64)
	recordDecode           func(time.Duration, bool)
}

type ctpRequestMetrics struct {
	Representation  ctpRepresentationMetrics
	Definitions     uint64
	DictionaryBytes uint64
}

func newCTPCodec() (*ctpCodec, error) {
	tokens, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return nil, fmt.Errorf("load GPT-5 tokenizer for CTP/1: %w", err)
	}
	return &ctpCodec{tokens: tokens}, nil
}

func (c *ctpCodec) prepareRequest(request *parsedResponsesRequest) (*ctpResponseTransform, ctpAdmissionDecision, ctpRequestMetrics, error) {
	if c == nil {
		return nil, ctpAdmissionDisabled, ctpRequestMetrics{}, nil
	}
	nativeBody, err := json.Marshal(request.fields)
	if err != nil {
		return nil, ctpAdmissionDisabled, ctpRequestMetrics{}, fmt.Errorf("encode native CTP comparison request: %w", err)
	}
	nativeTokens, err := c.count(nativeBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, ctpRequestMetrics{
			Representation: ctpRepresentationMetrics{
				NativeBytes: uint64(len(nativeBody)),
			},
		}, nil
	}
	requestMetrics := ctpRequestMetrics{Representation: ctpRepresentationMetrics{
		NativeTokens: uint64(nativeTokens),
		NativeBytes:  uint64(len(nativeBody)),
	}}
	view, err := decodeCTPRequestView(request.fields)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, err
	}
	if view.carrier == ctpCarrierNone {
		return nil, ctpAdmissionMissingCarrier, requestMetrics, nil
	}

	definitions, err := c.requestDefinitions(&view)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, err
	}

	if len(definitions) == 0 {
		return nil, ctpAdmissionNoDefinitions, requestMetrics, nil
	}

	// Freeze admission on the same immutable projection that owns dictionary discovery. Whole
	// appended history is still encoded and measured below, but it cannot toggle CTP for an
	// otherwise unchanged prompt prefix.
	admissionFields := make(map[string]json.RawMessage, 3)
	if instructions, ok := request.fields["instructions"]; ok {
		admissionFields["instructions"] = bytes.Clone(instructions)
	}
	if view.hasInput {
		input, err := json.Marshal(ctpStableRequestInput(&view))
		if err != nil {
			return nil, ctpAdmissionDisabled, requestMetrics, fmt.Errorf("encode CTP/1 admission input: %w", err)
		}
		admissionFields["input"] = input
	}
	if tools, ok := request.fields["tools"]; ok {
		admissionFields["tools"] = bytes.Clone(tools)
	}
	admissionNativeBody, err := json.Marshal(admissionFields)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, fmt.Errorf("encode native CTP/1 admission projection: %w", err)
	}
	admissionNativeTokens, err := c.count(admissionNativeBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, nil
	}
	admissionCompactFields, definitions, err := transformCTPRequest(admissionFields, definitions)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, err
	}
	admissionCompactBody, err := json.Marshal(admissionCompactFields)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, fmt.Errorf("encode compact CTP/1 admission projection: %w", err)
	}
	admissionCompactTokens, err := c.count(admissionCompactBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, nil
	}

	compactFields, appliedDefinitions, err := transformCTPRequest(request.fields, definitions)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, err
	}
	if !slices.Equal(appliedDefinitions, definitions) {
		return nil, ctpAdmissionDisabled, requestMetrics, errors.New("CTP/1 definitions changed outside the stable admission projection")
	}
	compactBody, err := json.Marshal(compactFields)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, err
	}
	compactTokens, err := c.count(compactBody)
	if err != nil {
		return nil, ctpAdmissionDisabled, requestMetrics, nil
	}
	requestMetrics.Representation.CompactTokens = uint64(compactTokens)
	requestMetrics.Representation.CompactBytes = uint64(len(compactBody))
	requestMetrics.Definitions = uint64(len(definitions))
	if len(definitions) > 0 {
		requestMetrics.DictionaryBytes = uint64(len(renderCTPDictionary(definitions)))
	}
	if admissionCompactTokens >= admissionNativeTokens {
		return nil, ctpAdmissionUnprofitable, requestMetrics, nil
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
		requestDefinitions:     definitionValues,
		originalInstructions:   originalInstructions,
		compactInstructions:    compactInstructions,
		hasCompactInstructions: hasCompactInstructions,
		tokens:                 c.tokens,
	}, ctpAdmissionAdmitted, requestMetrics, nil
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

type ctpTokenizedString struct {
	value   string
	tokens  []uint
	offsets []int
}

type ctpSeedKey struct {
	hash   uint64
	length int
}

type ctpSeedOccurrence struct {
	stringIndex int
	start       int
}

type ctpSubstringOccurrence struct {
	stringIndex int
	start       int
	end         int
}

type ctpSubstringCandidate struct {
	value       string
	tokens      int
	boundary    int
	occurrences map[ctpSubstringOccurrence]struct{}
	saving      int
}

func (c *ctpCodec) requestDefinitions(view *ctpRequestView) ([]ctpDefinition, error) {
	values, err := c.tokenizeRequestStrings(collectCTPRequestStrings(view))
	if err != nil {
		return nil, err
	}
	refTokens, err := c.tokens.Count("@{0}")
	if err != nil {
		return nil, fmt.Errorf("estimate CTP/1 reference: %w", err)
	}
	seedLength := refTokens + 1
	seeds := make(map[ctpSeedKey]ctpSeedOccurrence)
	candidatesByValue := make(map[string]*ctpSubstringCandidate)
	for stringIndex, value := range values {
		for start := 0; start+seedLength <= len(value.tokens); {
			key := ctpTokenSeedKey(value.tokens[start : start+seedLength])
			first, found := seeds[key]
			if !found {
				seeds[key] = ctpSeedOccurrence{stringIndex: stringIndex, start: start}
				start++
				continue
			}
			firstValue := values[first.stringIndex]
			if !slices.Equal(
				firstValue.tokens[first.start:first.start+seedLength],
				value.tokens[start:start+seedLength],
			) {
				start++
				continue
			}
			firstStart, currentStart := first.start, start
			firstEnd, currentEnd := firstStart+seedLength, currentStart+seedLength
			for firstStart > 0 && currentStart > 0 &&
				firstValue.tokens[firstStart-1] == value.tokens[currentStart-1] &&
				(first.stringIndex != stringIndex || firstEnd <= currentStart-1) {
				firstStart--
				currentStart--
			}
			for firstEnd < len(firstValue.tokens) && currentEnd < len(value.tokens) &&
				firstValue.tokens[firstEnd] == value.tokens[currentEnd] &&
				(first.stringIndex != stringIndex || firstEnd < currentStart) {
				firstEnd++
				currentEnd++
			}
			firstStart, firstEnd, currentStart, currentEnd = trimCTPMatchToReadableBoundaries(
				firstValue, value, firstStart, firstEnd, currentStart, currentEnd,
			)
			if firstEnd-firstStart < seedLength {
				start++
				continue
			}
			candidateValue := firstValue.value[firstValue.offsets[firstStart]:firstValue.offsets[firstEnd]]
			candidate := candidatesByValue[candidateValue]
			if candidate == nil {
				candidate = &ctpSubstringCandidate{
					value:       candidateValue,
					tokens:      firstEnd - firstStart,
					boundary:    ctpBoundaryScore(firstValue.value, firstValue.offsets[firstStart], firstValue.offsets[firstEnd]),
					occurrences: make(map[ctpSubstringOccurrence]struct{}),
				}
				candidatesByValue[candidateValue] = candidate
			}
			candidate.occurrences[ctpSubstringOccurrence{
				stringIndex: first.stringIndex,
				start:       firstValue.offsets[firstStart],
				end:         firstValue.offsets[firstEnd],
			}] = struct{}{}
			candidate.occurrences[ctpSubstringOccurrence{
				stringIndex: stringIndex,
				start:       value.offsets[currentStart],
				end:         value.offsets[currentEnd],
			}] = struct{}{}
			start = max(start+1, currentEnd)
		}
	}

	tagTokens, err := c.tokens.Count(ctpReferenceTag)
	if err != nil {
		return nil, fmt.Errorf("estimate CTP/1 reference tag: %w", err)
	}
	candidates := make([]ctpSubstringCandidate, 0, len(candidatesByValue))
	for _, candidate := range candidatesByValue {
		occurrences, stringsCount := countCTPNonoverlappingOccurrences(candidate.occurrences)
		if occurrences < 2 {
			continue
		}
		quoted, _ := json.Marshal(candidate.value)
		definitionTokens, err := c.tokens.Count("0=" + string(quoted) + "\n")
		if err != nil {
			return nil, fmt.Errorf("estimate CTP/1 definition: %w", err)
		}
		// Each definition and reference is an extra lookup for the model. Charging one virtual
		// token per indirection keeps marginal byte savings from overwhelming readability.
		indirectionCost := 1 + occurrences
		candidate.saving = candidate.tokens*occurrences - definitionTokens -
			refTokens*occurrences - tagTokens*stringsCount - indirectionCost
		if candidate.saving > 0 {
			candidates = append(candidates, *candidate)
		}
	}
	slices.SortFunc(candidates, func(left, right ctpSubstringCandidate) int {
		if left.saving != right.saving {
			return right.saving - left.saving
		}
		if left.boundary != right.boundary {
			return right.boundary - left.boundary
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

func (c *ctpCodec) tokenizeRequestStrings(values []string) ([]ctpTokenizedString, error) {
	tokenized := make([]ctpTokenizedString, 0, len(values))
	for _, value := range values {
		tokens, pieces, err := c.tokens.Encode(value)
		if err != nil {
			return nil, fmt.Errorf("tokenize CTP/1 request string: %w", err)
		}
		if len(tokens) != len(pieces) || strings.Join(pieces, "") != value {
			return nil, errors.New("tokenize CTP/1 request string: non-lossless token pieces")
		}
		offsets := make([]int, len(pieces)+1)
		for index, piece := range pieces {
			offsets[index+1] = offsets[index] + len(piece)
		}
		tokenized = append(tokenized, ctpTokenizedString{value: value, tokens: tokens, offsets: offsets})
	}
	return tokenized, nil
}

func ctpTokenSeedKey(tokens []uint) ctpSeedKey {
	const multiplier = uint64(1099511628211)
	var hash uint64 = 1469598103934665603
	for _, token := range tokens {
		hash = (hash ^ (uint64(token) + 1)) * multiplier
	}
	return ctpSeedKey{hash: hash, length: len(tokens)}
}

func trimCTPMatchToReadableBoundaries(
	left, right ctpTokenizedString,
	leftStart, leftEnd, rightStart, rightEnd int,
) (int, int, int, int) {
	for leftEnd-leftStart > 0 &&
		(!ctpTextBoundary(left.value, left.offsets[leftStart]) || !ctpTextBoundary(right.value, right.offsets[rightStart])) {
		leftStart++
		rightStart++
	}
	for leftEnd-leftStart > 0 &&
		(!ctpTextBoundary(left.value, left.offsets[leftEnd]) || !ctpTextBoundary(right.value, right.offsets[rightEnd])) {
		leftEnd--
		rightEnd--
	}
	return leftStart, leftEnd, rightStart, rightEnd
}

func ctpTextBoundary(value string, offset int) bool {
	if offset == 0 || offset == len(value) {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(value[:offset])
	next, _ := utf8.DecodeRuneInString(value[offset:])
	return unicode.IsSpace(previous) || unicode.IsPunct(previous) ||
		unicode.IsSpace(next) || unicode.IsPunct(next)
}

func ctpBoundaryScore(value string, start, end int) int {
	score := 0
	if ctpTextBoundary(value, start) {
		score++
	}
	if ctpTextBoundary(value, end) {
		score++
	}
	return score
}

func countCTPNonoverlappingOccurrences(occurrences map[ctpSubstringOccurrence]struct{}) (int, int) {
	ordered := slices.Collect(func(yield func(ctpSubstringOccurrence) bool) {
		for occurrence := range occurrences {
			if !yield(occurrence) {
				return
			}
		}
	})
	slices.SortFunc(ordered, func(left, right ctpSubstringOccurrence) int {
		if left.stringIndex != right.stringIndex {
			return left.stringIndex - right.stringIndex
		}
		if left.start != right.start {
			return left.start - right.start
		}
		return left.end - right.end
	})
	count, stringsCount := 0, 0
	lastString, lastEnd := -1, -1
	for _, occurrence := range ordered {
		if occurrence.stringIndex != lastString {
			lastString, lastEnd = occurrence.stringIndex, -1
			stringsCount++
		}
		if occurrence.start < lastEnd {
			continue
		}
		count++
		lastEnd = occurrence.end
	}
	return count, stringsCount
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
		value := ctpStableRequestInput(view)
		if text, ok := value.(string); ok {
			collect(text)
		} else if items, ok := value.([]any); ok {
			var preserveDeveloper func(string) string
			if view.carrier == ctpCarrierDeveloperMessage {
				preserveDeveloper = func(value string) string { return value }
			}
			transformCTPInput(items, collect, preserveDeveloper)
		}
	}
	if view.hasTools {
		transformCTPTools(view.tools, collect)
	}
	return values
}

// ctpStableRequestInput owns the request prefix that may influence both definitions and admission.
// A developer carrier after the first model item is retained only as framing, without admitting the
// intervening model history into that stable projection.
func ctpStableRequestInput(view *ctpRequestView) any {
	items, ok := view.input.([]any)
	if !ok {
		return view.input
	}
	prefixEnd := len(items)
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			prefixEnd = index
			break
		}
		typeName, _ := object["type"].(string)
		if typeName == "additional_tools" {
			continue
		}
		role, _ := object["role"].(string)
		if (typeName == "message" || typeName == "") &&
			(role == "developer" || role == "system" || role == "user") {
			continue
		}
		if typeName == "custom_tool_call_output" || typeName == "function_call_output" {
			continue
		}
		prefixEnd = index
		break
	}
	prefix := items[:prefixEnd]
	if view.carrier != ctpCarrierDeveloperMessage ||
		transformCTPInput(prefix, nil, func(value string) string { return value }) {
		return prefix
	}
	for _, item := range items[prefixEnd:] {
		if transformCTPInput([]any{item}, nil, func(value string) string { return value }) {
			return append(slices.Clone(prefix), item)
		}
	}
	return prefix
}

func transformCTPRequest(
	fields map[string]json.RawMessage,
	definitions []ctpDefinition,
) (map[string]json.RawMessage, []ctpDefinition, error) {
	for {
		transformed, retained, err := transformCTPRequestOnce(fields, definitions)
		if err != nil || len(retained) == len(definitions) {
			return transformed, retained, err
		}
		definitions = retained
	}
}

func transformCTPRequestOnce(
	fields map[string]json.RawMessage,
	definitions []ctpDefinition,
) (map[string]json.RawMessage, []ctpDefinition, error) {
	transformed := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		transformed[name] = bytes.Clone(value)
	}
	view, err := decodeCTPRequestView(transformed)
	if err != nil {
		return nil, nil, err
	}
	encoder := newCTPStringEncoder(definitions)
	used := make(map[string]int, len(definitions))
	encodeString := func(value string) string {
		encoded, references := encoder.encode(value)
		for id, count := range references {
			used[id] += count
		}
		return encoded
	}
	if view.hasInput {
		if text, ok := view.input.(string); ok {
			view.input = encodeString(text)
		} else {
			var preserveDeveloper func(string) string
			if view.carrier == ctpCarrierDeveloperMessage {
				preserveDeveloper = func(value string) string { return value }
			}
			if found := transformCTPInput(view.input, encodeString, preserveDeveloper); preserveDeveloper != nil && !found {
				return nil, nil, errors.New("locate CTP/1 developer instruction carrier")
			}
		}
		encoded, err := json.Marshal(view.input)
		if err != nil {
			return nil, nil, fmt.Errorf("encode CTP/1 input: %w", err)
		}
		transformed["input"] = encoded
	}
	if view.hasTools {
		transformCTPTools(view.tools, encodeString)
		encoded, err := json.Marshal(view.tools)
		if err != nil {
			return nil, nil, fmt.Errorf("encode CTP/1 tools: %w", err)
		}
		transformed["tools"] = encoded
	}
	retained := make([]ctpDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if used[definition.id] >= 2 {
			retained = append(retained, definition)
		}
	}
	if len(retained) != len(definitions) {
		return transformed, retained, nil
	}
	if len(retained) == 0 {
		return transformed, nil, nil
	}
	dictionary := renderCTPDictionary(retained)
	if view.carrier == ctpCarrierTopLevel {
		transformed["instructions"] = mustMarshalJSON(appendCTPDictionary(view.instructions, dictionary))
	} else {
		appendDeveloper := func(value string) string {
			return appendCTPDictionary(value, dictionary)
		}
		if !transformCTPInput(view.input, nil, appendDeveloper) {
			return nil, nil, errors.New("locate CTP/1 developer instruction carrier")
		}
		encoded, err := json.Marshal(view.input)
		if err != nil {
			return nil, nil, fmt.Errorf("encode CTP/1 developer carrier: %w", err)
		}
		transformed["input"] = encoded
	}
	return transformed, retained, nil
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

type ctpStringEncoder struct {
	byFirstByte map[byte][]ctpDefinition
}

func newCTPStringEncoder(definitions []ctpDefinition) ctpStringEncoder {
	encoder := ctpStringEncoder{byFirstByte: make(map[byte][]ctpDefinition)}
	for _, definition := range definitions {
		if definition.value == "" {
			continue
		}
		first := definition.value[0]
		encoder.byFirstByte[first] = append(encoder.byFirstByte[first], definition)
	}
	for first, candidates := range encoder.byFirstByte {
		slices.SortFunc(candidates, func(left, right ctpDefinition) int {
			if len(left.value) != len(right.value) {
				return len(right.value) - len(left.value)
			}
			return strings.Compare(left.id, right.id)
		})
		encoder.byFirstByte[first] = candidates
	}
	return encoder
}

func (e ctpStringEncoder) encode(value string) (string, map[string]int) {
	references := make(map[string]int)
	if len(e.byFirstByte) == 0 {
		return encodeCTPLiteralString(value), references
	}
	var body strings.Builder
	for index := 0; index < len(value); {
		matched := false
		for _, definition := range e.byFirstByte[value[index]] {
			if strings.HasPrefix(value[index:], definition.value) {
				fmt.Fprintf(&body, "@{%s}", definition.id)
				references[definition.id]++
				index += len(definition.value)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if _, length, ok := parseCTPReference(value[index:]); ok {
			body.WriteByte('@')
			body.WriteString(value[index : index+length])
			index += length
			continue
		}
		body.WriteByte(value[index])
		index++
	}
	if len(references) != 0 {
		return ctpReferenceTag + body.String(), references
	}
	return encodeCTPLiteralString(value), references
}

func encodeCTPLiteralString(value string) string {
	if strings.HasPrefix(value, ctpReferenceTag) || strings.HasPrefix(value, ctpLiteralTag) ||
		strings.HasPrefix(value, ctpDictionaryTag) {
		return ctpLiteralTag + value
	}
	return value
}

func renderCTPDictionary(definitions []ctpDefinition) string {
	var dictionary strings.Builder
	dictionary.WriteString(ctpDictionaryTag)
	for _, definition := range definitions {
		encoded, _ := json.Marshal(definition.value)
		fmt.Fprintf(&dictionary, "%s=%s\n", definition.id, encoded)
	}
	dictionary.WriteString(ctpDictionaryEnd)
	return dictionary.String()
}

func (t *ctpResponseTransform) TransformJSON(payload []byte) (transformed []byte, err error) {
	started := time.Time{}
	if t.recordDecode != nil {
		started = time.Now()
		defer func() {
			t.recordDecode(time.Since(started), err != nil)
		}()
	}
	return t.transformJSON(payload, true, cloneCTPDefinitions(t.requestDefinitions))
}

func (t *ctpResponseTransform) transformJSON(
	payload []byte,
	observeOutput bool,
	definitions map[string]string,
) ([]byte, error) {
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
			if err := t.transformOutputItem(item, observeOutput, definitions); err != nil {
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

func (t *ctpResponseTransform) TransformSSE(payload []byte) (transformed [][]byte, err error) {
	started := time.Time{}
	if t.recordDecode != nil {
		started = time.Now()
		defer func() {
			t.recordDecode(time.Since(started), err != nil)
		}()
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(payload, &event); err != nil || event == nil {
		return [][]byte{payload}, nil
	}
	typeName := jsonString(event, "type")
	switch typeName {
	case "response.output_item.added", "response.output_item.done":
		var item map[string]json.RawMessage
		if json.Unmarshal(event["item"], &item) == nil && item != nil {
			definitions := cloneCTPDefinitions(t.streamingDefinitions())
			itemID := jsonString(item, "id")
			if typeName == "response.output_item.added" && itemID != "" {
				if t.streamItemDefinitions == nil {
					t.streamItemDefinitions = make(map[string]map[string]string)
				}
				t.streamItemDefinitions[itemID] = cloneCTPDefinitions(definitions)
			} else if itemBase, ok := t.streamItemDefinitions[itemID]; ok {
				definitions = cloneCTPDefinitions(itemBase)
				delete(t.streamItemDefinitions, itemID)
			}
			if err := t.transformOutputItem(item, false, definitions); err != nil {
				return nil, err
			}
			event["item"] = mustMarshalJSON(item)
		}
	case "response.output_text.done":
		compact := jsonString(event, "text")
		definitions := cloneCTPDefinitions(t.streamingDefinitions())
		decoded, err := decodeCTPString(compact, definitions, upstreamJSONBufferBytes)
		if err != nil {
			return nil, err
		}
		event["text"] = mustMarshalJSON(decoded)
		t.observeAssistantText(compact, decoded)
	case "response.content_part.added", "response.content_part.done":
		var part map[string]json.RawMessage
		if json.Unmarshal(event["part"], &part) == nil && part != nil {
			definitions := t.streamingDefinitions()
			if typeName == "response.content_part.added" {
				definitions = cloneCTPDefinitions(definitions)
			}
			if err := t.transformTextPart(part, definitions); err != nil {
				return nil, err
			}
			event["part"] = mustMarshalJSON(part)
		}
	}
	if rawResponse, ok := event["response"]; ok {
		trimmed := bytes.TrimSpace(rawResponse)
		if len(trimmed) != 0 && trimmed[0] == '{' {
			decoded, err := t.transformJSON(
				rawResponse,
				false,
				cloneCTPDefinitions(t.requestDefinitions),
			)
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

func (t *ctpResponseTransform) transformOutputItem(
	item map[string]json.RawMessage,
	observeOutput bool,
	definitions map[string]string,
) error {
	typeName := jsonString(item, "type")
	switch typeName {
	case "message":
		if jsonString(item, "role") != "assistant" {
			return nil
		}
		if raw, ok := item["content"]; ok {
			decoded, err := t.transformMessageContent(raw, observeOutput, definitions)
			if err != nil {
				return err
			}
			item["content"] = decoded
		}
	}
	return nil
}

func (t *ctpResponseTransform) transformMessageContent(
	raw json.RawMessage,
	observeOutput bool,
	definitions map[string]string,
) (json.RawMessage, error) {
	value, err := decodeCTPJSON(raw)
	if err != nil {
		return nil, errors.New("decode CTP/1 message content")
	}
	if text, ok := value.(string); ok {
		decoded, err := decodeCTPString(text, definitions, upstreamJSONBufferBytes)
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
		decoded, err := decodeCTPString(text, definitions, upstreamJSONBufferBytes)
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

func (t *ctpResponseTransform) transformTextPart(
	part map[string]json.RawMessage,
	definitions map[string]string,
) error {
	typeName := jsonString(part, "type")
	if typeName != "output_text" && typeName != "text" {
		return nil
	}
	compact := jsonString(part, "text")
	decoded, err := decodeCTPString(compact, definitions, upstreamJSONBufferBytes)
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
	definitions, dictionaryBytes := ctpResponseDictionaryMetrics(compact)
	t.recordOutput(ctpRepresentationMetrics{
		NativeTokens:  uint64(nativeTokens),
		CompactTokens: uint64(compactTokens),
		NativeBytes:   uint64(len(native)),
		CompactBytes:  uint64(len(compact)),
	}, definitions, dictionaryBytes)
}

// The decoder has already validated this dictionary. This pass only measures its framing.
func ctpResponseDictionaryMetrics(value string) (definitions, dictionaryBytes uint64) {
	if !strings.HasPrefix(value, ctpDictionaryTag) {
		return 0, 0
	}
	dictionaryBytes = uint64(len(ctpDictionaryTag))
	value = value[len(ctpDictionaryTag):]
	for {
		newline := strings.IndexByte(value, '\n')
		if newline < 0 {
			return 0, 0
		}
		line := value[:newline]
		value = value[newline+1:]
		dictionaryBytes += uint64(newline + 1)
		if line == "END" {
			return definitions, dictionaryBytes
		}
		definitions++
	}
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

func (t *ctpResponseTransform) streamingDefinitions() map[string]string {
	if t.streamDefinitions == nil {
		t.streamDefinitions = cloneCTPDefinitions(t.requestDefinitions)
	}
	return t.streamDefinitions
}

func cloneCTPDefinitions(definitions map[string]string) map[string]string {
	cloned := make(map[string]string, len(definitions))
	for id, value := range definitions {
		cloned[id] = value
	}
	return cloned
}

func decodeCTPString(value string, definitions map[string]string, limit int) (string, error) {
	if strings.HasPrefix(value, ctpLiteralTag) {
		literal := value[len(ctpLiteralTag):]
		if len(literal) > limit {
			return "", errors.New("decoded CTP/1 string exceeds the router buffer budget")
		}
		return literal, nil
	}
	if strings.HasPrefix(value, ctpDictionaryTag) {
		body, err := extendCTPDefinitions(value[len(ctpDictionaryTag):], definitions)
		if err != nil {
			return "", err
		}
		value = body
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
		encoded = encoded[at:]
		if strings.HasPrefix(encoded, "@@{") {
			if _, length, ok := parseCTPReference(encoded[1:]); ok {
				if err := appendCTPDecoded(&decoded, encoded[1:1+length], limit); err != nil {
					return "", err
				}
				encoded = encoded[1+length:]
				continue
			}
		}
		id, length, ok := parseCTPReference(encoded)
		if !ok {
			if err := appendCTPDecoded(&decoded, "@", limit); err != nil {
				return "", err
			}
			encoded = encoded[1:]
			continue
		}
		definition, exists := definitions[id]
		if !exists {
			return "", fmt.Errorf("decode CTP/1 string: unknown reference %q", id)
		}
		if err := appendCTPDecoded(&decoded, definition, limit); err != nil {
			return "", err
		}
		encoded = encoded[length:]
	}
	return decoded.String(), nil
}

func extendCTPDefinitions(value string, definitions map[string]string) (string, error) {
	added := 0
	for {
		newline := strings.IndexByte(value, '\n')
		if newline < 0 {
			return "", errors.New("decode CTP/1 dictionary: missing END")
		}
		line := value[:newline]
		value = value[newline+1:]
		if line == "END" {
			if added == 0 {
				return "", errors.New("decode CTP/1 dictionary: empty extension")
			}
			if !strings.HasPrefix(value, ctpReferenceTag) {
				return "", errors.New("decode CTP/1 dictionary: missing reference body")
			}
			return value, nil
		}
		id, encoded, ok := strings.Cut(line, "=")
		if !ok || !validCTPReferenceID(id) {
			return "", errors.New("decode CTP/1 dictionary: malformed definition")
		}
		if _, exists := definitions[id]; exists {
			return "", fmt.Errorf("decode CTP/1 dictionary: definition %q already exists", id)
		}
		var definition string
		if err := json.Unmarshal([]byte(encoded), &definition); err != nil {
			return "", fmt.Errorf("decode CTP/1 dictionary definition %q: %w", id, err)
		}
		definitions[id] = definition
		added++
	}
}

func parseCTPReference(value string) (string, int, bool) {
	if !strings.HasPrefix(value, "@{") {
		return "", 0, false
	}
	end := strings.IndexByte(value[2:], '}')
	if end < 0 {
		return "", 0, false
	}
	id := value[2 : 2+end]
	if !validCTPReferenceID(id) {
		return "", 0, false
	}
	return id, 3 + end, true
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
