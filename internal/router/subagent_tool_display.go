package router

import (
	"encoding/json"
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/yusing/hpatch/internal/shellsyntax"
	"mvdan.cc/sh/v3/syntax"
)

// Presentation only: never evaluate code, expand paths, or alter the observed call.
func subagentToolActivityText(item map[string]json.RawMessage, qualifiedName string) string {
	name := jsonString(item, "name")
	if commentaryExcluded(jsonString(item, "namespace"), name) {
		return "Tool call: " + commentaryCode(qualifiedName)
	}
	input := jsonString(item, "arguments")
	if input == "" {
		input = jsonString(item, "input")
	}
	shortName := strings.TrimPrefix(qualifiedName, "functions.")
	if shortName == "exec" {
		if nested, ok := toolActivityUnwrapExec(input); ok {
			return subagentToolActivityText(nested, qualifiedToolName(jsonString(nested, "namespace"), jsonString(nested, "name")))
		}
	}
	var arguments map[string]json.RawMessage
	_ = json.Unmarshal([]byte(input), &arguments)
	if kind := jsonString(item, "type"); kind == "local_shell_call" || kind == "shell_call" {
		var action struct {
			Command  []string `json:"command"`
			Commands []string `json:"commands"`
		}
		if json.Unmarshal(item["action"], &action) == nil {
			if len(action.Commands) > 0 {
				return toolActivityShell(strings.Join(action.Commands, "\n"))
			}
			if len(action.Command) > 0 {
				return toolActivityShell(toolActivityArgv(action.Command))
			}
		}
		return "Run"
	}
	switch shortName {
	case "shell", "shell_command", "exec_command":
		script := input
		if arguments != nil {
			script = jsonString(arguments, "cmd")
			if script == "" {
				script = jsonString(arguments, "command")
			}
			var argv []string
			if json.Unmarshal(arguments["command"], &argv) == nil {
				script = toolActivityArgv(argv)
			}
		}
		return toolActivityShell(script)
	case "exec":
		return toolActivityDetail("Run JavaScript", input)
	case "view_image":
		return toolActivityDetail("View image", jsonString(arguments, "path"))
	case "write_stdin":
		return toolActivityDetail("Send input", jsonString(arguments, "chars"))
	case "apply_patch", "hpatch", "hpatch_recover":
		return toolActivityDetail("Edit", input)
	}
	switch jsonString(item, "type") {
	case "web_search_call":
		var action map[string]json.RawMessage
		_ = json.Unmarshal(item["action"], &action)
		switch jsonString(action, "type") {
		case "search":
			query := jsonString(action, "query")
			var queries []string
			if query == "" && json.Unmarshal(action["queries"], &queries) == nil {
				query = strings.Join(queries, "\n")
			}
			return toolActivityDetail("Search web", query)
		case "open_page":
			return toolActivityDetail("Open page", jsonString(action, "url"))
		case "find":
			return toolActivityDetail("Find in page", jsonString(action, "pattern"))
		}
		return "Search web"
	case "file_search_call":
		var queries []string
		_ = json.Unmarshal(item["queries"], &queries)
		return toolActivityDetail("Search files", strings.Join(queries, "\n"))
	case "image_generation_call":
		return "Generate image"
	case "code_interpreter_call":
		return toolActivityDetail("Run code", jsonString(item, "code"))
	}
	return toolActivityDetail("Tool call: "+commentaryCode(qualifiedName), input)
}

func toolActivityDetail(label, input string) string {
	if strings.TrimSpace(input) == "" {
		return label
	}
	return label + "\n" + toolActivityCode(input)
}

func toolActivityArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if len(argv) == 3 && (filepath.Base(argv[0]) == "bash" || filepath.Base(argv[0]) == "sh") &&
		(argv[1] == "-c" || argv[1] == "-lc") {
		return argv[2]
	}
	return workerCommand(argv[0], argv[1:])
}

func toolActivityShell(script string) string {
	// Native carriers may wrap the source in `shell bash $'...'`.
	for range 2 {
		program, err := syntax.NewParser().Parse(strings.NewReader(script), "")
		if err != nil || len(program.Stmts) != 1 {
			break
		}
		statement := program.Stmts[0]
		call, ok := statement.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) != 3 || len(call.Assigns) != 0 || len(statement.Redirs) != 0 ||
			statement.Background || statement.Negated || statement.Coprocess || statement.Disown {
			break
		}
		command, a := shellCatLiteral(call.Args[0])
		interpreter, b := shellCatLiteral(call.Args[1])
		body, c := shellCatLiteral(call.Args[2])
		if !a || !b || !c || command != "shell" || (interpreter != "bash" && interpreter != "sh") {
			break
		}
		script = body
	}
	parsed, err := shellsyntax.Parse(script)
	if err == nil && !parsed.HasScript && parsed.CommandTemplate == "" && len(parsed.Interpreter) == 1 &&
		(parsed.Interpreter[0] == "bash" || parsed.Interpreter[0] == "sh") {
		if summary, ok := toolActivityReads(parsed.Body); ok {
			return summary
		}
	}
	return toolActivityDetail("Run", script)
}

func toolActivityReads(script string) (string, bool) {
	program, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil || len(program.Stmts) == 0 {
		return "", false
	}
	var operations []struct{ label, detail string }
	add := func(label, detail string) {
		operations = append(operations, struct{ label, detail string }{label, detail})
	}

	for _, statement := range program.Stmts {
		call, ok := statement.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 || len(call.Assigns) != 0 || len(statement.Redirs) != 0 ||
			statement.Background || statement.Negated || statement.Coprocess || statement.Disown {
			return "", false
		}
		var argv []string
		for _, arg := range call.Args {
			value, literal := shellCatLiteral(arg)
			if !literal {
				return "", false
			}
			argv = append(argv, value)
		}
		switch argv[0] {
		case "cat", "hread":
			if len(argv) < 2 {
				return "", false
			}
			paths, readRange := argv[1:], ""
			if argv[0] == "hread" {
				if len(argv) != 2 && len(argv) != 3 {
					return "", false
				}
				paths = argv[1:2]
				if len(argv) == 3 {
					readRange = " " + argv[2]
				}
			}
			for _, path := range paths {

				if path == "" || strings.HasPrefix(path, "-") {
					return "", false
				}
				label, value := "Read", path
				if filepath.Base(path) == "SKILL.md" && filepath.Dir(path) != "." {
					label, value = "Skill Read", filepath.Base(filepath.Dir(path))
				}
				add(label, value+readRange)
			}
		case "inspect_file":
			if len(argv) != 2 {
				return "", false
			}
			add("Inspect", argv[1])
		case "ls":
			if len(argv) > 2 || (len(argv) == 2 && strings.HasPrefix(argv[1], "-")) {
				return "", false
			}
			path := "."
			if len(argv) == 2 {
				path = argv[1]
			}
			add("List", path)
		case "rg", "hgrep", "grep":
			if len(argv) < 2 {
				return "", false
			}
			// Keep all search flags and operands visible; do not guess which
			// operand is a query when an option may consume it.
			add("Search", script[int(call.Args[1].Pos().Offset()):int(call.End().Offset())])
		case "skills-mgr":
			if (len(argv) != 3 && len(argv) != 4) || argv[1] != "get" {
				return "", false
			}
			label := "Skill Read"
			if strings.Contains(argv[2], "/") {
				label = "Skill Reference Read"
			}
			detail := argv[2]
			if len(argv) == 4 {
				detail += " " + argv[3]
			}
			add(label, detail)

		default:
			return "", false
		}
	}
	var lines []string
	for _, operation := range operations {
		separator := " "
		if strings.ContainsAny(operation.detail, "\r\n") {
			separator = "\n"
		}
		lines = append(lines, operation.label+separator+toolActivityCode(operation.detail))
	}
	return strings.Join(lines, "\n\n"), true

}

// Recognize transparent Code Mode wrappers, not arbitrary programs containing a
// tool call (which may branch, execute other work, or never invoke that call).
func toolActivityUnwrapExec(source string) (map[string]json.RawMessage, bool) {
	parser := sitter.NewParser()
	defer parser.Close()
	if parser.SetLanguage(codeModeJavaScriptLanguage) != nil {
		return nil, false
	}
	bytes := []byte(source)
	tree := parser.Parse(bytes, nil)
	if tree == nil {
		return nil, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, false
	}
	var statements []*sitter.Node
	for i := range root.NamedChildCount() {
		node := root.NamedChild(uint(i))
		if node.Kind() != "comment" {
			statements = append(statements, node)
		}
	}
	if len(statements) == 0 || len(statements) > 2 {
		return nil, false
	}
	first := statements[0]
	var expression *sitter.Node
	if first.Kind() == "expression_statement" && len(statements) == 1 {
		expression = first.NamedChild(0)
	} else if first.Kind() == "lexical_declaration" && first.NamedChildCount() == 1 && len(statements) == 2 {
		declaration := first.NamedChild(0)
		binding := declaration.ChildByFieldName("name")
		if binding == nil || binding.Kind() != "identifier" {
			return nil, false
		}
		name := binding.Utf8Text(bytes)
		tail := strings.TrimSuffix(strings.TrimSpace(statements[1].Utf8Text(bytes)), ";")
		if tail != "text("+name+")" && tail != "text(JSON.stringify("+name+"))" &&
			tail != "text(JSON.stringify(Object.assign({}, "+name+", {\"retained\":false})))" {
			return nil, false
		}
		expression = declaration.ChildByFieldName("value")
	}
	if expression == nil || expression.Kind() != "await_expression" {
		return nil, false
	}
	call := expression.NamedChild(0)
	if call == nil || call.Kind() != "call_expression" {
		return nil, false
	}
	callee, args := call.ChildByFieldName("function"), call.ChildByFieldName("arguments")
	if callee == nil || args == nil || args.NamedChildCount() != 1 {
		return nil, false
	}
	name := strings.TrimPrefix(callee.Utf8Text(bytes), "tools.")
	switch name {
	case "exec_command", "shell_command", "shell", "view_image", "write_stdin", "apply_patch":
	default:
		return nil, false
	}
	if callee.Utf8Text(bytes) != "tools."+name {
		return nil, false
	}
	argument := args.NamedChild(0).Utf8Text(bytes)
	var value any
	if json.Unmarshal([]byte(argument), &value) != nil {
		return nil, false
	}
	item := map[string]json.RawMessage{"name": mustMarshalJSON(name)}
	if text, ok := value.(string); ok {
		item["input"] = mustMarshalJSON(text)
	} else {
		item["arguments"] = mustMarshalJSON(argument)
	}
	return item, true
}
