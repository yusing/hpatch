package hpatch

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// repairLineWindow is how many baseline lines accompany a failure on each side
// of the line the failing command addressed.
const repairLineWindow = 2

// repairPreviewLimit is the code-point cap for the failing line itself. It is
// wider than the final-state report's preview because a repair needs to show
// the span the selector actually addressed, which may sit late in a long line.
const repairPreviewLimit = 200
const repairListLimit = 16

// repairContext renders what a failing command needed to know and could not
// see: the baseline lines it addressed and, where the reason is a coordinate or
// anchor mismatch, the measurements that would have made the selector correct.
// Selectors resolve against a baseline the model is guessing at, so a failure
// without this context forces a blind retry that costs a whole script.
func (w *workspace) repairContext(command instruction, reason failureReason) string {
	if w.active == nil {
		return ""
	}
	editor := &w.active.editor
	if editor.baseline == "" {
		return ""
	}
	lines := logicalLines(editor.baseline)
	if len(lines) == 0 {
		return ""
	}

	var report strings.Builder
	// A selector that resolved but overlaps an earlier edit needs the conflict
	// explained, not its coordinates re-measured. The rejected selection was
	// never committed to editor state, so the window is centered on the line
	// the failing command addressed rather than on the stale cursor.
	if reason == reasonEditConflict {
		w.writeEditRepair(&report, editor, lines, reason, editor.addressedOffset(command, lines))
		return report.String()
	}
	switch command.operation {
	case "sel", "tsel":
		w.writeLineRepair(&report, editor, lines, command, reason)
	case "rsel":
		w.writeRangeRepair(&report, editor, lines, command, reason)
	case "bsel", "bsel_next":
		w.writeAnchorRepair(&report, editor, lines, command, reason)
	case "type", "del", "copy", "cut", "paste":
		w.writeEditRepair(&report, editor, lines, reason, editor.editOffset())
	}
	if report.Len() == 0 {
		return ""
	}
	return report.String()
}

// writeLineRepair explains a single-line selector failure. Out-of-range lines
// get the file's line count; a rejected column range or missing occurrence gets
// the addressed line with its measurements.
func (w *workspace) writeLineRepair(report *strings.Builder, editor *editor, lines []logicalLine, command instruction, reason failureReason) {
	number := command.lineNumber

	if number < 1 || number > len(lines) {
		fmt.Fprintf(report, "%s has %d lines; line %d does not exist\n", w.active.path, len(lines), number)
		writeLineWindow(report, editor.baseline, lines, min(max(number, 1), len(lines)))
		return
	}
	content := editor.baseline[lines[number-1].start:lines[number-1].contentEnd]
	columns := utf8.RuneCountInString(content)
	switch reason {
	case reasonCoordinateBounds:
		fmt.Fprintf(report, "line %d has %d columns; requested %d:%d\n", number, columns, command.start, command.end)
		fmt.Fprintf(report, "columns count Unicode code points, so one tab is one column\n")
	case reasonOccurrenceMissing:
		fmt.Fprintf(report, "line %d does not contain the requested occurrence\n", number)
		writeOccurrenceCandidates(report, editor.baseline, lines, command)
	}
	writeLineWindow(report, editor.baseline, lines, number)
	if reason == reasonCoordinateBounds && columns > 0 {
		fmt.Fprintf(report, "column guide for line %d: %s\n", number, columnGuide(content))
	}
}

func writeOccurrenceCandidates(report *strings.Builder, baseline string, lines []logicalLine, command instruction) {
	candidates := make([]int, 0, min(len(lines), repairListLimit))
	total := 0
	for index, line := range lines {
		if index+1 == command.lineNumber {
			continue
		}
		content := baseline[line.start:line.contentEnd]
		if !occurrenceGroupFits(content, command.text, command.occurrence, command.count) {
			continue
		}
		total++
		if len(candidates) < repairListLimit {
			candidates = append(candidates, index+1)
		}
	}
	if total == 0 {
		return
	}
	if total == 1 {
		fmt.Fprintf(report, "the requested occurrence is selectable on line %d\n", candidates[0])
		return
	}
	fmt.Fprintf(report, "the requested occurrence is selectable on lines %s\n", joinLineNumbers(candidates, total))
}

func occurrenceGroupFits(content, literal string, occurrence, count int) bool {
	offsets := nonOverlappingLiteralOffsets(content, literal)
	index := occurrence - 1
	if occurrence < 0 {
		index = len(offsets) + occurrence
	}
	if index < 0 || index >= len(offsets) {
		return false
	}
	if occurrence < 0 {
		return count <= index+1
	}
	return count <= len(offsets)-index
}

// writeRangeRepair explains a linewise selector failure against the file's
// actual line count.
func (w *workspace) writeRangeRepair(report *strings.Builder, editor *editor, lines []logicalLine, command instruction, reason failureReason) {
	if reason != reasonCoordinateBounds && reason != reasonOrderOrOverlap {
		return
	}
	start := command.lineNumber
	end := command.endLine
	fmt.Fprintf(report, "%s has %d lines; requested lines %d:%d\n", w.active.path, len(lines), start, end)
	writeLineWindow(report, editor.baseline, lines, min(max(start, 1), len(lines)))
}

// writeAnchorRepair explains a block-selector failure. An ambiguous anchor is
// repaired by choosing a unique one, so the lines where each anchor occurs are
// the information the retry needs; a missing anchor is repaired by correcting
// its text, so the searched scope matters instead.
func (w *workspace) writeAnchorRepair(report *strings.Builder, editor *editor, lines []logicalLine, command instruction, reason failureReason) {
	scopeStart, scopeEnd := 0, len(editor.baseline)
	if command.operation == "bsel_next" && editor.selection != nil {
		scopeStart, scopeEnd = editor.selection.start, editor.selection.end
	} else if command.operation == "bsel_next" {
		scopeStart = editor.cursor
	}
	scope := editor.baseline[scopeStart:scopeEnd]
	startMatches, startRecovered := blockAnchorMatches(scope, command.text)
	writeAnchorMatches(report, "START", command.text, startMatches, startRecovered, lines, scopeStart)
	if len(startMatches) == 1 {
		endScopeStart := startMatches[0].end
		endMatches, endRecovered := blockAnchorMatches(scope[endScopeStart:], command.endText)
		writeAnchorMatches(report, "END", command.endText, endMatches, endRecovered, lines, scopeStart+endScopeStart)
	} else {
		report.WriteString("END anchor was not evaluated because START is not unique\n")
	}
	if reason == reasonAnchorAmbiguous || reason == reasonAnchorMissing {
		report.WriteString("a block selection includes both anchors, so replacement text must reproduce what END covers\n")
	}
}

func writeAnchorMatches(report *strings.Builder, label, literal string, matches []literalMatch, recovered bool, lines []logicalLine, scopeStart int) {
	qualifier := ""
	if recovered {
		qualifier = " after normalizing horizontal whitespace"
	}
	switch len(matches) {
	case 0:
		fmt.Fprintf(report, "%s anchor %q has no occurrence%s in the searched scope\n", label, previewText(literal), qualifier)
	case 1:
		line := lineNumberAt(lines, scopeStart+matches[0].start)
		fmt.Fprintf(report, "%s anchor %q occurs once%s, at line %d\n", label, previewText(literal), qualifier, line)
	default:
		numbers := make([]int, 0, min(len(matches), repairListLimit))
		for _, match := range matches[:min(len(matches), repairListLimit)] {
			numbers = append(numbers, lineNumberAt(lines, scopeStart+match.start))
		}
		fmt.Fprintf(report, "%s anchor %q is ambiguous%s, occurring at lines %s\n", label, previewText(literal), qualifier, joinLineNumbers(numbers, len(matches)))
	}
}

// writeEditRepair explains an edit failure against the span the edit addressed,
// including the earlier command it conflicted with.
func (w *workspace) writeEditRepair(report *strings.Builder, editor *editor, lines []logicalLine, reason failureReason, offset int) {
	switch reason {
	case reasonSelectionRequired:
		report.WriteString("this command requires an active selection\n")
	case reasonClipboardEmpty:
		report.WriteString("copy or cut a selection before paste; the clipboard exists only for this script\n")
	case reasonEditConflict:
		report.WriteString("baseline content is claimed by an earlier command; each baseline span accepts one edit\n")
		if claimed := editor.claimedLineSpans(lines); claimed != "" {
			fmt.Fprintf(report, "already edited by earlier commands: %s\n", claimed)
		}
		report.WriteString("combine the intended change into one command per baseline span\n")
	default:
		return
	}
	writeLineWindow(report, editor.baseline, lines, lineNumberAt(lines, offset))
}

// editOffset is where an edit command acted: its selection when one is active,
// otherwise the cursor.
func (e *editor) editOffset() int {
	if e.selection != nil {
		return e.selection.start
	}
	return e.cursor
}

// addressedOffset is the baseline offset a failing selector named. A rejected
// selector leaves editor state untouched, so its own operands identify the
// relevant span; commands without a line operand fall back to edit state.
func (e *editor) addressedOffset(command instruction, lines []logicalLine) int {
	switch command.operation {
	case "sel", "tsel", "rsel":
		if command.lineNumber >= 1 && command.lineNumber <= len(lines) {
			return lines[command.lineNumber-1].start
		}
	}
	return e.editOffset()
}

// claimedLineSpans lists the baseline lines each recorded edit already claims,
// so a conflicting retry can see which spans are unavailable rather than
// rediscovering them one rejection at a time.
func (e *editor) claimedLineSpans(lines []logicalLine) string {
	edits := e.orderedEdits()
	claims := make([]string, 0, min(len(edits), repairListLimit))
	for _, edit := range edits[:min(len(edits), repairListLimit)] {
		start := lineNumberAt(lines, edit.start)
		end := lineNumberAt(lines, max(edit.start, edit.end-1))
		span := fmt.Sprint(start)
		if start != end {
			span = fmt.Sprintf("%d:%d", start, end)
		}
		claims = append(claims, fmt.Sprintf("command %d (%s) line %s", edit.command, edit.operation, span))
	}
	if omitted := len(edits) - len(claims); omitted > 0 {
		claims = append(claims, fmt.Sprintf("... (%d more edits)", omitted))
	}
	return strings.Join(claims, "; ")
}

// writeLineWindow renders baseline lines around number with one-based numbers,
// matching the final-state report's shape so both read the same way.
func writeLineWindow(report *strings.Builder, baseline string, lines []logicalLine, number int) {
	if number < 1 || number > len(lines) {
		return
	}
	start := max(1, number-repairLineWindow)
	end := min(len(lines), number+repairLineWindow)
	for index := start; index <= end; index++ {
		line := lines[index-1]
		marker := " "
		if index == number {
			marker = ">"
		}
		limit := 64
		if index == number {
			limit = repairPreviewLimit
		}
		fmt.Fprintf(report, "%s%d %s\n", marker, index, previewTextLimit(baseline[line.start:line.contentEnd], limit))
	}
}

// columnGuide reports the rune-column span of each whitespace-separated token
// on the line. Sampling individual columns is ambiguous, because a sampled
// character usually recurs on the line and the reader cannot tell which
// occurrence was meant; a token's start:end span is unambiguous and is what a
// column selector actually needs.
func columnGuide(content string) string {
	var spans []string
	column := 0
	start := 0
	total := 0
	var token strings.Builder
	flush := func(end int) {
		if token.Len() == 0 {
			return
		}
		total++
		if len(spans) < repairListLimit {
			spans = append(spans, fmt.Sprintf("%s=%d:%d", previewText(token.String()), start, end))
		}
		token.Reset()
	}
	for _, character := range content {
		column++
		if character == ' ' || character == '\t' {
			flush(column - 1)
			continue
		}
		if token.Len() == 0 {
			start = column
		}
		token.WriteRune(character)
	}
	flush(column)
	if total == 0 {
		return "line contains only whitespace"
	}
	if omitted := total - len(spans); omitted > 0 {
		spans = append(spans, fmt.Sprintf("... (%d more tokens)", omitted))
	}
	return strings.Join(spans, " ")
}

// lineNumberAt maps a baseline offset to its one-based logical line.
func lineNumberAt(lines []logicalLine, offset int) int {
	for index, line := range lines {
		if offset < line.fullEnd {
			return index + 1
		}
	}
	return len(lines)
}

func joinLineNumbers(numbers []int, total int) string {
	rendered := make([]string, 0, len(numbers)+1)
	for _, number := range numbers {
		rendered = append(rendered, fmt.Sprint(number))
	}
	if omitted := total - len(numbers); omitted > 0 {
		rendered = append(rendered, fmt.Sprintf("... (%d more occurrences)", omitted))
	}
	return strings.Join(rendered, ", ")
}
