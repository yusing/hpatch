package main

import (
	"os"

	"hpatch"
)

func main() {
	workingDirectory, err := os.Getwd()
	if err != nil {
		_, _ = os.Stderr.WriteString("hpatch: determining working directory: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(hpatch.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, workingDirectory))
}
