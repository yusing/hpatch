package router

import (
	"context"
	"strings"

	"mvdan.cc/sh/v3/interp"
)

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
