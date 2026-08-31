package router

// Source: openai/codex codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs
// and codex-rs/protocol/src/protocol.rs. These are the collaboration call and
// inter-agent message shapes visible at the Responses boundary.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const subagentCommentaryMessagePrefix = "msg_hpatch_subagent_commentary_"

type subagentPendingCall struct {
	callID        string
	added         []byte
	argumentsDone []byte
}

func subagentToolCatalog(fields map[string]json.RawMessage) map[string]struct{} {
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return nil
	}
	catalog := make(map[string]struct{})
	for _, item := range items {
		if jsonString(item, "type") != "additional_tools" {
			continue
		}
		var namespaces []map[string]json.RawMessage
		if json.Unmarshal(item["tools"], &namespaces) != nil {
			continue
		}
		for _, namespace := range namespaces {
			if jsonString(namespace, "type") != "namespace" {
				continue
			}
			var tools []map[string]json.RawMessage
			if json.Unmarshal(namespace["tools"], &tools) != nil {
				continue
			}
			for _, tool := range tools {
				name := jsonString(tool, "name")
				if jsonString(tool, "type") == "function" && name == "spawn_agent" {
					catalog[subagentToolKey(jsonString(namespace, "name"), name)] = struct{}{}
				}
			}
		}
	}
	return catalog
}

func subagentToolKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func subagentCommentaryMessageID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s%x", subagentCommentaryMessagePrefix, digest[:12])
}

func subagentCommentaryMessage(id, text string) map[string]json.RawMessage {
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

func subagentCommentaryDoneEvent(message map[string]json.RawMessage) []byte {
	return mustMarshalJSON(map[string]any{"type": "response.output_item.done", "item": message})
}

func prepareSubagentInputCommentary(fields map[string]json.RawMessage) []map[string]json.RawMessage {
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return nil
	}
	visible := make(map[string]struct{})
	for _, item := range items {
		if jsonString(item, "type") == "message" && strings.HasPrefix(jsonString(item, "id"), subagentCommentaryMessagePrefix) {
			visible[jsonString(item, "id")] = struct{}{}
		}
	}
	originalLen := len(items)
	items = slices.DeleteFunc(items, func(item map[string]json.RawMessage) bool {
		return jsonString(item, "type") == "message" &&
			strings.HasPrefix(jsonString(item, "id"), subagentCommentaryMessagePrefix)
	})
	if len(items) != originalLen {
		fields["input"] = mustMarshalJSON(items)
	}

	var commentary []map[string]json.RawMessage
	for _, item := range items {
		text, sender, ok := subagentResponse(item)
		if !ok {
			continue
		}
		id := subagentCommentaryMessageID("response\x00" + jsonString(item, "id") + "\x00" + sender + "\x00" + text)
		if _, alreadyVisible := visible[id]; alreadyVisible {
			continue
		}
		commentary = append(commentary, subagentCommentaryMessage(id, "Response from "+sender+":\n"+text))
	}
	return commentary
}

func subagentResponse(item map[string]json.RawMessage) (text, sender string, ok bool) {
	if jsonString(item, "type") != "agent_message" || jsonString(item, "recipient") != "/root" {
		return "", "", false
	}
	sender = jsonString(item, "author")
	if !strings.HasPrefix(sender, "/root/") {
		return "", "", false
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(item["content"], &content) != nil || len(content) != 1 || jsonString(content[0], "type") != "input_text" {
		return "", "", false
	}
	body := jsonString(content[0], "text")
	header, payload, found := strings.Cut(body, "\nPayload:\n")
	if !found || (!strings.HasPrefix(header, "Message Type: MESSAGE\n") && !strings.HasPrefix(header, "Message Type: FINAL_ANSWER\n")) {
		return "", "", false
	}
	return payload, sender, true
}

func subagentCallCommentary(
	item map[string]json.RawMessage,
	catalog map[string]struct{},
	parentModel, parentEffort string,
) (map[string]json.RawMessage, bool) {
	if jsonString(item, "type") != "function_call" {
		return nil, false
	}
	name := jsonString(item, "name")
	if _, exists := catalog[subagentToolKey(jsonString(item, "namespace"), name)]; !exists {
		return nil, false
	}
	callID := jsonString(item, "call_id")
	var arguments map[string]json.RawMessage
	if callID == "" || json.Unmarshal([]byte(jsonString(item, "arguments")), &arguments) != nil {
		return nil, false
	}
	if name != "spawn_agent" {
		return nil, false
	}
	model, effort := parentModel, parentEffort
	var requestedModel, requestedEffort, roleName string
	_ = json.Unmarshal(arguments["model"], &requestedModel)
	_ = json.Unmarshal(arguments["reasoning_effort"], &requestedEffort)
	_ = json.Unmarshal(arguments["agent_type"], &roleName)
	if strings.TrimSpace(requestedModel) != "" {
		model = requestedModel
	}
	if strings.TrimSpace(requestedEffort) != "" {
		effort = requestedEffort
	}
	var builder strings.Builder
	builder.WriteString("Starting subagent.\n")
	if roleName = strings.TrimSpace(roleName); roleName != "" {
		fmt.Fprintf(&builder, "Role: %s\n", roleName)
	}
	fmt.Fprintf(&builder, "Model: %s\nReasoning effort: %s", model, effort)
	id := subagentCommentaryMessageID(name + "\x00" + callID)
	return subagentCommentaryMessage(id, builder.String()), true
}

func tokenUsageCommentary(response []byte) map[string]json.RawMessage {
	counts, ok := usageFromResponsePayload(response, false)
	if !ok {
		return nil
	}
	var identity struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response, &identity) != nil || identity.ID == "" {
		return nil
	}
	cachedInput := counts.InputTokens - counts.UncachedInputTokens
	text := fmt.Sprintf(
		"Tokens: i=%d, ci=%d, o=%d, r=%d",
		counts.InputTokens,
		cachedInput,
		counts.OutputTokens,
		counts.ReasoningTokens,
	)
	id := subagentCommentaryMessageID("usage\x00" + identity.ID)
	return subagentCommentaryMessage(id, text)
}

func responseWithTokenUsageCommentary(response []byte) (
	map[string]json.RawMessage,
	map[string]json.RawMessage,
	error,
) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(response, &object); err != nil || object == nil {
		return nil, nil, errors.New("decode hpatch-enabled response")
	}
	message := tokenUsageCommentary(response)
	rawOutput, present := object["output"]
	if message == nil || !present {
		return object, message, nil
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(rawOutput, &output); err != nil {
		return nil, nil, errors.New("decode hpatch-enabled response output")
	}
	output = append(output, message)
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, nil, err
	}
	object["output"] = encoded
	return object, message, nil
}
