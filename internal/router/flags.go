package router

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

type routerFlags struct {
	*flag.FlagSet
	listenAddress        *string
	timeout              *time.Duration
	streamIdleTimeout    *time.Duration
	mode                 *string
	modelProtocol        *string
	mentorHandoffEnabled *bool
	providerBaseURL      *string
	grokEnabled          *bool
	grokAuthFile         *string
	captureOutput        *string
}

func newRouterFlags(stderr io.Writer) routerFlags {
	flags := flag.NewFlagSet("hpatch-router", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: hpatch-router [router flags]\n       hpatch-router [router flags] wrap codex [Codex arguments...]")
		flags.PrintDefaults()
	}
	return routerFlags{
		FlagSet:              flags,
		listenAddress:        flags.String("listen", defaultListenAddress, "HTTP listen address"),
		timeout:              flags.Duration("timeout", defaultRequestTimeout, "upstream response-start timeout"),
		streamIdleTimeout:    flags.Duration("stream-idle-timeout", defaultStreamIdleTimeout, "maximum upstream response-stream inactivity between bytes"),
		mode:                 flags.String("mode", defaultRewriteMode, "response mode: hpatch or passthrough"),
		modelProtocol:        flags.String("model-protocol", defaultModelProtocol, "model protocol: native or ctp2"),
		mentorHandoffEnabled: flags.Bool("mentor-handoff", true, "use gpt-5.6-sol high for eligible spawned subagents"),
		providerBaseURL:      flags.String("provider-base-url", codexBaseURL, "Codex provider base URL"),
		grokEnabled:          flags.Bool("grok", false, "enable native Grok subagents and plaintext collaboration projection"),
		grokAuthFile:         flags.String("grok-auth-file", "", "Grok OAuth credential file (default ~/.grok/auth.json)"),
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
	if len(command) > 0 && command[0] == "wrap" {
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "listen" || f.Name == "provider-base-url" {
				err = errors.Join(err, fmt.Errorf("wrap does not support --%s; it uses a random loopback port and the default upstream", f.Name))
			}
		})
	}
	return routerArgs, command, err
}
