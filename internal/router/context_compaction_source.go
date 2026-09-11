package router

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type compactionSourcePair struct {
	call, result int
	operation    compactionOperation
	known        bool
	scriptRefs   []string
}

func reduceContextCompactionSource(original, retained []json.RawMessage) []json.RawMessage {
	return reduceContextCompactionSourceWithFrontier(original, retained, compactionRecentOperations)
}

func reduceContextCompactionSourceWithFrontier(original, retained []json.RawMessage, recent int) []json.RawMessage {
	recent = max(1, recent)
	if len(original) != len(retained) {
		return retained
	}

	originalFields := make([]map[string]json.RawMessage, len(original))
	retainedFields := make([]map[string]json.RawMessage, len(retained))
	calls, results := make(map[string][]int), make(map[string][]int)
	var callOrder []int
	for index := range original {
		if json.Unmarshal(original[index], &originalFields[index]) != nil ||
			json.Unmarshal(retained[index], &retainedFields[index]) != nil {
			continue
		}
		id := jsonString(originalFields[index], "call_id")
		switch jsonString(originalFields[index], "type") {
		case "function_call", "custom_tool_call":
			calls[id] = append(calls[id], index)
			callOrder = append(callOrder, index)
		case "function_call_output", "custom_tool_call_output":
			results[id] = append(results[id], index)
		}
	}
	if len(callOrder) <= recent {
		return retained
	}
	cutoff := callOrder[len(callOrder)-recent]

	pairs := make(map[string]compactionSourcePair)
	for id, callPositions := range calls {
		if id == "" || !compactionCallID.MatchString(id) || len(callPositions) != 1 || len(results[id]) != 1 {
			continue
		}
		call, result := callPositions[0], results[id][0]
		callType := jsonString(originalFields[call], "type")
		if call >= cutoff || result >= cutoff || result <= call ||
			jsonString(originalFields[result], "type") != callType+"_output" ||
			jsonString(retainedFields[call], "type") != callType ||
			jsonString(retainedFields[result], "type") != callType+"_output" ||
			jsonString(retainedFields[call], "call_id") != id ||
			jsonString(retainedFields[result], "call_id") != id {
			continue
		}
		if !compactionSourceShellCall(originalFields[call]) || compactionSourceIdentityCrosses(originalFields, call, result) {
			continue
		}
		operation, known := compactionOperationCall(originalFields[call])
		pairs[id] = compactionSourcePair{
			call: call, result: result, operation: operation, known: known,
			scriptRefs: compactionSourceResultScriptRefs(originalFields[result]["output"]),
		}
	}
	if len(pairs) == 0 {
		return retained
	}
	output := retained
	cloned := false

	protected := contextCompactionReferencedResults(retained)
	for id := range contextCompactionReferencedResults(original) {
		protected[id] = true
	}
	rowReferences := make(map[string]bool)
	var ranges [][2]string
	unsafeEncoding := false
	for index, fields := range originalFields {
		if fields == nil || retainedFields[index] == nil {
			continue
		}
		kind, owner := jsonString(fields, "type"), jsonString(fields, "call_id")
		var references []json.RawMessage
		switch kind {
		case "function_call", "custom_tool_call":
			operation, known := compactionOperationCall(fields)
			normalized := known
			if known && operation.patchReport != "" {
				normalized = false
				if positions := results[owner]; len(positions) == 1 {
					_, normalized = compactionRetiredOutput(originalFields[positions[0]]["output"], operation)
				}
			}
			if normalized {
				references = append(references, operation.arguments)
				if operation.notice != nil {
					references = append(references, mustMarshalJSON(*operation.notice))
				}
			} else {
				references = append(references, fields["input"], fields["arguments"])
			}
		case "function_call_output", "custom_tool_call_output":
			visitOutput := func(text string) {
				compactionSourceVisitDecodedReferences(text, func(decoded string) {
					for line := range strings.SplitAfterSeq(decoded, "\n") {
						if match := compactionCompleteSourceRow.FindStringSubmatch(line); match != nil {
							line = strings.Replace(line, match[1], "", 1)
						}
						for _, match := range compactionSourceRangeReference.FindAllStringSubmatch(line, -1) {
							ranges = append(ranges, [2]string{match[1], match[2]})
						}
						for _, row := range compactionRowReference.FindAllString(line, -1) {
							rowReferences[row] = true
						}
					}
				}, &unsafeEncoding)
			}
			compactionVisitReferenceStrings(fields["output"], visitOutput)
			continue
		default:
			references = append(references, fields["content"], fields["summary"])
		}
		for _, raw := range references {
			compactionVisitReferenceStrings(raw, func(text string) {
				compactionSourceVisitDecodedReferences(text, func(decoded string) {
					for _, word := range compactionReferenceWord.FindAllString(decoded, -1) {
						if word != owner {
							if _, exists := pairs[word]; exists {
								protected[word] = true
							}
						}
					}
					for _, reference := range compactionScriptReference.FindAllString(decoded, -1) {
						for id, pair := range pairs {
							if id != owner && slices.Contains(pair.scriptRefs, reference) {
								protected[id] = true
							}
						}
					}
					for _, match := range compactionVisibleLineReference.FindAllStringSubmatch(decoded, -1) {
						for id := range pairs {
							if id != owner && strings.HasSuffix(id, match[1]) {
								protected[id] = true
							}
						}
					}
					for _, match := range compactionSourceRangeReference.FindAllStringSubmatch(decoded, -1) {
						ranges = append(ranges, [2]string{match[1], match[2]})
					}
					for _, row := range compactionRowReference.FindAllString(decoded, -1) {
						rowReferences[row] = true
					}
				}, &unsafeEncoding)
			})
		}
	}
	if unsafeEncoding {
		for id := range pairs {
			protected[id] = true
		}
	}

	for id, pair := range pairs {
		if protected[id] {
			continue
		}
		fields := retainedFields[pair.result]
		mapped := fields["output"]
		if pair.known && !compactionOutputNeedsSourcePreservation(mapped, rowReferences, ranges) {
			if reduced, ok := compactionRetiredOutput(mapped, pair.operation); ok && len(reduced) < len(mapped) {
				mapped = reduced
			}
		}
		if string(mapped) == string(fields["output"]) {
			mapped = mapCompactionCompletedOutput(mapped, func(text string) string {
				return compactionPruneSourceText(text, rowReferences, ranges)
			})
		}
		if string(mapped) == string(fields["output"]) {
			continue
		}
		if !cloned {
			output = slices.Clone(retained)
			cloned = true
		}
		copyFields := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			copyFields[key] = value
		}
		copyFields["output"] = mapped
		output[pair.result] = mustMarshalJSON(copyFields)
	}
	return output
}

func compactionSourceShellCall(fields map[string]json.RawMessage) bool {
	name := strings.TrimPrefix(jsonString(fields, "name"), "functions.")
	switch jsonString(fields, "type") {
	case "function_call":
		return name == "exec_command" || name == "write_stdin"
	case "custom_tool_call":
		return name == "shell" || name == "exec"
	default:
		return false
	}
}

func compactionSourceIdentityCrosses(fields []map[string]json.RawMessage, call, result int) bool {
	for index := call + 1; index < result; index++ {
		switch jsonString(fields[index], "type") {
		case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
			return true
		}
	}
	return false
}

func compactionSourceResultScriptRefs(raw json.RawMessage) []string {
	var references []string
	visitEnvelope := func(raw json.RawMessage) {
		var serialized string
		if json.Unmarshal(raw, &serialized) != nil {
			return
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(serialized), &envelope) != nil {
			return
		}
		if reference := jsonString(envelope, "script_ref"); reference != "" {
			references = append(references, reference)
		}
	}

	visitEnvelope(raw)
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		for _, part := range parts {
			if jsonString(part, "type") == "input_text" {
				visitEnvelope(part["text"])
			}
		}
	}
	return references
}

func compactionSourceVisitDecodedReferences(text string, visit func(string), unsafeEncoding *bool) {
	visit(text)
	decoded, changed, unsafe := compactionSourceDecodeReferenceEscapes(text)
	if changed {
		// Always visit the original above as well as this single decoded layer.
		// In particular, an escape which produces another escape spelling is
		// not interpreted again.
		visit(decoded)
	}
	if unsafe {
		*unsafeEncoding = true
	}
}

// compactionSourceDecodeReferenceEscapes decodes one static source layer. It
// understands URL/HTML character encodings and JavaScript string/template
// escapes, but never evaluates interpolation or joins expressions. Generated
// escape spellings remain literal so a second interpretation cannot hide the
// original evidence.
func compactionSourceDecodeReferenceEscapes(text string) (decoded string, changed, unsafe bool) {
	var result strings.Builder
	result.Grow(len(text))
	for index := 0; index < len(text); {
		switch text[index] {
		case '\\':
			value, width, recognized, valid := compactionSourceDecodeJSEscape(text[index:])
			if !recognized {
				result.WriteByte(text[index])
				index++
				continue
			}
			if !valid {
				unsafe = true
				result.WriteByte(text[index])
				index++
				continue
			}
			changed = true
			result.WriteString(value)
			index += width
		case '%':
			if index+2 < len(text) && compactionSourceHexValue(text[index+1]) >= 0 && compactionSourceHexValue(text[index+2]) >= 0 {
				result.WriteByte(byte(compactionSourceHexValue(text[index+1])<<4 | compactionSourceHexValue(text[index+2])))
				changed = true
				index += 3
				continue
			}
			result.WriteByte(text[index])
			index++
		case '&':
			value, width, recognized, valid := compactionSourceDecodeHTMLEscape(text[index:])
			if recognized && valid {
				result.WriteString(value)
				changed = true
				index += width
				continue
			}
			if recognized && !valid {
				unsafe = true
			}
			result.WriteByte(text[index])
			index++
		default:
			result.WriteByte(text[index])
			index++
		}
	}
	decoded = result.String()
	return decoded, changed, unsafe
}

func compactionSourceDecodeJSEscape(text string) (value string, width int, recognized, valid bool) {
	if len(text) < 2 || text[0] != '\\' {
		return "", 0, false, false
	}
	switch text[1] {
	case '\\', '\'', '"', '`', '/':
		return text[1:2], 2, true, true
	case 'b':
		return "\b", 2, true, true
	case 'f':
		return "\f", 2, true, true
	case 'n':
		return "\n", 2, true, true
	case 'r':
		return "\r", 2, true, true
	case 't':
		return "\t", 2, true, true
	case 'v':
		return "\v", 2, true, true
	case '\n':
		return "", 2, true, true
	case '\r':
		if len(text) >= 3 && text[2] == '\n' {
			return "", 3, true, true
		}
		return "", 2, true, true
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// Non-strict legacy octal consumes at most three digits for 0..3,
		// but only two for 4..7: \720003 is ':' followed by "0003".
		limit := 4 // Backslash plus at most three octal digits.
		if text[1] >= '4' {
			limit = 3
		}
		codePoint := rune(text[1] - '0')
		width := 2
		for width < min(len(text), limit) && text[width] >= '0' && text[width] <= '7' {
			codePoint = codePoint*8 + rune(text[width]-'0')
			width++
		}
		return string(codePoint), width, true, true
	case '8', '9':
		// NonOctalDecimalEscapeSequence in non-strict string literals.
		return text[1:2], 2, true, true
	case 'x':
		if len(text) < 3 || compactionSourceHexValue(text[2]) < 0 {
			return "", 0, false, false
		}
		if len(text) < 4 || compactionSourceHexValue(text[2]) < 0 || compactionSourceHexValue(text[3]) < 0 {
			return "", 0, true, false
		}
		return string(rune(compactionSourceHexValue(text[2])<<4 | compactionSourceHexValue(text[3]))), 4, true, true
	case 'u':
		if len(text) < 3 || text[2] != '{' && compactionSourceHexValue(text[2]) < 0 {
			return "", 0, false, false
		}
		return compactionSourceDecodeJSUnicodeEscape(text)
	default:
		// NonEscapeCharacter is an identity escape, including \: and \_.
		// Consume one code point; Unicode line continuations contribute none.
		codePoint, size := utf8.DecodeRuneInString(text[1:])
		if codePoint == utf8.RuneError && size == 1 {
			return "", 0, true, false
		}
		if codePoint == '\u2028' || codePoint == '\u2029' {
			return "", size + 1, true, true
		}
		return text[1 : size+1], size + 1, true, true
	}
}

func compactionSourceDecodeJSUnicodeEscape(text string) (value string, width int, recognized, valid bool) {
	if len(text) >= 3 && text[2] == '{' {
		end := strings.IndexByte(text[3:], '}')
		if end < 0 {
			return "", 0, true, false
		}
		end += 3
		digits := text[3:end]
		if len(digits) == 0 {
			return "", 0, true, false
		}
		parsed, err := strconv.ParseUint(digits, 16, 32)
		codePoint := rune(parsed)
		if err != nil || !utf8.ValidRune(codePoint) {
			return "", 0, true, false
		}
		return string(codePoint), end + 1, true, true
	}
	first, ok := compactionSourceParseJSCodeUnit(text)
	if !ok {
		return "", 0, true, false
	}
	if first >= 0xd800 && first <= 0xdbff {
		if len(text) < 12 || text[6] != '\\' || text[7] != 'u' {
			return "", 0, true, false
		}
		second, secondOK := compactionSourceParseJSCodeUnit(text[6:])
		if !secondOK || second < 0xdc00 || second > 0xdfff {
			return "", 0, true, false
		}
		codePoint := rune(0x10000 + (first-0xd800)<<10 + second - 0xdc00)
		return string(codePoint), 12, true, true
	}
	if first >= 0xdc00 && first <= 0xdfff {
		return "", 0, true, false
	}
	return string(first), 6, true, true
}

func compactionSourceParseJSCodeUnit(text string) (rune, bool) {
	if len(text) < 6 || text[0] != '\\' || text[1] != 'u' {
		return 0, false
	}
	return compactionSourceParseHex(text[2:6])
}

func compactionSourceParseHex(text string) (rune, bool) {
	var value rune
	for index := range len(text) {
		digit := compactionSourceHexValue(text[index])
		if digit < 0 {
			return 0, false
		}
		value = value<<4 | rune(digit)
	}
	return value, true
}

func compactionSourceHexValue(value byte) int {
	switch {
	case value >= '0' && value <= '9':
		return int(value - '0')
	case value >= 'a' && value <= 'f':
		return int(value-'a') + 10
	case value >= 'A' && value <= 'F':
		return int(value-'A') + 10
	default:
		return -1
	}
}

func compactionSourceDecodeHTMLEscape(text string) (value string, width int, recognized, valid bool) {
	if strings.HasPrefix(text, "&colon;") {
		return ":", len("&colon;"), true, true
	}
	if strings.HasPrefix(text, "&sol;") {
		return "/", len("&sol;"), true, true
	}
	if strings.HasPrefix(text, "&#") {
		base, start := 10, 2
		if len(text) > start && (text[start] == 'x' || text[start] == 'X') {
			base, start = 16, start+1
		}
		end := start
		for end < len(text) && ((base == 10 && text[end] >= '0' && text[end] <= '9') ||
			(base == 16 && compactionSourceHexValue(text[end]) >= 0)) {
			end++
		}
		if end == start || end >= len(text) || text[end] != ';' {
			return "", 0, false, false
		}
		codePoint, err := strconv.ParseInt(text[start:end], base, 32)
		if err != nil || !utf8.ValidRune(rune(codePoint)) {
			return "", 0, false, false
		}
		return string(rune(codePoint)), end + 1, true, true
	}
	return "", 0, false, false
}

func compactionOutputNeedsSourcePreservation(raw json.RawMessage, referenced map[string]bool, ranges [][2]string) bool {
	preserve := false
	mapCompactionCompletedOutput(raw, func(text string) string {
		if strings.HasPrefix(text, "[mekugi compaction: retired finished-operation output (") ||
			strings.HasPrefix(text, "[mekugi: output details omitted; unavailable; original bytes=") {
			preserve = true
			return text
		}
		if compactionTextReferencesRows(text, referenced, ranges) {
			preserve = true
		}
		return text
	})
	return preserve
}

func compactionSourceRowReferenced(token string, referenced map[string]bool, ranges [][2]string) bool {
	if referenced[token] {
		return true
	}
	lineText, _, ok := strings.Cut(token, ":")
	if !ok {
		return false
	}
	line, err := strconv.Atoi(lineText)
	if err != nil {
		return false
	}
	for _, rowRange := range ranges {
		startText, _, startOK := strings.Cut(rowRange[0], ":")
		endText, _, endOK := strings.Cut(rowRange[1], ":")
		start, startErr := strconv.Atoi(startText)
		end, endErr := strconv.Atoi(endText)
		if startOK && endOK && startErr == nil && endErr == nil &&
			line >= min(start, end) && line <= max(start, end) {
			return true
		}
	}
	return false
}

func compactionPruneSourceText(text string, referenced map[string]bool, ranges [][2]string) string {
	lines := strings.SplitAfter(text, "\n")
	tokens := make([]string, len(lines))
	keep := make([]bool, len(lines))
	for index, line := range lines {
		if match := compactionCompleteSourceRow.FindStringSubmatch(line); match != nil {
			tokens[index] = match[1]
		}
		if tokens[index] != "" && compactionSourceRowReferenced(tokens[index], referenced, ranges) {
			keep[index] = true
		}
	}

	for _, rowRange := range ranges {
		start, end := -1, -1
		for index, token := range tokens {
			if start < 0 && token == rowRange[0] {
				start = index
			}
			if start >= 0 && token == rowRange[1] {
				end = index
			}
		}
		if end >= start && start >= 0 {
			for index := start; index <= end; index++ {
				keep[index] = true
			}
		}
	}

	for start, line := range lines {
		if !compactionPythonTraceback.MatchString(line) {
			continue
		}
		for index := start; index < len(lines); index++ {
			keep[index] = true
		}
		break
	}

	var result strings.Builder
	for index := 0; index < len(lines); {
		if tokens[index] == "" || keep[index] {
			result.WriteString(lines[index])
			index++
			continue
		}
		end, size := index, 0
		for end < len(lines) && tokens[end] != "" && !keep[end] {
			size += len(lines[end])
			end++
		}
		count := end - index
		note := fmt.Sprintf("[mekugi compaction: omitted %d unreferenced verified source rows (%s through %s); omitted rows are not retained]\n",
			count, tokens[index], tokens[end-1])
		if count < 4 || size < 256 || len(note) >= size {
			for _, line := range lines[index:end] {
				result.WriteString(line)
			}
		} else {
			result.WriteString(note)
		}
		index = end
	}
	return result.String()
}

var (
	compactionCompleteSourceRow    = regexp.MustCompile(`^(?:"(?:\\.|[^"\\])*":)?([1-9][0-9]*:[0-9a-f]{4}) [^\r\n]*\r?\n$`)
	compactionSourceRangeReference = regexp.MustCompile(`\b([1-9][0-9]*:[0-9a-f]{4})\.\.([1-9][0-9]*:[0-9a-f]{4})\b`)
	compactionVisibleLineReference = regexp.MustCompile(`(?m)^(?:!V)?=([A-Za-z0-9_-]+),[1-9][0-9]*,[1-9][0-9]*$`)
)
