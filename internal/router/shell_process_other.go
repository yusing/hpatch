//go:build !unix

package router

import "os/exec"

func shellProcessExitCode(err *exec.ExitError) int {
	return err.ExitCode()
}
