package hpatch

import (
	"cmp"
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

type lineCorrection struct {
	editOrigin

	requestedLine int
	resolvedLine  int
	offset        int
}

type editor struct {
	baseline      string
	cursor        int
	selections    []selection
	textMatches   bool
	cursorCommand int
	edits         []baselineEdit
	corrections   []lineCorrection
}

type logicalLine struct {
	start      int
	contentEnd int
	fullEnd    int
}

func (e *editor) resetCursor() {
	e.cursor = 0
	e.clearSelections()
	e.cursorCommand = 0
}

func (e *editor) clearSelections() {
	e.selections = nil
	e.textMatches = false
}

func (e *editor) commitGeneration() {
	content := e.content()
	for index := range e.corrections {
		e.corrections[index].offset = e.mapCorrectionOffset(e.corrections[index].offset)
	}
	e.baseline = content
	e.edits = nil
	e.resetCursor()
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
	return withReason(reasonEditConflict, e.setSelections([]selection{{start: line.start + start, end: line.start + end}}, false))
}

func (e *editor) selectMatches(fromLine, count int, literal string, origin editOrigin) error {
	line, lineErr := lineAt(e.baseline, fromLine)
	found := 0
	if lineErr == nil {
		offsets := nonOverlappingLiteralOffsets(e.baseline[line.start:], literal, count)
		found = len(offsets)
		if found == count {
			selections := literalSelections(line.start, offsets, literal)
			return withReason(reasonEditConflict, e.setSelections(selections, true))
		}
	}

	limit := count
	if count < len(e.baseline) {
		limit++
	}
	offsets := nonOverlappingLiteralOffsets(e.baseline, literal, limit)
	if len(offsets) == count {
		selections := literalSelections(0, offsets, literal)
		if err := e.setSelections(selections, true); err != nil {
			return withReason(reasonEditConflict, err)
		}
		lines := logicalLines(e.baseline)
		e.corrections = append(e.corrections, lineCorrection{
			editOrigin:    origin,
			requestedLine: fromLine,
			resolvedLine:  lineNumberAt(lines, selections[0].start),
			offset:        selections[0].start,
		})
		return nil
	}

	if lineErr != nil {
		return withReason(reasonCoordinateBounds, lineErr)
	}
	return withReason(reasonOccurrenceMissing, fmt.Errorf(
		"found %d of %d requested matches of %q at or after line %d",
		found,
		count,
		literal,
		fromLine,
	))
}

func literalSelections(base int, offsets []int, literal string) []selection {
	selections := make([]selection, len(offsets))
	for index, offset := range offsets {
		start := base + offset
		selections[index] = selection{start: start, end: start + len(literal)}
	}
	return selections
}

func (e *editor) selectLines(startLine, endLine int) error {
	if startLine > endLine {
		return withReason(reasonOrderOrOverlap, fmt.Errorf("resolved line range start %d exceeds end %d", startLine, endLine))
	}
	lines := logicalLines(e.baseline)
	if startLine < 1 || endLine < 1 || startLine > len(lines) || endLine > len(lines) {
		return withReason(reasonCoordinateBounds, fmt.Errorf("line range %d:%d is outside the file", startLine, endLine))
	}
	return withReason(reasonEditConflict, e.setSelections([]selection{{
		start:    lines[startLine-1].start,
		end:      lines[endLine-1].fullEnd,
		linewise: true,
	}}, false))
}

func (e *editor) setSelections(candidates []selection, textMatches bool) error {
	for _, candidate := range candidates {
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
	}
	e.selections = candidates
	e.textMatches = textMatches
	return nil
}

func (e *editor) typeText(replacement string, origin editOrigin) error {
	if len(e.selections) == 0 {
		if err := e.recordEdits([]baselineEdit{{
			start:       e.cursor,
			end:         e.cursor,
			replacement: replacement,
			editOrigin:  origin,
		}}); err != nil {
			return withReason(reasonEditConflict, err)
		}
		if replacement != "" {
			e.cursorCommand = origin.command
		}
		return nil
	}

	edits := make([]baselineEdit, len(e.selections))
	for index, selected := range e.selections {
		selectedReplacement := replacement
		if selected.linewise && lineTerminatorSuffix(selectedReplacement) == "" {
			selectedReplacement += lineTerminatorSuffix(e.baseline[selected.start:selected.end])
		}
		edits[index] = baselineEdit{
			start:       selected.start,
			end:         selected.end,
			replacement: selectedReplacement,
			editOrigin:  origin,
		}
	}
	if err := e.recordEdits(edits); err != nil {
		return withReason(reasonEditConflict, err)
	}
	e.cursor = e.selections[len(e.selections)-1].end
	e.clearSelections()
	e.cursorCommand = origin.command
	return nil
}

func (e *editor) deleteSelection(origin editOrigin) error {
	if len(e.selections) == 0 {
		return withReason(reasonSelectionRequired, fmt.Errorf("del requires a selection"))
	}
	edits := make([]baselineEdit, len(e.selections))
	for index, selected := range e.selections {
		edits[index] = baselineEdit{start: selected.start, end: selected.end, editOrigin: origin}
	}
	if err := e.recordEdits(edits); err != nil {
		return withReason(reasonEditConflict, err)
	}
	e.cursor = e.selections[len(e.selections)-1].start
	e.clearSelections()
	e.cursorCommand = origin.command
	return nil
}

func (e *editor) selectedClipboard() (clipboardContent, bool) {
	if len(e.selections) == 0 {
		return clipboardContent{}, false
	}
	selected := e.selections[0]
	return clipboardContent{
		text:     e.baseline[selected.start:selected.end],
		linewise: selected.linewise,
	}, true
}

func (e *editor) pasteClipboard(clipboard clipboardContent, origin editOrigin) error {
	positions := []int{e.cursor}
	if len(e.selections) != 0 {
		positions = make([]int, len(e.selections))
		for index, selected := range e.selections {
			positions[index] = selected.end
		}
	}
	edits := make([]baselineEdit, len(positions))
	for index, position := range positions {
		if position > 0 && position < len(e.baseline) && e.baseline[position-1] == '\r' && e.baseline[position] == '\n' {
			position++
		}
		replacement := clipboard.text
		if clipboard.linewise {
			terminator := firstLineTerminator(e.baseline)
			if position > 0 && !endsWithLineTerminator(e.baseline[:position]) {
				replacement = terminator + replacement
			}
			if position < len(e.baseline) && !startsWithLineTerminator(e.baseline[position:]) && !endsWithLineTerminator(replacement) {
				replacement += terminator
			}
		}
		positions[index] = position
		edits[index] = baselineEdit{
			start:       position,
			end:         position,
			replacement: replacement,
			editOrigin:  origin,
		}
	}
	if err := e.recordEdits(edits); err != nil {
		return withReason(reasonEditConflict, err)
	}
	e.cursor = positions[len(positions)-1]
	e.clearSelections()
	e.cursorCommand = origin.command
	return nil
}

func (e *editor) recordEdits(candidates []baselineEdit) error {
	pending := slices.Clone(e.edits)
	additions := make([]baselineEdit, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.start == candidate.end && candidate.replacement == "" {
			continue
		}
		for _, existing := range pending {
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
		pending = append(pending, candidate)
		additions = append(additions, candidate)
	}
	e.edits = append(e.edits, additions...)
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
		if order := cmp.Compare(first.start, second.start); order != 0 {
			return order
		}
		return cmp.Compare(first.end, second.end)
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

func nonOverlappingLiteralOffsets(text, literal string, limit int) []int {
	return findLiteralOffsets(text, literal, len(literal), limit)
}

func findLiteralOffsets(text, literal string, advance, limit int) []int {
	var offsets []int
	for searchFrom := 0; searchFrom <= len(text)-len(literal); {
		relative := strings.Index(text[searchFrom:], literal)
		if relative < 0 {
			break
		}
		match := searchFrom + relative
		offsets = append(offsets, match)
		searchFrom = match + advance
		if limit > 0 && len(offsets) == limit {
			break
		}
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

func startsWithLineTerminator(text string) bool {
	return strings.HasPrefix(text, "\r") || strings.HasPrefix(text, "\n")
}

func endsWithLineTerminator(text string) bool {
	return lineTerminatorSuffix(text) != ""
}

func firstLineTerminator(text string) string {
	for index := range len(text) {
		switch text[index] {
		case '\r':
			if index+1 < len(text) && text[index+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		case '\n':
			return "\n"
		}
	}
	return "\n"
}
