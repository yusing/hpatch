package hpatch

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
)

type indentationCorrectionKind uint8

const (
	indentationCorrectionExact indentationCorrectionKind = iota + 1
	indentationCorrectionPythonWrapper
	indentationCorrectionBracedWrapper
)

type indentationPolicyKind uint8

const (
	indentationPolicyGo indentationPolicyKind = iota + 1
	indentationPolicyAuto
	indentationPolicyReject
)

type indentationWrapperLanguage uint8

const (
	indentationLanguagePython indentationWrapperLanguage = iota + 1
	indentationLanguageJavaScript
	indentationLanguageTypeScript
)

type languageSyntaxFailure struct {
	line    int
	column  int
	kind    string
	missing bool
}

type indentationCandidate struct {
	kind       indentationCorrectionKind
	correction *indentationCorrectionError
	wrapper    indentationWrapperCandidate
}

type indentationWrapperCandidate struct {
	childLine     int
	wrapperIndent string
	preserved     string
}

type preparedWrapperCorrection struct {
	editIndex   int
	sequence    int
	replacement string
	childStart  int
	childEnd    int
	candidate   indentationWrapperCandidate
}

type indentationWrapperProbe struct {
	replacementStart int
	replacementEnd   int
	childStart       int
	childEnd         int
	candidate        indentationWrapperCandidate
}

func indentationPolicy(path string) indentationPolicyKind {
	if filepath.Ext(path) == ".go" {
		return indentationPolicyGo
	}
	if _, _, ok := languageSyntaxForPath(path); ok {
		return indentationPolicyAuto
	}
	return indentationPolicyReject
}

func detectIndentationCandidate(baseline string, selected targetSpan, replacement string) (indentationCandidate, bool) {
	if !selected.linewise {
		return indentationCandidate{}, false
	}
	if correction := detectIndentationCorrection(baseline, selected, replacement); correction != nil {
		return indentationCandidate{
			kind:       indentationCorrectionExact,
			correction: correction,
		}, true
	}
	return detectIndentationWrapperCandidate(baseline, selected, replacement)
}

func detectIndentationWrapperCandidate(baseline string, selected targetSpan, replacement string) (indentationCandidate, bool) {
	selectedLines := logicalLines(baseline[selected.start:selected.end])
	if len(selectedLines) != 1 {
		return indentationCandidate{}, false
	}
	selectedLine := selectedLines[0]
	original := baseline[selected.start+selectedLine.start : selected.start+selectedLine.contentEnd]
	originalIndent, preserved := splitIndent(original)
	if preserved == "" || indentationCommentText(preserved) {
		return indentationCandidate{}, false
	}

	replacementLines := logicalLines(replacement)
	if len(replacementLines) == 2 {
		header := replacementLines[0]
		child := replacementLines[1]
		headerContent := replacement[header.start:header.contentEnd]
		childContent := replacement[child.start:child.contentEnd]
		headerIndent, headerText := splitIndent(headerContent)
		childIndent, childText := splitIndent(childContent)
		if headerIndent != originalIndent ||
			childText != preserved ||
			strings.Count(childContent, preserved) != 1 ||
			!pythonIfHeader(headerText) {
			return indentationCandidate{}, false
		}
		return indentationCandidate{
			kind: indentationCorrectionPythonWrapper,
			wrapper: indentationWrapperCandidate{
				childLine:     1,
				wrapperIndent: originalIndent,
				preserved:     preserved,
			},
			correction: &indentationCorrectionError{
				proposedLine:     childContent,
				proposedIndent:   childIndent,
				correctionIndent: originalIndent,
				correctedText:    replacement,
			},
		}, true
	}
	if len(replacementLines) != 3 {
		return indentationCandidate{}, false
	}
	header := replacementLines[0]
	child := replacementLines[1]
	closeLine := replacementLines[2]
	headerContent := replacement[header.start:header.contentEnd]
	childContent := replacement[child.start:child.contentEnd]
	closeContent := replacement[closeLine.start:closeLine.contentEnd]
	headerIndent, headerText := splitIndent(headerContent)
	childIndent, childText := splitIndent(childContent)
	closeIndent, closeText := splitIndent(closeContent)
	if headerIndent != originalIndent ||
		closeIndent != originalIndent ||
		closeText != "}" ||
		childText != preserved ||
		strings.Count(childContent, preserved) != 1 ||
		!bracedIfHeader(headerText) {
		return indentationCandidate{}, false
	}
	return indentationCandidate{
		kind: indentationCorrectionBracedWrapper,
		wrapper: indentationWrapperCandidate{
			childLine:     1,
			wrapperIndent: originalIndent,
			preserved:     preserved,
		},
		correction: &indentationCorrectionError{
			proposedLine:     childContent,
			proposedIndent:   childIndent,
			correctionIndent: originalIndent,
			correctedText:    replacement,
		},
	}, true
}

func indentationCommentText(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "#") || strings.HasPrefix(value, "//") ||
		strings.HasPrefix(value, "/*") || strings.HasPrefix(value, "*")
}

func pythonIfHeader(header string) bool {
	if !strings.HasPrefix(header, "if") || len(header) < 3 || (header[2] != ' ' && header[2] != '\t') {
		return false
	}
	header = strings.TrimRight(header, " \t")
	if !strings.HasSuffix(header, ":") {
		return false
	}
	condition := strings.TrimSpace(header[2 : len(header)-1])
	return condition != "" && !strings.ContainsAny(condition, "#;")
}

func bracedIfHeader(header string) bool {
	if !strings.HasPrefix(header, "if") || len(header) < 4 || (header[2] != ' ' && header[2] != '\t') {
		return false
	}
	header = strings.TrimRight(header, " \t")
	if !strings.HasSuffix(header, "{") {
		return false
	}
	open := strings.IndexByte(header, '(')
	close := strings.LastIndexByte(header, ')')
	if open < 0 || close <= open || close != len(header)-3 {
		return false
	}
	condition := strings.TrimSpace(header[open+1 : close])
	return condition != "" && !strings.ContainsAny(condition, ";/#")
}

func (w *workspace) applyLanguageIndentation(ctx context.Context) error {
	for _, file := range w.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.deleted || len(file.editor.pendingIndentation) == 0 ||
			indentationPolicy(file.path) != indentationPolicyAuto {
			continue
		}
		if err := w.applySupportedIndentation(ctx, file); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (w *workspace) applySupportedIndentation(ctx context.Context, file *fileState) error {
	language, _, ok := languageSyntaxForPath(file.path)
	if !ok {
		return nil
	}
	unit := inferIndentationUnit(file.editor.baseline, language)
	var prepared []preparedWrapperCorrection
	for _, candidate := range file.editor.pendingIndentation {
		if err := ctx.Err(); err != nil {
			return err
		}
		editIndex := candidate.sequence - 1
		if editIndex < 0 || editIndex >= len(file.editor.edits) ||
			file.editor.edits[editIndex].sequence != candidate.sequence {
			continue
		}
		switch candidate.kind {
		case indentationCorrectionExact:
			if file.editor.edits[editIndex].replacement != candidate.correction.correctedText {
				file.editor.edits[editIndex].replacement = candidate.correction.correctedText
			}
		case indentationCorrectionPythonWrapper, indentationCorrectionBracedWrapper:
			if unit == "" ||
				(candidate.kind == indentationCorrectionPythonWrapper && language != indentationLanguagePython) ||
				(candidate.kind == indentationCorrectionBracedWrapper &&
					language != indentationLanguageJavaScript && language != indentationLanguageTypeScript) {
				continue
			}
			corrected, childStart, childEnd, changed, ok := prepareWrapperReplacement(
				file.editor.edits[editIndex].replacement,
				candidate.wrapper,
				unit,
			)
			if !ok || !changed {
				continue
			}
			prepared = append(prepared, preparedWrapperCorrection{
				editIndex:   editIndex,
				sequence:    candidate.sequence,
				replacement: corrected,
				childStart:  childStart,
				childEnd:    childEnd,
				candidate:   candidate.wrapper,
			})
		}
	}
	if len(prepared) == 0 {
		return nil
	}

	probeEdits := slices.Clone(file.editor.edits)
	for _, correction := range prepared {
		probeEdits[correction.editIndex].replacement = correction.replacement
	}
	source := file.editor.contentWithEdits(probeEdits)
	probes := make([]indentationWrapperProbe, 0, len(prepared))
	ranges := renderedEditRanges(probeEdits)
	for _, correction := range prepared {
		span, ok := ranges[correction.sequence]
		if !ok {
			return nil
		}
		probes = append(probes, indentationWrapperProbe{
			replacementStart: span.start,
			replacementEnd:   span.end,
			childStart:       span.start + correction.childStart,
			childEnd:         span.start + correction.childEnd,
			candidate:        correction.candidate,
		})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	proven := proveWrapperMemberships(source, probes, language)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(proven) != len(prepared) || slices.Contains(proven, false) {
		return nil
	}
	for _, correction := range prepared {
		file.editor.edits[correction.editIndex].replacement = correction.replacement
	}
	return nil
}

func prepareWrapperReplacement(replacement string, candidate indentationWrapperCandidate, unit string) (corrected string, childStart, childEnd int, changed, ok bool) {
	lines := logicalLines(replacement)
	if candidate.childLine < 0 || candidate.childLine >= len(lines) {
		return replacement, 0, 0, false, false
	}
	child := lines[candidate.childLine]
	childContent := replacement[child.start:child.contentEnd]
	proposedIndent, preserved := splitIndent(childContent)
	if preserved != candidate.preserved || strings.Count(childContent, candidate.preserved) != 1 {
		return replacement, 0, 0, false, false
	}
	desiredIndent := candidate.wrapperIndent + unit
	if proposedIndent == desiredIndent {
		return replacement, 0, 0, false, true
	}
	corrected = replacement[:child.start] + desiredIndent + replacement[child.start+len(proposedIndent):]
	correctedLines := logicalLines(corrected)
	correctedChild := correctedLines[candidate.childLine]
	return corrected,
		correctedChild.start + len(desiredIndent),
		correctedChild.contentEnd,
		true,
		true
}

func renderedEditRanges(source []baselineEdit) map[int]renderedSpan {
	ranges := make(map[int]renderedSpan, len(source))
	baselineOffset := 0
	renderedOffset := 0
	for _, edit := range orderedBaselineEdits(source) {
		renderedOffset += edit.start - baselineOffset
		start, end := renderedOffset, renderedOffset+len(edit.replacement)
		ranges[edit.sequence] = renderedSpan{start: start, end: end}
		renderedOffset = end
		baselineOffset = max(baselineOffset, edit.end)
	}
	return ranges
}
