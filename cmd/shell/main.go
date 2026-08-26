//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/yusing/hpatch/internal/shellruntime"
)

func main() {
	root, err := shellruntime.Directory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell:", err)
		os.Exit(1)
	}
	threadID := os.Getenv(shellruntime.ThreadIDEnvironment)
	if threadID == "" {
		fmt.Fprintln(os.Stderr, "shell: CODEX_THREAD_ID is unavailable")
		os.Exit(1)
	}
	runtime, err := os.Readlink(shellruntime.Path(root, threadID))
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell: locate current hpatch runtime:", err)
		os.Exit(1)
	}
	arguments := append([]string{runtime}, os.Args[1:]...)
	if err := syscall.Exec(runtime, arguments, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "shell: start current hpatch runtime:", err)
		os.Exit(1)
	}
}
