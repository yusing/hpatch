package hpatch

import (
	"sort"
	"strings"
)

type whitespaceDeletion struct {
	start int
	end   int
}

const gitBinaryProbeSize = 8000

// fixChangedLineWhitespace removes Git-default whitespace errors only from
// lines introduced by the evaluated script.
func fixChangedLineWhitespace(content string, edits []baselineEdit, offsets *formattedOffsetMap) (string, []whitespaceDeletion) {
	lines := logicalLines(content)
	changed := make([]bool, len(lines))
	markReplacementResultLines(changed, lines, edits, offsets)

	var deletions []whitespaceDeletion
	for index, line := range lines {
		if !changed[index] {
			continue
		}
		deletions = append(deletions, trailingWhitespaceDeletion(content, line)...)
		deletions = append(deletions, spaceBeforeTabDeletions(content, line)...)
	}

	// A blank line is at EOF when every physical line after it is also blank.
	// Delete only lines attributed to an edit, retaining pre-existing blank
	// lines outside effective replacement spans.
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		if !onlyHorizontalWhitespace(content[line.start:line.contentEnd]) {
			break
		}
		if changed[index] {
			deletions = append(deletions, whitespaceDeletion{start: line.start, end: line.fullEnd})
		}
	}

	deletions = mergeWhitespaceDeletions(deletions)
	if len(deletions) == 0 {
		return content, nil
	}

	result := make([]byte, 0, len(content))
	cursor := 0
	for _, deletion := range deletions {
		result = append(result, content[cursor:deletion.start]...)
		cursor = deletion.end
	}
	result = append(result, content[cursor:]...)
	return string(result), deletions
}

func isGitDefaultBinary(content string) bool {
	return strings.IndexByte(content[:min(len(content), gitBinaryProbeSize)], 0) >= 0
}

func markReplacementResultLines(changed []bool, lines []logicalLine, edits []baselineEdit, offsets *formattedOffsetMap) {
	baselineOffset := 0
	renderedOffset := 0
	for _, edit := range orderedBaselineEdits(edits) {
		renderedOffset += edit.start - baselineOffset
		start := renderedOffset
		end := start + len(edit.replacement)
		switch {
		case start != end:
			start = offsets.mapOffset(start)
			end = offsets.mapOffset(end)
			if end < start {
				start, end = end, start
			}
			first := sort.Search(len(lines), func(index int) bool {
				return lines[index].fullEnd > start
			})
			for index := first; index < len(lines) && lines[index].start < end; index++ {
				changed[index] = true
			}
		case edit.target == targetVariantTextSingle || edit.target == targetVariantTextMultiple:
			point := offsets.mapOffset(start)
			index := sort.Search(len(lines), func(index int) bool {
				return lines[index].contentEnd >= point
			})
			if index < len(lines) && lines[index].start <= point {
				changed[index] = true
			}
		}
		renderedOffset += len(edit.replacement)
		baselineOffset = max(baselineOffset, edit.end)
	}
}

func newWhitespaceOffsetMap(contentLength int, deletions []whitespaceDeletion) *formattedOffsetMap {
	removed := 0
	for _, deletion := range deletions {
		removed += deletion.end - deletion.start
	}
	return &formattedOffsetMap{
		beforeLength: contentLength,
		afterLength:  contentLength - removed,
		deletions:    deletions,
	}
}

func trailingWhitespaceDeletion(content string, line logicalLine) []whitespaceDeletion {
	start := line.contentEnd
	for start > line.start && isHorizontalWhitespace(content[start-1]) {
		start--
	}
	if start == line.contentEnd {
		return nil
	}
	return []whitespaceDeletion{{start: start, end: line.contentEnd}}
}

func spaceBeforeTabDeletions(content string, line logicalLine) []whitespaceDeletion {
	indentEnd := line.start
	for indentEnd < line.contentEnd && isHorizontalWhitespace(content[indentEnd]) {
		indentEnd++
	}

	var deletions []whitespaceDeletion
	for offset := line.start; offset < indentEnd; {
		if content[offset] != ' ' {
			offset++
			continue
		}
		start := offset
		for offset < indentEnd && content[offset] == ' ' {
			offset++
		}
		if offset < indentEnd && content[offset] == '\t' {
			deletions = append(deletions, whitespaceDeletion{start: start, end: offset})
		}
	}
	return deletions
}

func isHorizontalWhitespace(value byte) bool {
	return value == ' ' || value == '\t'
}

func onlyHorizontalWhitespace(content string) bool {
	for index := range len(content) {
		if !isHorizontalWhitespace(content[index]) {
			return false
		}
	}
	return true
}

func mergeWhitespaceDeletions(deletions []whitespaceDeletion) []whitespaceDeletion {
	if len(deletions) < 2 {
		return deletions
	}
	sort.Slice(deletions, func(first, second int) bool {
		if deletions[first].start != deletions[second].start {
			return deletions[first].start < deletions[second].start
		}
		return deletions[first].end < deletions[second].end
	})

	merged := deletions[:1]
	for _, deletion := range deletions[1:] {
		last := &merged[len(merged)-1]
		if deletion.start <= last.end {
			last.end = max(last.end, deletion.end)
			continue
		}
		merged = append(merged, deletion)
	}
	return merged
}
