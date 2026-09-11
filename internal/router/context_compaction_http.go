package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Both transports use the same boundary before projection or provider calls.
// Codex remains the sole owner of scheduling and configuration.
func (c *contextCompactor) handler(next http.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		body, err := readResponsesRequest(io.LimitReader(request.Body, responsesRequestBufferBytes+1))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) > responsesRequestBufferBytes {
			http.Error(writer, "Responses request exceeds the router buffer budget", http.StatusRequestEntityTooLarge)
			return
		}
		parsed, err := parseResponsesRequest(body)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		standalone := request.URL.Path == "/v1/responses/compact"
		capsule, err := c.prepare(request.Context(), &parsed, request.Header, standalone)
		if err != nil {
			status := http.StatusUnprocessableEntity
			if failure, ok := errors.AsType[*contextCompactionRequestError](err); ok {
				status = failure.status
			}
			http.Error(writer, err.Error(), status)
			return
		}
		if len(capsule) == 0 {
			body, err = parsed.wireBody(parsed.fields)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			request.ContentLength = int64(len(body))
			next.ServeHTTP(writer, request)
			return
		}
		if standalone {
			writer.Header().Set("Content-Type", "application/json")
		} else {
			writer.Header().Set("Content-Type", "text/event-stream")
		}
		_ = writeContextCompactionResponse(writer, capsule, parsed.fields["input"], standalone)
	}
}

type contextCompactionRequestError struct {
	status  int
	message string
}

func (e *contextCompactionRequestError) Error() string { return e.message }

func hasLocalContextCompaction(input []json.RawMessage) bool {
	for _, raw := range input {
		var item map[string]json.RawMessage
		_ = json.Unmarshal(raw, &item)
		if strings.HasPrefix(jsonString(item, "encrypted_content"), "mekugi.compaction.") || strings.HasPrefix(jsonString(item, "id"), contextCompactionIDPrefix) {
			return true
		}
	}
	return false
}

// prepare restores native input in place and returns a capsule only for a local
// completion. It has no provider transport, including for unsupported requests.
func (c *contextCompactor) prepare(ctx context.Context, parsed *parsedResponsesRequest, headers http.Header, standalone bool) (json.RawMessage, error) {
	fail := func(status int, message string) (json.RawMessage, error) {
		return nil, &contextCompactionRequestError{status: status, message: message}
	}
	metadata, valid := decodeCodexTurnMetadata(headers)
	compacting := standalone || (valid && metadata.RequestKind == "compaction")
	var input []json.RawMessage
	if json.Unmarshal(parsed.fields["input"], &input) != nil || len(input) == 0 {
		if !compacting {
			return nil, nil
		}
		return fail(http.StatusBadRequest, "local compaction requires a nonempty input item array")
	}
	local := hasLocalContextCompaction(input)
	if !compacting && !local {
		return nil, nil
	}
	for _, item := range input {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || fields == nil {
			return fail(http.StatusBadRequest, "compaction input items must be objects")
		}
	}
	input, err := c.restore(ctx, input)
	if err != nil {
		return fail(http.StatusUnprocessableEntity, err.Error())
	}
	parsed.setInput(mustMarshalJSON(input))
	if local {
		// Provider-side history cannot represent a router-owned capsule. Send
		// the restored timeline in full instead of trimming a cached prefix or
		// naming a response that was completed only by the router.
		parsed.cachedInput = 0
		if _, exists := parsed.fields["previous_response_id"]; exists {
			parsed.fields["previous_response_id"] = json.RawMessage("null")
		}

	}
	body, err := parsed.wireBody(parsed.fields)
	if err != nil || len(body) > responsesRequestBufferBytes {
		return fail(http.StatusRequestEntityTooLarge, "restored history exceeds the router buffer budget")
	}
	if !compacting {
		return nil, nil
	}
	if parsed.model() == "" {
		return fail(http.StatusBadRequest, "compaction requires a model")
	}
	if !standalone {
		var details struct {
			Implementation string `json:"implementation"`
		}
		_ = json.Unmarshal(metadata.Compaction, &details)
		if !parsed.streamResponse || details.Implementation != "responses_compaction_v2" {
			return fail(http.StatusUnprocessableEntity, "local compaction requires the standalone compact endpoint or streaming compaction V2; provider summaries are disabled")
		}
		var last map[string]json.RawMessage
		_ = json.Unmarshal(input[len(input)-1], &last)
		if jsonString(last, "type") == "compaction_trigger" {
			input = input[:len(input)-1]
		}
	}
	reduced, _, err := selectCompactionWorkingSet(ctx, input,
		compactionTargetTokens, compactionOvershootTokens,
		reduceContextCompactionWithPlan, compactionVisibleStringTokens)
	if err != nil {
		return fail(http.StatusUnprocessableEntity, err.Error())
	}
	capsule, err := c.seal(ctx, reduced)
	if err != nil {
		return fail(http.StatusUnprocessableEntity, err.Error())
	}
	parsed.setInput(mustMarshalJSON(reduced))
	return capsule, nil
}

// The stream framing is shared by HTTP/SSE and the WebSocket output adapter.
func writeContextCompactionResponse(writer io.Writer, capsule, retained json.RawMessage, standalone bool) error {
	var sealedItem struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(capsule, &sealedItem)
	responseID := "resp_" + strings.TrimPrefix(sealedItem.ID, "cmp_")
	if standalone {
		// Keep real user messages visible to legacy Codex's input handling.
		// Historical canonical context stays only inside the capsule.
		var input, output []json.RawMessage
		_ = json.Unmarshal(retained, &input)
		for _, item := range input {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(item, &fields)
			if jsonString(fields, "type") == "message" && jsonString(fields, "role") == "user" && !contextCompactionFreshContext(item) {
				output = append(output, item)
			}
		}
		output = append(output, capsule)
		return json.NewEncoder(writer).Encode(map[string]any{
			"object": "response.compaction", "id": responseID,
			"created_at": time.Now().Unix(), "output": output,
		})
	}
	for sequence, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": capsule},
		{"type": "response.output_item.done", "output_index": 0, "item": capsule},
		{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []json.RawMessage{capsule}}},
	} {
		event["sequence_number"] = sequence
		if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event["type"], mustMarshalJSON(event)); err != nil {
			return err
		}
	}
	return nil
}

// Restore the native timeline once. Codex may carry a subset of the original
// messages alongside the capsule and inject fresh canonical context between
// them. Align those carried items with the snapshot rather than blindly dropping
// the prefix or duplicating every user message. Unmatched current context stays.
func (c *contextCompactor) restore(ctx context.Context, input []json.RawMessage) ([]json.RawMessage, error) {
	type restoredItem struct {
		raw          json.RawMessage
		fromEnvelope bool
	}
	withinBudget := func(items []restoredItem) bool {
		remaining := responsesRequestBufferBytes - 2 // JSON array brackets.
		for index, item := range items {
			if index > 0 {
				remaining-- // JSON array separator.
			}
			if remaining < 0 || len(item.raw) > remaining {
				return false
			}
			remaining -= len(item.raw)
		}
		return true
	}
	var output []restoredItem
	for _, item := range input {

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		retained, local, err := c.open(ctx, item)
		if err != nil {
			return nil, err
		}
		if !local {
			output = append(output, restoredItem{raw: item})
			continue
		}
		retainedItems := make([]restoredItem, len(retained))
		positions := make(map[string][]int, len(retained))
		for index, raw := range retained {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || fields == nil {
				return nil, errors.New("invalid item in retained compaction history")
			}
			if strings.HasPrefix(jsonString(fields, "encrypted_content"), "mekugi.compaction.") || strings.HasPrefix(jsonString(fields, "id"), contextCompactionIDPrefix) {
				return nil, errors.New("nested local compaction envelope is not supported")
			}
			retainedItems[index] = restoredItem{raw: raw, fromEnvelope: true}
			identity := contextCompactionItemIdentity(raw)
			positions[identity] = append(positions[identity], index)
		}
		// Codex retains the newest end of history. Match backwards so a
		// repeated no-ID user message anchors fresh context at its latest
		// occurrence, rather than before an older conflicting instruction.
		matched := make([]int, len(output))
		limit := len(retained)
		for index := len(output) - 1; index >= 0; index-- {
			matched[index] = -1
			carried := output[index].raw
			if !output[index].fromEnvelope && contextCompactionFreshContext(carried) {
				continue
			}
			matches := positions[contextCompactionItemIdentity(carried)]
			match, _ := slices.BinarySearch(matches, limit)
			if match > 0 {
				matched[index] = matches[match-1]
			} else {
				for candidate := range limit {
					if contextCompactionTruncatedMatch(carried, retained[candidate]) {
						if matched[index] >= 0 {
							return nil, errors.New("ambiguous truncated message in compacted history")
						}
						matched[index] = candidate
					}
				}
				var carriedFields map[string]json.RawMessage
				_ = json.Unmarshal(carried, &carriedFields)
				if matched[index] < 0 && (contextCompactionTruncation.Match(carried) || (jsonString(carriedFields, "type") == "message" && jsonString(carriedFields, "role") == "user")) {
					return nil, errors.New("cannot reconcile truncated compacted history without losing context")
				}
			}
			if matched[index] >= 0 {
				limit = matched[index]
			}
		}
		var merged, pending []restoredItem
		cursor := 0
		for position, carried := range output {
			index := matched[position]
			if index < 0 {
				pending = append(pending, carried)
				continue
			}

			merged = append(merged, retainedItems[cursor:index]...)
			merged = append(merged, pending...)
			pending = nil
			merged = append(merged, retainedItems[index])
			cursor = index + 1
		}
		merged = append(merged, retainedItems[cursor:]...)
		candidate := append(merged, pending...)
		if !withinBudget(candidate) {
			return nil, errors.New("restored compaction history exceeds the router buffer budget")
		}
		output = candidate
	}
	if !withinBudget(output) {
		return nil, errors.New("restored compaction history exceeds the router buffer budget")
	}
	result := make([]json.RawMessage, len(output))
	for index, item := range output {
		result[index] = item.raw
	}
	return result, nil

}

// Newly injected canonical context must keep its current position even when
// its text equals an older instruction. Content alone is not an event identity.
func contextCompactionFreshContext(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if jsonString(fields, "type") != "message" {
		return false
	}
	if role := jsonString(fields, "role"); role == "developer" || role == "system" {
		return true
	}
	var metadata struct {
		Kinds []string `json:"content_item_kinds"`
	}
	_ = json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata)
	var content []map[string]json.RawMessage
	_ = json.Unmarshal(fields["content"], &content)
	if len(metadata.Kinds) > 0 {
		// Follow Codex's conservative authorization classification: unknown,
		// incomplete, mixed, and media-preparation input remains real user input.
		return len(metadata.Kinds) == len(content) && !slices.ContainsFunc(metadata.Kinds, func(kind string) bool {
			return kind == "" || kind == "unknown" || strings.HasPrefix(kind, "user.") ||
				kind == "images.preparation_error" || kind == "images.unsupported" || kind == "audio.unsupported"
		})
	}
	// Older clients omit classifications on freshly reinjected context.
	// Source: Codex context/user_instructions.rs and world_state/environment.rs.
	return len(content) > 0 && !slices.ContainsFunc(content, func(part map[string]json.RawMessage) bool {
		text := strings.TrimSpace(jsonString(part, "text"))
		return !((strings.HasPrefix(text, "<environment_context>") && strings.HasSuffix(text, "</environment_context>")) ||
			(strings.HasPrefix(text, "# AGENTS.md instructions") && strings.HasSuffix(text, "</INSTRUCTIONS>")))
	})

}

var contextCompactionTruncation = regexp.MustCompile(`…[0-9]+ tokens truncated…`)

// Verify a client-produced truncation against the authenticated original; do
// not guess the client's budget or reproduce its retention policy.
// Source: Codex compact_remote_v2.rs truncate_message_text_to_token_budget;
// utils/string/src/truncate.rs format_truncation_marker/assemble_truncated_output.
func contextCompactionTruncatedMatch(carried, original json.RawMessage) bool {
	var left, right map[string]json.RawMessage
	_ = json.Unmarshal(carried, &left)
	_ = json.Unmarshal(original, &right)
	if jsonString(left, "type") != "message" || jsonString(right, "type") != "message" ||
		jsonString(left, "role") != jsonString(right, "role") {
		return false
	}
	var leftContent, rightContent []map[string]json.RawMessage
	if json.Unmarshal(left["content"], &leftContent) != nil || json.Unmarshal(right["content"], &rightContent) != nil ||
		len(leftContent) == 0 || len(leftContent) > len(rightContent) {
		return false
	}
	// Truncation can introduce "unknown" content classifications. Other
	// metadata still participates in identity and must not be discarded.
	for _, fields := range []map[string]json.RawMessage{left, right} {
		delete(fields, "content")
		var metadata map[string]json.RawMessage
		if json.Unmarshal(fields["internal_chat_message_metadata_passthrough"], &metadata) == nil {
			var kinds []string
			_ = json.Unmarshal(metadata["content_item_kinds"], &kinds)
			if !slices.ContainsFunc(kinds, func(kind string) bool { return kind != "unknown" }) {
				delete(metadata, "content_item_kinds")
				if len(metadata) == 0 {
					delete(fields, "internal_chat_message_metadata_passthrough")
				} else {
					fields["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(metadata)
				}
			}
		}
	}
	if contextCompactionCanonicalJSON(mustMarshalJSON(left)) != contextCompactionCanonicalJSON(mustMarshalJSON(right)) {
		return false
	}
	shortened := len(leftContent) < len(rightContent)
	for index, part := range leftContent {
		full := rightContent[index]
		leftText, rightText := jsonString(part, "text"), jsonString(full, "text")
		delete(part, "text")
		delete(full, "text")
		if contextCompactionCanonicalJSON(mustMarshalJSON(part)) != contextCompactionCanonicalJSON(mustMarshalJSON(full)) {
			return false
		}
		if leftText == rightText {
			continue
		}
		markers := contextCompactionTruncation.FindAllStringIndex(leftText, -1)
		if len(markers) != 1 {
			return false
		}
		start, end := markers[0][0], markers[0][1]
		head, tail := leftText[:start], leftText[end:]
		if !strings.HasPrefix(rightText, head) || !strings.HasSuffix(rightText, tail) || len(head)+len(tail) >= len(rightText) {
			return false
		}
		shortened = true
	}
	return shortened
}

func contextCompactionItemIdentity(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	kind, role := jsonString(fields, "type"), jsonString(fields, "role")
	if id := jsonString(fields, "id"); id != "" {
		return kind + "\x00" + role + "\x00id:" + id
	}
	if id := jsonString(fields, "call_id"); id != "" {
		return kind + "\x00call:" + id
	}
	return contextCompactionCanonicalJSON(raw)
}

func contextCompactionCanonicalJSON(raw json.RawMessage) string {
	// Codex serializes typed items in a different field order. Use numbers
	// without float conversion so canonicalization cannot merge large IDs.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	return string(mustMarshalJSON(value))
}
