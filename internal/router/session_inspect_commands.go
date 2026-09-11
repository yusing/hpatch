package router

import (
	"encoding/json"
	"strings"

	"github.com/yusing/mekugi/capturer"
	"mvdan.cc/sh/v3/syntax"
)

// Read only a literal leading assignment in an observed native command. This
// recovers our carrier's correlation ID without retaining the command, evaluating
// shell code, or inventing a parent for unrecognized/external commands.
func inspectionAXCallID(encoded json.RawMessage) string {
	var command string
	if json.Unmarshal(encoded, &command) != nil {
		var argv []string
		if json.Unmarshal(encoded, &argv) != nil || len(argv) != 3 ||
			(shellInterpreterName(argv[0]) != "bash" && shellInterpreterName(argv[0]) != "sh") ||
			(argv[1] != "-c" && argv[1] != "-lc") {
			return ""
		}
		command = argv[2]
	}
	if !strings.HasPrefix(command, capturer.AXCallIDEnvironment+"=") {
		return ""
	}
	program, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(program.Stmts) == 0 {
		return ""
	}
	call, ok := program.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	identity := ""
	for _, assignment := range call.Assigns {
		if assignment.Name == nil || assignment.Name.Value != capturer.AXCallIDEnvironment {
			continue
		}
		if identity != "" || assignment.Append || assignment.Index != nil || assignment.Value == nil {
			return ""
		}
		value, literal := shellCatLiteral(assignment.Value)
		if !literal || !capturer.ValidAXIdentity(value) {
			return ""
		}
		identity = value
	}
	return identity
}
