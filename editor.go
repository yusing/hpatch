package hpatch

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type selection struct {
	start    int
	end      int
	linewise bool
}

type editor struct {
	text      string
	cursor    int
	selection *selection
}

type logicalLine struct {
	start      int
	contentEnd int
	fullEnd    int
}

func (e *editor) resetCursor() {
	e.cursor = 0
	e.selection = nil
}

func (e *editor) selectColumns(lineNumber, startColumn, endColumn int) error {
	line, err := lineAt(e.text, lineNumber)
	if err != nil {
		return err
	}
	content := e.text[line.start:line.contentEnd]
	start, end, ok := runeColumnOffsets(content, startColumn, endColumn)
	if !ok {
		return fmt.Errorf("columns %d:%d are outside line %d", startColumn, endColumn, lineNumber)
	}
	e.selection = &selection{start: line.start + start, end: line.start + end}
	return nil
}

func (e *editor) selectOccurrence(lineNumber, occurrence int, literal string) error {
	line, err := lineAt(e.text, lineNumber)
	if err != nil {
		return err
	}
	content := e.text[line.start:line.contentEnd]
	offsets := nonOverlappingLiteralOffsets(content, literal)

	index := occurrence - 1
	if occurrence < 0 {
		index = len(offsets) + occurrence
	}
	if index < 0 || index >= len(offsets) {
		return fmt.Errorf("occurrence %d of %q not found on line %d", occurrence, literal, lineNumber)
	}
	start := line.start + offsets[index]
	e.selection = &selection{start: start, end: start + len(literal)}
	return nil
}

func (e *editor) selectBlock(startLiteral, endLiteral string) error {
	scopeStart, scopeEnd := e.cursor, len(e.text)
	if e.selection != nil {
		scopeStart, scopeEnd = e.selection.start, e.selection.end
	}
	scope := e.text[scopeStart:scopeEnd]
	startOffsets := literalOffsets(scope, startLiteral)
	if len(startOffsets) != 1 {
		return fmt.Errorf("start literal %q occurs %d times in the search scope; want exactly once", startLiteral, len(startOffsets))
	}
	endOffsets := literalOffsets(scope, endLiteral)
	if len(endOffsets) != 1 {
		return fmt.Errorf("end literal %q occurs %d times in the search scope; want exactly once", endLiteral, len(endOffsets))
	}
	start := startOffsets[0]
	endStart := endOffsets[0]
	if endStart < start+len(startLiteral) {
		return fmt.Errorf("end literal %q precedes or overlaps start literal %q in the search scope", endLiteral, startLiteral)
	}
	e.selection = &selection{
		start: scopeStart + start,
		end:   scopeStart + endStart + len(endLiteral),
	}
	return nil
}

func (e *editor) selectLines(startLine, endLine int) error {
	lines := logicalLines(e.text)
	if startLine > len(lines) || endLine > len(lines) {
		return fmt.Errorf("line range %d:%d is outside the file", startLine, endLine)
	}
	e.selection = &selection{
		start:    lines[startLine-1].start,
		end:      lines[endLine-1].fullEnd,
		linewise: true,
	}
	return nil
}

func (e *editor) typeText(replacement string) {
	start, end := e.cursor, e.cursor
	if e.selection != nil {
		start, end = e.selection.start, e.selection.end
		if e.selection.linewise && lineTerminatorSuffix(replacement) == "" {
			replacement += lineTerminatorSuffix(e.text[start:end])
		}
	}
	e.text = e.text[:start] + replacement + e.text[end:]
	e.cursor = start + len(replacement)
	e.selection = nil
}

func (e *editor) deleteSelection() error {
	if e.selection == nil {
		return fmt.Errorf("del requires a selection")
	}
	start := e.selection.start
	e.text = e.text[:start] + e.text[e.selection.end:]
	e.cursor = start
	e.selection = nil
	return nil
}

func (e *editor) duplicateSelection() error {
	if e.selection == nil {
		return fmt.Errorf("dup requires a selection")
	}
	selected := *e.selection
	copyText := e.text[selected.start:selected.end]
	separator := ""
	if selected.linewise && !endsWithLineTerminator(copyText) {
		separator = firstLineTerminator(e.text)
	}
	insertion := separator + copyText
	e.text = e.text[:selected.end] + insertion + e.text[selected.end:]
	copyStart := selected.end + len(separator)
	e.selection = &selection{
		start:    copyStart,
		end:      copyStart + len(copyText),
		linewise: selected.linewise,
	}
	e.cursor = e.selection.end
	return nil
}

func literalOffsets(text, literal string) []int {
	var offsets []int
	for searchFrom := 0; searchFrom <= len(text)-len(literal); {
		relative := strings.Index(text[searchFrom:], literal)
		if relative < 0 {
			break
		}
		match := searchFrom + relative
		offsets = append(offsets, match)
		searchFrom = match + 1
	}
	return offsets
}

func nonOverlappingLiteralOffsets(text, literal string) []int {
	var offsets []int
	for searchFrom := 0; searchFrom <= len(text)-len(literal); {
		relative := strings.Index(text[searchFrom:], literal)
		if relative < 0 {
			break
		}
		match := searchFrom + relative
		offsets = append(offsets, match)
		searchFrom = match + len(literal)
	}
	return offsets
}

func logicalLines(text string) []logicalLine {
	var lines []logicalLine
	for start := 0; start < len(text); {
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
		lines = append(lines, logicalLine{start: start, contentEnd: contentEnd, fullEnd: fullEnd})
		start = fullEnd
	}
	return lines
}

func lineAt(text string, number int) (logicalLine, error) {
	lines := logicalLines(text)
	if number < 1 || number > len(lines) {
		return logicalLine{}, fmt.Errorf("line %d is outside the file", number)
	}
	return lines[number-1], nil
}

func runeColumnOffsets(content string, startColumn, endColumn int) (int, int, bool) {
	runeCount := utf8.RuneCountInString(content)
	if startColumn < 1 || endColumn < startColumn || endColumn > runeCount {
		return 0, 0, false
	}
	return byteOffsetAtRune(content, startColumn-1), byteOffsetAtRune(content, endColumn), true
}

func byteOffsetAtRune(text string, runeIndex int) int {
	if runeIndex == utf8.RuneCountInString(text) {
		return len(text)
	}
	seen := 0
	for offset := range text {
		if seen == runeIndex {
			return offset
		}
		seen++
	}
	return len(text)
}

func lineTerminatorSuffix(text string) string {
	switch {
	case strings.HasSuffix(text, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(text, "\n"):
		return "\n"
	case strings.HasSuffix(text, "\r"):
		return "\r"
	default:
		return ""
	}
}

func endsWithLineTerminator(text string) bool {
	return lineTerminatorSuffix(text) != ""
}

func firstLineTerminator(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		if index > 0 && text[index-1] == '\r' {
			return "\r\n"
		}
		return "\n"
	}
	if strings.Contains(text, "\r") {
		return "\r"
	}
	return "\n"
}
