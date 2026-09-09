package router

import (
	"encoding/json"
	"strings"
)

// Observe complete calls, not argument deltas. This is a user-only description
// of a request, never a second executable call or a claim of tool success.
func (t *hpatchResponseTransform) collectSubagentToolCall(item map[string]json.RawMessage) {
	if !t.subagentTurn {
		return
	}
	kind := jsonString(item, "type")
	if !strings.HasSuffix(kind, "_call") {
		return
	}
	if status := jsonString(item, "status"); status != "" && status != "completed" && status != "failed" {
		return
	}
	id := jsonString(item, "id")
	if id == "" || len(id) > maxCommentaryPublicationBytes-len("tool-call\x00") {
		return
	}
	name := jsonString(item, "name")
	if name == "" {
		name = strings.TrimSuffix(kind, "_call")
	}
	name = qualifiedToolName(jsonString(item, "namespace"), name)
	if len(name) > maxCommentaryPublicationBytes {
		return
	}
	text := subagentToolActivityText(item, name)
	t.proxy.activity.collect(t.threadID, "tool-call\x00"+id, "tool", text)
}

func toolActivityPreview(input string) string {
	preview, _, _ := toolActivityPreviewLimit(input, 240)
	return preview
}

func toolActivityPreviewLimit(input string, limit int) (string, int, bool) {
	// Preserve source layout. Inline code would fold newlines in Markdown.
	var preview strings.Builder
	count := 0
	for offset, r := range input {
		if offset >= 4096 || count >= limit {
			preview.WriteString("…")
			return preview.String(), count, true
		}

		preview.WriteRune(r)
		if r != ' ' && r != '\n' && r != '\r' && r != '\t' {
			count++
		}
	}
	return preview.String(), count, false
}

func toolActivityCode(input string) string {
	preview := toolActivityPreview(input)
	if !strings.ContainsAny(preview, "\r\n") {
		return commentaryCode(preview)
	}
	fence := "```"
	for strings.Contains(preview, fence) {
		fence += "`"
	}
	return fence + "\n" + preview + "\n" + fence
}

// Only router-authored, single-operation displays can share a heading.
// Mixed summaries remain intact instead of being grouped by their first action.
func toolActivityGroup(text string) (heading, detail string) {
	for _, label := range []string{
		"Skill Reference Read", "Skill Read", "Read", "Search web", "Search files",
		"Search", "List", "Inspect", "Run JavaScript", "Run code", "Run",
		"Open page", "Find in page", "View image", "Send input", "Edit",
	} {
		for _, separator := range []string{" ", "\n"} {
			if detail, ok := strings.CutPrefix(text, label+separator); ok {
				// Multiline source is fenced; unfenced blank lines separate
				// independently labelled operations inside a single call.
				if strings.HasPrefix(detail, "```") {
					lines := strings.Split(detail, "\n")
					for index, line := range lines[1:] {
						if line == lines[0] && index+2 != len(lines) {
							return "", ""
						}
					}
				} else if strings.Contains(detail, "\n\n") {
					return "", ""
				}

				return label, detail
			}
		}
	}
	return "", ""
}
