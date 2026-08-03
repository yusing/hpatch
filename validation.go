package hpatch

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

type indentationCorrectionError struct {
	proposedLine     string
	proposedIndent   string
	correctionIndent string
	correctedText    string
}

func (e *indentationCorrectionError) Error() string {
	return "indentation-only change to preserved text"
}

func (e *indentationCorrectionError) diagnostic() string {
	var output strings.Builder
	fmt.Fprintf(&output, "proposed text: %s\n", strconv.Quote(e.proposedLine))
	fmt.Fprintf(
		&output,
		"indentation: proposed=%s correction=%s\n",
		strconv.Quote(e.proposedIndent),
		strconv.Quote(e.correctionIndent),
	)
	return output.String()
}

func detectIndentationCorrection(baseline string, selected targetSpan, replacement string) *indentationCorrectionError {
	if !selected.linewise {
		return nil
	}
	selectedLines := logicalLines(baseline[selected.start:selected.end])
	if len(selectedLines) != 1 {
		return nil
	}
	selectedLine := selectedLines[0]
	original := baseline[selected.start+selectedLine.start : selected.start+selectedLine.contentEnd]
	originalIndent, preserved := splitIndent(original)
	if preserved == "" {
		return nil
	}

	replacementLines := logicalLines(replacement)
	if len(replacementLines) != 1 {
		return nil
	}
	line := replacementLines[0]
	content := replacement[line.start:line.contentEnd]
	proposedIndent, remainder := splitIndent(content)
	if remainder != preserved || proposedIndent == originalIndent {
		return nil
	}

	corrected := replacement[:line.start] + originalIndent + replacement[line.start+len(proposedIndent):]
	return &indentationCorrectionError{
		proposedLine:     content,
		proposedIndent:   proposedIndent,
		correctionIndent: originalIndent,
		correctedText:    corrected,
	}
}

func splitIndent(line string) (string, string) {
	end := 0
	for end < len(line) && (line[end] == ' ' || line[end] == '\t') {
		end++
	}
	return line[:end], line[end:]
}

func correctedTypeCommand(command instruction, correctedText string) string {
	if command.delimiter != "" {
		return command.source + command.lineTerminator + correctedText + command.delimiter
	}
	encoded, err := json.Marshal(correctedText)
	if err != nil {
		panic("encoding a Go string as JSON cannot fail: " + err.Error())
	}
	if command.valueStart <= 0 || command.valueStart > len(command.source) {
		return command.source
	}
	return command.source[:command.valueStart] + string(encoded)
}

func commandCorrectionsOf(err error) []CommandCorrection {
	commands := commandsOf(err)
	corrections := make([]CommandCorrection, 0, len(commands))
	for _, command := range commands {
		if command.Correction == "" {
			continue
		}
		corrections = append(corrections, CommandCorrection{
			Command:     command.Command,
			Replacement: command.Correction,
		})
	}
	return corrections
}

func hostRejectionsOf(err error) []HostRejection {
	commands := commandsOf(err)
	rejections := make([]HostRejection, 0, len(commands))
	for _, command := range commands {
		rejections = append(rejections, HostRejection{
			Command:         command.Command,
			SourceLine:      command.Line,
			Operation:       command.Operation,
			Target:          hostTargetName(command.Attempt.target),
			Reason:          hostReasonName(command.Reason),
			Path:            command.Path,
			GeneratedLine:   command.GeneratedLine,
			GeneratedColumn: command.GeneratedColumn,
			ValueLine:       command.ValueLine,
		})
	}
	return rejections
}

func hostTargetName(target targetVariant) string {
	index := int(target) - 1
	if index < 0 || index >= len(targetVariantNames) {
		return ""
	}
	return targetVariantNames[index]
}

func hostReasonName(reason failureReason) string {
	if int(reason) < 0 || int(reason) >= len(failureReasonNames) {
		return failureReasonNames[reasonOther]
	}
	return failureReasonNames[reason]
}

func (w *workspace) formatGoFiles() *commandError {
	for _, file := range w.files {
		if file.deleted || filepath.Ext(file.path) != ".go" {
			continue
		}
		content := file.editor.content()
		if !file.created && file.original == content && filepath.Ext(file.originalPath) == ".go" {
			continue
		}
		formatted, err := format.Source([]byte(content))
		if err != nil {
			line, column := generatedPositionOf(err)
			location := file.editor.syntaxFailureLocation(content, line, column)
			if location.origin.command == 0 {
				location.origin = file.validationOrigin()
			}
			repair := generatedSourceRepair(content, line, column)
			repair += multilineValueRepair(location.origin.command, location.replacement, location.valueLine)
			return formatCommandError(
				file, location.origin, reasonLanguageSyntax, fmt.Sprintf("format Go source: %v", err),
				repair, line, column, location.valueLine,
			)
		}
		if string(formatted) != content {
			final := string(formatted)
			offsets, err := newFormattedOffsetMap(content, final)
			if err != nil {
				return formatCommandError(file, file.validationOrigin(), reasonOther, fmt.Sprintf("map formatted Go source: %v", err), "", 0, 0, 0)
			}
			file.editor.finalContent = &final
			file.editor.finalOffsets = offsets
		}
	}
	return nil
}

func (f *fileState) validationOrigin() editOrigin {
	if f.editor.lastOrigin.command != 0 {
		return f.editor.lastOrigin
	}
	return f.mutationOrigin
}

func generatedPositionOf(err error) (int, int) {
	if failures, ok := errors.AsType[scanner.ErrorList](err); ok && len(failures) != 0 && failures[0] != nil {
		return failures[0].Pos.Line, failures[0].Pos.Column
	}
	return 0, 0
}

const syntaxLocalizationGroupLimit = 32

type syntaxEditGroup struct {
	origin   editOrigin
	edits    []baselineEdit
	distance int

	replacement string
	valueLine   int
}

type syntaxFailureLocation struct {
	origin      editOrigin
	replacement string
	valueLine   int
}

func (e *editor) syntaxFailureLocation(content string, line, column int) syntaxFailureLocation {
	generatedOffset := generatedByteOffset(content, line, column)
	groups := e.syntaxEditGroups(generatedOffset, len(content))
	if len(groups) == 0 {
		return syntaxFailureLocation{origin: e.lastOrigin}
	}
	if len(groups) == 1 || len(groups) > syntaxLocalizationGroupLimit {
		return syntaxLocationOf(closestSyntaxEditGroup(groups))
	}
	if _, err := format.Source([]byte(e.baseline)); err != nil {
		return syntaxLocationOf(closestSyntaxEditGroup(groups))
	}

	// Remove far-away groups first. The remaining set is one-minimal: removing
	// any retained command makes the candidate source syntactically valid.
	slices.SortFunc(groups, func(first, second syntaxEditGroup) int {
		if order := cmp.Compare(second.distance, first.distance); order != 0 {
			return order
		}
		return cmp.Compare(first.origin.command, second.origin.command)
	})
	for index := 0; index < len(groups); {
		candidate := slices.Concat(groups[:index], groups[index+1:])
		if _, err := format.Source([]byte(e.contentWithSyntaxGroups(candidate))); err != nil {
			groups = candidate
			continue
		}
		index++
	}
	return syntaxLocationOf(closestSyntaxEditGroup(groups))
}

func syntaxLocationOf(group syntaxEditGroup) syntaxFailureLocation {
	return syntaxFailureLocation{origin: group.origin, replacement: group.replacement, valueLine: group.valueLine}
}

func (e *editor) syntaxEditGroups(generatedOffset, contentLength int) []syntaxEditGroup {
	indices := make(map[int]int)
	var groups []syntaxEditGroup
	for _, edit := range e.edits {
		index, ok := indices[edit.command]
		if !ok {
			index = len(groups)
			indices[edit.command] = index
			groups = append(groups, syntaxEditGroup{origin: edit.editOrigin, distance: contentLength})
		}
		groups[index].edits = append(groups[index].edits, edit)
	}

	baselineOffset := 0
	renderedOffset := 0
	for _, edit := range e.orderedEdits() {
		renderedOffset += edit.start - baselineOffset
		start := renderedOffset
		end := start + len(edit.replacement)
		distance := max(start-generatedOffset, generatedOffset-end, 0)
		index := indices[edit.command]
		if distance <= groups[index].distance {
			groups[index].distance = distance
			if edit.multilineValue {
				groups[index].replacement = edit.replacement
				groups[index].valueLine = replacementValueLine(edit.replacement, generatedOffset-start)
			}
		}
		renderedOffset = end
		baselineOffset = max(baselineOffset, edit.end)
	}
	return groups
}

func replacementValueLine(replacement string, offset int) int {
	lines := physicalValueLines(replacement)
	if len(lines) == 0 {
		return 0
	}
	offset = min(max(offset, 0), len(replacement))
	return lineNumberAt(lines, offset)
}

func physicalValueLines(value string) []logicalLine {
	physical := hpatchsyntax.SplitPhysicalLines(value)
	lines := make([]logicalLine, 0, len(physical))
	offset := 0
	for _, line := range physical {
		if line.Text == "" && line.Terminator == "" && offset == len(value) {
			break
		}
		contentEnd := offset + len(line.Text)
		fullEnd := contentEnd + len(line.Terminator)
		lines = append(lines, logicalLine{start: offset, contentEnd: contentEnd, fullEnd: fullEnd})
		offset = fullEnd
	}
	return lines
}

func closestSyntaxEditGroup(groups []syntaxEditGroup) syntaxEditGroup {
	return slices.MinFunc(groups, func(first, second syntaxEditGroup) int {
		if order := cmp.Compare(first.distance, second.distance); order != 0 {
			return order
		}
		return cmp.Compare(second.origin.command, first.origin.command)
	})
}

func (e *editor) contentWithSyntaxGroups(groups []syntaxEditGroup) string {
	var edits []baselineEdit
	for _, group := range groups {
		edits = append(edits, group.edits...)
	}
	return e.contentWithEdits(edits)
}

func generatedByteOffset(content string, line, column int) int {
	lines := renderedLines(content)
	if line < 1 || line > len(lines) {
		return len(content)
	}
	current := lines[line-1]
	return min(current.start+max(column-1, 0), current.contentEnd)
}

type formatToken struct {
	kind       token.Token
	literal    string
	start, end int
}

type formattedOffsetMap struct {
	beforeLength int
	afterLength  int
	before       []formatToken
	after        []formatToken
}

func newFormattedOffsetMap(before, after string) (*formattedOffsetMap, error) {
	beforeTokens := scanFormatTokens(before)
	afterTokens := scanFormatTokens(after)
	if len(beforeTokens) != len(afterTokens) {
		return nil, fmt.Errorf("token count changed from %d to %d", len(beforeTokens), len(afterTokens))
	}

	type tokenKey struct {
		kind    token.Token
		literal string
	}
	available := make(map[tokenKey][]formatToken)
	for _, current := range afterTokens {
		key := tokenKey{kind: current.kind, literal: current.literal}
		available[key] = append(available[key], current)
	}
	aligned := make([]formatToken, len(beforeTokens))
	for index, current := range beforeTokens {
		key := tokenKey{kind: current.kind, literal: current.literal}
		matches := available[key]
		if len(matches) == 0 {
			return nil, fmt.Errorf("formatted token %q has no source match", current.literal)
		}
		aligned[index] = matches[0]
		available[key] = matches[1:]
	}

	return &formattedOffsetMap{
		beforeLength: len(before),
		afterLength:  len(after),
		before:       beforeTokens,
		after:        aligned,
	}, nil
}

func scanFormatTokens(source string) []formatToken {
	files := token.NewFileSet()
	file := files.AddFile("", -1, len(source))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(source), nil, scanner.ScanComments)

	var tokens []formatToken
	for {
		position, kind, literal := lexer.Scan()
		if kind == token.EOF {
			return tokens
		}
		if kind == token.SEMICOLON {
			continue
		}
		if literal == "" {
			literal = kind.String()
		}
		start := file.Offset(position)
		tokens = append(tokens, formatToken{
			kind:    kind,
			literal: literal,
			start:   start,
			end:     start + len(literal),
		})
	}
}

func (m *formattedOffsetMap) mapOffset(offset int) int {
	if m == nil {
		return offset
	}
	offset = min(max(offset, 0), m.beforeLength)
	next := sort.Search(len(m.before), func(index int) bool {
		return m.before[index].start >= offset
	})
	if next < len(m.before) && m.before[next].start == offset {
		return m.after[next].start
	}
	if previous := next - 1; previous >= 0 && offset <= m.before[previous].end {
		return m.after[previous].start + min(offset-m.before[previous].start, m.after[previous].end-m.after[previous].start)
	}

	beforeStart, afterStart := 0, 0
	if next > 0 {
		beforeStart = m.before[next-1].end
		afterStart = m.after[next-1].end
	}
	beforeEnd, afterEnd := m.beforeLength, m.afterLength
	if next < len(m.before) {
		beforeEnd = m.before[next].start
		afterEnd = m.after[next].start
	}
	if beforeEnd == beforeStart {
		return min(max(afterStart, 0), m.afterLength)
	}
	if afterStart > afterEnd {
		if offset-beforeStart <= beforeEnd-offset {
			return min(afterStart, m.afterLength)
		}
		return max(afterEnd, 0)
	}
	return afterStart + (offset-beforeStart)*(afterEnd-afterStart)/(beforeEnd-beforeStart)
}

func formatCommandError(file *fileState, origin editOrigin, reason failureReason, message, repair string, generatedLine, generatedColumn, valueLine int) *commandError {
	category := ""
	if origin.operation != "" {
		category = commandCategory(origin.operation)
	}
	return &commandError{
		Attempt:         commandAttempt{recognized: origin.operation != "", target: origin.target},
		Reason:          reason,
		Command:         origin.command,
		Line:            origin.line,
		Operation:       origin.operation,
		Path:            file.path,
		Category:        category,
		Message:         message,
		Repair:          repair,
		GeneratedLine:   generatedLine,
		GeneratedColumn: generatedColumn,
		ValueLine:       valueLine,
	}
}
