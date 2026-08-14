// Package patchtest applies the OpenAI apply_patch subset emitted and compared by
// this project. It is test and benchmark support, not an installed runtime surface.
package patchtest

import (
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
)

var hunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Apply returns a cloned tree after applying one patch envelope.
func Apply(initial map[string]string, patch string) (map[string]string, error) {
	tree := make(map[string]string, len(initial))
	maps.Copy(tree, initial)
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return nil, fmt.Errorf("invalid patch envelope")
	}

	for index := 1; index < len(lines)-1; {
		line := lines[index]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimPrefix(line, "*** Add File: ")
			index++
			var content []string
			for index < len(lines)-1 && !strings.HasPrefix(lines[index], "*** ") {
				if !strings.HasPrefix(lines[index], "+") {
					return nil, fmt.Errorf("invalid add line %q", lines[index])
				}
				content = append(content, strings.TrimPrefix(lines[index], "+"))
				index++
			}
			if _, exists := tree[path]; exists {
				return nil, fmt.Errorf("add destination %s exists", path)
			}
			tree[path] = strings.Join(content, "\n")

		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimPrefix(line, "*** Delete File: ")
			if _, exists := tree[path]; !exists {
				return nil, fmt.Errorf("delete source %s is missing", path)
			}
			delete(tree, path)
			index++

		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(line, "*** Update File: ")
			content, exists := tree[path]
			if !exists {
				return nil, fmt.Errorf("update source %s is missing", path)
			}
			index++
			destination := path
			if index < len(lines)-1 && strings.HasPrefix(lines[index], "*** Move to: ") {
				destination = strings.TrimPrefix(lines[index], "*** Move to: ")
				index++
			}
			var hunkLines []string
			for index < len(lines)-1 && !strings.HasPrefix(lines[index], "*** ") {
				hunkLines = append(hunkLines, lines[index])
				index++
			}
			updated, err := applyHunks(content, hunkLines)
			if err != nil {
				return nil, fmt.Errorf("updating %s: %w", path, err)
			}
			delete(tree, path)
			if destination != path {
				if _, occupied := tree[destination]; occupied {
					return nil, fmt.Errorf("move destination %s exists", destination)
				}
			}
			tree[destination] = updated

		default:
			return nil, fmt.Errorf("unknown patch action %q", line)
		}
	}
	return tree, nil
}

func applyHunks(content string, patch []string) (string, error) {
	current := strings.Split(content, "\n")
	for index := 0; index < len(patch); {
		if !strings.HasPrefix(patch[index], "@@") {
			return "", fmt.Errorf("expected hunk, got %q", patch[index])
		}
		hint := 0
		if match := hunkPattern.FindStringSubmatch(patch[index]); match != nil {
			hint, _ = strconv.Atoi(match[1])
			hint--
		}
		index++
		var oldLines, newLines []string
		for index < len(patch) && !strings.HasPrefix(patch[index], "@@") {
			line := patch[index]
			if line == "" || (line[0] != ' ' && line[0] != '+' && line[0] != '-') {
				return "", fmt.Errorf("invalid hunk line %q", line)
			}
			if line[0] != '+' {
				oldLines = append(oldLines, line[1:])
			}
			if line[0] != '-' {
				newLines = append(newLines, line[1:])
			}
			index++
		}
		position := findLines(current, oldLines, hint)
		if position < 0 {
			return "", fmt.Errorf("hunk context not found")
		}
		replacement := make([]string, 0, len(current)-len(oldLines)+len(newLines))
		replacement = append(replacement, current[:position]...)
		replacement = append(replacement, newLines...)
		replacement = append(replacement, current[position+len(oldLines):]...)
		current = replacement
	}
	return strings.Join(current, "\n"), nil
}

func findLines(content, sought []string, hint int) int {
	if hint >= 0 && hint+len(sought) <= len(content) && equalLines(content[hint:hint+len(sought)], sought) {
		return hint
	}
	for start := 0; start+len(sought) <= len(content); start++ {
		if equalLines(content[start:start+len(sought)], sought) {
			return start
		}
	}
	return -1
}

func equalLines(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
