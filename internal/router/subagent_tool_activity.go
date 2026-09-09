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

func toolActivityCode(input string) string {
	if !strings.ContainsAny(input, "\r\n") {
		return commentaryCode(input)
	}
	fence := "```"
	for strings.Contains(input, fence) {
		fence += "`"
	}
	return fence + "\n" + input + "\n" + fence
}

// Indent continuation lines so fenced source stays inside its list item.
// Blank lines outside a fence separate classified operations from one call.
func toolActivityNested(text string) string {
	var out strings.Builder
	fence := ""
	newItem := true
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" && fence == "" {
			out.WriteString("\n")
			newItem = true
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		if newItem {
			out.WriteString("- ")
			newItem = false
		} else {
			out.WriteString("  ")
		}
		out.WriteString(line)
		if fence != "" {
			if line == fence {
				fence = ""
			}
		} else if strings.HasPrefix(line, "```") && strings.Trim(line, "`") == "" {
			fence = line
		}
	}
	return out.String()
}
