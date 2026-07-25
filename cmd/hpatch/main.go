package main

import (
	"io"
	"os"
	"path/filepath"

	"hpatch"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	gainMode := len(args) == 1 && args[0] == "gain"
	workingDirectory := ""
	if !gainMode {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			_, _ = io.WriteString(stderr, "hpatch: determining working directory: "+err.Error()+"\n")
			return 1
		}
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		if gainMode {
			_, _ = io.WriteString(stderr, "hpatch: determining user config directory: "+err.Error()+"\n")
			return 1
		}
		_, _ = io.WriteString(stderr, "hpatch: warning: determining user config directory: "+err.Error()+"\n")
		return hpatch.Run(args, stdin, stdout, stderr, workingDirectory, "")
	}
	return hpatch.Run(args, stdin, stdout, stderr, workingDirectory, filepath.Join(configDirectory, "hpatch"))
}
