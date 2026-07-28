package router

// Source: hpatch_workspace.go:1:100 workspace framing and translated patch rebasing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const hpatchWorkspaceDirective = "workspace_id"

const hpatchWorkspaceToolInstructions = `
Workspace selection:
  The router supplies the current workspace IDs and declared absolute paths in a
  final developer message. When that message lists more than one workspace, a
  complete script must start with one workspace_id WORKSPACE_ID line. With one
  workspace the line is optional. The workspace line is host framing, not an
  hpatch command, and is not included in command numbers. A correction may omit
  it and keeps the rejected script's workspace.
`

func hpatchWorkspaceInstructions(workspaces []routingWorkspace) string {
	var description strings.Builder
	description.WriteString("\nWorkspace selection:\n")
	for _, workspace := range workspaces {
		fmt.Fprintf(&description, "- %s: %q\n", workspace.id, workspace.declared)
	}
	if len(workspaces) == 1 {
		fmt.Fprintf(&description, "A complete script may optionally start with `workspace_id %s`.\n", workspaces[0].id)
	} else {
		description.WriteString("A complete script must start with one `workspace_id WORKSPACE_ID` line.\n")
	}
	description.WriteString("The workspace line is host framing, not an hpatch command, and is not included in command numbers. A correction may omit it and keeps the rejected script's workspace.\n")
	return description.String()
}

func appendHPatchWorkspaceMessage(fields map[string]json.RawMessage, workspaces []routingWorkspace) (string, error) {
	text := hpatchWorkspaceInstructions(workspaces)
	var items []json.RawMessage
	if err := json.Unmarshal(fields["input"], &items); err != nil {
		return "", errors.New("responses request input must be an array for hpatch workspace metadata")
	}
	message, err := json.Marshal(map[string]any{
		"type": "message",
		"role": "developer",
		"content": []map[string]string{{
			"type": "input_text",
			"text": text,
		}},
	})
	if err != nil {
		return "", fmt.Errorf("encode hpatch workspace metadata: %w", err)
	}
	items = append(items, message)
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode Responses input with hpatch workspace metadata: %w", err)
	}
	fields["input"] = encoded
	return text, nil
}

func hpatchWorkspaceDirectivePrefix(input string) string {
	first, _, hasNewline := strings.Cut(input, "\n")
	fields := strings.Fields(strings.TrimSuffix(first, "\r"))
	if !hasNewline || len(fields) != 2 || fields[0] != hpatchWorkspaceDirective {
		return ""
	}
	return input[:len(first)+1]
}

func parseHPatchWorkspaceDirective(input string, workspaces []routingWorkspace) (*routingWorkspace, string, bool, error) {
	first, rest, hasNewline := strings.Cut(input, "\n")
	first = strings.TrimSuffix(first, "\r")
	fields := strings.Fields(first)
	if len(fields) == 0 || fields[0] != hpatchWorkspaceDirective {
		return nil, input, false, nil
	}
	if len(fields) != 2 || !hasNewline {
		return nil, "", false, errorsForHPatchWorkspace("workspace_id must be followed by one workspace ID and a complete script", workspaces)
	}
	for index := range workspaces {
		if workspaces[index].id == fields[1] {
			return &workspaces[index], rest, true, nil
		}
	}
	return nil, "", false, errorsForHPatchWorkspace(fmt.Sprintf("unknown workspace_id %q", fields[1]), workspaces)
}

func errorsForHPatchWorkspace(message string, workspaces []routingWorkspace) error {
	ids := make([]string, len(workspaces))
	for index, workspace := range workspaces {
		ids[index] = workspace.id
	}
	return fmt.Errorf("%s; available workspace IDs: %s", message, strings.Join(ids, ", "))
}

func hpatchWorkspaceByID(workspaces []routingWorkspace, id string) (*routingWorkspace, bool) {
	for index := range workspaces {
		if workspaces[index].id == id {
			return &workspaces[index], true
		}
	}
	return nil, false
}

func rebaseHPatchPatch(patch []byte, root string) ([]byte, error) {
	var rebased strings.Builder
	rebased.Grow(len(patch))
	for part := range strings.SplitAfterSeq(string(patch), "\n") {
		line := strings.TrimSuffix(part, "\n")
		newline := part[len(line):]
		prefix := ""
		for _, candidate := range []string{"*** Add File: ", "*** Delete File: ", "*** Update File: ", "*** Move to: "} {
			if strings.HasPrefix(line, candidate) {
				prefix = candidate
				break
			}
		}
		if prefix == "" {
			rebased.WriteString(part)
			continue
		}
		path := strings.TrimPrefix(line, prefix)
		if !filepath.IsLocal(path) || filepath.Clean(path) == "." {
			return nil, fmt.Errorf("hpatch translation contains non-local path %q", path)
		}
		absolute := filepath.Clean(filepath.Join(root, path))
		if !pathWithin(root, absolute) {
			return nil, fmt.Errorf("hpatch translation path %q escapes workspace", path)
		}
		rebased.WriteString(prefix)
		rebased.WriteString(absolute)
		rebased.WriteString(newline)
	}
	return []byte(rebased.String()), nil
}
