package hpatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	absoluteLinePattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	relativeLinePattern = regexp.MustCompile(`^(?:\+[0-9]+|-[1-9][0-9]*)$`)
	selectPattern       = regexp.MustCompile(`^sel (\S+) ([1-9][0-9]*):([1-9][0-9]*)$`)
	textSelectPattern   = regexp.MustCompile(`^tsel (\S+) (-?[1-9][0-9]*) (.+)$`)
	rangePattern        = regexp.MustCompile(`^rsel (\S+):(\S+)$`)
)

type lineReference struct {
	value    int
	relative bool
}

type instruction struct {
	attempt    commandAttempt
	line       int
	operation  string
	path       string
	lineRef    lineReference
	endLineRef lineReference
	start      int
	end        int
	occurrence int
	count      int
	text       string
	endText    string
}

type program struct {
	instructions []instruction
}

type commandError struct {
	Attempt   commandAttempt
	Reason    failureReason
	Command   int
	Line      int
	Operation string
	Path      string
	Category  string
	Message   string
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

func parse(source string, relativeLines bool) (*program, error) {
	program := &program{}
	for index, raw := range strings.Split(source, "\n") {
		lineNumber := index + 1
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		commandIndex := len(program.instructions) + 1
		attempt := recognizeCommandAttempt(line)
		command, err := parseInstruction(lineNumber, line, relativeLines)
		if err != nil {
			message := err.Error()
			if sourceError, ok := errors.AsType[*commandError](err); ok {
				message = sourceError.Message
			}
			return nil, &commandError{
				Attempt:   attempt,
				Reason:    reasonOf(err, reasonSyntax),
				Command:   commandIndex,
				Line:      lineNumber,
				Operation: strings.Fields(line)[0],
				Category:  "syntax",
				Message:   message,
			}
		}
		command.attempt = attemptForInstruction(command)
		program.instructions = append(program.instructions, command)
	}
	return program, nil
}

func recognizeCommandAttempt(line string) commandAttempt {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return commandAttempt{}
	}
	switch fields[0] {
	case "in", "new", "mv", "rm", "bsel", "bsel_next", "type", "del", "dup":
		return commandAttempt{recognized: true}
	case "sel":
		coordinate := recognizeLineVariant(fields, 1)
		return commandAttempt{recognized: coordinate != coordinateNone, coordinate: coordinate}
	case "rsel":
		coordinate := recognizeRangeVariant(fields)
		return commandAttempt{recognized: coordinate != coordinateNone, coordinate: coordinate}
	case "tsel":
		coordinate := recognizeLineVariant(fields, 1)
		span := recognizeTextSpanVariant(line)
		if coordinate == coordinateNone || span == textSpanNone {
			return commandAttempt{}
		}
		return commandAttempt{recognized: true, coordinate: coordinate, textSpan: span}
	default:
		return commandAttempt{}
	}
}

func recognizeLineVariant(fields []string, index int) coordinateVariant {
	if len(fields) <= index {
		return coordinateNone
	}
	value := fields[index]
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return coordinateRelative
	}
	if isUnsignedDecimal(value) {
		return coordinateAbsolute
	}
	return coordinateNone
}

func recognizeRangeVariant(fields []string) coordinateVariant {
	if len(fields) < 2 {
		return coordinateNone
	}
	start, end, ok := strings.Cut(fields[1], ":")
	if !ok || start == "" || end == "" {
		return coordinateNone
	}
	startVariant := recognizeLineVariant([]string{start}, 0)
	endVariant := recognizeLineVariant([]string{end}, 0)
	if startVariant == coordinateNone || endVariant == coordinateNone {
		return coordinateNone
	}
	if startVariant == coordinateRelative || endVariant == coordinateRelative {
		return coordinateRelative
	}
	return coordinateAbsolute
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
	_, trailing, ok := decodeLeadingTextOperand(match[3])
	if !ok {
		return textSpanNone
	}
	trailing = strings.TrimSpace(trailing)
	if trailing != "" && trailing != "1" {
		return textSpanMultiple
	}
	return textSpanSingle
}

func decodeLeadingTextOperand(encoded string) (string, string, bool) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	var text string
	if err := decoder.Decode(&text); err != nil {
		return "", "", false
	}
	return text, encoded[decoder.InputOffset():], true
}

func attemptForInstruction(command instruction) commandAttempt {
	attempt := commandAttempt{recognized: true}
	switch command.operation {
	case "sel", "rsel":
		attempt.coordinate = coordinateAbsolute
		if command.lineRef.relative {
			attempt.coordinate = coordinateRelative
		}
	case "tsel":
		attempt.coordinate = coordinateAbsolute
		if command.lineRef.relative {
			attempt.coordinate = coordinateRelative
		}
		attempt.textSpan = textSpanSingle
		if command.count > 1 {
			attempt.textSpan = textSpanMultiple
		}
	}
	return attempt
}

func parseInstruction(sourceLine int, line string, relativeLines bool) (instruction, error) {
	for _, operation := range []string{"in", "new", "mv"} {
		if path, ok := strings.CutPrefix(line, operation+" "); ok {
			if path == "" {
				return instruction{}, scriptError(sourceLine, "path must not be empty")
			}
			return instruction{line: sourceLine, operation: operation, path: filepath.Clean(path)}, nil
		}
	}

	if line == "rm" || line == "del" || line == "dup" {
		return instruction{line: sourceLine, operation: line}, nil
	}

	if match := selectPattern.FindStringSubmatch(line); match != nil {
		lineRef, err := parseLineReference(sourceLine, match[1], relativeLines)
		if err != nil {
			return instruction{}, err
		}
		start, err := parseInteger(sourceLine, match[2])
		if err != nil {
			return instruction{}, err
		}
		end, err := parseInteger(sourceLine, match[3])
		if err != nil {
			return instruction{}, err
		}
		if start > end {
			return instruction{}, scriptFailure(sourceLine, reasonOrderOrOverlap, "selection start exceeds end")
		}
		return instruction{
			line:      sourceLine,
			operation: "sel",
			lineRef:   lineRef,
			start:     start,
			end:       end,
		}, nil
	}

	if match := textSelectPattern.FindStringSubmatch(line); match != nil {
		lineRef, err := parseLineReference(sourceLine, match[1], relativeLines)
		if err != nil {
			return instruction{}, err
		}
		occurrence, err := parseInteger(sourceLine, match[2])
		if err != nil {
			return instruction{}, err
		}
		value, count, err := decodeTextSelection(match[3])
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
			lineRef:    lineRef,
			occurrence: occurrence,
			count:      count,
			text:       value,
		}, nil
	}

	for _, operation := range []string{"bsel", "bsel_next"} {
		valueText, ok := strings.CutPrefix(line, operation+" ")
		if !ok {
			continue
		}
		startText, endText, err := decodeTwoJSONStrings(valueText)
		if err != nil {
			return instruction{}, scriptError(sourceLine, "invalid "+operation+" JSON strings")
		}
		if startText == "" || endText == "" {
			return instruction{}, scriptError(sourceLine, operation+" literals must not be empty")
		}
		if startText == endText {
			return instruction{}, scriptFailure(sourceLine, reasonOrderOrOverlap, operation+" literals must differ")
		}
		return instruction{line: sourceLine, operation: operation, text: startText, endText: endText}, nil
	}

	if match := rangePattern.FindStringSubmatch(line); match != nil {
		start, err := parseLineReference(sourceLine, match[1], relativeLines)
		if err != nil {
			return instruction{}, err
		}
		end, err := parseLineReference(sourceLine, match[2], relativeLines)
		if err != nil {
			return instruction{}, err
		}
		if start.relative != end.relative {
			return instruction{}, scriptFailure(sourceLine, reasonOrderOrOverlap, "rsel endpoints must both be absolute or both be relative")
		}
		if !start.relative && start.value > end.value {
			return instruction{}, scriptFailure(sourceLine, reasonOrderOrOverlap, "line range start exceeds end")
		}
		return instruction{line: sourceLine, operation: "rsel", lineRef: start, endLineRef: end}, nil
	}

	if valueText, ok := strings.CutPrefix(line, "type "); ok {
		value, err := decodeJSONString(valueText)
		if err != nil {
			return instruction{}, scriptError(sourceLine, "invalid JSON string")
		}
		return instruction{line: sourceLine, operation: "type", text: value}, nil
	}

	return instruction{}, scriptError(sourceLine, "unknown or malformed command")
}

func decodeTextSelection(encoded string) (string, int, error) {
	text, trailing, ok := decodeLeadingTextOperand(encoded)
	if !ok {
		return "", 0, errors.New("invalid JSON string")
	}
	if trailing == "" {
		return text, 1, nil
	}
	if !isJSONWhitespace(trailing[0]) {
		return "", 0, withReason(reasonInvalidCount, errors.New("tsel count must be separated by whitespace"))
	}
	countText := strings.TrimSpace(trailing)
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

func decodeTwoJSONStrings(encoded string) (string, string, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	var start, end string
	if err := decoder.Decode(&start); err != nil {
		return "", "", err
	}
	separatorOffset := decoder.InputOffset()
	if separatorOffset >= int64(len(encoded)) || !isJSONWhitespace(encoded[separatorOffset]) {
		return "", "", errors.New("bsel literals must be separated by whitespace")
	}
	if err := decoder.Decode(&end); err != nil {
		return "", "", err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", "", errors.New("trailing JSON value")
		}
		return "", "", err
	}
	return start, end, nil
}

func isJSONWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func decodeJSONString(encoded string) (string, error) {
	var value string
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		return "", err
	}
	return value, nil
}

func parseLineReference(sourceLine int, value string, relativeLines bool) (lineReference, error) {
	relative := strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-")
	if relative && !relativeLines {
		return lineReference{}, scriptFailure(sourceLine, reasonRelativeDisabled, "relative line references are disabled by HPATCH_DISABLE_RELATIVE_LINES=1")
	}
	if (!relative && !absoluteLinePattern.MatchString(value)) || (relative && !relativeLinePattern.MatchString(value)) {
		return lineReference{}, scriptError(sourceLine, fmt.Sprintf("invalid line reference %q", value))
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return lineReference{}, scriptError(sourceLine, "line reference is out of range")
	}
	return lineReference{value: number, relative: relative}, nil
}

func parseInteger(sourceLine int, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, scriptError(sourceLine, "number is out of range")
	}
	return number, nil
}

func scriptError(line int, message string) *commandError {
	return scriptFailure(line, reasonSyntax, message)
}

func scriptFailure(line int, reason failureReason, message string) *commandError {
	return &commandError{Line: line, Reason: reason, Message: message}
}
