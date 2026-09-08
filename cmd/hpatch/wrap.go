package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/yusing/hpatch/internal/router"
)

func runWrap(routerArgs, args []string) int {
	if len(args) == 0 || args[0] != "codex" {
		fmt.Fprintln(os.Stderr, "usage: hpatch [flags] codex [Codex arguments...]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	// Codex shares the foreground process group and handles terminal Ctrl-C itself.
	// Catch it here without canceling the router or delivering a second interrupt.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	code, err := wrapCodex(ctx, routerArgs, args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "hpatch:", err)
	}
	return code
}

func wrapCodex(ctx context.Context, routerArgs, args []string) (int, error) {
	if err := validateCodexArgs(args); err != nil {
		return 2, err
	}
	executable, err := exec.LookPath("codex")
	if err != nil {
		return 1, fmt.Errorf("locate codex: %w", err)
	}
	issues := router.NewCriticalErrors()
	defer func() {
		for _, message := range issues.Pending() {
			fmt.Fprintln(os.Stderr, message)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan router.Session, 1)
	routerDone := make(chan error, 1)
	go func() {
		routerDone <- router.RunSession(ctx, routerArgs, issues, func(session router.Session) {
			ready <- session
		})
	}()
	var session router.Session
	select {
	case err := <-routerDone:
		return 1, err
	case session = <-ready:
	}
	// Announce once before Codex takes over the terminal, never during its UI.
	fmt.Fprintf(os.Stderr, "hpatch dashboard: %s/\n", strings.TrimSuffix(session.BaseURL, "/v1"))
	cmd := exec.CommandContext(ctx, executable, codexArgs(session.BaseURL, args)...)
	cmd.Env = append(os.Environ(), "HPATCH_BASE_URL="+session.BaseURL)
	if session.FrontendDirectory != "" {
		cmd.Env = append(cmd.Env, "PATH="+session.FrontendDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		return 1, errors.Join(fmt.Errorf("launch codex: %w", err), <-routerDone)
	}
	codexDone := make(chan error, 1)
	go func() { codexDone <- cmd.Wait() }()
	var codexErr, routerErr error
	select {
	case codexErr = <-codexDone:
		cancel()
		routerErr = <-routerDone
	case routerErr = <-routerDone:
		if routerErr == nil && ctx.Err() == nil {
			routerErr = errors.New("router stopped before codex exited")
		}
		cancel()
		codexErr = <-codexDone
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](codexErr); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), routerErr
		}
		return exitErr.ExitCode(), routerErr
	}
	if err := errors.Join(codexErr, routerErr); err != nil {
		return 1, err
	}
	return 0, nil
}

func codexArgs(baseURL string, args []string) []string {
	// Keep both overrides in the final command's config layer: Codex subcommands
	// can replace pre-subcommand -c settings with their own. Never cross --.
	index := slices.Index(args, "--")
	if index < 0 {
		index = len(args)
	}
	return slices.Insert(slices.Clone(args), index,
		"-c", `model_provider="hpatch_wrap"`,
		"-c", fmt.Sprintf(`model_providers.hpatch_wrap={name="hpatch",base_url=%q,wire_api="responses",requires_openai_auth=true}`, baseURL),
	)
}

func validateCodexArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--oss" || arg == "--local-provider" || strings.HasPrefix(arg, "--local-provider=") {
			return errors.New("hpatch codex does not support provider-selection arguments; custom providers are not supported")
		}
		var override string
		switch {
		case arg == "-c" || arg == "--config":
			if i+1 < len(args) {
				i++
				override = args[i]
			}
		case strings.HasPrefix(arg, "--config="):
			override = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-c"):
			override = strings.TrimPrefix(arg, "-c")
		}
		key, _, _ := strings.Cut(strings.TrimPrefix(override, "="), "=")
		root, _, _ := strings.Cut(key, ".")
		root = strings.Trim(strings.TrimSpace(root), `"'`)
		if root == "model_provider" || root == "model_providers" || root == "openai_base_url" || root == "oss_provider" {
			return errors.New("hpatch codex does not support provider overrides; it overrides config.toml provider selection and uses the router's default upstream")
		}
	}
	return nil
}
