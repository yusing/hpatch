// merge-captures is a benchmark-only artifact command, not an installed user tool.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/yusing/hpatch/capturer"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: merge-captures METRICS CAPTURE SESSION_DIRECTORY...")
		os.Exit(2)
	}
	var metrics, capture bytes.Buffer
	err := capturer.MergeSessions(os.Args[3:], &metrics, &capture)
	if err == nil {
		err = errors.Join(os.WriteFile(os.Args[1], metrics.Bytes(), 0o600), os.WriteFile(os.Args[2], capture.Bytes(), 0o600))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
