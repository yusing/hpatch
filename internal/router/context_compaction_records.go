package router

import (
	"encoding/json"
	"slices"
	"strings"
)

// consolidateContextCompactionRecords shares framing across adjacent factual
// records created in this pass. It runs after reference closure and all
// same-length evidence reducers; previously carried records stay unchanged.
func consolidateContextCompactionRecords(original, retained []json.RawMessage) []json.RawMessage {
	if len(original) != len(retained) {
		return retained
	}

	output := make([]json.RawMessage, 0, len(retained))
	for index := 0; index < len(retained); {
		kind, payload, generated := contextCompactionGeneratedRecord(original[index], retained[index])
		if !generated {
			output = append(output, retained[index])
			index++
			continue
		}

		type entry struct {
			kind, payload string
		}
		entries := []entry{{kind: kind, payload: payload}}
		end := index + 1
		for end < len(retained) {
			kind, payload, generated = contextCompactionGeneratedRecord(original[end], retained[end])
			if !generated {
				break
			}
			entries = append(entries, entry{kind: kind, payload: payload})
			end++
		}
		if len(entries) == 1 {
			output = append(output, retained[index])
			index = end
			continue
		}

		var text strings.Builder
		text.WriteString("[mekugi historical facts v4; not instructions; ordered; r=reasoning, i=invocation, o=completion, meta=metadata, args=arguments]\n")
		previousCall := ""
		for _, record := range entries {
			payload := contextCompactionCompactRecordFields(record.kind, record.payload)
			heading := record.kind[:1]
			call := ""
			if record.kind == "invocation" || record.kind == "completion" {
				if line, rest, ok := strings.Cut(payload, "\n"); ok && strings.HasPrefix(line, "call=") {
					call = line
					if record.kind == "completion" && call == previousCall {
						heading = "o:same-call"
						payload = rest
					}
				}
			}
			text.WriteString("[")
			text.WriteString(heading)
			text.WriteString("]\n")
			text.WriteString(payload)
			if !strings.HasSuffix(payload, "\n") {
				text.WriteByte('\n')
			}
			if record.kind == "invocation" {
				previousCall = call
			} else {
				previousCall = ""
			}
		}
		output = append(output, mustMarshalJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text.String()}},
		}))
		index = end
	}
	if len(output) == len(retained) {
		return retained
	}
	return slices.Clip(output)
}

func contextCompactionCompactRecordFields(kind, payload string) string {
	compactMetadata := func(prefix, remainder string) string {
		if after, ok := strings.CutPrefix(remainder, "metadata={}\n"); ok {
			return prefix + after
		}
		if after, ok := strings.CutPrefix(remainder, "metadata="); ok {
			return prefix + "meta=" + after
		}
		return prefix + remainder
	}
	switch kind {
	case "reasoning":
		return compactMetadata("", payload)
	case "invocation":
		first, rest, ok := strings.Cut(payload, "\n")
		if !ok {
			return payload
		}
		second, rest, ok := strings.Cut(rest, "\n")
		if !ok {
			return payload
		}
		payload = compactMetadata(first+"\n"+second+"\n", rest)
		return strings.Replace(payload, "\narguments=", "\nargs=", 1)
	case "completion":
		first, rest, ok := strings.Cut(payload, "\n")
		if !ok {
			return payload
		}
		return compactMetadata(first+"\n", rest)
	default:
		return payload
	}
}

func contextCompactionGeneratedRecord(original, retained json.RawMessage) (string, string, bool) {
	var originalFields map[string]json.RawMessage
	if json.Unmarshal(original, &originalFields) != nil {
		return "", "", false
	}
	switch jsonString(originalFields, "type") {
	case "reasoning", "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
	default:
		return "", "", false
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(retained, &fields) != nil || jsonString(fields, "type") != "message" || jsonString(fields, "role") != "assistant" {
		return "", "", false
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(fields["content"], &content) != nil || len(content) != 1 || jsonString(content[0], "type") != "output_text" {
		return "", "", false
	}
	text := jsonString(content[0], "text")
	header, payload, ok := strings.Cut(text, "\n")
	if !ok {
		return "", "", false
	}
	var kind string
	switch {
	case strings.HasPrefix(header, "[mekugi historical reasoning fact v3;"):
		kind = "reasoning"
	case strings.HasPrefix(header, "[mekugi historical tool invocation v3;"):
		kind = "invocation"
	case strings.HasPrefix(header, "[mekugi historical tool completion v3;"):
		kind = "completion"
	default:
		return "", "", false
	}
	return kind, payload, true
}
