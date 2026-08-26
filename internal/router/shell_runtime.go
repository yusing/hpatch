package router

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/yusing/hpatch/internal/shellruntime"
)

func (p *hpatchProxy) storeShellRuntime(threadID string) (string, error) {
	runtimePath := shellruntime.Path(p.shellDirectory, threadID)
	directory := filepath.Dir(runtimePath)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errors.New("hpatch proxy is closed")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Remove(runtimePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Symlink(p.registry.shellRuntime, runtimePath); err != nil {
		return "", err
	}
	p.shellSessions[directory] = struct{}{}
	return directory, nil
}
