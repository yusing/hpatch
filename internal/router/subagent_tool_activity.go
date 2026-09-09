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
	text := "Tool call: " + commentaryCode(name)
	// Keep the card scannable even for Code Mode scripts. Collaboration arguments
	// can carry opaque messages; show only the tool identity for those calls.
	if !commentaryExcluded(jsonString(item, "namespace"), jsonString(item, "name")) {
		input := jsonString(item, "arguments")
		if input == "" {
			input = jsonString(item, "input")
		}
		if preview := toolActivityPreview(input); preview != "" {
			text += "\n" + commentaryCode(preview)
		}
	}
	t.proxy.activity.collect(t.threadID, "tool-call\x00"+id, "tool", text)
}

func toolActivityPreview(input string) string {
	// Bound work as well as output; a large script needs only its opening context.
	var preview strings.Builder
	space := false
	count := 0
	for offset, r := range input {
		if offset >= 4096 {
			preview.WriteString("…")
			break
		}
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			space = preview.Len() > 0
			continue
		}
		if count >= 240 {
			preview.WriteString("…")
			break
		}
		if space {
			preview.WriteByte(' ')
			space = false
		}
		preview.WriteRune(r)
		count++
	}
	return preview.String()
}
