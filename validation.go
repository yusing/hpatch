package hpatch

import (
	"cmp"
	"context"
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
				file, location.origin, reasonLanguageSyntax, goSyntaxFailureMessage(err),
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
	w.autofixWhitespace()
	return nil
}

func (w *workspace) validateLanguageFiles(ctx context.Context) *commandError {
	for _, file := range w.files {
		if err := ctx.Err(); err != nil {
			return nil
		}
		language, name, ok := languageSyntaxForPath(file.path)
		if file.deleted || !ok {
			continue
		}
		content := file.editor.content()
		if !file.created && file.originalPath == file.path && file.original == content {
			continue
		}
		failure, found := findLanguageSyntaxFailure(content, language)
		if err := ctx.Err(); err != nil {
			return nil
		}
		if !found {
			continue
		}
		location := file.editor.languageSyntaxFailureLocation(content, failure.line, failure.column, language)
		if location.origin.command == 0 {
			location.origin = file.validationOrigin()
		}
		repair := generatedSourceRepairForLanguage(content, failure.line, failure.column, name)
		repair += multilineValueRepair(location.origin.command, location.replacement, location.valueLine)
		return formatCommandError(
			file,
			location.origin,
			reasonLanguageSyntax,
			languageSyntaxFailureMessage(name, failure),
			repair,
			failure.line,
			failure.column,
			location.valueLine,
		)
	}
	return nil
}

func languageSyntaxForPath(path string) (indentationWrapperLanguage, string, bool) {
	switch filepath.Ext(path) {
	case ".py":
		return indentationLanguagePython, "Python", true
	case ".js":
		return indentationLanguageJavaScript, "JavaScript", true
	case ".ts":
		return indentationLanguageTypeScript, "TypeScript", true
	default:
		return 0, "", false
	}
}

func languageSyntaxFailureMessage(name string, failure languageSyntaxFailure) string {
	if failure.missing {
		if failure.kind != "" {
			return fmt.Sprintf("parse %s source: missing %q at %d:%d", name, failure.kind, failure.line, failure.column)
		}
		return fmt.Sprintf("parse %s source: missing syntax at %d:%d", name, failure.line, failure.column)
	}
	if failure.kind != "" {
		return fmt.Sprintf("parse %s source: syntax error %q at %d:%d", name, failure.kind, failure.line, failure.column)
	}
	return fmt.Sprintf("parse %s source: syntax error at %d:%d", name, failure.line, failure.column)
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

func goSyntaxFailureMessage(err error) string {
	if failures, ok := errors.AsType[scanner.ErrorList](err); ok && len(failures) != 0 && failures[0] != nil {
		message := failures[0].Msg
		if omitted := len(failures) - 1; omitted > 0 {
			return fmt.Sprintf("%s (and %d more errors)", message, omitted)
		}
		return message
	}
	return err.Error()
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
	return e.syntaxFailureLocationWith(content, line, column, func(source string) bool {
		_, err := format.Source([]byte(source))
		return err == nil
	})
}

func (e *editor) languageSyntaxFailureLocation(content string, line, column int, language indentationWrapperLanguage) syntaxFailureLocation {
	return e.syntaxFailureLocationWith(content, line, column, func(source string) bool {
		_, found := findLanguageSyntaxFailure(source, language)
		return !found
	})
}

func (e *editor) syntaxFailureLocationWith(content string, line, column int, valid func(string) bool) syntaxFailureLocation {
	generatedOffset := generatedByteOffset(content, line, column)
	groups := e.syntaxEditGroups(generatedOffset, len(content))
	if len(groups) == 0 {
		return syntaxFailureLocation{origin: e.lastOrigin}
	}
	if len(groups) == 1 || len(groups) > syntaxLocalizationGroupLimit {
		return syntaxLocationOf(closestSyntaxEditGroup(groups))
	}
	if !valid(e.baseline) {
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
		if !valid(e.contentWithSyntaxGroups(candidate)) {
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
	deletions    []whitespaceDeletion
	subsequent   *formattedOffsetMap
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
	var mapped int
	if len(m.deletions) != 0 {
		mapped = m.mapDeletedOffset(offset)
	} else {
		mapped = m.mapTokenOffset(offset)
	}
	return m.subsequent.mapOffset(mapped)
}

func (m *formattedOffsetMap) mapDeletedOffset(offset int) int {
	offset = min(max(offset, 0), m.beforeLength)
	removed := 0
	for _, deletion := range m.deletions {
		if offset <= deletion.start {
			return offset - removed
		}
		if offset < deletion.end {
			return deletion.start - removed
		}
		removed += deletion.end - deletion.start
	}
	return offset - removed
}

func (m *formattedOffsetMap) mapTokenOffset(offset int) int {
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
