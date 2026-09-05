package router

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/yusing/hpatch/internal/shellruntime"
	"github.com/yusing/hpatch/internal/shellsyntax"
)

// A session pins both the private script tree and its cleanup parent. Neither
// model references nor later symlink replacements can redirect its operations.
type shellSession struct {
	parent  *os.Root
	thread  *os.Root
	scripts *os.Root
	name    string
	timers  map[string]*time.Timer
}

func (s *shellSession) close() error {
	for _, timer := range s.timers {
		timer.Stop()
	}
	var cleanupErr error
	entries, err := fs.ReadDir(s.scripts.FS(), ".")
	cleanupErr = errors.Join(cleanupErr, err)
	for _, entry := range entries {
		cleanupErr = errors.Join(cleanupErr, s.scripts.RemoveAll(entry.Name()))
	}
	if err := s.thread.Remove(".runtime"); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	cleanupErr = errors.Join(cleanupErr,
		removeShellDirectory(s.thread, "scripts", s.scripts),
		removeShellDirectory(s.parent, s.name, s.thread),
	)
	return errors.Join(cleanupErr, s.scripts.Close(), s.thread.Close(), s.parent.Close())
}

func removeShellDirectory(parent *os.Root, name string, owned *os.Root) error {
	identity, err := owned.Stat(".")
	if err != nil {
		return err
	}
	current, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(identity, current) {
		return nil
	}
	// Never recurse through a name that another writer can replace. A final
	// replacement with a nonempty directory must survive even after this check.
	return parent.Remove(name)
}

func (s *shellSession) setRuntime(worker string) error {
	// Removing the launcher link does not follow its target. Scripts are kept in
	// a separate capability so retained edits cannot replace this launcher.
	if err := s.thread.Remove(".runtime"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.thread.Symlink(worker, ".runtime")
}

func (p *hpatchProxy) storeShellRuntime(threadID string) (string, error) {
	runtimePath, err := shellruntime.Path(p.shellDirectory, threadID)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(filepath.Dir(runtimePath), "scripts")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errors.New("hpatch proxy is closed")
	}
	if session, ok := p.shellSessions[directory]; ok {
		return directory, session.setRuntime(p.registry.shellRuntime)
	}
	parent, err := os.OpenRoot(p.shellDirectory)
	if err != nil {
		return "", err
	}
	name := filepath.Base(filepath.Dir(runtimePath))
	thread, err := openShellDirectory(parent, name)
	if err != nil {
		_ = parent.Close()
		return "", err
	}
	scripts, err := openShellDirectory(thread, "scripts")
	if err != nil {
		_ = thread.Close()
		_ = parent.Close()
		return "", err
	}
	session := &shellSession{parent: parent, thread: thread, scripts: scripts, name: name, timers: make(map[string]*time.Timer)}
	if err := session.setRuntime(p.registry.shellRuntime); err != nil {
		_ = scripts.Close()
		_ = thread.Close()
		_ = parent.Close()
		return "", err
	}
	p.shellSessions[directory] = session
	return directory, nil
}

func openShellDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return openExistingShellDirectory(parent, name)
}

func openExistingShellDirectory(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("shell storage %q is not a directory", name)
	}
	// Keep the selected name as an intermediate component: OpenRoot otherwise
	// opens its final component before checking its type, which can block on a
	// FIFO swapped in after Lstat. Traversing name/. requires a directory first.
	root, err := parent.OpenRoot(name + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("shell storage %q changed while opening", name)
	}
	return root, nil
}

func openRetainedShellFile(directory, threadID, reference string) (*os.File, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("%s must be an absolute path", shellruntime.RuntimeDirectoryEnvironment)
	}
	runtimePath, err := shellruntime.Path(directory, threadID)
	if err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	thread, err := openExistingShellDirectory(parent, filepath.Base(filepath.Dir(runtimePath)))
	if err != nil {
		return nil, err
	}
	defer thread.Close()
	scripts, err := openExistingShellDirectory(thread, "scripts")
	if err != nil {
		return nil, err
	}
	defer scripts.Close()
	name, err := shellArtifactName(reference)
	if err != nil {
		return nil, err
	}
	return openRegularShellFile(scripts, name)
}

func shellArtifactName(reference string) (string, error) {
	name, ok := strings.CutPrefix(reference, shellArtifactPrefix)
	if !ok || shellruntime.ValidateID(name) != nil || name == ".runtime" {
		return "", errors.New("retained script requires @shell/<artifact-id>")
	}
	return name, nil
}

func openRegularShellFile(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("retained shell script is not a regular file")
	}
	// A file replaced by a FIFO after Lstat must not block before Stat can reject
	// the opened descriptor. Regular-file reads ignore O_NONBLOCK.
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("retained shell script is not a regular file")
	}
	return file, nil
}

func (p *hpatchProxy) shellRoot(directory string) (*os.Root, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return nil, errors.New("hpatch proxy is closed")
	}
	session, ok := p.shellSessions[directory]
	if !ok {
		return nil, errors.New("retained shell storage is unavailable")
	}
	return session.scripts, nil
}

func (p *hpatchProxy) retainShell(directory, callID, script string) (string, bool) {
	if shellruntime.ValidateID(callID) != nil || callID == ".runtime" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.shellSessions[directory]
	if p.closed || !ok {
		return "", false
	}
	if _, pendingExpiry := session.timers[callID]; pendingExpiry {
		return "", false
	}
	// Exclusive creation rejects duplicate IDs and preexisting symlinks rather
	// than overwriting an artifact or following a link supplied by another writer.
	file, err := session.scripts.OpenFile(callID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false
	}
	_, writeErr := io.WriteString(file, script)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		_ = session.scripts.Remove(callID)
		return "", false
	}
	session.timers[callID] = time.AfterFunc(shellArtifactTTL, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if !p.closed {
			_ = session.scripts.Remove(callID)
			delete(session.timers, callID)
		}
	})
	return shellArtifactPrefix + callID, true
}

func (p *hpatchProxy) resolveShellInput(directory, input string) (string, error) {
	seen := make(map[string]bool)
	for {
		parsed, err := shellsyntax.Parse(input)
		if err != nil {
			return "", err
		}
		if !parsed.HasScript {
			return input, nil
		}
		name, err := shellArtifactName(parsed.ScriptPath)
		if err != nil {
			return "", err
		}
		if seen[name] {
			return "", errors.New("retained shell reference cycle")
		}
		seen[name] = true
		root, err := p.shellRoot(directory)
		if err != nil {
			return "", err
		}
		file, err := openRegularShellFile(root, name)
		if err != nil {
			return "", fmt.Errorf("read retained shell script: %w", err)
		}
		content, readErr := io.ReadAll(file)
		err = errors.Join(readErr, file.Close())
		if err != nil {
			return "", fmt.Errorf("read retained shell script: %w", err)
		}
		if !utf8.Valid(content) {
			return "", errors.New("retained shell script is not UTF-8")
		}
		input = string(content)
	}
}
