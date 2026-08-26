package shellruntime

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	RuntimeDirectoryEnvironment = "HPATCH_RUNTIME_DIR"
	ThreadIDEnvironment         = "CODEX_THREAD_ID"
)

func Directory() (string, error) {
	directory := os.Getenv(RuntimeDirectoryEnvironment)
	if directory == "" {
		directory = os.TempDir()
	}
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("%s must be an absolute path", RuntimeDirectoryEnvironment)
	}
	return filepath.Clean(directory), nil
}

func Path(root, threadID string) string {
	return filepath.Join(root, "hpatch-"+threadID, ".runtime")
}
