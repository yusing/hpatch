package hpatch

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

type targetSpan struct {
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
	targetStart int
	targetEnd   int
	replacement string
	sequence    int
}

type editor struct {
	baseline     string
	edits        []baselineEdit
	lastOrigin   editOrigin
	finalContent *string
	finalOffsets *formattedOffsetMap
}

type logicalLine struct {
	start      int
	contentEnd int
	fullEnd    int
}

func (e *editor) resolveTarget(target targetSpec) ([]targetSpan, error) {
	switch target.kind {
	case targetLine:
		line, err := resolveRow(e.baseline, target.start)
		if err != nil {
			return nil, err
		}
		return []targetSpan{{start: line.start, end: line.fullEnd, linewise: true}}, nil
	case targetRange:
		start, err := resolveRow(e.baseline, target.start)
		if err != nil {
			return nil, err
		}
		end, err := resolveRow(e.baseline, target.end)
		if err != nil {
			return nil, err
		}
		if target.start.line > target.end.line {
			return nil, withReason(reasonTargetOrder, fmt.Errorf(
				"row range start %d exceeds end %d",
				target.start.line,
				target.end.line,
			))
		}
		return []targetSpan{{start: start.start, end: end.fullEnd, linewise: true}}, nil
	case targetText:
		anchor, err := resolveRow(e.baseline, target.start)
		if err != nil {
			return nil, err
		}
		offsets := nonOverlappingLiteralOffsets(e.baseline[anchor.start:], target.literal, target.count)
		if len(offsets) != target.count {
			return nil, withReason(reasonOccurrenceMissing, fmt.Errorf(
				"found %d of %d requested matches of %q at or after line %d",
				len(offsets),
				target.count,
				target.literal,
				target.start.line,
			))
		}
		spans := make([]targetSpan, len(offsets))
		for index, offset := range offsets {
			start := anchor.start + offset
			spans[index] = targetSpan{start: start, end: start + len(target.literal)}
		}
		return spans, nil
	default:
		return nil, withReason(reasonInitialization, fmt.Errorf("mutation requires an explicit target"))
	}
}

func resolveRow(baseline string, reference rowReference) (logicalLine, error) {
	lines := logicalLines(baseline)
	if reference.line < 1 || reference.line > len(lines) {
		return logicalLine{}, withReason(reasonRowMissing, fmt.Errorf(
			"row %d is outside immutable baseline with %d lines",
			reference.line,
			len(lines),
		))
	}
	line := lines[reference.line-1]
	actual := hashLine(lineContent(baseline, line))
	if actual != reference.hash {
		return logicalLine{}, withReason(reasonRowStale, fmt.Errorf(
			"row %d is stale: expected hash %s, actual %s",
			reference.line,
			reference.hash,
			actual,
		))
	}
	return line, nil
}

func (e *editor) applyMutation(operation string, target targetSpec, value string, origin editOrigin) error {
	spans, err := e.resolveTarget(target)
	if err != nil {
		return err
	}
	edits := make([]baselineEdit, len(spans))
	for index, span := range spans {
		replacement := value
		start, end := span.start, span.end
		switch operation {
		case "type":
			if span.linewise && lineTerminatorSuffix(replacement) == "" {
				replacement += lineTerminatorSuffix(e.baseline[span.start:span.end])
			}
			if len(spans) == 1 {
				if correction := detectIndentationCorrection(e.baseline, span, replacement); correction != nil {
					return correction
				}
			}
		case "type-":
			end = start
		case "type+":
			start = end
		case "del":
			replacement = ""
		default:
			panic("parsed instruction has no mutation executor: " + operation)
		}
		edits[index] = baselineEdit{
			start:       start,
			end:         end,
			targetStart: span.start,
			targetEnd:   span.end,
			replacement: replacement,
			editOrigin:  origin,
		}
	}
	if err := e.recordEdits(edits); err != nil {
		return withReason(reasonEditConflict, err)
	}
	return nil
}

func (e *editor) initialize(value string, origin editOrigin) {
	e.baseline = ""
	e.edits = nil
	e.finalContent = nil
	e.finalOffsets = nil
	if value == "" {
		return
	}
	e.edits = []baselineEdit{{
		start:       0,
		end:         0,
		targetStart: 0,
		targetEnd:   0,
		replacement: value,
		sequence:    1,
		editOrigin:  origin,
	}}
	e.lastOrigin = origin
}

func (e *editor) recordEdits(candidates []baselineEdit) error {
	pending := slices.Clone(e.edits)
	additions := make([]baselineEdit, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.start == candidate.end && candidate.replacement == "" {
			continue
		}
		if candidate.start != candidate.end && candidate.replacement == e.baseline[candidate.start:candidate.end] {
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
		candidate.sequence = len(pending) + 1
		pending = append(pending, candidate)
		additions = append(additions, candidate)
	}
	e.edits = append(e.edits, additions...)
	if len(additions) != 0 {
		e.lastOrigin = additions[len(additions)-1].editOrigin
	}
	return nil
}

func describeEditConflict(baseline string, first, second baselineEdit) (string, bool) {
	firstInsertion := first.start == first.end
	secondInsertion := second.start == second.end
	switch {
	case firstInsertion && secondInsertion:
		return "", false
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
		firstInsertion := first.start == first.end
		secondInsertion := second.start == second.end
		if firstInsertion != secondInsertion {
			if firstInsertion {
				return -1
			}
			return 1
		}
		return cmp.Compare(first.sequence, second.sequence)
	})
	return edits
}

func (e *editor) content() string {
	if e.finalContent != nil {
		return *e.finalContent
	}
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
