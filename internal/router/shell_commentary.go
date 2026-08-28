package router

import (
	"context"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func shellArgumentsHaveCommentary(arguments []string) bool {
	if len(arguments) < 2 {
		return false
	}
	variant := syntax.LangBash
	switch shellInterpreterName(arguments[0]) {
	case "sh":
		variant = syntax.LangPOSIX
	case "bash":
	default:
		return false
	}
	program, err := syntax.NewParser(syntax.Variant(variant)).Parse(strings.NewReader(arguments[len(arguments)-1]), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(program, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 || len(call.Args[0].Parts) != 1 {
			return !found
		}
		literal, ok := call.Args[0].Parts[0].(*syntax.Lit)
		if ok && literal.Value == "commentary" {
			found = true
		}
		return !found
	})
	return found
}

func shellCommentaryCallHandler(sink shellCommentarySink) interp.CallHandlerFunc {
	return func(ctx context.Context, arguments []string) ([]string, error) {
		if arguments[0] != commentaryArgumentName {
			return arguments, nil
		}
		if sink != nil {
			_ = sink.Publish(ctx, strings.Join(arguments[1:], " "))
		}
		// `command true` bypasses any user-defined function named true while
		// preserving the shell's normal redirection behavior for this command.
		return []string{"command", "true"}, nil
	}
}
