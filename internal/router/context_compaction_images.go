package router

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// Ordinary historical images are intentionally discarded during compaction.
// Fresh request images and mandatory instruction content are not rewritten.
const compactionImagePlaceholder = "[Image]"

func compactionImageParts(item map[string]json.RawMessage) (string, []json.RawMessage) {
	key := ""
	switch jsonString(item, "type") {
	case "message", "agent_message":
		key = "content"
	case "function_call_output", "custom_tool_call_output":
		key = "output"
	}
	var parts []json.RawMessage
	if key != "" {
		_ = json.Unmarshal(item[key], &parts)
	}
	return key, parts
}

func compactionImageURL(raw json.RawMessage) string {
	var part map[string]json.RawMessage
	if json.Unmarshal(raw, &part) == nil && jsonString(part, "type") == "input_image" {
		return jsonString(part, "image_url")
	}
	return ""
}

func compactionImageUsage(items []json.RawMessage) (count, bytes int) {
	for _, raw := range items {
		var item map[string]json.RawMessage
		_ = json.Unmarshal(raw, &item)
		_, parts := compactionImageParts(item)
		for _, part := range parts {
			if url := compactionImageURL(part); url != "" {
				count++
				bytes += len(url)
			}
		}
	}
	return count, bytes
}

func stripCompactionImages(ctx context.Context, input []json.RawMessage) ([]json.RawMessage, bool, error) {
	result := slices.Clone(input)
	changed := false
	for index, raw := range input {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return nil, false, fmt.Errorf("invalid image history item")
		}
		if contextCompactionFreshContext(raw) {
			continue
		}
		key, parts := compactionImageParts(item)
		itemChanged := false
		for partIndex, raw := range parts {
			var part map[string]json.RawMessage
			if json.Unmarshal(raw, &part) != nil || jsonString(part, "type") != "input_image" {
				continue
			}
			textType := "input_text"
			if jsonString(item, "type") == "message" && jsonString(item, "role") == "assistant" {
				textType = "output_text"
			}
			parts[partIndex] = mustMarshalJSON(map[string]string{"type": textType, "text": compactionImagePlaceholder})
			var metadata map[string]json.RawMessage
			if json.Unmarshal(item["internal_chat_message_metadata_passthrough"], &metadata) == nil && metadata != nil {
				var kinds []string
				if json.Unmarshal(metadata["content_item_kinds"], &kinds) == nil && len(kinds) == len(parts) {
					kinds[partIndex] = "unknown"
					metadata["content_item_kinds"] = mustMarshalJSON(kinds)
					item["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(metadata)
				}
			}
			itemChanged = true
		}
		if itemChanged {
			item[key] = mustMarshalJSON(parts)
			result[index] = mustMarshalJSON(item)
			changed = true
		}
	}
	return result, changed, nil
}
