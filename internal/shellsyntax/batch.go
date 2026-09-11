package shellsyntax

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Split separates programs before host translation. After a program body starts,
// column-one interpreter selectors and params directives are reserved boundaries,
// including inside language strings and heredocs. Command templates remain local
// to their program. Only an omitted params directive inherits the previous object.
func Split(input string) ([]string, error) {
	var programs []string
	start, offset := 0, 0
	bodyStarted := false
	for remaining := input; remaining != ""; {
		line, rest := splitFirstLine(remaining)
		trimmed := trimField(line)
		key, _, directive := parseDirectiveLine(trimmed)
		selector := strings.HasPrefix(trimmed, "#!") && !isDirectiveCandidate(trimmed)
		boundary := line == strings.TrimLeft(line, " \t") &&
			(selector || key == "params" || key == "script")
		if boundary && (bodyStarted || (selector && offset > start)) {
			programs = append(programs, input[start:offset])
			start = offset
			bodyStarted = false
		}
		if !directive && !(offset == start && selector) {
			bodyStarted = true
		}
		offset += len(remaining) - len(rest)
		remaining = rest
	}
	programs = append(programs, input[start:])

	var inherited map[string]any
	for index, source := range programs {
		parsed, err := Parse(source)
		if err != nil {
			return nil, fmt.Errorf("shell program %d: %w", index+1, err)
		}
		if len(programs) > 1 {
			if parsed.HasScript {
				return nil, fmt.Errorf("shell program %d: #!script must be the sole directive", index+1)
			}
			if strings.TrimSpace(parsed.Body) == "" {
				return nil, fmt.Errorf("shell program %d: batch programs must have a body", index+1)
			}
		}
		if parsed.HasParams {
			inherited = parsed.Params
		} else if inherited != nil {
			params, err := json.Marshal(inherited)
			if err != nil {
				return nil, err
			}
			header := "#!params=" + string(params) + "\n"
			line, rest := splitFirstLine(source)
			trimmed := trimField(line)
			if strings.HasPrefix(trimmed, "#!") && !isDirectiveCandidate(trimmed) {
				// Preserve the authored selector and its complete line terminator.
				header = source[:len(source)-len(rest)] + header
				source = rest
			}
			programs[index] = header + source
		}
	}
	return programs, nil
}
