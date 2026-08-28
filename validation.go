package hpatch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/parser"
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
		locations := command.Locations
		if len(locations) == 0 {
			locations = []commandErrorLocation{{
				GeneratedLine:   command.GeneratedLine,
				GeneratedColumn: command.GeneratedColumn,
				ValueLine:       command.ValueLine,
			}}
		}
		for _, location := range locations {
			rejections = append(rejections, HostRejection{
				Command:         command.Command,
				SourceLine:      command.Line,
				Operation:       command.Operation,
				Target:          hostTargetName(command.Target),
				Reason:          hostReasonName(command.Reason),
				Path:            command.Path,
				GeneratedLine:   location.GeneratedLine,
				GeneratedColumn: location.GeneratedColumn,
				ValueLine:       location.ValueLine,
			})
		}
	}
	return rejections
}

func hostFailuresOf(err error, failureStage string) []HostFailure {
	commands := commandsOf(err)
	if len(commands) == 0 {
		if failureStage == "" {
			return nil
		}
		return []HostFailure{{Reason: failureStage + "-failure", Scope: "new-transaction"}}
	}
	languageCommands := make(map[int]struct{})
	for _, command := range commands {
		if command.Reason == reasonLanguageSyntax {
			languageCommands[command.Command] = struct{}{}
		}
	}
	failures := make([]HostFailure, 0, len(commands))
	for _, command := range commands {
		scope := "field-local"
		switch command.Reason {
		case reasonEditConflict:
			scope = "multi-command"
			if command.CorrectionScope != "" {
				scope = command.CorrectionScope
			}
		case reasonActiveFile, reasonInitialization, reasonFilePath:
			scope = "new-script"
		case reasonLanguageSyntax:
			if len(languageCommands) > 1 {
				scope = "multi-command"
			}
		}
		failures = append(failures, HostFailure{
			Command:    command.Command,
			Path:       command.Path,
			Reason:     hostReasonName(command.Reason),
			Scope:      scope,
			Suggestion: strings.TrimSpace(command.Repair),
		})
	}
	return failures
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

func (w *workspace) validationFailure(ctx context.Context) error {
	failures := w.formatGoFiles(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	failures = append(failures, w.validateLanguageFiles(ctx)...)
	if err := ctx.Err(); err != nil {
		return err
	}
	return groupValidationFailures(failures)
}

func (w *workspace) formatGoFiles(ctx context.Context) []*commandError {
	var failures []*commandError
	for _, file := range w.files {
		if ctx.Err() != nil {
			return failures
		}
		if file.deleted || filepath.Ext(file.path) != ".go" {
			continue
		}
		content := file.editor.content()
		if !file.created && file.original == content && filepath.Ext(file.originalPath) == ".go" {
			continue
		}
		formatted, err := format.Source([]byte(content))
		if err != nil {
			for _, failure := range discoverGoSyntaxFailures(ctx, content, err) {
				location := file.editor.syntaxFailureLocation(content, failure.line, failure.column)
				if location.origin.command == 0 {
					location.origin = file.validationOrigin()
				}
				repair := generatedSourceRepair(content, failure.line, failure.column)
				repair += multilineValueRepair(location.origin.command, location.replacement, location.valueLine)
				commandFailure := formatCommandError(
					file, location.origin, reasonLanguageSyntax, failure.message,
					repair, failure.line, failure.column, location.valueLine,
				)
				if !failure.counted {
					commandFailure.Occurrences = 0
				}
				failures = append(failures, commandFailure)
			}
			continue
		}
		if string(formatted) != content {
			final := string(formatted)
			offsets, err := newFormattedOffsetMap(content, final)
			if err != nil {
				failures = append(failures, formatCommandError(file, file.validationOrigin(), reasonOther, fmt.Sprintf("map formatted Go source: %v", err), "", 0, 0, 0))
				continue
			}
			file.editor.finalContent = &final
			file.editor.finalOffsets = offsets
		}
	}
	w.autofixWhitespace()
	return failures
}

func (w *workspace) validateLanguageFiles(ctx context.Context) []*commandError {
	var failures []*commandError
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
		syntaxFailures := collapseLanguageSyntaxCascades(ctx, content, language, findLanguageSyntaxFailures(content, language))
		if err := ctx.Err(); err != nil {
			return nil
		}
		for _, failure := range syntaxFailures {
			location := file.editor.languageSyntaxFailureLocation(content, failure.line, failure.column, language)
			if location.origin.command == 0 {
				location.origin = file.validationOrigin()
			}
			repair := generatedSourceRepairForLanguage(content, failure.line, failure.column, name)
			repair += multilineValueRepair(location.origin.command, location.replacement, location.valueLine)
			failures = append(failures, formatCommandError(
				file,
				location.origin,
				reasonLanguageSyntax,
				languageSyntaxFailureMessage(name, failure),
				repair,
				failure.line,
				failure.column,
				location.valueLine,
			))
		}
	}
	return failures
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

type goSyntaxFailure struct {
	line    int
	column  int
	message string
	counted bool
}

func goSyntaxFailures(err error) []goSyntaxFailure {
	if scannerFailures, ok := errors.AsType[scanner.ErrorList](err); ok && len(scannerFailures) != 0 {
		failures := make([]goSyntaxFailure, 0, len(scannerFailures))
		for _, failure := range scannerFailures {
			if failure == nil {
				continue
			}
			failures = append(failures, goSyntaxFailure{
				line:    failure.Pos.Line,
				column:  failure.Pos.Column,
				message: failure.Msg,
				counted: true,
			})
		}
		if len(failures) != 0 {
			return failures
		}
	}
	return []goSyntaxFailure{{message: err.Error(), counted: true}}
}

func parseGoSyntaxFailures(source string) []goSyntaxFailure {
	_, err := parser.ParseFile(
		token.NewFileSet(),
		"",
		source,
		parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution,
	)
	if err == nil {
		return nil
	}
	return goSyntaxFailures(err)
}

func goSyntaxFailuresForSource(source string, fallback error) []goSyntaxFailure {
	failures := parseGoSyntaxFailures(source)
	if len(failures) == 0 {
		if fallback == nil {
			return nil
		}
		return goSyntaxFailures(fallback)
	}
	if fallback == nil {
		return failures
	}

	type position struct {
		line   int
		column int
	}
	counted := make(map[position]int)
	for _, failure := range goSyntaxFailures(fallback) {
		counted[position{line: failure.line, column: failure.column}]++
	}
	for index := range failures {
		key := position{line: failures[index].line, column: failures[index].column}
		failures[index].counted = counted[key] > 0
		if failures[index].counted {
			counted[key]--
		}
	}
	return failures
}

func discoverGoSyntaxFailures(ctx context.Context, content string, initial error) []goSyntaxFailure {
	candidate := content
	fallback := initial
	seenLines := make(map[int]bool)
	var discovered []goSyntaxFailure
	for {
		if ctx.Err() != nil {
			return discovered
		}
		failures := goSyntaxFailuresForSource(candidate, fallback)
		fallback = nil
		if len(failures) == 0 {
			return discovered
		}
		collapsed := collapseGoSyntaxCascades(ctx, candidate, failures)
		newLines := make(map[int]bool)
		for _, failure := range collapsed {
			if failure.line > 0 && !seenLines[failure.line] {
				newLines[failure.line] = true
			}
		}
		if len(newLines) == 0 {
			if len(discovered) == 0 {
				discovered = append(discovered, collapsed...)
			}
			return discovered
		}
		for _, failure := range collapsed {
			if newLines[failure.line] {
				discovered = append(discovered, failure)
			}
		}
		for line := range newLines {
			seenLines[line] = true
			var ok bool
			candidate, ok = blankGeneratedLine(candidate, line)
			if !ok {
				return discovered
			}
		}
	}
}

func collapseGoSyntaxCascades(ctx context.Context, content string, failures []goSyntaxFailure) []goSyntaxFailure {
	locations := make([]goSyntaxFailure, 0, len(failures))
	collapsed := make([]goSyntaxFailure, 0, len(failures))
	remainingByRepairLine := make(map[int]map[int]struct{})
	for _, failure := range failures {
		if ctx.Err() != nil {
			return collapsed
		}
		mapped := false
		for _, location := range locations {
			if failure.line == location.line {
				mapped = true
				break
			}
			if location.line < 1 {
				continue
			}
			remaining, ok := remainingByRepairLine[location.line]
			if !ok {
				if ctx.Err() != nil {
					return collapsed
				}
				remaining = goSyntaxFailureLinesAfterBlank(content, location.line)
				remainingByRepairLine[location.line] = remaining
			}
			if _, remains := remaining[failure.line]; !remains {
				counted := failure.counted
				failure = location
				failure.counted = counted
				mapped = true
				break
			}
		}
		if !mapped {
			locations = append(locations, failure)
		}
		collapsed = append(collapsed, failure)
	}
	return collapsed
}

func goSyntaxFailureLinesAfterBlank(content string, repairLine int) map[int]struct{} {
	candidate, ok := blankGeneratedLine(content, repairLine)
	if !ok {
		return nil
	}
	lines := make(map[int]struct{})
	for _, failure := range parseGoSyntaxFailures(candidate) {
		lines[failure.line] = struct{}{}
	}
	return lines
}

func collapseLanguageSyntaxCascades(ctx context.Context, content string, language indentationWrapperLanguage, failures []languageSyntaxFailure) []languageSyntaxFailure {
	locations := make([]languageSyntaxFailure, 0, len(failures))
	collapsed := make([]languageSyntaxFailure, 0, len(failures))
	remainingByRepairLine := make(map[int]map[int]struct{})
	for _, failure := range failures {
		if ctx.Err() != nil {
			return collapsed
		}
		mapped := false
		for _, location := range locations {
			if failure.line == location.line {
				mapped = true
				break
			}
			if location.line < 1 {
				continue
			}
			remaining, ok := remainingByRepairLine[location.line]
			if !ok {
				if ctx.Err() != nil {
					return collapsed
				}
				remaining = languageSyntaxFailureLinesAfterBlank(content, language, location.line)
				remainingByRepairLine[location.line] = remaining
			}
			if _, remains := remaining[failure.line]; !remains {
				failure = location
				mapped = true
				break
			}
		}
		if !mapped {
			locations = append(locations, failure)
		}
		collapsed = append(collapsed, failure)
	}
	return collapsed
}

func languageSyntaxFailureLinesAfterBlank(content string, language indentationWrapperLanguage, repairLine int) map[int]struct{} {
	candidate, ok := blankGeneratedLine(content, repairLine)
	if !ok {
		return nil
	}
	lines := make(map[int]struct{})
	for _, failure := range findLanguageSyntaxFailures(candidate, language) {
		lines[failure.line] = struct{}{}
	}
	return lines
}

func blankGeneratedLine(content string, line int) (string, bool) {
	lines := renderedLines(content)
	if line < 1 || line > len(lines) {
		return "", false
	}
	current := lines[line-1]
	candidate := []byte(content)
	for index := current.start; index < current.contentEnd; index++ {
		candidate[index] = ' '
	}
	return string(candidate), true
}

type validationFailureGroupKey struct {
	command int
	path    string
}

func groupValidationFailures(failures []*commandError) error {
	if len(failures) == 0 {
		return nil
	}
	slices.SortFunc(failures, func(first, second *commandError) int {
		if order := cmp.Compare(first.Command, second.Command); order != 0 {
			return order
		}
		if order := cmp.Compare(first.Path, second.Path); order != 0 {
			return order
		}
		if order := cmp.Compare(first.ValueLine, second.ValueLine); order != 0 {
			return order
		}
		if order := cmp.Compare(first.GeneratedLine, second.GeneratedLine); order != 0 {
			return order
		}
		return cmp.Compare(first.GeneratedColumn, second.GeneratedColumn)
	})

	indices := make(map[validationFailureGroupKey]int)
	groups := make([]*commandError, 0, len(failures))
	for _, failure := range failures {
		key := validationFailureGroupKey{command: failure.Command, path: failure.Path}
		index, ok := indices[key]
		if !ok {
			index = len(groups)
			indices[key] = index
			group := *failure
			group.Repair = ""
			group.Locations = nil
			groups = append(groups, &group)
		}
		group := groups[index]
		locationIndex := slices.IndexFunc(group.Locations, func(location commandErrorLocation) bool {
			return location.ValueLine == failure.ValueLine
		})
		if locationIndex >= 0 {
			group.Locations[locationIndex].Occurrences += failure.Occurrences
			continue
		}
		group.Locations = append(group.Locations, commandErrorLocation{
			Message:         failure.Message,
			Repair:          failure.Repair,
			GeneratedLine:   failure.GeneratedLine,
			GeneratedColumn: failure.GeneratedColumn,
			ValueLine:       failure.ValueLine,
			Occurrences:     max(failure.Occurrences, 1),
		})
	}

	for _, group := range groups {
		first := group.Locations[0]
		group.GeneratedLine = first.GeneratedLine
		group.GeneratedColumn = first.GeneratedColumn
		group.ValueLine = first.ValueLine
		if len(group.Locations) == 1 {
			group.Message = first.Message
			if first.Occurrences > 1 {
				group.Message = fmt.Sprintf("%s (and %d more errors)", group.Message, first.Occurrences-1)
			}
		} else {
			group.Message = fmt.Sprintf("%d distinct syntax failures", len(group.Locations))
		}
		for _, location := range group.Locations {
			if location.Repair == "" {
				continue
			}
			if group.Repair != "" {
				group.Repair += "\n"
			}
			group.Repair += location.Repair
		}
	}
	if len(groups) == 1 {
		return groups[0]
	}
	return &commandGroupError{commands: groups}
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
		return len(findLanguageSyntaxFailures(source, language)) == 0
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
		Target:          origin.target,
		Reason:          reason,
		Command:         origin.command,
		Line:            origin.line,
		Operation:       origin.operation,
		Path:            file.path,
		Category:        category,
		Message:         message,
		Occurrences:     1,
		Repair:          repair,
		GeneratedLine:   generatedLine,
		GeneratedColumn: generatedColumn,
		ValueLine:       valueLine,
	}
}
