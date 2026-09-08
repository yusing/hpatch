package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yusing/hpatch/internal/router"
)

func main() {
	os.Exit(run())
}

// Source: main.go:36:48 process signals and router exit behavior.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if handled, exitCode := router.RunToolPluginWorker(
		ctx,
		os.Args[0],
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
	); handled {
		return exitCode
	}
	routerArgs, command, err := router.SplitCommand(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		router.PrintUsage(os.Stdout)
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hpatch:", err)
		return 2
	}
	stop()
	return runWrap(routerArgs, command)
}
