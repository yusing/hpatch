package hpatch

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

func translate(changes []change) (string, error) {
	if len(changes) == 0 {
		return "", fmt.Errorf("script does not change the workspace")
	}

	var patch strings.Builder
	patch.WriteString("*** Begin Patch\n")
	for _, change := range changes {
		switch change.kind {
		case changeAdd:
			writeAddition(&patch, change.path, change.content)
		case changeDelete:
			fmt.Fprintf(&patch, "*** Delete File: %s\n", change.originalPath)
		case changeUpdate:
			if err := writeUpdate(&patch, change); err != nil {
				return "", err
			}
		}
	}
	patch.WriteString("*** End Patch\n")
	return patch.String(), nil
}

func writeAddition(patch *strings.Builder, path, content string) {
	fmt.Fprintf(patch, "*** Add File: %s\n", path)
	content = normalizeLineEndings(content)
	for line := range strings.SplitSeq(content, "\n") {
		patch.WriteByte('+')
		patch.WriteString(line)
		patch.WriteByte('\n')
	}
}

func writeUpdate(patch *strings.Builder, change change) error {
	fmt.Fprintf(patch, "*** Update File: %s\n", change.originalPath)
	if change.path != change.originalPath {
		fmt.Fprintf(patch, "*** Move to: %s\n", change.path)
	}

	if change.original == change.content {
		writeMoveVerification(patch, change.original)
		return nil
	}

	diff, err := unambiguousDiff(change)
	if err != nil {
		return err
	}
	firstNewline := strings.IndexByte(diff, '\n')
	if firstNewline < 0 {
		return fmt.Errorf("rendering update for %s produced no header", change.originalPath)
	}
	secondRelative := strings.IndexByte(diff[firstNewline+1:], '\n')
	if secondRelative < 0 {
		return fmt.Errorf("rendering update for %s produced no hunks", change.originalPath)
	}
	hunks := diff[firstNewline+1+secondRelative+1:]
	if !strings.HasPrefix(hunks, "@@") {
		return fmt.Errorf("rendering update for %s produced no hunks", change.originalPath)
	}
	hunks = normalizeHunkHeaders(hunks)
	patch.WriteString(hunks)
	if !strings.HasSuffix(hunks, "\n") {
		patch.WriteByte('\n')
	}
	return nil
}

func unambiguousDiff(change change) (string, error) {
	original := normalizeLineEndings(change.original)
	result := normalizeLineEndings(change.content)
	lineCount := len(strings.Split(original, "\n"))
	for contextLines := 3; ; contextLines = min(contextLines*2, lineCount) {
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(original),
			B:        difflib.SplitLines(result),
			FromFile: change.originalPath,
			ToFile:   change.path,
			Context:  contextLines,
		})
		if err != nil {
			return "", fmt.Errorf("rendering update for %s: %w", change.originalPath, err)
		}
		if diffHunksAreUnique(original, diff) {
			return diff, nil
		}
		if contextLines == lineCount {
			return "", fmt.Errorf("rendering update for %s produced ambiguous hunks", change.originalPath)
		}
	}
}

func diffHunksAreUnique(original, diff string) bool {
	lines := strings.Split(diff, "\n")
	originalLines := strings.Split(original, "\n")
	for index := 2; index < len(lines); {
		if !strings.HasPrefix(lines[index], "@@") {
			index++
			continue
		}
		index++
		var sought []string
		for index < len(lines) && !strings.HasPrefix(lines[index], "@@") {
			line := lines[index]
			if line != "" && line[0] != '+' {
				sought = append(sought, line[1:])
			}
			index++
		}
		if countLineSequence(originalLines, sought) != 1 {
			return false
		}
	}
	return true
}

func countLineSequence(lines, sought []string) int {
	count := 0
	for start := 0; start+len(sought) <= len(lines); start++ {
		if slices.Equal(lines[start:start+len(sought)], sought) {
			count++
		}
	}
	return count
}

func normalizeLineEndings(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func normalizeHunkHeaders(hunks string) string {
	lines := strings.Split(hunks, "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			lines[index] = "@@"
		}
	}
	return strings.Join(lines, "\n")
}

func writeMoveVerification(patch *strings.Builder, content string) {
	content = normalizeLineEndings(content)
	patch.WriteString("@@\n")
	if content == "" {
		patch.WriteString("-\n+\n")
		return
	}
	line, _, _ := strings.Cut(content, "\n")
	patch.WriteByte(' ')
	patch.WriteString(line)
	patch.WriteByte('\n')
}
