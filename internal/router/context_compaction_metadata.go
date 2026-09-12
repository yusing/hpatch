package router

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

var contextCompactionMetadataKinds = map[string]bool{
	"message":                 true,
	"reasoning":               true,
	"function_call":           true,
	"custom_tool_call":        true,
	"function_call_output":    true,
	"custom_tool_call_output": true,
	"agent_message":           true,
}

// reduceContextCompactionMetadata removes redundant transport correlation from
// older native items only when stable item identity and retained content make
// restoration independent of that correlation.
func reduceContextCompactionMetadata(input []json.RawMessage) []json.RawMessage {
	cutoff := contextCompactionMetadataRecentCutoff(input)
	if cutoff == 0 {
		return input
	}

	ids := make(map[string]int)
	for _, raw := range input {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			continue
		}
		if id, ok := contextCompactionMetadataID(fields["id"]); ok {
			ids[id]++
		}
	}

	references := contextCompactionMetadataReferences(input)

	var output []json.RawMessage
	for index, raw := range input {
		if index >= cutoff {
			break
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil || !contextCompactionMetadataKinds[jsonString(fields, "type")] {
			continue
		}
		id, ok := contextCompactionMetadataID(fields["id"])
		if !ok || ids[id] != 1 {
			continue
		}
		var metadata map[string]json.RawMessage
		if json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata) != nil || metadata == nil {
			continue
		}

		values := make(map[string]string, 2)
		valid := true
		for _, key := range []string{"turn_id", "create_time"} {
			value, exists := metadata[key]
			if !exists {
				continue
			}
			reference, ok := contextCompactionMetadataReference(key, value)
			if !ok {
				valid = false
				break
			}
			values[key] = reference
		}
		if !valid || len(values) == 0 {
			continue
		}

		nextMetadata := make(map[string]json.RawMessage, len(metadata))
		for key, value := range metadata {
			nextMetadata[key] = value
		}
		for key, reference := range values {
			referenced := references.unsafe || slices.ContainsFunc(references.text, func(text string) bool {
				return strings.Contains(text, reference)
			})
			if key == "create_time" && slices.Contains(references.numbers, reference) {
				referenced = true
			}
			if !referenced {
				delete(nextMetadata, key)
			}
		}
		if len(nextMetadata) == len(metadata) {
			continue
		}

		nextFields := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			nextFields[key] = value
		}
		if len(nextMetadata) == 0 {
			delete(nextFields, "internal_chat_message_metadata_passthrough")
		} else {
			nextFields["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(nextMetadata)
		}
		if output == nil {
			output = slices.Clone(input)
		}
		output[index] = mustMarshalJSON(nextFields)
	}
	if output == nil {
		return input
	}
	return output
}

type contextCompactionMetadataReferenceSet struct {
	text    []string
	numbers []string
	unsafe  bool
}

func contextCompactionMetadataReferences(input []json.RawMessage) contextCompactionMetadataReferenceSet {
	var references contextCompactionMetadataReferenceSet
	decoded := make(map[string]bool)
	for _, raw := range input {
		raw = contextCompactionMetadataWithoutBookkeeping(raw)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var root any
		if decoder.Decode(&root) != nil {
			continue
		}
		queue := []any{root}
		for len(queue) > 0 {
			value := queue[0]
			queue = queue[1:]
			switch value := value.(type) {
			case string:
				compactionSourceVisitDecodedReferences(value, func(text string) {
					references.text = append(references.text, text)
				}, &references.unsafe)
				if decoded[value] {
					continue
				}
				decoded[value] = true
				nestedDecoder := json.NewDecoder(strings.NewReader(value))
				nestedDecoder.UseNumber()
				var nested any
				if nestedDecoder.Decode(&nested) == nil {
					queue = append(queue, nested)
				}
			case json.Number:
				references.numbers = append(references.numbers, value.String())
			case []any:
				queue = append(queue, value...)
			case map[string]any:
				for _, nested := range value {
					queue = append(queue, nested)
				}
			}
		}
	}
	return references
}

func contextCompactionMetadataRecentCutoff(input []json.RawMessage) int {
	var calls []int
	for index, raw := range input {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		switch jsonString(fields, "type") {
		case "function_call", "custom_tool_call":
			calls = append(calls, index)
		}
	}
	if len(calls) <= compactionRecentOperations {
		return 0
	}
	return calls[len(calls)-compactionRecentOperations]
}

func contextCompactionMetadataID(raw json.RawMessage) (string, bool) {
	var id string
	return id, json.Unmarshal(raw, &id) == nil && id != ""
}

func contextCompactionMetadataReference(key string, raw json.RawMessage) (string, bool) {
	if key == "turn_id" {
		var value string
		return value, json.Unmarshal(raw, &value) == nil && value != ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", false
	}
	number, ok := value.(json.Number)
	return number.String(), ok && number.String() != ""
}

func contextCompactionMetadataWithoutBookkeeping(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return raw
	}
	_, directTurn := fields["turn_id"]
	_, directCreated := fields["create_time"]
	var metadata map[string]json.RawMessage
	metadataOK := json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata) == nil && metadata != nil
	nestedTurn, nestedCreated := false, false
	if metadataOK {
		_, nestedTurn = metadata["turn_id"]
		_, nestedCreated = metadata["create_time"]
	}
	if !directTurn && !directCreated && !nestedTurn && !nestedCreated {
		return raw
	}
	nextFields := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		nextFields[key] = value
	}
	delete(nextFields, "turn_id")
	delete(nextFields, "create_time")
	if nestedTurn || nestedCreated {
		nextMetadata := make(map[string]json.RawMessage, len(metadata))
		for key, value := range metadata {
			nextMetadata[key] = value
		}
		delete(nextMetadata, "turn_id")
		delete(nextMetadata, "create_time")
		if len(nextMetadata) == 0 {
			delete(nextFields, "internal_chat_message_metadata_passthrough")
		} else {
			nextFields["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(nextMetadata)
		}
	}
	return mustMarshalJSON(nextFields)
}
