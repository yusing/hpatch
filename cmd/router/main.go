package main

import (
	"context"
	"errors"
	"fmt"
	"hpatch/internal/router"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run())
}

// Source: main.go:36:48 process signals and router exit behavior.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := router.Run(ctx, os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "router: canceled")
			return 130
		}
		fmt.Fprintln(os.Stderr, "router:", err)
		return 1
	}
	return 0
}
