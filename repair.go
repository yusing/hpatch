package hpatch

import (
	"fmt"
	"strconv"
	"strings"
)

const repairLineWindow = 2

const (
	repairPreviewLimit = 200
	repairListLimit    = 16
)

func (w *workspace) repairContext(command instruction, reason failureReason) string {
	if w.active == nil || w.active.editor.baseline == "" || command.target.kind == targetNone {
		return ""
	}
	editor := &w.active.editor
	lines := logicalLines(editor.baseline)
	if len(lines) == 0 {
		return ""
	}

	var report strings.Builder
	switch reason {
	case reasonRowStale:
		stale := command.target.start
		if command.target.kind == targetRange {
			if _, err := resolveRow(editor.baseline, command.target.start); err == nil {
				stale = command.target.end
			}
		}
		writeLineWindow(&report, editor.baseline, lines, stale.line)
	case reasonOccurrenceMissing:
		writeTextTargetRepair(&report, editor, lines, command.target)
	case reasonTargetOrder:
		fmt.Fprintf(&report, "row range resolves to lines %d:%d\n", command.target.start.line, command.target.end.line)
		writeLineWindow(&report, editor.baseline, lines, command.target.start.line)
	case reasonEditConflict:
		report.WriteString("baseline content conflicts with an earlier mutation\n")
		if claimed := editor.claimedLineSpans(lines); claimed != "" {
			fmt.Fprintf(&report, "earlier mutations: %s\n", claimed)
		}
		writeLineWindow(&report, editor.baseline, lines, command.target.start.line)
	}
	return report.String()
}

func writeTextTargetRepair(report *strings.Builder, editor *editor, lines []logicalLine, target targetSpec) {
	anchor, err := resolveRow(editor.baseline, target.start)
	if err != nil {
		return
	}
	offsets := nonOverlappingLiteralOffsets(editor.baseline[anchor.start:], target.literal, target.count)
	fmt.Fprintf(report, "found %d of %d requested matches at or after line %d\n", len(offsets), target.count, target.start.line)
	report.WriteString("if an earlier mutation introduces the target, apply that prerequisite, reread, and submit a later invocation\n")
	if len(offsets) != 0 {
		matchLines := make([]int, 0, min(len(offsets), repairListLimit))
		for _, offset := range offsets[:min(len(offsets), repairListLimit)] {
			matchLines = append(matchLines, lineNumberAt(lines, anchor.start+offset))
		}
		fmt.Fprintf(report, "matching lines: %s\n", joinLineNumbers(matchLines, len(offsets)))
	}
	writeLineWindow(report, editor.baseline, lines, target.start.line)
}

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
		writeHashLine(report, index, content, previewTextLimit(content, limit))
	}
}

func generatedSourceRepair(content string, line, column int) string {
	lines := renderedLines(content)
	if line < 1 || line > len(lines) {
		return ""
	}

	var report strings.Builder
	fmt.Fprintf(&report, "generated Go near %d:%d\n", line, column)
	start := max(1, line-repairLineWindow)
	end := min(len(lines), line+repairLineWindow)
	for index := start; index <= end; index++ {
		current := lines[index-1]
		limit := 64
		marker := " "
		if index == line {
			limit = repairPreviewLimit
			marker = ">"
		}
		text := lineContent(content, current)
		fmt.Fprintf(&report, "%s %d | %s\n", marker, index, previewTextLimit(text, limit))
	}
	return report.String()
}

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
