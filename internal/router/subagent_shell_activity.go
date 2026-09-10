package router

import (
	"encoding/json"
	"strings"

	"github.com/yusing/mekugi/internal/shellsyntax"
)

// Only presentation reads these references. In particular, accepting a params
// header here does not make that combination valid executable shell syntax.
func toolActivityScriptReference(script string) string {
	script = strings.TrimSpace(script)
	if strings.HasPrefix(script, "#!params=") {
		_, script, _ = strings.Cut(script, "\n")
	}
	parsed, err := shellsyntax.Parse(strings.TrimSpace(script))
	if err == nil && parsed.HasScript {
		return "#!script=" + parsed.ScriptPath
	}
	return ""
}

// Keep the excerpt to one line and 120 characters, including the ellipsis.
func toolActivityCommandExcerpt(script string) string {
	script, _ = toolActivityUnwrapShell(script, "bash")
	if parsed, err := shellsyntax.Parse(script); err == nil && !parsed.HasScript {
		script = parsed.Body
	}
	script = strings.TrimSpace(script)
	line, _, more := strings.Cut(script, "\n")
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > 120 || more && len(runes) >= 120 {
		line = string(runes[:119])
		more = true
	}
	if more {
		line = strings.TrimSpace(line) + "…"
	}
	return line
}

func toolActivityShellCall(item map[string]json.RawMessage, name string, requireResultMetadata bool) (string, map[string]json.RawMessage, string) {
	name = strings.TrimPrefix(name, "functions.")
	input := jsonString(item, "arguments")
	if input == "" {
		input = jsonString(item, "input")
	}
	if name == "exec" {
		if nested, ok := toolActivityUnwrapExec(input, requireResultMetadata); ok {
			return toolActivityShellCall(nested, jsonString(nested, "name"), requireResultMetadata)
		}
	}
	var args map[string]json.RawMessage
	_ = json.Unmarshal([]byte(input), &args)
	script := input
	if args != nil {
		script = jsonString(args, "cmd")
		if script == "" {
			script = jsonString(args, "command")
		}
		var argv []string
		if json.Unmarshal(args["command"], &argv) == nil && len(argv) > 0 {
			script = workerCommand(argv[0], argv[1:])
			if len(argv) == 3 && (argv[0] == "bash" || argv[0] == "sh") && (argv[1] == "-c" || argv[1] == "-lc") {
				script = argv[2]
			}
		}
	}
	if name == "shell" || name == "exec_command" || name == "shell_command" {
		script, _ = toolActivityUnwrapShell(script, "bash")
	}
	return name, args, script
}

func (t *mekugiResponseTransform) shellActivityExcerpt(script string) string {
	if reference := toolActivityScriptReference(script); reference != "" {
		resolved, err := t.proxy.resolveShellInput(t.shellDirectory, reference)
		if err != nil {
			return ""
		}
		script = resolved
	}
	return toolActivityCommandExcerpt(script)
}

func (t *mekugiResponseTransform) shellActivityDisplay(item map[string]json.RawMessage, name string) (string, bool) {
	name, args, script := toolActivityShellCall(item, name, false)
	label, excerpt := "", ""
	switch name {
	case "write_stdin":
		if jsonString(args, "chars") != "" {
			return "", false
		}
		label = "Still Running"
		excerpt = t.activityShellSessions[strings.TrimSpace(string(args["session_id"]))]
	case "shell", "shell_command", "exec_command":
		if toolActivityScriptReference(script) == "" {
			return "", false
		}
		label = "Running stored script"
		excerpt = t.shellActivityExcerpt(script)
	default:
		return "", false
	}
	if excerpt == "" {
		return label + " · command unavailable", true
	}
	return label + "\n" + commentaryCode(excerpt), true
}

// Reconstruct from this request's visible call/result pairs, not the latest
// command or another thread's session ID. No process state is retained globally.
func (t *mekugiResponseTransform) prepareShellActivity(input json.RawMessage) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(input, &items) != nil {
		return
	}
	calls := make(map[string]string)
	t.activityShellSessions = make(map[string]string)
	for _, item := range items {
		kind, callID := jsonString(item, "type"), jsonString(item, "call_id")
		if callID == "" {
			continue
		}
		if kind == "function_call" || kind == "custom_tool_call" {
			if len(calls) >= 1024 {
				continue
			}
			name, args, script := toolActivityShellCall(item, qualifiedToolName(jsonString(item, "namespace"), jsonString(item, "name")), true)
			switch name {
			case "shell", "shell_command", "exec_command":
				calls[callID] = t.shellActivityExcerpt(script)
			case "write_stdin":
				calls[callID] = t.activityShellSessions[strings.TrimSpace(string(args["session_id"]))]
			}
		} else if kind == "function_call_output" || kind == "custom_tool_call_output" {
			excerpt := calls[callID]
			delete(calls, callID)
			if excerpt != "" && len(t.activityShellSessions) < 1024 {
				if session := toolActivityOutputSession(item["output"]); session != "" {
					t.activityShellSessions[session] = excerpt
				}
			}
		}
	}
}

func toolActivityOutputSession(raw json.RawMessage) string {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) == nil && value != nil {
		var id json.Number
		if json.Unmarshal(value["session_id"], &id) == nil && id != "" {
			return string(id)
		}
		return ""
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		for _, part := range parts {
			if session := toolActivityOutputSession(mustMarshalJSON(part.Text)); session != "" {
				return session
			}
		}
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		return toolActivityOutputSession(json.RawMessage(text))
	}
	// Native exec metadata precedes command output. Never inspect output lines
	// for a session marker, since the program can print arbitrary text.
	for line := range strings.SplitSeq(text, "\n") {
		if line == "Output:" || line == "Final output:" {
			break
		}
		if id, ok := strings.CutPrefix(line, "Process running with session ID "); ok {
			var number json.Number
			if json.Unmarshal([]byte(id), &number) == nil && number != "" {
				return string(number)
			}
		}
	}
	return ""
}
