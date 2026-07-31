package hpatch

import (
	"fmt"
	"strconv"
	"strings"
)

// repairLineWindow is how many baseline lines accompany a failure on each side
// of the line the failing command addressed.
const repairLineWindow = 2

// repairPreviewLimit is the code-point cap for the failing line itself. It is
// wider than the final-state report's preview because a repair needs to show
// the span the selector actually addressed, which may sit late in a long line.
const (
	repairPreviewLimit = 200
	repairListLimit    = 16
)

// repairContext renders baseline context only when the failed command has a
// uniquely resolved anchor or existing editor state identifies the edit span.
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
	if reason == reasonEditConflict {
		offset := editor.editOffset()
		switch command.operation {
		case "tsel", "rsel":
			line, _, err := resolveLineHash(editor.baseline, command.lineHash)
			if err != nil {
				return ""
			}
			offset = line.start
		}
		w.writeEditRepair(&report, editor, lines, reason, offset)
		return report.String()
	}

	switch command.operation {
	case "tsel":
		w.writeTextSelectorRepair(&report, editor, lines, command, reason)
	case "rsel":
		w.writeRangeRepair(&report, editor, lines, command, reason)
	case "type", "del", "copy", "cut", "paste":
		w.writeEditRepair(&report, editor, lines, reason, editor.editOffset())
	}
	return report.String()
}

func (w *workspace) writeTextSelectorRepair(report *strings.Builder, editor *editor, lines []logicalLine, command instruction, reason failureReason) {
	if reason != reasonOccurrenceMissing {
		return
	}
	line, number, err := resolveLineHash(editor.baseline, command.lineHash)
	if err != nil {
		return
	}
	offsets := nonOverlappingLiteralOffsets(editor.baseline[line.start:], command.text, command.count)
	fmt.Fprintf(report, "found %d of %d requested matches at or after line %d\n", len(offsets), command.count, number)
	if len(offsets) != 0 {
		matchLines := make([]int, 0, min(len(offsets), repairListLimit))
		for _, offset := range offsets[:min(len(offsets), repairListLimit)] {
			matchLines = append(matchLines, lineNumberAt(lines, line.start+offset))
		}
		fmt.Fprintf(report, "matching lines: %s\n", joinLineNumbers(matchLines, len(offsets)))
	}
	writeLineWindow(report, editor.baseline, lines, number)
}

func (w *workspace) writeRangeRepair(report *strings.Builder, editor *editor, lines []logicalLine, command instruction, reason failureReason) {
	if reason != reasonOrderOrOverlap {
		return
	}
	_, start, startErr := resolveLineHash(editor.baseline, command.lineHash)
	_, end, endErr := resolveLineHash(editor.baseline, command.endHash)
	if startErr != nil || endErr != nil {
		return
	}
	fmt.Fprintf(report, "resolved hashline range to lines %d:%d\n", start, end)
	writeLineWindow(report, editor.baseline, lines, start)
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
	if len(e.selections) != 0 {
		return e.selections[0].start
	}
	return e.cursor
}

// claimedLineSpans lists the baseline lines each recorded edit already claims.
func (e *editor) claimedLineSpans(lines []logicalLine) string {
	edits := e.orderedEdits()
	claims := make([]string, 0, min(len(edits), repairListLimit))
	for _, edit := range edits[:min(len(edits), repairListLimit)] {
		start := lineNumberAt(lines, edit.start)
		end := lineNumberAt(lines, max(edit.start, edit.end-1))
		span := strconv.Itoa(start)
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

// writeLineWindow renders baseline lines around number using the same hash-only
// row shape as hread and the final-state report.
func writeLineWindow(report *strings.Builder, baseline string, lines []logicalLine, number int) {
	if number < 1 || number > len(lines) {
		return
	}
	start := max(1, number-repairLineWindow)
	end := min(len(lines), number+repairLineWindow)
	for index := start; index <= end; index++ {
		line := lines[index-1]
		limit := 64
		if index == number {
			limit = repairPreviewLimit
		}
		content := lineContent(baseline, line)
		writeHashLine(report, content, previewTextLimit(content, limit))
	}
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
		rendered = append(rendered, strconv.Itoa(number))
	}
	if omitted := total - len(numbers); omitted > 0 {
		rendered = append(rendered, fmt.Sprintf("... (%d more occurrences)", omitted))
	}
	return strings.Join(rendered, ", ")
}
