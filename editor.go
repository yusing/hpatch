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
	baseline      string
	cursor        int
	selection     *selection
	cursorCommand int
	edits         []baselineEdit
}

type logicalLine struct {
	start      int
	contentEnd int
	fullEnd    int
}

func (e *editor) resetCursor() {
	e.cursor = 0
	e.selection = nil
	e.cursorCommand = 0
}

func (e *editor) selectColumns(lineNumber, startColumn, endColumn int) error {
	line, err := lineAt(e.baseline, lineNumber)
	if err != nil {
		return withReason(reasonCoordinateBounds, err)
	}
	content := e.baseline[line.start:line.contentEnd]
	start, end, ok := runeColumnOffsets(content, startColumn, endColumn)
	if !ok {
		return withReason(reasonCoordinateBounds, fmt.Errorf("columns %d:%d are outside line %d", startColumn, endColumn, lineNumber))
	}
	return withReason(reasonEditConflict, e.setSelection(selection{start: line.start + start, end: line.start + end}))
}

func (e *editor) selectOccurrence(lineNumber, occurrence, count int, literal string) error {
	line, err := lineAt(e.baseline, lineNumber)
	if err != nil {
		return withReason(reasonCoordinateBounds, err)
	}
	content := e.baseline[line.start:line.contentEnd]
	offsets := nonOverlappingLiteralOffsets(content, literal)

	index := occurrence - 1
	if occurrence < 0 {
		index = len(offsets) + occurrence
	}
	if index < 0 || index >= len(offsets) {
		return withReason(reasonOccurrenceMissing, fmt.Errorf("occurrence %d of %q not found on line %d", occurrence, literal, lineNumber))
	}
	startIndex, endIndex := index, index
	if occurrence < 0 {
		if count > index+1 {
			return withReason(reasonOccurrenceMissing, fmt.Errorf("occurrence group of count %d from %d of %q not found on line %d", count, occurrence, literal, lineNumber))
		}
		startIndex = index - count + 1
	} else {
		if count > len(offsets)-index {
			return withReason(reasonOccurrenceMissing, fmt.Errorf("occurrence group of count %d from %d of %q not found on line %d", count, occurrence, literal, lineNumber))
		}
		endIndex = index + count - 1
	}
	start := line.start + offsets[startIndex]
	end := line.start + offsets[endIndex] + len(literal)
	return withReason(reasonEditConflict, e.setSelection(selection{start: start, end: end}))
}

func (e *editor) selectBlock(startLiteral, endLiteral string) (bool, error) {
	return e.selectBlockInScope(startLiteral, endLiteral, 0, len(e.baseline), "the active file baseline")
}

func (e *editor) selectNextBlock(startLiteral, endLiteral string) (bool, error) {
	scopeStart, scopeEnd := e.cursor, len(e.baseline)
	scopeDescription := "the baseline suffix from the current cursor"
	if e.selection != nil {
		scopeStart, scopeEnd = e.selection.start, e.selection.end
		scopeDescription = "the current baseline selection"
	}
	return e.selectBlockInScope(startLiteral, endLiteral, scopeStart, scopeEnd, scopeDescription)
}

func (e *editor) selectBlockInScope(startLiteral, endLiteral string, scopeStart, scopeEnd int, scopeDescription string) (bool, error) {
	scope := e.baseline[scopeStart:scopeEnd]
	startMatches, startRecovered := blockAnchorMatches(scope, startLiteral)
	if len(startMatches) != 1 {
		reason := reasonAnchorMissing
		if len(startMatches) > 1 {
			reason = reasonAnchorAmbiguous
		}
		return false, withReason(reason, fmt.Errorf(
			"start literal %q occurs %d times%s in %s; want exactly once",
			startLiteral,
			len(startMatches),
			blockMatchQualifier(startRecovered),
			scopeDescription,
		))
	}

	start := startMatches[0]
	endMatches, endRecovered := blockAnchorMatches(scope[start.end:], endLiteral)
	if len(endMatches) != 1 {
		reason := reasonAnchorMissing
		if len(endMatches) > 1 {
			reason = reasonAnchorAmbiguous
		}
		return false, withReason(reason, fmt.Errorf(
			"end literal %q occurs %d times%s after start in %s; want exactly once",
			endLiteral,
			len(endMatches),
			blockMatchQualifier(endRecovered),
			scopeDescription,
		))
	}
	end := endMatches[0]
	recovered := startRecovered || endRecovered
	return recovered, withReason(reasonEditConflict, e.setSelection(selection{
		start: scopeStart + start.start,
		end:   scopeStart + start.end + end.end,
	}))
}

func (e *editor) selectLines(startLine, endLine int) error {
	if startLine > endLine {
		return withReason(reasonOrderOrOverlap, fmt.Errorf("resolved line range start %d exceeds end %d", startLine, endLine))
	}
	lines := logicalLines(e.baseline)
	if startLine < 1 || endLine < 1 || startLine > len(lines) || endLine > len(lines) {
		return withReason(reasonCoordinateBounds, fmt.Errorf("line range %d:%d is outside the file", startLine, endLine))
	}
	return withReason(reasonEditConflict, e.setSelection(selection{
		start:    lines[startLine-1].start,
		end:      lines[endLine-1].fullEnd,
		linewise: true,
	}))
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
		return withReason(reasonEditConflict, err)
	}
	e.cursor = end
	e.selection = nil
	if start != end || replacement != "" {
		e.cursorCommand = origin.command
	}
	return nil
}

func (e *editor) deleteSelection(origin editOrigin) error {
	if e.selection == nil {
		return withReason(reasonSelectionRequired, fmt.Errorf("del requires a selection"))
	}
	selected := *e.selection
	if err := e.recordEdit(baselineEdit{start: selected.start, end: selected.end, editOrigin: origin}); err != nil {
		return withReason(reasonEditConflict, err)
	}
	e.cursor = selected.start
	e.selection = nil
	e.cursorCommand = origin.command
	return nil
}

func (e *editor) duplicateSelection(origin editOrigin) error {
	if e.selection == nil {
		return withReason(reasonSelectionRequired, fmt.Errorf("dup requires a selection"))
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
		return withReason(reasonEditConflict, err)
	}
	e.cursor = selected.end
	e.selection = nil
	e.cursorCommand = origin.command
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

func (e *editor) orderedEdits() []baselineEdit {
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
	return edits
}

func (e *editor) content() string {
	edits := e.orderedEdits()

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

type literalMatch struct {
	start int
	end   int
}

func blockAnchorMatches(text, literal string) ([]literalMatch, bool) {
	offsets := literalOffsets(text, literal)
	if len(offsets) > 0 {
		matches := make([]literalMatch, len(offsets))
		for index, offset := range offsets {
			matches[index] = literalMatch{start: offset, end: offset + len(literal)}
		}
		return matches, false
	}
	if !strings.ContainsAny(literal, " \t") {
		return nil, false
	}
	return horizontalWhitespaceLiteralMatches(text, literal), true
}

func blockMatchQualifier(tolerant bool) string {
	if tolerant {
		return " with horizontal whitespace ignored"
	}
	return ""
}

func horizontalWhitespaceLiteralMatches(text, literal string) []literalMatch {
	if literal == "" {
		return nil
	}
	var matches []literalMatch
	for start := range len(text) {
		if isHorizontalWhitespace(literal[0]) && start > 0 && isHorizontalWhitespace(text[start-1]) {
			continue
		}
		end, ok := matchHorizontalWhitespaceLiteral(text, literal, start)
		if ok {
			matches = append(matches, literalMatch{start: start, end: end})
		}
	}
	return matches
}

func matchHorizontalWhitespaceLiteral(text, literal string, textOffset int) (int, bool) {
	literalOffset := 0
	for literalOffset < len(literal) {
		if isHorizontalWhitespace(literal[literalOffset]) {
			if textOffset >= len(text) || !isHorizontalWhitespace(text[textOffset]) {
				return 0, false
			}
			for literalOffset < len(literal) && isHorizontalWhitespace(literal[literalOffset]) {
				literalOffset++
			}
			for textOffset < len(text) && isHorizontalWhitespace(text[textOffset]) {
				textOffset++
			}
			continue
		}
		if textOffset >= len(text) || text[textOffset] != literal[literalOffset] {
			return 0, false
		}
		literalOffset++
		textOffset++
	}
	return textOffset, true
}

func isHorizontalWhitespace(character byte) bool {
	return character == ' ' || character == '\t'
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
