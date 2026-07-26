package hpatch

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type selection struct {
	start    int
	end      int
	linewise bool
}

type editOrigin struct {
	command   int
	line      int
	operation string
}

type baselineEdit struct {
	editOrigin
	start       int
	end         int
	replacement string
}

type editor struct {
	baseline  string
	cursor    int
	selection *selection
	edits     []baselineEdit
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
	line, err := lineAt(e.baseline, lineNumber)
	if err != nil {
		return err
	}
	content := e.baseline[line.start:line.contentEnd]
	start, end, ok := runeColumnOffsets(content, startColumn, endColumn)
	if !ok {
		return fmt.Errorf("columns %d:%d are outside line %d", startColumn, endColumn, lineNumber)
	}
	return e.setSelection(selection{start: line.start + start, end: line.start + end})
}

func (e *editor) selectOccurrence(lineNumber, occurrence int, literal string) error {
	line, err := lineAt(e.baseline, lineNumber)
	if err != nil {
		return err
	}
	content := e.baseline[line.start:line.contentEnd]
	offsets := nonOverlappingLiteralOffsets(content, literal)

	index := occurrence - 1
	if occurrence < 0 {
		index = len(offsets) + occurrence
	}
	if index < 0 || index >= len(offsets) {
		return fmt.Errorf("occurrence %d of %q not found on line %d", occurrence, literal, lineNumber)
	}
	start := line.start + offsets[index]
	return e.setSelection(selection{start: start, end: start + len(literal)})
}

func (e *editor) selectBlock(startLiteral, endLiteral string) error {
	scopeStart, scopeEnd := e.cursor, len(e.baseline)
	if e.selection != nil {
		scopeStart, scopeEnd = e.selection.start, e.selection.end
	}
	scope := e.baseline[scopeStart:scopeEnd]
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
	return e.setSelection(selection{
		start: scopeStart + start,
		end:   scopeStart + endStart + len(endLiteral),
	})
}

func (e *editor) selectLines(startLine, endLine int) error {
	lines := logicalLines(e.baseline)
	if startLine > len(lines) || endLine > len(lines) {
		return fmt.Errorf("line range %d:%d is outside the file", startLine, endLine)
	}
	return e.setSelection(selection{
		start:    lines[startLine-1].start,
		end:      lines[endLine-1].fullEnd,
		linewise: true,
	})
}

func (e *editor) setSelection(candidate selection) error {
	for _, edit := range e.edits {
		if edit.start == edit.end {
			continue
		}
		start := max(candidate.start, edit.start)
		end := min(candidate.end, edit.end)
		if start >= end {
			continue
		}
		startLine := baselineLine(e.baseline, start)
		endLine := baselineLine(e.baseline, end-1)
		span := fmt.Sprintf("baseline line %d was already modified", startLine)
		if startLine != endLine {
			span = fmt.Sprintf("baseline lines %d:%d were already modified", startLine, endLine)
		}
		return fmt.Errorf(
			"selection conflicts with edit from command %d (source line %d, operation %q): %s",
			edit.command,
			edit.line,
			edit.operation,
			span,
		)
	}
	e.selection = &candidate
	return nil
}

func (e *editor) typeText(replacement string, origin editOrigin) error {
	start, end := e.cursor, e.cursor
	if e.selection != nil {
		start, end = e.selection.start, e.selection.end
		if e.selection.linewise && lineTerminatorSuffix(replacement) == "" {
			replacement += lineTerminatorSuffix(e.baseline[start:end])
		}
	}
	if err := e.recordEdit(baselineEdit{start: start, end: end, replacement: replacement, editOrigin: origin}); err != nil {
		return err
	}
	e.cursor = end
	e.selection = nil
	return nil
}

func (e *editor) deleteSelection(origin editOrigin) error {
	if e.selection == nil {
		return fmt.Errorf("del requires a selection")
	}
	selected := *e.selection
	if err := e.recordEdit(baselineEdit{start: selected.start, end: selected.end, editOrigin: origin}); err != nil {
		return err
	}
	e.cursor = selected.start
	e.selection = nil
	return nil
}

func (e *editor) duplicateSelection(origin editOrigin) error {
	if e.selection == nil {
		return fmt.Errorf("dup requires a selection")
	}
	selected := *e.selection
	copyText := e.baseline[selected.start:selected.end]
	separator := ""
	if selected.linewise && !endsWithLineTerminator(copyText) {
		separator = firstLineTerminator(e.baseline)
	}
	if err := e.recordEdit(baselineEdit{
		start:       selected.end,
		end:         selected.end,
		replacement: separator + copyText,
		editOrigin:  origin,
	}); err != nil {
		return err
	}
	e.cursor = selected.end
	e.selection = nil
	return nil
}

func (e *editor) recordEdit(candidate baselineEdit) error {
	if candidate.start == candidate.end && candidate.replacement == "" {
		return nil
	}
	for _, existing := range e.edits {
		description, conflict := describeEditConflict(e.baseline, existing, candidate)
		if !conflict {
			continue
		}
		return fmt.Errorf(
			"conflicts with edit from command %d (source line %d, operation %q): %s",
			existing.command,
			existing.line,
			existing.operation,
			description,
		)
	}
	e.edits = append(e.edits, candidate)
	return nil
}

func describeEditConflict(baseline string, first, second baselineEdit) (string, bool) {
	firstInsertion := first.start == first.end
	secondInsertion := second.start == second.end
	switch {
	case firstInsertion && secondInsertion:
		if first.start != second.start {
			return "", false
		}
		return fmt.Sprintf("baseline line %d receives multiple insertions at one position", baselineLine(baseline, first.start)), true
	case firstInsertion:
		if first.start <= second.start || first.start >= second.end {
			return "", false
		}
		return fmt.Sprintf("baseline line %d is both replaced and inserted into", baselineLine(baseline, first.start)), true
	case secondInsertion:
		if second.start <= first.start || second.start >= first.end {
			return "", false
		}
		return fmt.Sprintf("baseline line %d is both replaced and inserted into", baselineLine(baseline, second.start)), true
	default:
		start := max(first.start, second.start)
		end := min(first.end, second.end)
		if start >= end {
			return "", false
		}
		startLine := baselineLine(baseline, start)
		endLine := baselineLine(baseline, end-1)
		if startLine == endLine {
			return fmt.Sprintf("baseline line %d is modified by both edits", startLine), true
		}
		return fmt.Sprintf("baseline lines %d:%d are modified by both edits", startLine, endLine), true
	}
}

func baselineLine(text string, offset int) int {
	lines := logicalLines(text)
	for index, line := range lines {
		if offset < line.fullEnd {
			return index + 1
		}
	}
	if len(lines) == 0 {
		return 1
	}
	return len(lines)
}

func (e *editor) firstEdit() (baselineEdit, bool) {
	if len(e.edits) == 0 {
		return baselineEdit{}, false
	}
	return e.edits[0], true
}

func (e *editor) content() string {
	edits := slices.Clone(e.edits)
	slices.SortFunc(edits, func(first, second baselineEdit) int {
		switch {
		case first.start < second.start:
			return -1
		case first.start > second.start:
			return 1
		case first.end < second.end:
			return -1
		case first.end > second.end:
			return 1
		default:
			return 0
		}
	})

	var result strings.Builder
	cursor := 0
	for _, edit := range edits {
		result.WriteString(e.baseline[cursor:edit.start])
		result.WriteString(edit.replacement)
		cursor = max(cursor, edit.end)
	}
	result.WriteString(e.baseline[cursor:])
	return result.String()
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
