package hpatch

import (
	"encoding/json"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
			Command:    command.Command,
			SourceLine: command.Line,
			Operation:  command.Operation,
			Target:     hostTargetName(command.Attempt.target),
			Reason:     hostReasonName(command.Reason),
			Path:       command.Path,
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
			return formatCommandError(file, reasonLanguageSyntax, fmt.Sprintf("format Go source: %v", err))
		}
		if string(formatted) != content {
			final := string(formatted)
			offsets, err := newFormattedOffsetMap(content, final)
			if err != nil {
				return formatCommandError(file, reasonOther, fmt.Sprintf("map formatted Go source: %v", err))
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

func formatCommandError(file *fileState, reason failureReason, message string) *commandError {
	origin := file.validationOrigin()
	category := ""
	if origin.operation != "" {
		category = commandCategory(origin.operation)
	}
	return &commandError{
		Attempt:   commandAttempt{recognized: origin.operation != ""},
		Reason:    reason,
		Command:   origin.command,
		Line:      origin.line,
		Operation: origin.operation,
		Path:      file.path,
		Category:  category,
		Message:   message,
	}
}
