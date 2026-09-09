package router

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var (
	compactionNarrationPath = regexp.MustCompile(`(?:^|[ \t(\["'])(?:[.~]/|[A-Za-z0-9_.-]+/)[^ \t\r\n)\]"']+`)
)

// reduceContextCompactionNarration shortens only older assistant-authored
// prose. Authority-bearing roles and the recent operation frontier stay exact.
func reduceContextCompactionNarration(input []json.RawMessage) []json.RawMessage {
	cutoff := contextCompactionMetadataRecentCutoff(input)
	if cutoff == 0 {
		return input
	}

	callIDs := make(map[string]bool)
	assistantIDs := make(map[string]bool)
	fields := make([]map[string]json.RawMessage, len(input))
	for index, raw := range input {
		if json.Unmarshal(raw, &fields[index]) != nil {
			continue
		}
		kind, id := jsonString(fields[index], "type"), jsonString(fields[index], "call_id")
		switch kind {
		case "function_call", "custom_tool_call":
			if id != "" {
				callIDs[id] = true
			}
		}
		if index < cutoff && kind == "message" && jsonString(fields[index], "role") == "assistant" {
			if itemID := jsonString(fields[index], "id"); itemID != "" && compactionCallID.MatchString(itemID) {
				assistantIDs[itemID] = true
			}
		}
	}
	referencedAssistantIDs, unsafeAssistantIDReference := contextCompactionReferencedNarrationIDs(fields, assistantIDs)

	type occurrence struct {
		index int
		text  string
	}
	var occurrences []occurrence
	for index := 0; index < cutoff; index++ {
		if contextCompactionNarrationKind(fields[index]) {
			for _, text := range contextCompactionNarrationTexts(fields[index]["content"]) {
				occurrences = append(occurrences, occurrence{index: index, text: text})
			}
		}
	}
	later := make(map[string]int)
	for _, occurrence := range occurrences {
		later[contextCompactionNarrationKey(fields[occurrence.index], occurrence.text)] = occurrence.index
	}

	output := input
	cloned := false
	for index := 0; index < cutoff; index++ {
		if !contextCompactionNarrationKind(fields[index]) {
			continue
		}
		mapped, contentChanged := contextCompactionMapNarration(fields[index]["content"], func(text string) string {
			if text == "" || contextCompactionNarrationReferences(text, callIDs) {
				return text
			}
			key := contextCompactionNarrationKey(fields[index], text)
			if later[key] > index {
				return contextCompactionNarrationReplacement(text,
					"[hpatch: exact repeated historical narration omitted; later identical occurrence retained]")
			}
			return text
		})
		removeID := false
		if jsonString(fields[index], "type") == "message" && jsonString(fields[index], "role") == "assistant" {
			id := jsonString(fields[index], "id")
			removeID = id != "" && compactionCallID.MatchString(id) && !unsafeAssistantIDReference && !referencedAssistantIDs[id]
		}
		if !contentChanged && !removeID {
			continue
		}
		next := maps.Clone(fields[index])
		if contentChanged {
			next["content"] = mapped
		}
		if removeID {
			delete(next, "id")
		}
		if !cloned {
			output = slices.Clone(input)
			cloned = true
		}
		output[index] = mustMarshalJSON(next)
	}
	return output
}

func contextCompactionReferencedNarrationIDs(fields []map[string]json.RawMessage, assistantIDs map[string]bool) (map[string]bool, bool) {
	referenced := make(map[string]bool)
	unsafeEncoding := false
	for _, item := range fields {
		for key, raw := range item {
			if key == "id" || key == "call_id" || key == "turn_id" || key == "create_time" || key == "encrypted_content" {
				continue
			}
			compactionVisitReferenceStrings(raw, func(text string) {
				compactionSourceVisitDecodedReferences(text, func(decoded string) {
					for _, word := range compactionReferenceWord.FindAllString(decoded, -1) {
						if assistantIDs[word] {
							referenced[word] = true
						}
					}
				}, &unsafeEncoding)
			})
		}
	}
	return referenced, unsafeEncoding
}

func contextCompactionNarrationKind(fields map[string]json.RawMessage) bool {
	kind, role := jsonString(fields, "type"), jsonString(fields, "role")
	// Codex V2 may carry agent_message items. A no-ID item is reconciled by
	// canonical content, so rewriting even exactly repeated agent prose can
	// destroy its restoration identity. Ordinary assistant messages are not
	// carried by the supported local continuation path.
	return kind == "message" && role == "assistant"
}

func contextCompactionNarrationTexts(raw json.RawMessage) []string {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		return []string{direct}
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var result []string
	for _, part := range parts {
		var text string
		if json.Unmarshal(part["text"], &text) == nil {
			result = append(result, text)
		}
	}
	return result
}

func contextCompactionMapNarration(raw json.RawMessage, mapText func(string) string) (json.RawMessage, bool) {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		mapped := mapText(direct)
		return mustMarshalJSON(mapped), mapped != direct
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return raw, false
	}
	changed := false
	for index, part := range parts {
		var text string
		if json.Unmarshal(part["text"], &text) != nil {
			continue
		}
		mapped := mapText(text)
		if mapped == text {
			continue
		}
		parts[index] = maps.Clone(part)
		parts[index]["text"] = mustMarshalJSON(mapped)
		changed = true
	}
	if !changed {
		return raw, false
	}
	return mustMarshalJSON(parts), true
}

func contextCompactionNarrationKey(fields map[string]json.RawMessage, text string) string {
	return jsonString(fields, "type") + "\x00" + jsonString(fields, "role") + "\x00" + text
}

func contextCompactionNarrationReplacement(original, replacement string) string {
	if len(replacement) >= len(original) {
		return original
	}
	before, beforeOK := compactionVisibleStringTokens(mustMarshalJSON(original))
	after, afterOK := compactionVisibleStringTokens(mustMarshalJSON(replacement))
	if !beforeOK || !afterOK || after >= before {
		return original
	}
	return replacement
}

func contextCompactionNarrationReferences(text string, callIDs map[string]bool) bool {
	if strings.Contains(text, "`") || strings.Contains(text, "http://") || strings.Contains(text, "https://") ||
		compactionNarrationPath.MatchString(text) {
		return true
	}
	unsafeEncoding, referenced := false, false
	compactionSourceVisitDecodedReferences(text, func(decoded string) {
		if compactionRowReference.MatchString(decoded) || compactionSourceRangeReference.MatchString(decoded) ||
			compactionScriptReference.MatchString(decoded) {
			referenced = true
		}
		for _, word := range compactionReferenceWord.FindAllString(decoded, -1) {
			if callIDs[word] {
				referenced = true
			}
		}
	}, &unsafeEncoding)
	return referenced || unsafeEncoding
}
