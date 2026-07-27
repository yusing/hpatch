package hpatch

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type boundaryAffinity uint8

const (
	boundaryBefore boundaryAffinity = iota
	boundaryAfter
)

type renderedCoordinate struct {
	line   int
	column int
}

func (w *workspace) finalStateReport() string {
	if w.active == nil {
		return "no active file\n"
	}
	return w.active.editor.finalStateReport(w.active.path)
}

func (e *editor) finalStateReport(path string) string {
	content := e.content()
	lines := renderedLines(content)
	var report strings.Builder
	var previewOffset int
	if e.selection != nil {
		startOffset := e.mapBaselineOffset(e.selection.start, boundaryAfter, 0)
		endOffset := e.mapBaselineOffset(e.selection.end, boundaryBefore, 0)
		start := renderedCoordinateAt(content, lines, startOffset)
		end := renderedCoordinateAt(content, lines, endOffset)
		fmt.Fprintf(&report, "in %s %d:%d-%d:%d\n", escapeReportControls(path), start.line, start.column, end.line, end.column)
		previewOffset = startOffset
	} else {
		affinity := boundaryBefore
		if e.cursorCommand != 0 {
			affinity = boundaryAfter
		}
		offset := e.mapBaselineOffset(e.cursor, affinity, e.cursorCommand)
		position := renderedCoordinateAt(content, lines, offset)
		fmt.Fprintf(&report, "in %s %d:%d\n", escapeReportControls(path), position.line, position.column)
		previewOffset = offset
	}

	position := renderedCoordinateAt(content, lines, previewOffset)
	start := max(0, position.line-2)
	if start+3 > len(lines) {
		start = max(0, len(lines)-3)
	}
	for index := start; index < min(start+3, len(lines)); index++ {
		line := lines[index]
		fmt.Fprintf(&report, "%d %s\n", index+1, previewText(content[line.start:line.contentEnd]))
	}
	return report.String()
}

func (e *editor) mapBaselineOffset(offset int, affinity boundaryAffinity, targetCommand int) int {
	renderedOffset := 0
	baselineOffset := 0
	for _, edit := range e.orderedEdits() {
		if edit.start > offset {
			break
		}
		renderedOffset += edit.start - baselineOffset

		if targetCommand != 0 && edit.command == targetCommand {
			return renderedOffset + len(edit.replacement)
		}
		if edit.start == offset && affinity == boundaryBefore {
			return renderedOffset
		}
		renderedOffset += len(edit.replacement)
		baselineOffset = edit.end
		if edit.end > offset {
			return renderedOffset
		}
	}
	return renderedOffset + max(0, offset-baselineOffset)
}

func renderedLines(text string) []logicalLine {
	lines := logicalLines(text)
	if text == "" || endsWithLineTerminator(text) {
		lines = append(lines, logicalLine{start: len(text), contentEnd: len(text), fullEnd: len(text)})
	}
	return lines
}

func renderedCoordinateAt(text string, lines []logicalLine, offset int) renderedCoordinate {
	offset = min(max(offset, 0), len(text))
	for index, line := range lines {
		if offset <= line.contentEnd {
			return renderedCoordinate{line: index + 1, column: utf8.RuneCountInString(text[line.start:offset]) + 1}
		}
		if offset < line.fullEnd {
			return renderedCoordinate{line: index + 1, column: utf8.RuneCountInString(text[line.start:line.contentEnd]) + 1}
		}
	}
	last := lines[len(lines)-1]
	return renderedCoordinate{line: len(lines), column: utf8.RuneCountInString(text[last.start:last.contentEnd]) + 1}
}

func previewText(text string) string {
	return previewTextLimit(text, 64)
}

// previewTextLimit renders text on one output line, escaping control characters
// and truncating to limit code points.
func previewTextLimit(text string, limit int) string {
	var preview strings.Builder
	count := 0
	for _, character := range text {
		if count == limit {
			break
		}
		count++
		if unicode.IsControl(character) {
			quoted := strconv.QuoteRune(character)
			preview.WriteString(quoted[1 : len(quoted)-1])
			continue
		}
		preview.WriteRune(character)
	}
	return preview.String()
}

func escapeReportControls(text string) string {
	var escaped strings.Builder
	for _, character := range text {
		if unicode.IsControl(character) {
			quoted := strconv.QuoteRune(character)
			escaped.WriteString(quoted[1 : len(quoted)-1])
			continue
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}
