package hpatch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	selectPattern     = regexp.MustCompile(`^sel ([1-9][0-9]*) ([1-9][0-9]*):([1-9][0-9]*)$`)
	textSelectPattern = regexp.MustCompile(`^tsel ([1-9][0-9]*) (-?[1-9][0-9]*) (.+)$`)
	rangePattern      = regexp.MustCompile(`^rsel ([1-9][0-9]*):([1-9][0-9]*)$`)
)

type instruction struct {
	line       int
	operation  string
	path       string
	lineNumber int
	start      int
	end        int
	occurrence int
	text       string
}

type program struct {
	instructions []instruction
}

type commandError struct {
	Line    int
	Message string
}

func (e *commandError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

func parse(source string) (*program, error) {
	program := &program{}
	for index, raw := range strings.Split(source, "\n") {
		lineNumber := index + 1
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		command, err := parseInstruction(lineNumber, line)
		if err != nil {
			return nil, err
		}
		program.instructions = append(program.instructions, command)
	}
	return program, nil
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

	if line == "rm" || line == "del" || line == "dup" {
		return instruction{line: sourceLine, operation: line}, nil
	}

	if match := selectPattern.FindStringSubmatch(line); match != nil {
		lineNumber, err := parseInteger(sourceLine, match[1])
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
			line:       sourceLine,
			operation:  "sel",
			lineNumber: lineNumber,
			start:      start,
			end:        end,
		}, nil
	}

	if match := textSelectPattern.FindStringSubmatch(line); match != nil {
		lineNumber, err := parseInteger(sourceLine, match[1])
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
			lineNumber: lineNumber,
			occurrence: occurrence,
			text:       value,
		}, nil
	}

	if match := rangePattern.FindStringSubmatch(line); match != nil {
		start, err := parseInteger(sourceLine, match[1])
		if err != nil {
			return instruction{}, err
		}
		end, err := parseInteger(sourceLine, match[2])
		if err != nil {
			return instruction{}, err
		}
		if start > end {
			return instruction{}, scriptError(sourceLine, "line range start exceeds end")
		}
		return instruction{line: sourceLine, operation: "rsel", start: start, end: end}, nil
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

func decodeJSONString(encoded string) (string, error) {
	var value string
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		return "", err
	}
	return value, nil
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
