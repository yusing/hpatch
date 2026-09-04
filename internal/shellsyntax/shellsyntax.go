package shellsyntax

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Parsed struct {
	Interpreter     []string       `json:"interpreter,omitempty"`
	Body            string         `json:"body,omitempty"`
	CommandTemplate string         `json:"commandTemplate,omitempty"`
	Params          map[string]any `json:"params"`
	HasParams       bool           `json:"hasParams,omitzero"`
	ScriptPath      string         `json:"scriptPath,omitempty"`
	HasScript       bool           `json:"hasScript,omitzero"`
}

// Parse reads the portable shell header. A retained-script path is returned to
// the host without reading it; filesystem resolution remains host-owned.
func Parse(input string) (Parsed, error) {
	if strings.ContainsRune(input, 0) {
		return Parsed{}, errors.New("script must not contain a NUL byte")
	}

	retainedLine, retainedBody := splitFirstLine(input)
	retainedLine = trimField(retainedLine)
	if path, ok := strings.CutPrefix(retainedLine, "#!script="); ok {
		if retainedBody != "" {
			return Parsed{}, errors.New("#!script must be the sole directive")
		}
		return Parsed{ScriptPath: path, HasScript: true}, nil
	}

	interpreter := []string{"bash"}
	body := input
	firstLine, firstBody := splitFirstLine(input)
	trimmed := trimField(firstLine)
	if strings.HasPrefix(trimmed, "#!") && !isDirectiveCandidate(trimmed) {
		selector := trimField(strings.TrimPrefix(trimmed, "#!"))
		if selector == "" {
			return Parsed{}, errors.New("shebang must select an interpreter")
		}
		interpreter = splitFields(selector)
		if interpreter[0] == "env" || interpreter[0] == "/usr/bin/env" {
			interpreter = interpreter[1:]
			if len(interpreter) != 0 && interpreter[0] == "-S" {
				interpreter = interpreter[1:]
			}
			if len(interpreter) == 0 || strings.HasPrefix(interpreter[0], "-") {
				return Parsed{}, errors.New("env shebang must select an interpreter")
			}
		}
		body = firstBody
	}

	commandTemplate, params, hasParams, body, err := parseDirectives(body)
	if err != nil {
		return Parsed{}, err
	}
	return Parsed{
		Interpreter:     interpreter,
		Body:            body,
		CommandTemplate: commandTemplate,
		Params:          params,
		HasParams:       hasParams,
	}, nil
}

// InterpreterIdentity normalizes an interpreter path for policy comparisons.
func InterpreterIdentity(interpreter string) string {
	base := filepath.Base(strings.ReplaceAll(interpreter, "\\", "/"))
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base
}

func parseDirectives(input string) (commandTemplate string, params map[string]any, hasParams bool, body string, err error) {
	remaining := input
	seen := make(map[string]struct{}, 2)
	for remaining != "" {
		line, rest := splitFirstLine(remaining)
		trimmed := trimField(line)
		key, value, ok := parseDirectiveLine(trimmed)
		if !ok {
			if malformedDirective(trimmed) || strings.HasPrefix(trimmed, "!") {
				return "", nil, false, "", errors.New("shell directive must use #!{key}={value}")
			}
			break
		}
		if key != "cmd" && key != "params" {
			return "", nil, false, "", fmt.Errorf("unsupported shell directive #!%s", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return "", nil, false, "", fmt.Errorf("shell directive #!%s must not occur more than once", key)
		}
		seen[key] = struct{}{}

		switch key {
		case "cmd":
			if value == "" {
				return "", nil, false, "", errors.New("command template must not be empty")
			}
			if strings.Count(value, "{.}") != 1 {
				return "", nil, false, "", errors.New("command template must contain exactly one {.} placeholder")
			}
			commandTemplate = value
		case "params":
			if err := json.Unmarshal([]byte(value), &params); err != nil {
				return "", nil, false, "", fmt.Errorf("#!params must contain a JSON object: %w", err)
			}
			if params == nil {
				return "", nil, false, "", errors.New("#!params must contain a JSON object")
			}
			hasParams = true
		}
		remaining = rest
	}
	return commandTemplate, params, hasParams, remaining, nil
}

func parseDirectiveLine(line string) (key, value string, ok bool) {
	if rest, matched := strings.CutPrefix(line, "#!"); matched {
		separator := strings.IndexByte(rest, '=')
		if separator > 0 && validDirectiveKey(rest[:separator]) {
			return rest[:separator], rest[separator+1:], true
		}
	}
	if rest, matched := strings.CutPrefix(line, "#"); matched {
		rest = strings.TrimLeft(rest, " \t")
		if rest, matched = strings.CutPrefix(rest, "!params"); matched &&
			(rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return "params", strings.TrimLeft(rest, " \t"), true
		}
	}
	return "", "", false
}

func validDirectiveKey(value string) bool {
	if value == "" {
		return false
	}
	first := value[0]
	if !('A' <= first && first <= 'Z') && !('a' <= first && first <= 'z') {
		return false
	}
	for _, character := range value[1:] {
		if character != '-' && character != '_' &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func isDirectiveCandidate(line string) bool {
	_, _, ok := parseDirectiveLine(line)
	return ok || malformedDirective(line)
}

func malformedDirective(line string) bool {
	for _, name := range []string{"#!cmd", "#!params"} {
		if rest, ok := strings.CutPrefix(line, name); ok &&
			(rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return true
		}
	}
	return false
}

func splitFirstLine(input string) (line, body string) {
	for index := range len(input) {
		if input[index] != '\r' && input[index] != '\n' {
			continue
		}
		end := index + 1
		if input[index] == '\r' && end < len(input) && input[end] == '\n' {
			end++
		}
		return input[:index], input[end:]
	}
	return input, ""
}

func trimField(value string) string {
	return strings.Trim(value, " \t")
}

func splitFields(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == ' ' || character == '\t'
	})
}
