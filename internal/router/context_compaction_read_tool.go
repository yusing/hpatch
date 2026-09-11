package router

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const (
	compactionDocsSearchTool  = "mcp__openaiDeveloperDocs__search_openai_docs"
	compactionDocsFetchTool   = "mcp__openaiDeveloperDocs__fetch_openai_doc"
	compactionDocsOpenAPITool = "mcp__openaiDeveloperDocs__get_openapi_spec"
)

func compactionReadTool(tool string) bool {
	switch tool {
	case compactionDocsSearchTool, compactionDocsFetchTool, compactionDocsOpenAPITool:
		return true
	default:
		return false
	}
}

// compactionRetiredReadToolOutput accepts only the faithful two-part projection
// of a successful MCP CallToolResult. Tool and content metadata stay intact;
// only a recognized documentation body is replaced.
func compactionRetiredReadToolOutput(raw json.RawMessage, tool string) (json.RawMessage, bool) {
	if !compactionReadTool(tool) {
		return nil, false
	}

	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) != 2 ||
		jsonString(parts[0], "type") != "input_text" ||
		!strings.HasPrefix(jsonString(parts[0], "text"), "Script completed\n") ||
		jsonString(parts[1], "type") != "input_text" {
		return nil, false
	}

	var serialized string
	if json.Unmarshal(parts[1]["text"], &serialized) != nil {
		return nil, false
	}

	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(serialized), &result) != nil || result == nil {
		return compactionRetiredTruncatedReadToolOutput(parts, serialized, tool)
	}
	if rawError, exists := result["isError"]; exists {
		var isError bool
		if json.Unmarshal(rawError, &isError) != nil || isError {
			return nil, false
		}
	}

	var content []map[string]json.RawMessage
	if json.Unmarshal(result["content"], &content) != nil || len(content) == 0 {
		return nil, false
	}

	changed := false
	for index, block := range content {
		if jsonString(block, "type") != "text" {
			return nil, false
		}

		var body string
		if json.Unmarshal(block["text"], &body) != nil {
			return nil, false
		}

		reduced, ok := compactionRetiredReadToolText(tool, body)
		if !ok {
			continue
		}
		content[index] = maps.Clone(block)
		content[index]["text"] = mustMarshalJSON(reduced)
		changed = true
	}
	if !changed {
		return raw, true
	}

	result = maps.Clone(result)
	result["content"] = mustMarshalJSON(content)
	parts[1] = maps.Clone(parts[1])
	parts[1]["text"] = mustMarshalJSON(string(mustMarshalJSON(result)))
	return mustMarshalJSON(parts), true
}

var compactionTruncatedReadBody = regexp.MustCompile(`\\"(?:snippet|snippets|body|content|text|markdown|highlights)\\"[ \t]*:[ \t]*\\"`)

// compactionRetiredTruncatedReadToolOutput repairs no unknown structure. It
// accepts one client truncation only when that marker is wholly inside a
// recognized escaped documentation-body string and replacing that complete
// value makes the original CallToolResult parse again.
func compactionRetiredTruncatedReadToolOutput(parts []map[string]json.RawMessage, serialized, tool string) (json.RawMessage, bool) {
	markers := contextCompactionTruncation.FindAllStringIndex(serialized, -1)
	if len(markers) != 1 {
		return nil, false
	}
	marker := markers[0]
	for _, match := range compactionTruncatedReadBody.FindAllStringIndex(serialized, -1) {
		start := match[1]
		end := compactionEscapedJSONStringEnd(serialized, start)
		if end < 0 || marker[0] <= start || marker[1] >= end {
			continue
		}
		evidence, ok := compactionTruncatedEscapedBodyEvidence(serialized[start:end], marker[0]-start, marker[1]-start)
		if !ok {
			continue
		}
		replacement := fmt.Sprintf(
			"[mekugi compaction: client-truncated historical documentation body had %d retained encoded bytes around an unavailable middle; unambiguous body span omitted]\n%s",
			end-start, evidence)
		encoded, ok := compactionEncodeEscapedJSONStringBody(replacement)
		if !ok || len(encoded) >= end-start {
			continue
		}
		repaired := serialized[:start] + encoded + serialized[end:]
		objectStart := strings.IndexByte(repaired, '{')
		if objectStart < 0 {
			continue
		}
		var result map[string]json.RawMessage
		if json.Unmarshal([]byte(repaired[objectStart:]), &result) != nil || result == nil {
			continue
		}
		if rawError, exists := result["isError"]; exists {
			var isError bool
			if json.Unmarshal(rawError, &isError) != nil || isError {
				continue
			}
		}
		var content []map[string]json.RawMessage
		if json.Unmarshal(result["content"], &content) != nil || len(content) == 0 {
			continue
		}
		for _, block := range content {
			if jsonString(block, "type") != "text" {
				return nil, false
			}
			var body string
			if json.Unmarshal(block["text"], &body) != nil {
				return nil, false
			}
		}
		repaired = repaired[:objectStart] + string(mustMarshalJSON(result))
		copyParts := slices.Clone(parts)
		copyParts[len(copyParts)-1] = maps.Clone(copyParts[len(copyParts)-1])
		copyParts[len(copyParts)-1]["text"] = mustMarshalJSON(repaired)
		return mustMarshalJSON(copyParts), true
	}
	return nil, false
}

func compactionEscapedJSONStringEnd(text string, start int) int {
	for index := start; index < len(text); index++ {
		if text[index] != '"' {
			continue
		}
		backslashes := 0
		for previous := index - 1; previous >= start && text[previous] == '\\'; previous-- {
			backslashes++
		}
		if backslashes == 1 {
			return index - 1
		}
	}
	return -1
}

func compactionDecodeEscapedJSONStringBody(raw string) (string, bool) {
	var nested string
	if json.Unmarshal([]byte(`"`+raw+`"`), &nested) != nil {
		return "", false
	}
	var body string
	return body, json.Unmarshal([]byte(`"`+nested+`"`), &body) == nil
}

func compactionTruncatedEscapedBodyEvidence(raw string, markerStart, markerEnd int) (string, bool) {
	if markerStart <= 0 || markerEnd <= markerStart || markerEnd >= len(raw) {
		return "", false
	}
	prefixEnd := strings.LastIndex(raw[:markerStart], `\\n`)
	suffixOffset := strings.Index(raw[markerEnd:], `\\n`)
	if prefixEnd < 0 || suffixOffset < 0 {
		return "", false
	}
	prefixEnd += len(`\\n`)
	suffixStart := markerEnd + suffixOffset + len(`\\n`)
	prefix, prefixOK := compactionDecodeEscapedJSONStringBody(raw[:prefixEnd])
	suffix, suffixOK := compactionDecodeEscapedJSONStringBody(raw[suffixStart:])
	if !prefixOK || !suffixOK {
		return "", false
	}

	var result strings.Builder
	result.WriteString(compactionReadBodyEvidence(prefix))
	result.WriteString("[mekugi: exact encoded truncation-boundary line follows]\n")
	result.WriteString(raw[prefixEnd:suffixStart])
	result.WriteByte('\n')
	result.WriteString(compactionReadBodyEvidence(suffix))
	return result.String(), true
}

func compactionEncodeEscapedJSONStringBody(body string) (string, bool) {
	nested := string(mustMarshalJSON(body))
	nested = nested[1 : len(nested)-1]
	outer := string(mustMarshalJSON(nested))
	if len(outer) < 2 {
		return "", false
	}
	return outer[1 : len(outer)-1], true
}

func compactionRetiredReadToolText(tool, body string) (string, bool) {
	switch tool {
	case compactionDocsSearchTool:
		if result, ok := compactionRetiredSearchResults(body); ok {
			return compactionSuccessfulReadHeader(len(body), "search result") + result, true
		}
	case compactionDocsFetchTool:
		if result, ok := compactionRetiredDocumentText(body); ok {
			return compactionSuccessfulReadHeader(len(body), "fetched document") + result, true
		}
	case compactionDocsOpenAPITool:
		if result, ok := compactionRetiredOpenAPI(body); ok {
			return compactionSuccessfulReadHeader(len(body), "OpenAPI document") + result, true
		}
	}
	return "", false
}

func compactionSuccessfulReadHeader(originalBytes int, kind string) string {
	return fmt.Sprintf("[mekugi compaction: successful read-tool return; %s was %d original bytes; unmarked historical body omitted and not currently retrievable]\n", kind, originalBytes)
}

func compactionRetiredSearchResults(text string) (string, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &object) == nil && object != nil {
		for _, key := range []string{"results", "hits", "data", "items", "documents"} {
			raw, exists := object[key]
			if !exists {
				continue
			}
			reduced, ok := compactionRetiredSearchItems(raw)
			if !ok {
				return "", false
			}
			object = maps.Clone(object)
			object[key] = reduced
			return string(mustMarshalJSON(object)), true
		}
		return "", false
	}

	var items []map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &items) != nil {
		return "", false
	}
	reduced, ok := compactionRetiredSearchItemMaps(items)
	if !ok {
		return "", false
	}
	return string(reduced), true
}

func compactionRetiredSearchItems(raw json.RawMessage) (json.RawMessage, bool) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return compactionRetiredSearchItemMaps(items)
}

func compactionRetiredSearchItemMaps(items []map[string]json.RawMessage) (json.RawMessage, bool) {
	if len(items) == 0 {
		return nil, false
	}

	changed := false
	for index, item := range items {
		identified := false
		for _, key := range []string{"url", "source_url", "title", "id", "objectID", "citation", "citation_id", "source"} {
			if raw, exists := item[key]; exists && len(raw) > 0 && string(raw) != "null" {
				identified = true
				break
			}
		}
		if !identified {
			return nil, false
		}

		reduced := maps.Clone(item)
		itemChanged := false
		for _, key := range []string{"snippet", "snippets", "body", "content", "text", "markdown", "highlights"} {
			raw, exists := item[key]
			if !exists || len(raw) < 256 {
				continue
			}
			body, ok := compactionRetiredReadJSONBody(raw)
			if !ok {
				return nil, false
			}
			reduced[key] = body
			itemChanged = true
		}
		for _, key := range []string{"_snippetResult", "_highlightResult"} {
			raw, exists := item[key]
			if !exists {
				continue
			}
			annotations, ok := compactionRetiredSearchAnnotations(raw)
			if !ok {
				continue
			}
			reduced[key] = annotations
			itemChanged = true
		}
		if itemChanged {
			items[index] = reduced
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	return mustMarshalJSON(items), true
}

func compactionRetiredSearchAnnotations(raw json.RawMessage) (json.RawMessage, bool) {
	var annotations map[string]json.RawMessage
	if json.Unmarshal(raw, &annotations) != nil || annotations == nil {
		return nil, false
	}

	reduced := maps.Clone(annotations)
	changed := false
	for _, key := range []string{"snippet", "body", "content", "text", "markdown"} {
		value, exists := annotations[key]
		if !exists {
			continue
		}
		retired, ok := compactionRetiredSearchAnnotationValue(value)
		if !ok {
			continue
		}
		reduced[key] = retired
		changed = true
	}
	if !changed {
		return nil, false
	}
	return mustMarshalJSON(reduced), true
}

func compactionRetiredSearchAnnotationValue(raw json.RawMessage) (json.RawMessage, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if len(text) < 256 {
			return nil, false
		}
		return mustMarshalJSON(compactionRetiredReadBodyString(text)), true
	}

	var annotation map[string]json.RawMessage
	if json.Unmarshal(raw, &annotation) == nil && annotation != nil {
		value, exists := annotation["value"]
		if !exists {
			return nil, false
		}
		var text string
		if json.Unmarshal(value, &text) != nil || len(text) < 256 {
			return nil, false
		}
		reduced := maps.Clone(annotation)
		reduced["value"] = mustMarshalJSON(compactionRetiredReadBodyString(text))
		return mustMarshalJSON(reduced), true
	}

	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, false
	}
	changed := false
	for index, entry := range entries {
		reduced, ok := compactionRetiredSearchAnnotationValue(entry)
		if !ok {
			continue
		}
		entries[index] = reduced
		changed = true
	}
	if !changed {
		return nil, false
	}
	return mustMarshalJSON(entries), true
}

func compactionRetiredReadJSONBody(raw json.RawMessage) (json.RawMessage, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return mustMarshalJSON(compactionRetiredReadBodyString(text)), true
	}

	var stringsOnly []string
	if json.Unmarshal(raw, &stringsOnly) == nil && len(stringsOnly) > 0 {
		for index, text := range stringsOnly {
			stringsOnly[index] = compactionRetiredReadBodyString(text)
		}
		return mustMarshalJSON(stringsOnly), true
	}

	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) == nil && len(entries) > 0 {
		changed := false
		for index, entry := range entries {
			reduced, ok := compactionRetiredReadBodyObject(entry)
			if !ok {
				continue
			}
			entries[index] = reduced
			changed = true
		}
		if !changed {
			return nil, false
		}
		return mustMarshalJSON(entries), true
	}

	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		reduced, ok := compactionRetiredReadBodyObject(object)
		if ok {
			return mustMarshalJSON(reduced), true
		}
	}
	return nil, false
}

func compactionRetiredReadBodyObject(object map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	reduced := maps.Clone(object)
	changed := false
	for _, key := range []string{"snippet", "body", "content", "text", "markdown", "highlight"} {
		raw, exists := object[key]
		if !exists || len(raw) < 64 {
			continue
		}

		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, false
		}
		reduced[key] = mustMarshalJSON(compactionRetiredReadBodyString(text))
		changed = true
	}
	return reduced, changed
}

func compactionRetiredReadBodyString(text string) string {
	return fmt.Sprintf("[mekugi compaction: historical document body retired (%d original bytes); selected provenance and diagnostics follow]\n%s",
		len(text), compactionReadBodyEvidence(text))
}

func compactionRetiredDocumentText(text string) (string, bool) {
	if len(text) < 512 {
		return "", false
	}
	evidence := compactionReadBodyEvidence(text)
	if evidence == "" {
		return "", false
	}
	return evidence, true
}

func compactionReadBodyEvidence(text string) string {
	lines := strings.SplitAfter(text, "\n")
	keep := make([]bool, len(lines))
	inFrontmatter := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if index == 0 && trimmed == "---" {
			inFrontmatter = true
		}
		if inFrontmatter {
			keep[index] = true
			if index > 0 && trimmed == "---" {
				inFrontmatter = false
			}
			continue
		}

		if strings.HasPrefix(trimmed, "#") ||
			strings.Contains(line, "https://") || strings.Contains(line, "http://") ||
			strings.Contains(line, "【") && strings.Contains(line, "】") ||
			strings.HasPrefix(lower, "source:") || strings.HasPrefix(lower, "url:") ||
			strings.HasPrefix(lower, "citation:") || strings.HasPrefix(lower, "title:") ||
			strings.HasPrefix(lower, "retrieved:") || strings.HasPrefix(lower, "updated:") ||
			strings.HasPrefix(lower, "published:") {
			keep[index] = true
		}
		if compactionDiagnostic.MatchString(line) {
			for nearby := max(0, index-2); nearby < min(len(lines), index+3); nearby++ {
				keep[nearby] = true
			}
		}
	}

	var result strings.Builder
	for index, line := range lines {
		if keep[index] {
			result.WriteString(line)
		}
	}
	return result.String()
}

func compactionRetiredOpenAPI(text string) (string, bool) {
	var document map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &document) != nil || document == nil {
		return "", false
	}
	if jsonString(document, "openapi") == "" && jsonString(document, "swagger") == "" {
		return "", false
	}

	var paths map[string]json.RawMessage
	if json.Unmarshal(document["paths"], &paths) != nil || paths == nil {
		return "", false
	}

	changed := false
	reducedPaths := maps.Clone(paths)
	for path, raw := range paths {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return "", false
		}

		reducedItem := maps.Clone(item)
		for key, value := range item {
			if compactionOpenAPIMethod(key) {
				var operation map[string]json.RawMessage
				if json.Unmarshal(value, &operation) != nil || operation == nil {
					return "", false
				}
				reducedOperation := maps.Clone(operation)
				for _, bodyKey := range []string{"description", "parameters", "requestBody", "responses", "callbacks"} {
					body, exists := operation[bodyKey]
					if !exists || len(body) < 64 {
						continue
					}
					reducedOperation[bodyKey] = mustMarshalJSON(fmt.Sprintf(
						"[mekugi compaction: historical OpenAPI %s retired (%d serialized bytes)]", bodyKey, len(body)))
					changed = true
				}
				reducedItem[key] = mustMarshalJSON(reducedOperation)
				continue
			}
			if key == "description" || key == "parameters" {
				if len(value) >= 64 {
					reducedItem[key] = mustMarshalJSON(fmt.Sprintf(
						"[mekugi compaction: historical OpenAPI path %s retired (%d serialized bytes)]", key, len(value)))
					changed = true
				}
			}
		}
		reducedPaths[path] = mustMarshalJSON(reducedItem)
	}

	reduced := maps.Clone(document)
	reduced["paths"] = mustMarshalJSON(reducedPaths)
	for _, key := range []string{"components", "webhooks"} {
		raw, exists := document[key]
		if !exists || len(raw) < 64 {
			continue
		}
		reduced[key] = mustMarshalJSON(fmt.Sprintf(
			"[mekugi compaction: historical OpenAPI %s retired (%d serialized bytes)]", key, len(raw)))
		changed = true
	}
	if !changed {
		return "", false
	}
	return string(mustMarshalJSON(reduced)), true
}

func compactionOpenAPIMethod(value string) bool {
	switch value {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}
