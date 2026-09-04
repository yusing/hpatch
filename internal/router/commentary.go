package router

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const commentaryArgumentName = "commentary"

type commentaryTool struct {
	qualifiedName string
	display       string
	explicit      bool
}

type commentaryToolCatalog map[string]commentaryTool

func functionToolKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func prepareCommentaryTools(fields map[string]json.RawMessage, tools *responsesToolCatalog) (commentaryToolCatalog, error) {
	catalog := make(commentaryToolCatalog)
	instrument := func(namespace string, tool map[string]json.RawMessage, addParameter bool) error {
		if jsonString(tool, "type") != "function" {
			return nil
		}
		name := jsonString(tool, "name")
		if name == "" || commentaryExcluded(namespace, name) {
			return nil
		}
		key := functionToolKey(namespace, name)
		qualifiedName := qualifiedToolName(namespace, name)
		if _, exists := catalog[key]; exists {
			return fmt.Errorf("commentary tool %q is defined more than once", qualifiedName)
		}
		entry := commentaryTool{qualifiedName: qualifiedName, display: jsonString(tool, "title")}
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
					return fmt.Errorf("%s parameters properties must be an object", qualifiedName)
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

	if tools.top.present {
		if err := tools.top.err; err != nil {
			return nil, fmt.Errorf("decode Responses tools for commentary: %w", err)
		}
		for _, tool := range tools.top.tools {
			if err := instrument("", tool, true); err != nil {
				return nil, err
			}
		}
		fields["tools"] = mustMarshalJSON(tools.top.tools)
	}

	if tools.inputObjectsErr != nil {
		return catalog, nil
	}
	// The provider owns configured additional_tools schemas. They receive
	// defaults, but the router never adds a parameter to them.
	for _, group := range tools.additional {
		if !group.tools.present {
			return nil, errors.New("decode additional tools for commentary: unexpected end of JSON input")
		}
		if err := group.tools.err; err != nil {
			return nil, fmt.Errorf("decode additional tools for commentary: %w", err)
		}
		for index, tool := range group.tools.tools {
			if jsonString(tool, "type") != "namespace" {
				if err := instrument("", tool, false); err != nil {
					return nil, err
				}
				continue
			}
			namespace := jsonString(tool, "name")
			node := group.tools.nodes[index]
			if node == nil || node.nested == nil {
				return nil, fmt.Errorf("decode %s tools for commentary: unexpected end of JSON input", namespace)
			}
			if err := node.nested.err; err != nil {
				return nil, fmt.Errorf("decode %s tools for commentary: %w", namespace, err)
			}
			for _, child := range node.nested.tools {
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
	if namespace == "collaboration" {
		return true
	}
	qualified := qualifiedToolName(namespace, name)
	return qualified == "functions.send_user_message_async" || qualified == "send_user_message_async"
}

func commentaryDefault(tool commentaryTool, arguments map[string]json.RawMessage) string {
	switch tool.qualifiedName {
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
	tool, exists := catalog[functionToolKey(jsonString(item, "namespace"), jsonString(item, "name"))]
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
			var value *string
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return structuredCommentary{}, false, fmt.Errorf("%s commentary must be a string", tool.qualifiedName)
			}
			delete(arguments, commentaryArgumentName)
			result.arguments = string(mustMarshalJSON(arguments))
			if strings.TrimSpace(*value) != "" {
				result.text = *value
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

func assistantCommentaryMessage(id, text string) map[string]json.RawMessage {
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

func assistantCommentaryDoneEvent(message map[string]json.RawMessage) []byte {
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
		return assistantCommentaryMessage(messageID, extracted.text), nil
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
	return assistantCommentaryMessage(messageID, extracted.text), nil
}

func (p *hpatchProxy) drainCommentarySession(sessionID string) []publishedCommentary {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.commentary == nil || p.activeSessions[sessionID] > 1 {
		return nil
	}
	return p.commentary.drainSession(sessionID)
}

func (t *hpatchResponseTransform) cancelCommentaryTokens() {
	for _, token := range t.commentaryTokens {
		t.proxy.commentary.cancel(token)
	}
	t.commentaryTokens = nil
}

// validateHPatchCompactionRequest recognizes local Codex compaction requests,
// which stream through /responses without exposing model tools.
// Source: openai/codex codex-rs/core/src/compact.rs:228:273 and client.rs:795:881.

func (p *hpatchProxy) commentaryMessageIDs(sessionID string) map[string]struct{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]struct{})
	if session := p.sessions[sessionID]; session != nil {
		for _, history := range session.calls {
			for _, messageID := range history.commentaryMessageIDs {
				result[messageID] = struct{}{}
			}
		}
	}
	return result
}

func (p *hpatchProxy) addCommentaryMessageID(sessionID, callID, messageID string) bool {
	history, exists := p.history(sessionID, callID)
	if !exists {
		return false
	}
	if slices.Contains(history.commentaryMessageIDs, messageID) {
		return true
	}
	history.commentaryMessageIDs = append(history.commentaryMessageIDs, messageID)
	return p.rememberBatch(sessionID, map[string]hpatchHistory{callID: history}) == nil
}

func (t *hpatchResponseTransform) subagentCallMessage(item map[string]json.RawMessage) map[string]json.RawMessage {
	message, matched := subagentCallCommentary(
		item,
		t.subagentTools,
		t.parentModel,
		t.parentReasoningEffort,
	)
	if !matched {
		return nil
	}
	id := jsonString(message, "id")
	if _, emitted := t.commentaryEmitted[id]; emitted {
		return nil
	}
	t.commentaryEmitted[id] = struct{}{}
	return message
}

func (t *hpatchResponseTransform) runtimeCommentaryMessage(publication publishedCommentary) map[string]json.RawMessage {
	if publication.text == "" || !t.proxy.addCommentaryMessageID(
		t.historySessionID, publication.callID, publication.messageID,
	) {
		return nil
	}
	if history, exists := t.local[publication.callID]; exists && !slices.Contains(history.commentaryMessageIDs, publication.messageID) {
		history.commentaryMessageIDs = append(history.commentaryMessageIDs, publication.messageID)
		t.local[publication.callID] = history
	}
	return assistantCommentaryMessage(publication.messageID, publication.text)
}

func (t *hpatchResponseTransform) localStartCommentary(item map[string]json.RawMessage) map[string]json.RawMessage {
	if t.proxy.commentaryEndpoint == "" {
		return nil
	}
	callID := jsonString(item, "call_id")
	history, exists := t.local[callID]
	if !exists || jsonString(history.upstreamItem, "type") == "function_call" {
		return nil
	}
	if history.toolName != codeModeCommentaryHistoryTool && history.pluginID == builtinToolsPluginID && history.toolName == "shell" {
		return nil
	}
	if len(history.commentaryMessageIDs) == 0 {
		if history.toolName == codeModeCommentaryHistoryTool {
			return nil
		}
		history.commentaryMessageIDs = []string{commentaryMessageID(callID)}
		t.local[callID] = history
	}
	messageID := history.commentaryMessageIDs[0]
	if _, emitted := t.commentaryEmitted[messageID]; emitted {
		return nil
	}
	t.commentaryEmitted[messageID] = struct{}{}
	text := "Running the requested operation."
	if history.toolName != codeModeCommentaryHistoryTool {
		text = commentaryDefault(commentaryTool{qualifiedName: history.toolName, display: history.toolName}, nil)
	}
	return assistantCommentaryMessage(messageID, text)
}
