package shellruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("ID must not be empty")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("ID must not be %q", id)
	}
	if strings.ContainsAny(id, "/\\\x00") {
		return fmt.Errorf("ID must be a single filename component")
	}
	return nil
}

func Path(root, threadID string) (string, error) {
	if err := ValidateID(threadID); err != nil {
		return "", fmt.Errorf("thread ID: %w", err)
	}
	return filepath.Join(root, "hpatch-"+threadID, ".runtime"), nil
}
