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

type renderedLine struct {
	start      int
	contentEnd int
	fullEnd    int
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

func renderedLines(text string) []renderedLine {
	lines := make([]renderedLine, 0, strings.Count(text, "\n")+1)
	for start := 0; ; {
		contentEnd := start
		for contentEnd < len(text) && text[contentEnd] != '\r' && text[contentEnd] != '\n' {
			contentEnd++
		}
		fullEnd := contentEnd
		if fullEnd < len(text) {
			fullEnd++
			if text[contentEnd] == '\r' && fullEnd < len(text) && text[fullEnd] == '\n' {
				fullEnd++
			}
		}
		lines = append(lines, renderedLine{start: start, contentEnd: contentEnd, fullEnd: fullEnd})
		if fullEnd >= len(text) {
			if fullEnd == len(text) && contentEnd < fullEnd {
				lines = append(lines, renderedLine{start: fullEnd, contentEnd: fullEnd, fullEnd: fullEnd})
			}
			return lines
		}
		start = fullEnd
	}
}

func renderedCoordinateAt(text string, lines []renderedLine, offset int) renderedCoordinate {
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
	var preview strings.Builder
	count := 0
	for _, character := range text {
		if count == 64 {
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
