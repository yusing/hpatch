package mekugi

import "strings"

type renderedSpan struct {
	start int
	end   int
}

type renderedEdit struct {
	baselineEdit
	span renderedSpan
}

// projectBaselineEdits orders one immutable edit snapshot and computes its
// pre-format rendered spans. It borrows replacement strings without copying
// content; endpoint interpretation belongs to each consumer.
func projectBaselineEdits(source []baselineEdit) []renderedEdit {
	edits := orderedBaselineEdits(source)
	projection := make([]renderedEdit, 0, len(edits))
	baselineOffset, renderedOffset := 0, 0
	for _, edit := range edits {
		renderedOffset += edit.start - baselineOffset
		span := renderedSpan{start: renderedOffset, end: renderedOffset + len(edit.replacement)}
		projection = append(projection, renderedEdit{baselineEdit: edit, span: span})
		renderedOffset = span.end
		baselineOffset = max(baselineOffset, edit.end)
	}
	return projection
}

// renderedEdits shares one projection until an editor mutation changes the
// snapshot. Indentation probes and syntax-subset replays project their own
// snapshots and never replace the editor's cached projection.
func (e *editor) renderedEdits() []renderedEdit {
	if e.projected == nil {
		e.projected = projectBaselineEdits(e.edits)
	}
	return e.projected
}

func (e *editor) contentWithProjection(projection []renderedEdit) string {
	var result strings.Builder
	cursor := 0
	for _, edit := range projection {
		result.WriteString(e.baseline[cursor:edit.start])
		result.WriteString(edit.replacement)
		cursor = max(cursor, edit.end)
	}
	result.WriteString(e.baseline[cursor:])
	return result.String()
}
