package router

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

const commentaryArgumentName = "commentary"

type commentaryTool struct {
	namespace string
	name      string
	display   string
	explicit  bool
}

type commentaryToolCatalog map[string]commentaryTool

func commentaryToolKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func prepareCommentaryTools(fields map[string]json.RawMessage) (commentaryToolCatalog, error) {
	catalog := make(commentaryToolCatalog)
	instrument := func(namespace string, tool map[string]json.RawMessage, addParameter bool) error {
		if jsonString(tool, "type") != "function" {
			return nil
		}
		name := jsonString(tool, "name")
		if name == "" || commentaryExcluded(namespace, name) {
			return nil
		}
		key := commentaryToolKey(namespace, name)
		if _, exists := catalog[key]; exists {
			return fmt.Errorf("commentary tool %q is defined more than once", qualifiedToolName(namespace, name))
		}
		entry := commentaryTool{namespace: namespace, name: name, display: jsonString(tool, "title")}
		if entry.display == "" {
			entry.display = name
		}
		var strict bool
		_ = json.Unmarshal(tool["strict"], &strict)
		if addParameter && !strict {
			var parameters map[string]json.RawMessage
			if json.Unmarshal(tool["parameters"], &parameters) == nil && jsonString(parameters, "type") == "object" {
				var properties map[string]json.RawMessage
				if raw, exists := parameters["properties"]; !exists {
					properties = make(map[string]json.RawMessage)
				} else if json.Unmarshal(raw, &properties) != nil || properties == nil {
					return fmt.Errorf("%s parameters properties must be an object", qualifiedToolName(namespace, name))
				}
				if _, owned := properties[commentaryArgumentName]; !owned {
					properties[commentaryArgumentName] = mustMarshalJSON(map[string]string{
						"type":        "string",
						"description": "Optional concise progress commentary shown before this operation.",
					})
					parameters["properties"] = mustMarshalJSON(properties)
					tool["parameters"] = mustMarshalJSON(parameters)
					entry.explicit = true
				}
			}
		}
		catalog[key] = entry
		return nil
	}

	if rawTools, exists := fields["tools"]; exists {
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return nil, fmt.Errorf("decode Responses tools for commentary: %w", err)
		}
		for _, tool := range tools {
			if err := instrument("", tool, true); err != nil {
				return nil, err
			}
		}
		fields["tools"] = mustMarshalJSON(tools)
	}

	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return catalog, nil
	}
	// The provider owns configured additional_tools schemas. They receive
	// defaults, but the router never adds a parameter to them.
	for _, item := range items {
		if jsonString(item, "type") != "additional_tools" {
			continue
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(item["tools"], &tools); err != nil {
			return nil, fmt.Errorf("decode additional tools for commentary: %w", err)
		}
		for _, tool := range tools {
			if jsonString(tool, "type") != "namespace" {
				if err := instrument("", tool, false); err != nil {
					return nil, err
				}
				continue
			}
			namespace := jsonString(tool, "name")
			var nested []map[string]json.RawMessage
			if err := json.Unmarshal(tool["tools"], &nested); err != nil {
				return nil, fmt.Errorf("decode %s tools for commentary: %w", namespace, err)
			}
			for _, child := range nested {
				if err := instrument(namespace, child, false); err != nil {
					return nil, err
				}
			}
		}
	}
	return catalog, nil
}

func qualifiedToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

func commentaryExcluded(namespace, name string) bool {
	qualified := qualifiedToolName(namespace, name)
	return qualified == "functions.send_user_message_async" || qualified == "send_user_message_async"
}

func commentaryDefault(tool commentaryTool, arguments map[string]json.RawMessage) string {
	switch qualifiedToolName(tool.namespace, tool.name) {
	case "functions.wait", "wait":
		return "Waiting for the running operation."
	case "functions.write_stdin", "write_stdin":
		var input string
		_ = json.Unmarshal(arguments["chars"], &input)
		if input == "" {
			return "Waiting for command output."
		}
		return "Sending input to the running command."
	case "functions.apply_patch", "apply_patch", "functions.hpatch", "hpatch":
		return "Applying the requested changes."
	case "functions.request_user_input", "request_user_input":
		return "Waiting for your input."
	case "web.run":
		return "Looking up current information."
	case "image_gen.imagegen":
		return "Generating the requested image."
	default:
		return "Using " + tool.display + "."
	}
}

type structuredCommentary struct {
	text              string
	originalArguments string
	arguments         string
}

func extractStructuredCommentary(item map[string]json.RawMessage, catalog commentaryToolCatalog) (structuredCommentary, bool, error) {
	if jsonString(item, "type") != "function_call" {
		return structuredCommentary{}, false, nil
	}
	tool, exists := catalog[commentaryToolKey(jsonString(item, "namespace"), jsonString(item, "name"))]
	if !exists {
		return structuredCommentary{}, false, nil
	}
	original := jsonString(item, "arguments")
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal([]byte(original), &arguments); err != nil || arguments == nil {
		return structuredCommentary{}, false, errors.New("commentary function arguments must be a JSON object")
	}
	result := structuredCommentary{originalArguments: original, arguments: original}
	if tool.explicit {
		if raw, present := arguments[commentaryArgumentName]; present {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return structuredCommentary{}, false, fmt.Errorf("%s commentary must be a string", qualifiedToolName(tool.namespace, tool.name))
			}
			delete(arguments, commentaryArgumentName)
			result.arguments = string(mustMarshalJSON(arguments))
			if strings.TrimSpace(text) != "" {
				result.text = text
				return result, true, nil
			}
		}
	}
	result.text = commentaryDefault(tool, arguments)
	return result, true, nil
}

func commentaryMessageID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("msg_hpatch_commentary_%x", digest[:12])
}

func commentaryMessage(id, text string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"type":   mustMarshalJSON("message"),
		"id":     mustMarshalJSON(id),
		"status": mustMarshalJSON("completed"),
		"role":   mustMarshalJSON("assistant"),
		"phase":  mustMarshalJSON("commentary"),
		"content": mustMarshalJSON([]any{map[string]any{
			"type": "output_text", "text": text, "annotations": []any{},
		}}),
	}
}

func commentaryDoneEvent(message map[string]json.RawMessage) []byte {
	return mustMarshalJSON(map[string]any{"type": "response.output_item.done", "item": message})
}

func (t *hpatchResponseTransform) transformStructuredCommentary(item map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	extracted, matched, err := extractStructuredCommentary(item, t.commentaryTools)
	if err != nil || !matched {
		return nil, err
	}
	callID := jsonString(item, "call_id")
	if callID == "" {
		return nil, errors.New("upstream emitted commentary function call without a call ID")
	}
	messageID := commentaryMessageID(callID)
	if retained, exists := t.local[callID]; exists {
		if retained.script != extracted.originalArguments || retained.carrierPayload != extracted.arguments {
			return nil, fmt.Errorf("commentary call %q changed arguments", callID)
		}
		item["arguments"] = mustMarshalJSON(extracted.arguments)
		return commentaryMessage(messageID, extracted.text), nil
	}
	original := maps.Clone(item)
	t.recordLocal(callID, &hpatchHistory{
		toolName:             qualifiedToolName(jsonString(item, "namespace"), jsonString(item, "name")),
		script:               extracted.originalArguments,
		carrierKind:          codeModeCarrierFunction,
		carrierName:          jsonString(item, "name"),
		carrierPayload:       extracted.arguments,
		upstreamItem:         original,
		commentaryMessageIDs: []string{messageID},
	})
	item["arguments"] = mustMarshalJSON(extracted.arguments)
	return commentaryMessage(messageID, extracted.text), nil
}
