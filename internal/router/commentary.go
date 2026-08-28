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
	instrument := func(namespace string, tool map[string]json.RawMessage) error {
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
		display := jsonString(tool, "title")
		if display == "" {
			display = name
		}
		entry := commentaryTool{namespace: namespace, name: name, display: display}
		var strict bool
		_ = json.Unmarshal(tool["strict"], &strict)
		if !strict {
			var parameters map[string]json.RawMessage
			if json.Unmarshal(tool["parameters"], &parameters) == nil && parameters != nil && jsonString(parameters, "type") == "object" {
				var properties map[string]json.RawMessage
				if raw, exists := parameters["properties"]; !exists {
					properties = make(map[string]json.RawMessage)
				} else if json.Unmarshal(raw, &properties) != nil || properties == nil {
					return fmt.Errorf("%s parameters properties must be an object", qualifiedToolName(namespace, name))
				}
				if _, exists := properties[commentaryArgumentName]; exists {
					catalog[key] = entry
					return nil
				}
				properties[commentaryArgumentName] = mustMarshalJSON(map[string]string{
					"type":        "string",
					"description": "Optional concise progress commentary shown to the user before this operation. Omit it to use the operation default.",
				})
				parameters["properties"] = mustMarshalJSON(properties)
				tool["parameters"] = mustMarshalJSON(parameters)
				entry.explicit = true
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
			if err := instrument("", tool); err != nil {
				return nil, err
			}
		}
		fields["tools"] = mustMarshalJSON(tools)
	}

	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return catalog, nil
	}
	changed := false
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
				if err := instrument("", tool); err != nil {
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
				if err := instrument(namespace, child); err != nil {
					return nil, err
				}
			}
			tool["tools"] = mustMarshalJSON(nested)
		}
		item["tools"] = mustMarshalJSON(tools)
		changed = true
	}
	if changed {
		fields["input"] = mustMarshalJSON(items)
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
	switch qualified {
	case "functions.send_user_message_async", "send_user_message_async",
		"functions.commentary", "commentary",
		"functions.hread", "functions.hgrep", "functions.hsymbol", "functions.inspect_file",
		"hread", "hgrep", "hsymbol", "inspect_file":
		return true
	default:
		return false
	}
}

func commentaryDefault(tool commentaryTool, arguments map[string]json.RawMessage) string {
	switch qualifiedToolName(tool.namespace, tool.name) {
	case "functions.wait", "wait":
		return "Waiting for the running operation."
	case "functions.exec_command", "exec_command":
		return "Running the requested command."
	case "functions.write_stdin", "write_stdin":
		var input string
		_ = json.Unmarshal(arguments["chars"], &input)
		if input != "" {
			return "Sending input to the running command."
		}
		return "Waiting for command output."
	case "functions.apply_patch", "apply_patch", "functions.hpatch", "hpatch":
		return "Applying the requested changes."
	case "functions.shell", "shell":
		return "Running the requested commands."
	case "functions.hpatch_recover", "hpatch_recover":
		return "Repairing the rejected edit."
	case "functions.view_image", "view_image":
		return "Opening the requested image."
	case "functions.request_user_input", "request_user_input":
		return "Waiting for your input."
	case "functions.request_permissions", "request_permissions":
		return "Requesting permission to continue."
	case "functions.update_plan", "update_plan":
		return "Updating the task plan."
	case "functions.get_goal", "get_goal":
		return "Checking the active task goal."
	case "functions.create_goal", "create_goal":
		return "Creating the task goal."
	case "functions.update_goal", "update_goal":
		return "Updating the task goal."
	case "functions.list_mcp_resources", "list_mcp_resources":
		return "Listing available MCP resources."
	case "functions.list_mcp_resource_templates", "list_mcp_resource_templates":
		return "Listing available MCP resource templates."
	case "functions.read_mcp_resource", "read_mcp_resource":
		return "Reading the requested MCP resource."
	case "collaboration.spawn_agent":
		return "Starting an agent."
	case "collaboration.send_message":
		return "Sending a message to the agent."
	case "collaboration.followup_task":
		return "Sending a follow-up task to the agent."
	case "collaboration.wait_agent":
		return "Waiting for agent results."
	case "collaboration.interrupt_agent":
		return "Interrupting the agent."
	case "collaboration.list_agents":
		return "Checking agent status."
	case "image_gen.imagegen":
		return "Generating the requested image."
	case "web.run":
		return "Looking up current information."
	case "functions.wait_for_environment", "wait_for_environment":
		return "Waiting for the environment."
	case "clock.curr_time":
		return "Checking the current time."
	case "clock.sleep":
		return "Waiting for the requested time."
	case "functions.tool_search", "tool_search":
		return "Finding the appropriate tool."
	case "functions.report_issue", "report_issue":
		return "Reporting the issue."
	default:
		return "Using " + tool.display + "."
	}
}

type structuredCommentary struct {
	text              string
	originalArguments string
	arguments         string
}

func commentaryMessageID(callID string) string {
	digest := sha256.Sum256([]byte(callID))
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
	// Codex accepts terminal assistant-message items without a synthetic
	// content lifecycle; using that native path keeps commentary atomic.
	return mustMarshalJSON(map[string]any{
		"type": "response.output_item.done",
		"item": message,
	})
}

func (t *hpatchResponseTransform) transformStructuredCommentary(item map[string]json.RawMessage) (map[string]json.RawMessage, bool, error) {
	extracted, matched, err := extractStructuredCommentary(item, t.commentaryTools)
	if err != nil || !matched {
		return nil, false, err
	}
	callID := jsonString(item, "call_id")
	if callID == "" {
		return nil, false, errors.New("upstream emitted commentary function call without a call ID")
	}
	messageID := commentaryMessageID(callID)
	if retained, exists := t.local[callID]; exists {
		if retained.commentaryMessageID != messageID || retained.script != extracted.originalArguments ||
			retained.carrierPayload != extracted.arguments {
			return nil, false, fmt.Errorf("commentary call %q changed arguments", callID)
		}
		item["arguments"] = mustMarshalJSON(extracted.arguments)
		return commentaryMessage(messageID, extracted.text), extracted.arguments != extracted.originalArguments, nil
	}
	original := maps.Clone(item)
	history := hpatchHistory{
		toolName:            qualifiedToolName(jsonString(item, "namespace"), jsonString(item, "name")),
		script:              extracted.originalArguments,
		carrierKind:         codeModeCarrierFunction,
		carrierName:         jsonString(item, "name"),
		carrierPayload:      extracted.arguments,
		upstreamItem:        original,
		commentaryMessageID: messageID,
	}
	t.recordLocal(callID, &history)
	item["arguments"] = mustMarshalJSON(extracted.arguments)
	return commentaryMessage(messageID, extracted.text), extracted.arguments != extracted.originalArguments, nil
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
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return structuredCommentary{}, false, fmt.Errorf("%s commentary must be a string", qualifiedToolName(tool.namespace, tool.name))
			}
			delete(arguments, commentaryArgumentName)
			encoded, err := json.Marshal(arguments)
			if err != nil {
				return structuredCommentary{}, false, err
			}
			result.arguments = string(encoded)
			if strings.TrimSpace(value) != "" {
				result.text = value
				return result, true, nil
			}
		}
	}
	result.text = commentaryDefault(tool, arguments)
	return result, true, nil
}
