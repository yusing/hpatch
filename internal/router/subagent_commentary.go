package router

// Source: openai/codex codex-rs/protocol/src/protocol.rs.
// Inter-agent message shapes visible at the Responses boundary.

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
		label := "[" + commentaryCode(recipient) + " <- " + commentaryCode(sender) + "] Message received."
		if text != "" {
			label = "[" + commentaryCode(recipient) + " <- " + commentaryCode(sender) + "] Reply received:\n" + text
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
