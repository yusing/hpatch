package router

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Repeated verified source excerpts are replacement evidence even when Code
// Mode carries the read through an opaque script. We do not interpret that
// script or infer which command ran: only completed, successful output qualifies.
func reduceRepeatedCompactionRows(input []json.RawMessage, protected map[string]bool) []json.RawMessage {
	type source struct {
		lines  []string
		start  int
		callID string
	}
	// A retained-source reference must identify one call and one result.
	calls, results := make(map[string]int), make(map[string]int)
	positions := make(map[string]int)
	for index, raw := range input {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		id := jsonString(fields, "call_id")
		switch jsonString(fields, "type") {
		case "function_call", "custom_tool_call":
			calls[id]++
			positions[id] = index
		case "function_call_output", "custom_tool_call_output":
			results[id]++
		}
	}
	sources := make(map[string]source)
	lastResult := true
	for index := len(input) - 1; index >= 0; index-- {
		var fields map[string]json.RawMessage
		if json.Unmarshal(input[index], &fields) != nil {
			continue
		}
		kind, callID := jsonString(fields, "type"), jsonString(fields, "call_id")
		if (kind != "function_call_output" && kind != "custom_tool_call_output") || callID == "" {
			continue
		}
		if calls[callID] != 1 || results[callID] != 1 || positions[callID] >= index {
			lastResult = false
			continue
		}
		var retained []string
		output := mapCompactionCompletedOutput(fields["output"], func(text string) string {
			lines := strings.SplitAfter(text, "\n")
			var result strings.Builder
			for row := 0; row < len(lines); {
				key := compactionRowWindow(lines, row)
				prior, found := sources[key]
				if lastResult || protected[callID] || key == "" || !found {
					result.WriteString(lines[row])
					row++
					continue
				}
				count := 4
				for row+count < len(lines) && prior.start+count < len(prior.lines) &&
					compactionSourceRow.MatchString(lines[row+count]) && lines[row+count] == prior.lines[prior.start+count] {
					count++
				}
				size := 0
				for _, line := range lines[row : row+count] {
					size += len(line)
				}
				note := ""
				// Bound formatting by the span it might replace, even when a
				// very long call ID is matched by many separate short excerpts.
				if size >= 256 && size > len(prior.callID) {
					note = fmt.Sprintf("[mekugi compaction: %d source rows (%s through %s) retained verbatim in later tool result %q]\n",
						count, strings.Fields(lines[row])[0], strings.Fields(lines[row+count-1])[0], prior.callID)
				}
				if note == "" || len(note) >= size {

					// Retain the whole rejected match. Rescanning every suffix
					// would be quadratic for large excerpts or long call IDs.
					for _, line := range lines[row : row+count] {
						result.WriteString(line)
					}
					row += count
					continue
				}
				result.WriteString(note)
				row += count
			}
			reduced := result.String()
			retained = append(retained, reduced)
			return reduced
		})
		if string(output) != string(fields["output"]) {
			fields["output"] = output
			input[index] = mustMarshalJSON(fields)
		}
		// Index only the final retained text, after the complete result is
		// processed. References cannot target this same result or deleted rows.
		for _, text := range retained {
			lines := strings.SplitAfter(text, "\n")
			for row := range lines {
				if key := compactionRowWindow(lines, row); key != "" {
					if _, exists := sources[key]; !exists {
						sources[key] = source{lines: lines, start: row, callID: callID}
					}
				}
			}
		}
		lastResult = false
	}
	return input
}

// Require complete verified-row lines, not partial substrings of diagnostics.
var compactionSourceRow = regexp.MustCompile(`^[1-9][0-9]*:[0-9a-f]{4} [^\r\n]*\r?\n$`)

func compactionRowWindow(lines []string, start int) string {
	if start+4 > len(lines) {
		return ""
	}
	for _, line := range lines[start : start+4] {
		if !compactionSourceRow.MatchString(line) {
			return ""
		}
	}
	return strings.Join(lines[start:start+4], "")
}

func mapCompactionCompletedOutput(raw json.RawMessage, transform func(string) string) json.RawMessage {
	if encode, text, ok := contextCompactionOutput(raw); ok {
		if reduced := transform(text); reduced != text {
			return encode(reduced)
		}
		return raw
	}
	// Codex stores Code Mode output as input_text content blocks. The first
	// block is the executor's status; subsequent blocks hold its emitted values.
	// A yielded or failed execution is not a completed source of evidence.
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) < 2 {
		return raw
	}
	var status map[string]json.RawMessage
	if json.Unmarshal(parts[0], &status) != nil || jsonString(status, "type") != "input_text" ||
		!strings.HasPrefix(jsonString(status, "text"), "Script completed\n") {
		return raw
	}
	changed := false
	for index := 1; index < len(parts); index++ {
		var part map[string]json.RawMessage
		if json.Unmarshal(parts[index], &part) != nil || jsonString(part, "type") != "input_text" {
			continue
		}
		encode, text, ok := contextCompactionOutput(part["text"])
		if !ok {
			continue
		}
		if reduced := transform(text); reduced != text {
			part["text"] = encode(reduced)
			parts[index] = mustMarshalJSON(part)
			changed = true
		}
	}
	if changed {
		return mustMarshalJSON(parts)
	}
	return raw
}

// Replacement notes are durable references, not just progress prose. Protect
// their targets on later compactions as well as within the current reduction.
var compactionRetainedReference = regexp.MustCompile(`(?m)^\[mekugi compaction: (?:matching search listing retained verbatim in tool result |[0-9]+ source rows \([^\r\n]*\) retained verbatim in later tool result )("(?:\\.|[^"\\])*")`)

func contextCompactionReferencedResults(input []json.RawMessage) map[string]bool {
	protected := make(map[string]bool)
	for _, raw := range input {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var evidence json.RawMessage
		switch jsonString(fields, "type") {
		case "function_call_output", "custom_tool_call_output":
			evidence = fields["output"]
		case "message":
			// Retired consumers carry the same notes in factual assistant records.
			if jsonString(fields, "role") != "assistant" {
				continue
			}
			evidence = fields["content"]
		default:
			continue
		}
		compactionVisitReferenceStrings(evidence, func(text string) {
			for _, match := range compactionRetainedReference.FindAllStringSubmatch(text, -1) {
				if id, err := strconv.Unquote(match[1]); err == nil {
					protected[id] = true
				}
			}
		})
	}
	return protected
}
