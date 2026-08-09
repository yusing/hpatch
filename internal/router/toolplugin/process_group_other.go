//go:build !unix

package toolplugin

import "os/exec"

func configurePluginProcessGroup(_ *exec.Cmd) {}
