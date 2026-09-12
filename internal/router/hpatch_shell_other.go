//go:build !unix

package router

import (
	"fmt"
	"os"
)

// The fixed shell runtime and its writable PTY transport are Unix-only.
func openHpatchInput(*os.File) (*os.File, func(), error) {
	return nil, nil, fmt.Errorf("streamed HPATCH source requires the Unix shell runtime")
}

func lockHpatchCheckpoint(*os.Root, string) (func(), error) {
	return nil, fmt.Errorf("mixed-script retention requires the Unix shell runtime")
}
