package hpatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

var (
	absoluteLinePattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	textSelectPattern   = regexp.MustCompile(`^tsel (\S+) (.+)$`)
	rangePattern        = regexp.MustCompile(`^rsel (\S+):(\S+)$`)
)

type instruction struct {
	attempt        commandAttempt
	source         string
	line           int
	operation      string
	path           string
	lineNumber     int
	endLine        int
	count          int
	text           string
	delimiter      string
	lineTerminator string
}

type program struct {
	instructions []instruction
}

type commandGroupError struct {
	commands []*commandError
}

func (e *commandGroupError) Error() string {
	messages := make([]string, len(e.commands))
	for index, command := range e.commands {
		messages[index] = command.Error()
	}
	return strings.Join(messages, "\n")
}

func (e *commandGroupError) Unwrap() []error {
	failures := make([]error, len(e.commands))
	for index, command := range e.commands {
		failures[index] = command
	}
	return failures
}

func commandsOf(err error) []*commandError {
	if failures, ok := errors.AsType[*commandGroupError](err); ok {
		return failures.commands
	}
	if command, ok := errors.AsType[*commandError](err); ok {
		return []*commandError{command}
	}
	return nil
}

type commandError struct {
	Attempt   commandAttempt
	Reason    failureReason
	Command   int
	Line      int
	Operation string
	Path      string
	Category  string
	Source    string
	Message   string
	// Repair is multi-line baseline context that a retry needs in order to
	// correct this command. It is excluded from Error, whose result is
	// sanitized onto one line, and is emitted separately.
	Repair     string
	Correction string
}

func (e *commandError) Error() string {
	var context []string
	if e.Command != 0 {
		context = append(context, fmt.Sprintf("command %d", e.Command))
	}
	context = append(context, fmt.Sprintf("source line %d", e.Line))
	if e.Operation != "" {
		context = append(context, fmt.Sprintf("operation %q", e.Operation))
	}
	if e.Path != "" {
		context = append(context, fmt.Sprintf("path %q", e.Path))
	}
	if e.Category != "" {
		context = append(context, "category "+e.Category)
	}
	return fmt.Sprintf("%s: %s", strings.Join(context, ", "), e.Message)
}

func parse(source string) (*program, error) {
	program := &program{}
	var failures []*commandError
	commandIndex := 0
	lines := hpatchsyntax.SplitPhysicalLines(source)
	for index := 0; index < len(lines); {
		headerIndex := index
		line := lines[headerIndex].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}
		commandIndex++
		sourceLine := headerIndex + 1
		attempt := recognizeCommandAttempt(line)

		frame, frameErr := hpatchsyntax.FrameCommand(lines, headerIndex, line)
		index = frame.Next
		var command instruction
		var err error
		switch {
		case frameErr != nil:
			err = scriptError(sourceLine, frameErr.Error())
		case frame.Delimiter != "":
			command = instruction{line: sourceLine, operation: "type", text: frame.Body}
		default:
			command, err = parseInstruction(sourceLine, line)
		}
		if err != nil {
			message := err.Error()
			if sourceError, ok := errors.AsType[*commandError](err); ok {
				message = sourceError.Message
			}
			failures = append(failures, &commandError{
				Attempt:   attempt,
				Reason:    reasonOf(err, reasonSyntax),
				Command:   commandIndex,
				Line:      sourceLine,
				Operation: strings.Fields(line)[0],
				Category:  "syntax",
				Source:    line,
				Message:   message,
			})
			continue
		}
		command.source = line
		command.delimiter = frame.Delimiter
		command.lineTerminator = lines[headerIndex].Terminator

		command.attempt = attemptForInstruction(command)
		program.instructions = append(program.instructions, command)
	}
	if len(failures) != 0 {
		return nil, &commandGroupError{commands: failures}
	}
	return program, nil
}

func recognizeCommandAttempt(line string) commandAttempt {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return commandAttempt{}
	}
	switch fields[0] {
	case "in", "new", "mv", "rm", "type", "del", "copy", "cut", "paste", "commit":
		return commandAttempt{recognized: true}
	case "rsel":
		return commandAttempt{recognized: recognizeRange(fields)}
	case "tsel":
		span := recognizeTextSpanVariant(line)
		if !recognizeLine(fields, 1) || span == textSpanNone {
			return commandAttempt{}
		}
		return commandAttempt{recognized: true, textSpan: span}
	default:
		return commandAttempt{}
	}
}

func recognizeLine(fields []string, index int) bool {
	return len(fields) > index && isUnsignedDecimal(fields[index])
}

func recognizeRange(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	start, end, ok := strings.Cut(fields[1], ":")
	return ok && isUnsignedDecimal(start) && isUnsignedDecimal(end)
}

func isUnsignedDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func recognizeTextSpanVariant(line string) textSpanVariant {
	match := textSelectPattern.FindStringSubmatch(line)
	if match == nil {
		return textSpanNone
	}
	_, trailing, err := hpatchsyntax.DecodeQuoted(match[2])
	if err != nil {
		return textSpanNone
	}
	trailing = strings.TrimSpace(trailing)
	if trailing != "" && trailing != "1" {
		return textSpanMultiple
	}
	return textSpanSingle
}

func attemptForInstruction(command instruction) commandAttempt {
	attempt := commandAttempt{recognized: true}
	if command.operation == "tsel" {
		attempt.textSpan = textSpanSingle
		if command.count > 1 {
			attempt.textSpan = textSpanMultiple
		}
	}
	return attempt
}

func parseInstruction(sourceLine int, line string) (instruction, error) {
	for _, operation := range []string{"in", "new", "mv"} {
		if path, ok := strings.CutPrefix(line, operation+" "); ok {
			if path == "" {
				return instruction{}, scriptError(sourceLine, "path must not be empty")
			}
			return instruction{line: sourceLine, operation: operation, path: filepath.Clean(path)}, nil
		}
	}

	if line == "rm" || line == "del" || line == "copy" || line == "cut" || line == "paste" || line == "commit" {
		return instruction{line: sourceLine, operation: line}, nil
	}

	if match := textSelectPattern.FindStringSubmatch(line); match != nil {
		lineNumber, err := parseLineNumber(sourceLine, match[1])
		if err != nil {
			return instruction{}, err
		}
		value, count, err := decodeTextSelection(match[2])
		if err != nil {
			return instruction{}, scriptFailure(sourceLine, reasonOf(err, reasonSyntax), err.Error())
		}
		if value == "" {
			return instruction{}, scriptError(sourceLine, "tsel text must not be empty")
		}
		if strings.ContainsAny(value, "\r\n") {
			return instruction{}, scriptError(sourceLine, "tsel text must stay on one line")
		}
		return instruction{
			line:       sourceLine,
			operation:  "tsel",
			lineNumber: lineNumber,
			count:      count,
			text:       value,
		}, nil
	}

	if match := rangePattern.FindStringSubmatch(line); match != nil {
		start, err := parseLineNumber(sourceLine, match[1])
		if err != nil {
			return instruction{}, err
		}
		end, err := parseLineNumber(sourceLine, match[2])
		if err != nil {
			return instruction{}, err
		}
		if start > end {
			return instruction{}, scriptFailure(sourceLine, reasonOrderOrOverlap, "line range start exceeds end")
		}
		return instruction{line: sourceLine, operation: "rsel", lineNumber: start, endLine: end}, nil
	}
	if valueText, ok := strings.CutPrefix(line, "type "); ok {
		value, trailing, err := hpatchsyntax.DecodeQuoted(valueText)
		if err != nil {
			return instruction{}, scriptError(sourceLine, "invalid quoted string for type: "+err.Error())
		}
		if !onlyOperandWhitespace(trailing) {
			return instruction{}, scriptError(sourceLine, "trailing text after type string")
		}
		return instruction{line: sourceLine, operation: "type", text: value}, nil
	}

	return instruction{}, scriptError(sourceLine, "unknown or malformed command")
}

func decodeTextSelection(encoded string) (string, int, error) {
	text, trailing, err := hpatchsyntax.DecodeQuoted(encoded)
	if err != nil {
		return "", 0, fmt.Errorf("invalid quoted string for tsel: %w", err)
	}
	if trailing == "" {
		return text, 1, nil
	}
	if !isOperandWhitespace(trailing[0]) {
		return "", 0, withReason(reasonInvalidCount, errors.New("tsel count must be separated by whitespace"))
	}
	countText := strings.Trim(trailing, " \t\r\n")
	if countText == "" {
		return text, 1, nil
	}
	if !absoluteLinePattern.MatchString(countText) {
		return "", 0, withReason(reasonInvalidCount, errors.New("invalid tsel count"))
	}
	count, err := strconv.Atoi(countText)
	if err != nil {
		return "", 0, withReason(reasonInvalidCount, errors.New("tsel count is out of range"))
	}
	return text, count, nil
}

func onlyOperandWhitespace(value string) bool {
	for index := range len(value) {
		if !isOperandWhitespace(value[index]) {
			return false
		}
	}
	return true
}

func isOperandWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func parseLineNumber(sourceLine int, value string) (int, error) {
	if !absoluteLinePattern.MatchString(value) {
		return 0, scriptError(sourceLine, fmt.Sprintf("invalid line reference %q", value))
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, scriptError(sourceLine, "line reference is out of range")
	}
	return number, nil
}

func scriptError(line int, message string) *commandError {
	return scriptFailure(line, reasonSyntax, message)
}

func scriptFailure(line int, reason failureReason, message string) *commandError {
	return &commandError{Line: line, Reason: reason, Message: message}
}
