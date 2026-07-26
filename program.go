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
	line       int
	operation  string
	path       string
	lineRef    lineReference
	endLineRef lineReference
	start      int
	end        int
	occurrence int
	text       string
	endText    string
}

type program struct {
	instructions []instruction
}

type commandError struct {
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
		command, err := parseInstruction(lineNumber, line, relativeLines)
		if err != nil {
			message := err.Error()
			if sourceError, ok := errors.AsType[*commandError](err); ok {
				message = sourceError.Message
			}
			return nil, &commandError{
				Command:   commandIndex,
				Line:      lineNumber,
				Operation: strings.Fields(line)[0],
				Category:  "syntax",
				Message:   message,
			}
		}
		program.instructions = append(program.instructions, command)
	}
	return program, nil
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
			return instruction{}, scriptError(sourceLine, "selection start exceeds end")
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
		value, err := decodeJSONString(match[3])
		if err != nil {
			return instruction{}, scriptError(sourceLine, "invalid JSON string")
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
			return instruction{}, scriptError(sourceLine, operation+" literals must differ")
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
			return instruction{}, scriptError(sourceLine, "rsel endpoints must both be absolute or both be relative")
		}
		if !start.relative && start.value > end.value {
			return instruction{}, scriptError(sourceLine, "line range start exceeds end")
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
		return lineReference{}, scriptError(sourceLine, "relative line references are disabled by HPATCH_DISABLE_RELATIVE_LINES=1")
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
	return &commandError{Line: line, Message: message}
}
