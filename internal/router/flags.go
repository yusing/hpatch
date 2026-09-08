package router

import (
	"flag"
	"fmt"
	"io"
	"time"
)

type routerFlags struct {
	*flag.FlagSet
	timeout              *time.Duration
	streamIdleTimeout    *time.Duration
	mode                 *string
	modelProtocol        *string
	mentorHandoffEnabled *bool
	grokEnabled          *bool
	grokAuthFile         *string
	captureOutput        *string
	metricsOutput        *string
}

func newRouterFlags(stderr io.Writer) routerFlags {
	flags := flag.NewFlagSet("hpatch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: hpatch [flags] codex [Codex arguments...]")
		flags.PrintDefaults()
	}
	return routerFlags{
		FlagSet:              flags,
		timeout:              flags.Duration("timeout", defaultRequestTimeout, "upstream response-start timeout"),
		streamIdleTimeout:    flags.Duration("stream-idle-timeout", defaultStreamIdleTimeout, "maximum upstream response-stream inactivity between bytes"),
		mode:                 flags.String("mode", defaultRewriteMode, "response mode: hpatch or passthrough"),
		modelProtocol:        flags.String("model-protocol", defaultModelProtocol, "model protocol: native or ctp2"),
		mentorHandoffEnabled: flags.Bool("mentor-handoff", true, "use gpt-5.6-sol high for eligible spawned subagents"),
		grokEnabled:          flags.Bool("grok", false, "enable native Grok subagents and plaintext collaboration projection"),
		grokAuthFile:         flags.String("grok-auth-file", "", "Grok OAuth credential file (default ~/.grok/auth.json)"),
		metricsOutput:        flags.String("metrics-output", "", "optional final metrics JSON path"),
		captureOutput:        flags.String("capture-output", "", "optional sanitized capture JSONL path"),
	}
}

// SplitCommand parses router flags without consuming the following command or its arguments.
func SplitCommand(args []string) (routerArgs, command []string, err error) {
	flags := newRouterFlags(io.Discard)
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	command = flags.Args()
	routerArgs = args[:len(args)-len(command)]
	return routerArgs, command, err
}

// PrintUsage describes the session-only launch interface.
func PrintUsage(w io.Writer) { newRouterFlags(w).Usage() }
