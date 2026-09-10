package shellruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	RuntimeDirectoryEnvironment = "MEKUGI_RUNTIME_DIR"
	ThreadIDEnvironment         = "CODEX_THREAD_ID"
	runtimeLocatorPrefix        = "mekugi-runtime-"
	legacyRuntimeLocatorPrefix  = "hpatch-runtime-"
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
	return filepath.Join(root, runtimeLocatorPrefix+threadID), nil
}

// CurrentPath is the locator the PATH-installed helper follows. New sessions
// write mekugi-runtime-<thread>. In-flight older routers still write
// hpatch-runtime-<thread>; the helper must find those after a helper upgrade.
func CurrentPath(root, threadID string) (string, error) {
	path, err := Path(root, threadID)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	legacy := filepath.Join(root, legacyRuntimeLocatorPrefix+threadID)
	if _, err := os.Lstat(legacy); err == nil {
		return legacy, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

// ScriptsPath locates the exclusively-created storage for a thread's active
// retained artifacts. It is separate from the persistent flat runtime locator.
func ScriptsPath(root, threadID string) (string, error) {
	if err := ValidateID(threadID); err != nil {
		return "", fmt.Errorf("thread ID: %w", err)
	}
	return filepath.Join(root, "mekugi-scripts-"+threadID), nil
}
