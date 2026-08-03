package hpatch

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
	file          *fileState
	operation     string
	spans         []renderedSpan
	previewOffset int
}

func (w *workspace) finalStateReport(changes []change) string {
	var report strings.Builder
	if w.active == nil {
		report.WriteString("no active file\n")
	} else {
		fmt.Fprintf(&report, "in %s\n", escapeReportControls(w.active.path))
	}

	last := w.lastReportedEdit()
	if last == nil {
		report.WriteString("last none\n")
	} else {
		last.writeSummary(&report)
	}
	w.writeFileSummary(&report, changes)
	if w.active != nil {
		w.writeActivePreview(&report, last)
	}
	return report.String()
}

func (w *workspace) lastReportedEdit() *reportedEdit {
	if len(w.reportedEdits) == 0 {
		return nil
	}
	return w.reportedEdits[len(w.reportedEdits)-1]
}

func (e *editor) reportedEdit(origin editOrigin) *reportedEdit {
	var spans []renderedSpan
	for _, edit := range e.edits {
		if edit.command == origin.command {
			spans = append(spans, renderedSpan{start: edit.targetStart, end: edit.targetEnd})
		}
	}
	if len(spans) == 0 {
		return nil
	}

	previewOffset := 0
	renderedOffset := 0
	baselineOffset := 0
	for _, edit := range e.orderedEdits() {
		renderedOffset += edit.start - baselineOffset
		if edit.command == origin.command {
			previewOffset = renderedOffset
			break
		}
		renderedOffset += len(edit.replacement)
		baselineOffset = max(baselineOffset, edit.end)
	}
	return &reportedEdit{operation: origin.operation, spans: spans, previewOffset: previewOffset}
}

func (e *reportedEdit) writeSummary(report *strings.Builder) {
	document := renderedDocument{content: e.file.editor.baseline, lines: renderedLines(e.file.editor.baseline)}
	fmt.Fprintf(
		report,
		"last %s %s %d ranges ",
		e.operation,
		escapeReportControls(e.file.path),
		len(e.spans),
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
		fmt.Fprintf(report, "%d:%d-%d:%d", start.line, start.column, end.line, end.column)
	}
	if omitted := len(spans) - locationLimit; omitted > 0 {
		fmt.Fprintf(report, " +%d more", omitted)
	}
	report.WriteByte('\n')
}

func (w *workspace) writeActivePreview(report *strings.Builder, last *reportedEdit) {
	document := renderedDocument{content: w.active.editor.content(), lines: previewLines(w.active.editor.content())}
	line := 1
	if last != nil && last.file == w.active && len(last.spans) != 0 {
		offset := w.active.editor.finalOffsets.mapOffset(last.previewOffset)
		line = renderedCoordinateAt(document.content, document.lines, offset).line
	}
	line = min(max(line, 1), len(document.lines))
	start := max(0, line-2)
	if start+3 > len(document.lines) {
		start = max(0, len(document.lines)-3)
	}
	for index := start; index < min(start+3, len(document.lines)); index++ {
		writePreviewLine(report, document, index)
	}
}

func previewLines(text string) []logicalLine {
	if text == "" {
		return []logicalLine{{}}
	}
	return logicalLines(text)
}

func writePreviewLine(report *strings.Builder, document renderedDocument, index int) {
	line := document.lines[index]
	content := lineContent(document.content, line)
	writeHashLine(report, index+1, content, previewText(content))
}

func (w *workspace) writeFileSummary(report *strings.Builder, changes []change) {
	added, updated, moved, deleted := 0, 0, 0, 0
	for _, change := range changes {
		switch change.kind {
		case changeAdd:
			added++
		case changeDelete:
			deleted++
		case changeUpdate:
			if change.originalPath != change.path {
				moved++
			}
			if change.original != change.content {
				updated++
			}
		}
	}
	fmt.Fprintf(report, "files add=%d update=%d move=%d delete=%d\n", added, updated, moved, deleted)
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
