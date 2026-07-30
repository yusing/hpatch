package hpatch

import (
	"cmp"
	"fmt"
	"slices"
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

type renderedSpan struct {
	start int
	end   int
}

type renderedDocument struct {
	content string
	lines   []logicalLine
}

type reportedEdit struct {
	file        *fileState
	operation   string
	textMatches bool
	spans       []renderedSpan
}

type reportedLineCorrection struct {
	file       *fileState
	correction lineCorrection
}

func (w *workspace) finalStateReport(changes []change) string {
	var report strings.Builder
	var activeSpans []renderedSpan
	var activeDocument renderedDocument
	if w.active == nil {
		report.WriteString("no active file\n")
	} else {
		activeDocument = w.active.editor.renderedDocument()
		activeSpans = w.active.editor.writeFinalState(&report, w.active.path, activeDocument)
	}
	lastEdit := w.lastReportedEdit()
	var lastEditDocument renderedDocument
	if lastEdit != nil {
		if lastEdit.file == w.active {
			lastEditDocument = activeDocument
		} else {
			lastEditDocument = lastEdit.file.editor.renderedDocument()
		}
		projected := *lastEdit
		projected.spans = lastEdit.file.editor.mapFinalSpans(lastEdit.spans)
		lastEdit = &projected
		lastEdit.writeSummary(&report, lastEditDocument)
	}
	w.writeFileSummary(&report, changes)

	switch {
	case w.active != nil && len(w.active.editor.selections) != 0:
		writePreview(&report, activeDocument, activeSpans, len(activeSpans) > 1)
	case lastEdit != nil:
		writePreview(&report, lastEditDocument, lastEdit.spans, len(lastEdit.spans) > 1)
	case w.active != nil:
		writePreview(&report, activeDocument, activeSpans, false)
	}

	var corrections []reportedLineCorrection
	for _, file := range w.files {
		for _, correction := range file.editor.corrections {
			corrections = append(corrections, reportedLineCorrection{file: file, correction: correction})
		}
	}
	slices.SortFunc(corrections, func(first, second reportedLineCorrection) int {
		return cmp.Compare(first.correction.command, second.correction.command)
	})
	for _, repaired := range corrections {
		repaired.file.editor.writeLineCorrection(&report, repaired.file.path, repaired.correction)
	}
	return report.String()
}

func (w *workspace) lastReportedEdit() *reportedEdit {
	if len(w.reportedEdits) == 0 {
		return nil
	}
	return w.reportedEdits[len(w.reportedEdits)-1]
}

func (e *editor) renderedDocument() renderedDocument {
	content := e.content()
	return renderedDocument{content: content, lines: renderedLines(content)}
}

func (e *editor) reportedEdit(origin editOrigin, textMatches bool) *reportedEdit {
	spans := e.renderedEdits(origin.command)
	if len(spans) == 0 {
		return nil
	}
	return &reportedEdit{operation: origin.operation, textMatches: textMatches, spans: spans}
}

func (e *editor) renderedEdits(command int) []renderedSpan {
	var spans []renderedSpan
	renderedOffset := 0
	baselineOffset := 0
	for _, edit := range e.orderedEdits() {
		renderedOffset += edit.start - baselineOffset
		start := renderedOffset
		renderedOffset += len(edit.replacement)
		baselineOffset = max(baselineOffset, edit.end)
		if edit.command == command {
			spans = append(spans, renderedSpan{start: start, end: renderedOffset})
		}
	}
	return spans
}

func (e *editor) writeFinalState(report *strings.Builder, path string, document renderedDocument) []renderedSpan {
	if len(e.selections) != 0 {
		spans := make([]renderedSpan, len(e.selections))
		for index, selected := range e.selections {
			spans[index] = renderedSpan{
				start: e.mapFinalOffset(e.mapBaselineOffset(selected.start, boundaryAfter, 0)),
				end:   e.mapFinalOffset(e.mapBaselineOffset(selected.end, boundaryBefore, 0)),
			}
		}
		if len(spans) == 1 {
			start := renderedCoordinateAt(document.content, document.lines, spans[0].start)
			end := renderedCoordinateAt(document.content, document.lines, spans[0].end)
			fmt.Fprintf(report, "in %s %d:%d-%d:%d\n", escapeReportControls(path), start.line, start.column, end.line, end.column)
			return spans
		}
		fmt.Fprintf(report, "in %s %d selections: ", escapeReportControls(path), len(spans))
		writeSpanLocations(report, document, spans)
		return spans
	}

	affinity := boundaryBefore
	if e.cursorCommand != 0 {
		affinity = boundaryAfter
	}
	offset := e.mapBaselineOffset(e.cursor, affinity, e.cursorCommand)
	offset = e.mapFinalOffset(offset)
	position := renderedCoordinateAt(document.content, document.lines, offset)
	fmt.Fprintf(report, "in %s %d:%d\n", escapeReportControls(path), position.line, position.column)
	return []renderedSpan{{start: offset, end: offset}}
}

func (e *reportedEdit) writeSummary(report *strings.Builder, document renderedDocument) {
	noun := "edit"
	if len(e.spans) != 1 {
		noun = "edits"
	}
	if e.textMatches {
		noun = "tsel match"
		if len(e.spans) != 1 {
			noun = "tsel matches"
		}
	}
	fmt.Fprintf(
		report,
		"last edit in %s: %s %d %s: ",
		escapeReportControls(e.file.path),
		e.operation,
		len(e.spans),
		noun,
	)
	writeSpanLocations(report, document, e.spans)
}

func writeSpanLocations(report *strings.Builder, document renderedDocument, spans []renderedSpan) {
	const locationLimit = 3
	for index, span := range spans[:min(len(spans), locationLimit)] {
		if index != 0 {
			report.WriteString(", ")
		}
		start := renderedCoordinateAt(document.content, document.lines, span.start)
		end := renderedCoordinateAt(document.content, document.lines, span.end)
		fmt.Fprintf(report, "%d:%d", start.line, start.column)
		if start != end {
			fmt.Fprintf(report, "-%d:%d", end.line, end.column)
		}
	}
	if omitted := len(spans) - locationLimit; omitted > 0 {
		fmt.Fprintf(report, ", … +%d", omitted)
	}
	report.WriteByte('\n')
}

func writePreview(report *strings.Builder, document renderedDocument, spans []renderedSpan, separate bool) {
	if !separate {
		position := renderedCoordinateAt(document.content, document.lines, spans[0].start)
		start := max(0, position.line-2)
		if start+3 > len(document.lines) {
			start = max(0, len(document.lines)-3)
		}
		for index := start; index < min(start+3, len(document.lines)); index++ {
			writePreviewLine(report, document, index)
		}
		return
	}

	lastLine := -1
	written := 0
	for _, span := range spans {
		line := renderedCoordinateAt(document.content, document.lines, span.start).line - 1
		if line == lastLine {
			continue
		}
		writePreviewLine(report, document, line)
		lastLine = line
		written++
		if written == 3 {
			return
		}
	}
}

func writePreviewLine(report *strings.Builder, document renderedDocument, index int) {
	line := document.lines[index]
	fmt.Fprintf(report, "%d|%s\n", index+1, previewText(document.content[line.start:line.contentEnd]))
}

func (w *workspace) writeFileSummary(report *strings.Builder, changes []change) {
	if len(changes) == 0 {
		report.WriteString("files: no changes\n")
		return
	}
	if len(changes) == 1 && !w.shouldReportSingleFileAction(changes[0]) {
		return
	}

	type actionCount struct {
		count int
		name  string
	}
	actions := []actionCount{
		{name: "updated"},
		{name: "moved"},
		{name: "moved+updated"},
		{name: "added"},
		{name: "deleted"},
	}
	for _, change := range changes {
		switch change.kind {
		case changeAdd:
			actions[3].count++
		case changeDelete:
			actions[4].count++
		case changeUpdate:
			moved := change.originalPath != change.path
			updated := change.original != change.content
			switch {
			case moved && updated:
				actions[2].count++
			case moved:
				actions[1].count++
			case updated:
				actions[0].count++
			}
		}
	}

	report.WriteString("files: ")
	written := 0
	for _, action := range actions {
		if action.count == 0 {
			continue
		}
		if written != 0 {
			report.WriteString(", ")
		}
		fmt.Fprintf(report, "%d %s", action.count, action.name)
		written++
	}
	report.WriteByte('\n')
}

func (w *workspace) shouldReportSingleFileAction(change change) bool {
	if change.kind != changeUpdate || change.originalPath != change.path {
		return true
	}
	return w.active == nil || w.active.path != change.path
}

func (e *editor) writeLineCorrection(report *strings.Builder, path string, correction lineCorrection) {
	content := e.content()
	lines := renderedLines(content)
	offset := e.mapCorrectionOffset(correction.offset)
	offset = e.mapFinalOffset(offset)
	position := renderedCoordinateAt(content, lines, offset)
	fmt.Fprintf(
		report,
		"repaired command %d %s line %d to %d in %s\n",
		correction.command,
		correction.operation,
		correction.requestedLine,
		correction.resolvedLine,
		escapeReportControls(path),
	)

	start := max(0, position.line-2)
	if start+3 > len(lines) {
		start = max(0, len(lines)-3)
	}
	for index := start; index < min(start+3, len(lines)); index++ {
		line := lines[index]
		fmt.Fprintf(report, "%d|%s\n", index+1, previewText(content[line.start:line.contentEnd]))
	}
}

func (e *editor) mapCorrectionOffset(offset int) int {
	renderedOffset := 0
	baselineOffset := 0
	for _, edit := range e.orderedEdits() {
		if edit.start > offset {
			break
		}
		renderedOffset += edit.start - baselineOffset
		if edit.start == edit.end {
			renderedOffset += len(edit.replacement)
			baselineOffset = edit.end
			continue
		}
		if edit.start == offset || edit.end > offset {
			return renderedOffset
		}
		renderedOffset += len(edit.replacement)
		baselineOffset = edit.end
	}
	return renderedOffset + max(0, offset-baselineOffset)
}

func (e *editor) mapFinalOffset(offset int) int {
	return e.finalOffsets.mapOffset(offset)
}

func (e *editor) mapFinalSpans(spans []renderedSpan) []renderedSpan {
	if e.finalOffsets == nil {
		return spans
	}
	projected := make([]renderedSpan, len(spans))
	for index, span := range spans {
		projected[index] = renderedSpan{
			start: e.mapFinalOffset(span.start),
			end:   e.mapFinalOffset(span.end),
		}
	}
	return projected
}

func (e *editor) mapBaselineOffset(offset int, affinity boundaryAffinity, targetCommand int) int {
	renderedOffset := 0
	baselineOffset := 0
	for _, edit := range e.orderedEdits() {
		if edit.start > offset {
			break
		}
		renderedOffset += edit.start - baselineOffset

		if targetCommand != 0 && edit.command == targetCommand && (edit.start == offset || edit.end == offset) {
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

// previewTextLimit renders text on one output line, preserving tabs, escaping
// other control characters, and truncating to limit code points.
func previewTextLimit(text string, limit int) string {
	var preview strings.Builder
	count := 0
	for _, character := range text {
		if count == limit {
			break
		}
		count++
		if unicode.IsControl(character) && character != '\t' {
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
