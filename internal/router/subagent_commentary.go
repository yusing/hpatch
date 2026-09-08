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

	"github.com/yusing/hpatch/internal/commentaryid"
)

const subagentCommentaryMessagePrefix = commentaryid.SubagentPrefix

type subagentPendingCall struct {
	callID        string
	added         []byte
	argumentsDone []byte
}

func subagentToolCatalog(tools *responsesToolCatalog) map[string]struct{} {
	if tools.inputObjectsErr != nil {
		return nil
	}
	catalog := make(map[string]struct{})
	sections := []*responsesToolSection{tools.top}
	for _, group := range tools.additional {
		sections = append(sections, group.tools)
	}
	for _, section := range sections {
		if !section.present || section.err != nil {
			continue
		}
		for index, namespace := range section.tools {
			if namespace == nil || namespace.Type != "namespace" {
				continue
			}
			node := section.nodes[index]
			if node == nil || node.nested == nil || node.nested.err != nil {
				continue
			}
			for _, tool := range node.nested.tools {
				if tool != nil && tool.Type == "function" && slices.Contains([]string{"spawn_agent", "followup_task", "send_message", "wait_agent", "interrupt_agent"}, tool.Name) {
					catalog[functionToolKey(namespace.Name, tool.Name)] = struct{}{}
				}
			}
		}
	}
	return catalog
}

func subagentCommentaryMessageID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s%x", subagentCommentaryMessagePrefix, digest[:12])
}

func prepareSubagentInputCommentary(fields map[string]json.RawMessage, recipient string) []map[string]json.RawMessage {
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
	budget := maxCommentaryPublicationBytes
	for _, item := range items {
		text, sender, ok := subagentResponse(item)
		if !ok || jsonString(item, "recipient") != recipient {
			continue
		}
		id := subagentCommentaryMessageID("response\x00" + jsonString(item, "id") + "\x00" + sender + "\x00" + text)
		if _, alreadyVisible := visible[id]; alreadyVisible {
			continue
		}
		label := "[" + recipient + " <- " + sender + "] Message received."
		if text != "" {
			label = "[" + recipient + " <- " + sender + "] Reply received:\n" + text
		}
		if len(label) <= budget && len(commentary) < maxCommentaryEventsPerRoute {
			budget -= len(label)
			commentary = append(commentary, assistantCommentaryMessage(id, label))
		}
	}
	return commentary
}

func subagentResponse(item map[string]json.RawMessage) (text, sender string, ok bool) {
	if jsonString(item, "type") != "agent_message" {
		return "", "", false
	}
	sender = jsonString(item, "author")
	if sender != "/root" && !strings.HasPrefix(sender, "/root/") || strings.ContainsAny(sender, "\r\n\x00") {
		return "", "", false
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(item["content"], &content) != nil || len(content) != 1 {
		return "", "", false
	}
	if jsonString(content[0], "type") == "encrypted_content" {
		return "", sender, true
	}
	if jsonString(content[0], "type") != "input_text" {
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
	parentModel, parentEffort, author string,
) (map[string]json.RawMessage, bool) {
	if jsonString(item, "type") != "function_call" {
		return nil, false
	}
	name := jsonString(item, "name")
	if _, exists := catalog[functionToolKey(jsonString(item, "namespace"), name)]; !exists {
		return nil, false
	}
	callID := jsonString(item, "call_id")
	var arguments map[string]json.RawMessage
	if callID == "" || json.Unmarshal([]byte(jsonString(item, "arguments")), &arguments) != nil {
		return nil, false
	}
	if author == "" {
		author = "/root"
	}
	target := jsonString(arguments, "target")
	label := "[" + author + "] "
	if target != "" {
		label = "[" + author + " -> " + target + "] "
	}
	var action string
	switch name {
	case "followup_task":
		action = "Follow-up requested."
	case "send_message":
		return nil, false
	case "wait_agent":
		return nil, false
	case "interrupt_agent":
		action = "Interruption requested."
	case "spawn_agent":
	default:
		return nil, false
	}
	id := subagentCommentaryMessageID(name + "\x00" + callID)
	if action != "" {
		if strings.ContainsAny(target, "\r\n\x00") || len(label)+len(action) > maxCommentaryPublicationBytes {
			return nil, false
		}
		return assistantCommentaryMessage(id, label+action), true
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
	builder.WriteString("[" + author + "] Spawn requested.\n")
	if roleName = strings.TrimSpace(roleName); roleName != "" {
		fmt.Fprintf(&builder, "Role: `%s`\n", roleName)
	}
	fmt.Fprintf(&builder, "Model: `%s`\nReasoning effort: `%s`", model, effort)
	if builder.Len() > maxCommentaryPublicationBytes {
		return nil, false
	}
	return assistantCommentaryMessage(id, builder.String()), true
}

// tokenUsageCommentary reports usage only alongside a completed substantive answer.
func tokenUsageCommentary(response []byte, counts tokenCounts, observed bool, terminalStatus string) map[string]json.RawMessage {
	if !observed {
		return nil
	}
	var identity struct {
		ID     string                       `json:"id"`
		Status string                       `json:"status"`
		Output []map[string]json.RawMessage `json:"output"`
	}

	if json.Unmarshal(response, &identity) != nil || identity.ID == "" {
		return nil
	}
	status := identity.Status
	if terminalStatus != "" {
		status = terminalStatus
	}
	if status != "completed" {
		return nil
	}
	substantive := false
	for _, item := range identity.Output {
		// Client tool items are dispatch requests even when their item status is
		// completed. Hosted tools can finish before the accompanying final answer.
		switch jsonString(item, "type") {
		case "function_call", "custom_tool_call", "computer_call", "local_shell_call", "apply_patch_call", "mcp_approval_request":
			return nil
		case "tool_search_call":
			if jsonString(item, "execution") != "server" {
				return nil
			}
		case "shell_call":
			var environment map[string]json.RawMessage
			if json.Unmarshal(item["environment"], &environment) != nil || jsonString(environment, "type") != "container_reference" {
				return nil
			}
		}
		if strings.HasSuffix(jsonString(item, "type"), "_call") {
			if callStatus := jsonString(item, "status"); callStatus != "completed" && callStatus != "failed" {
				return nil
			}
		}

		if jsonString(item, "type") != "message" || jsonString(item, "role") != "assistant" {
			continue
		}
		if phase := jsonString(item, "phase"); phase != "" && phase != "final_answer" {
			continue
		}
		if itemStatus := jsonString(item, "status"); itemStatus != "" && itemStatus != "completed" {
			continue
		}
		var content []map[string]json.RawMessage
		if json.Unmarshal(item["content"], &content) != nil {
			continue
		}
		for _, part := range content {
			if jsonString(part, "type") == "output_text" && strings.TrimSpace(jsonString(part, "text")) != "" ||
				jsonString(part, "type") == "refusal" && strings.TrimSpace(jsonString(part, "refusal")) != "" {
				substantive = true
			}
		}
	}
	if !substantive {
		return nil
	}

	cachedInput := counts.InputTokens - counts.UncachedInputTokens
	text := fmt.Sprintf(
		"Tokens:\nInput: `%d`\nCached input: `%d`\nOutput: `%d`\nReasoning: `%d`",
		counts.InputTokens,
		cachedInput,
		counts.OutputTokens,
		counts.ReasoningTokens,
	)
	id := subagentCommentaryMessageID("usage\x00" + identity.ID)
	return assistantCommentaryMessage(id, text)
}

// responseWithTokenUsageCommentary extracts a response object and token usage commentary.
func responseWithTokenUsageCommentary(response []byte, counts tokenCounts, usageObserved bool, terminalStatus string) (
	map[string]json.RawMessage,
	map[string]json.RawMessage,
	error,
) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(response, &object); err != nil || object == nil {
		return nil, nil, errors.New("decode hpatch-enabled response")
	}
	message := tokenUsageCommentary(response, counts, usageObserved, terminalStatus)
	rawOutput, present := object["output"]
	if message == nil || !present {
		return object, message, nil
	}
	var output []map[string]json.RawMessage
	if err := json.Unmarshal(rawOutput, &output); err != nil {
		return nil, nil, errors.New("decode hpatch-enabled response output")
	}
	output = append([]map[string]json.RawMessage{message}, output...)
	encoded, err := marshalProtocolJSON(output)
	if err != nil {
		return nil, nil, err
	}
	object["output"] = encoded
	return object, message, nil
}
