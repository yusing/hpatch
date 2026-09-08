package router

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/yusing/hpatch/internal/commentaryid"
)

const commentaryArgumentName = "commentary"

type commentaryTool struct {
	qualifiedName string
	explicit      bool
}

type commentaryToolCatalog map[string]commentaryTool

func functionToolKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func prepareCommentaryTools(fields map[string]json.RawMessage, tools *responsesToolCatalog) (commentaryToolCatalog, error) {
	catalog := make(commentaryToolCatalog)
	instrument := func(namespace string, tool *responsesToolDefinition, addParameter bool) error {
		if tool.Type != "function" {
			return nil
		}
		name := tool.Name
		if name == "" || commentaryExcluded(namespace, name) {
			return nil
		}
		key := functionToolKey(namespace, name)
		qualifiedName := qualifiedToolName(namespace, name)
		if _, exists := catalog[key]; exists {
			return fmt.Errorf("commentary tool %q is defined more than once", qualifiedName)
		}
		entry := commentaryTool{qualifiedName: qualifiedName}
		var strict bool
		_ = json.Unmarshal(tool.rawField("strict"), &strict)
		if addParameter && !strict {
			var parameters map[string]json.RawMessage
			if json.Unmarshal(tool.rawField("parameters"), &parameters) == nil && jsonString(parameters, "type") == "object" {
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
					tool.setRawField("parameters", mustMarshalJSON(parameters))
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
	// The provider owns configured additional_tools schemas; the router never
	// adds a commentary parameter to them.
	for _, group := range tools.additional {
		if !group.tools.present {
			return nil, errors.New("decode additional tools for commentary: unexpected end of JSON input")
		}
		if err := group.tools.err; err != nil {
			return nil, fmt.Errorf("decode additional tools for commentary: %w", err)
		}
		for index, tool := range group.tools.tools {
			if tool.Type != "namespace" {
				if err := instrument("", tool, false); err != nil {
					return nil, err
				}
				continue
			}
			namespace := tool.Name
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
	if namespace == "collaboration" || namespace != "" && slices.Contains([]string{"spawn_agent", "followup_task", "send_message", "wait_agent", "interrupt_agent"}, name) {
		return true
	}
	qualified := qualifiedToolName(namespace, name)
	return qualified == "functions.send_user_message_async" || qualified == "send_user_message_async"
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
	return result, true, nil
}

func commentaryMessageID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s%x", commentaryid.OperationPrefix, digest[:12])
}

// assistantCommentaryMessage creates an assistant commentary message with the given ID and text.
func assistantCommentaryMessage(id, text string) map[string]json.RawMessage {
	encoded := mustMarshalJSON(responses.ResponseOutputMessageParam{
		ID: id,
		Content: []responses.ResponseOutputMessageContentUnionParam{{
			OfOutputText: new(responses.ResponseOutputTextParam{
				Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
				Text:        text,
			}),
		}},
		Status: responses.ResponseOutputMessageStatusCompleted,
		Phase:  responses.ResponseOutputMessagePhaseCommentary,
	})
	var message map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &message); err != nil {
		panic(err)
	}
	return message
}

// assistantCommentaryDoneEvent creates a response.output_item.done event for a commentary message.
func assistantCommentaryDoneEvent(message map[string]json.RawMessage) []byte {
	return mustMarshalJSON(struct {
		Type string                     `json:"type"`
		Item map[string]json.RawMessage `json:"item"`
	}{
		Type: "response.output_item.done",
		Item: message,
	})
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
		return t.operationCommentaryMessage(messageID, extracted.text), nil
	}
	var messageIDs []string
	if extracted.text != "" {
		messageIDs = []string{messageID}
	}

	original := maps.Clone(item)
	t.recordLocal(callID, &hpatchHistory{
		toolName:             qualifiedToolName(jsonString(item, "namespace"), jsonString(item, "name")),
		script:               extracted.originalArguments,
		carrierKind:          codeModeCarrierFunction,
		carrierName:          jsonString(item, "name"),
		carrierPayload:       extracted.arguments,
		upstreamItem:         original,
		commentaryMessageIDs: messageIDs,
	})
	item["arguments"] = mustMarshalJSON(extracted.arguments)
	return t.operationCommentaryMessage(messageID, extracted.text), nil
}

func (p *hpatchProxy) drainCommentarySession(sessionID, threadID string) []publishedCommentary {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.commentary == nil {
		return nil
	}
	// Call-scoped deferred progress still needs a non-concurrent session.
	// Shell progress already has exact thread identity and is drained atomically.
	if p.activeSessions[sessionID] > 1 {
		return p.commentary.drainThreadSession(sessionID, threadID)
	}
	return p.commentary.drainSession(sessionID, threadID)
}

// Only thread routes lack a carrier subscription. Keep call-scoped live delivery separate.
func (p *hpatchProxy) drainThreadCommentarySession(sessionID, threadID string) []publishedCommentary {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.commentary == nil {
		return nil
	}
	return p.commentary.drainThreadSession(sessionID, threadID)
}

type commentarySubscription struct {
	token     string
	callID    string
	handedOff bool
}

func (t *hpatchResponseTransform) handOffCommentary(callID string) {
	for index := range t.commentarySubscriptions {
		if t.commentarySubscriptions[index].callID == callID {
			t.commentarySubscriptions[index].handedOff = true
		}
	}
}

func (t *hpatchResponseTransform) releaseCommentarySubscriptions() {
	for _, subscription := range t.commentarySubscriptions {
		// Once the carrier is handed off, publication completion and broker
		// expiry own the route, regardless of how the provider response ends.
		if !subscription.handedOff {
			t.proxy.commentary.cancel(subscription.token)
		}
	}
	t.commentarySubscriptions = nil
}

// validateHPatchCompactionRequest recognizes local Codex compaction requests,
// which stream through /responses without exposing model tools.
// Source: openai/codex codex-rs/core/src/compact.rs:228:273 and client.rs:795:881.

func (p *hpatchProxy) commentaryMessageIDs(sessionID string) map[string]struct{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]struct{})
	if p.commentary != nil {
		maps.Copy(result, p.commentary.threadMessageIDs(sessionID))
	}
	if session := p.sessions[sessionID]; session != nil {
		for _, history := range session.calls {
			for _, messageID := range history.commentaryMessageIDs {
				result[messageID] = struct{}{}
			}
		}
	}
	return result
}

func (p *hpatchProxy) addCommentaryMessageID(sessionID, threadID, callID, messageID string) bool {
	if callID == "" {
		return p.commentary.hasThreadMessageID(threadID, messageID)
	}
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

func (t *hpatchResponseTransform) runtimeCommentaryMessage(publication publishedCommentary) map[string]json.RawMessage {
	if publication.text == "" || !t.proxy.addCommentaryMessageID(
		t.historySessionID, t.shellThreadID, publication.callID, publication.messageID,
	) {
		return nil
	}
	if history, exists := t.local[publication.callID]; exists && !slices.Contains(history.commentaryMessageIDs, publication.messageID) {
		history.commentaryMessageIDs = append(history.commentaryMessageIDs, publication.messageID)
		t.local[publication.callID] = history
	}
	return assistantCommentaryMessage(publication.messageID, publication.text)
}

func attributedCommentary(author, text string) string {
	if author == "" {
		return text
	}
	prefix := "[" + commentaryCode(author) + "] "
	if hasCommentaryAuthor(text, author) {
		return text
	}
	return prefix + text
}

// commentaryCode keeps backticks in names or previews from ending the code span.
func commentaryCode(value string) string {
	longest, run := 0, 0
	for _, r := range value {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if longest > 0 || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}

func hasCommentaryAuthor(text, author string) bool {
	return strings.HasPrefix(text, "["+commentaryCode(author)+"] ")
}

func (t *hpatchResponseTransform) operationCommentaryMessage(id, text string) map[string]json.RawMessage {
	if text == "" {
		return nil
	}

	t.proxy.activity.collect(t.threadID, id, "operation", text)
	return assistantCommentaryMessage(id, attributedCommentary(t.commentaryAuthor, text))
}

// Completed provider commentary is copied to the root without rewriting the
// child's original message. Router-owned messages already have their own paths.
func (t *hpatchResponseTransform) collectProviderCommentary(message map[string]json.RawMessage) {
	if !t.subagentTurn || jsonString(message, "type") != "message" ||
		jsonString(message, "role") != "assistant" || jsonString(message, "phase") != "commentary" ||
		jsonString(message, "status") != "completed" {
		return
	}
	id := jsonString(message, "id")
	if id == "" || len(id) > maxCommentaryPublicationBytes-len("provider-message\x00") || commentaryid.Generated(id) {
		return
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(message["content"], &content) != nil {
		return
	}
	var text strings.Builder
	for _, part := range content {
		if jsonString(part, "type") != "output_text" {
			continue
		}
		value := jsonString(part, "text")
		if len(value) > maxCommentaryPublicationBytes-text.Len() {
			return
		}
		text.WriteString(value)
	}
	t.proxy.activity.collect(t.threadID, "provider-message\x00"+id, "commentary", text.String())
}
