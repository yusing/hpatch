package router

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func ensureWorkerSymlinkInDirectory(executable, directory, name string) (string, error) {
	link := filepath.Join(directory, name)
	if _, err := os.Lstat(link); err == nil {
		return verifyWorkerSymlink(link, executable, name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect %s worker symlink: %w", name, err)
	}
	if err := os.Symlink(executable, link); err != nil {
		if errors.Is(err, os.ErrExist) {
			return verifyWorkerSymlink(link, executable, name)
		}
		return "", fmt.Errorf("create %s worker symlink: %w", name, err)
	}
	return link, nil
}

func verifyWorkerSymlink(link, executable, name string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("install %s worker: %s already exists and is not a symlink", name, link)
	}
	if target != executable {
		return "", fmt.Errorf("install %s worker: %s points to %s, want %s", name, link, target, executable)
	}
	return link, nil
}

func ensureWorkerFrontendSymlink(executable, wrapper, directory, name string) (string, error) {
	link := filepath.Join(directory, name)
	currentTarget, err := os.Readlink(link)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(wrapper, link); err != nil {
			return "", fmt.Errorf("create %s worker frontend: %w", name, err)
		}
		return link, nil
	}
	if err != nil {
		return "", fmt.Errorf("install %s worker frontend: %s already exists and is not a symlink", name, link)
	}
	if currentTarget == wrapper {
		return link, nil
	}

	absoluteTarget := currentTarget
	if !filepath.IsAbs(absoluteTarget) {
		absoluteTarget = filepath.Join(directory, absoluteTarget)
	}
	absoluteTarget = filepath.Clean(absoluteTarget)
	_, snapshotTarget := toolRegistryIDFromDirectory(filepath.Dir(absoluteTarget))
	staleSnapshot := snapshotTarget && filepath.Base(absoluteTarget) == name
	legacyTarget := false
	if !snapshotTarget {
		resolvedTarget, resolveErr := filepath.EvalSymlinks(link)
		legacyTarget = resolveErr == nil && resolvedTarget == executable
	}
	if !staleSnapshot && !legacyTarget {
		return "", fmt.Errorf("install %s worker frontend: %s points to %s, want %s", name, link, currentTarget, wrapper)
	}

	temporary, err := os.CreateTemp(directory, "."+name+"-")
	if err != nil {
		return "", fmt.Errorf("prepare %s worker frontend: %w", name, err)
	}
	temporaryPath := temporary.Name()
	if err := errors.Join(temporary.Close(), os.Remove(temporaryPath)); err != nil {
		return "", fmt.Errorf("prepare %s worker frontend: %w", name, err)
	}
	defer os.Remove(temporaryPath) //nolint:errcheck // Best-effort cleanup after rename or failure.
	if err := os.Symlink(wrapper, temporaryPath); err != nil {
		return "", fmt.Errorf("prepare %s worker frontend: %w", name, err)
	}
	if err := os.Rename(temporaryPath, link); err != nil {
		return "", fmt.Errorf("replace %s worker frontend: %w", name, err)
	}
	return link, nil
}

func removeWorkerFrontendSymlink(link, wrapper string) error {
	target, err := os.Readlink(link)
	if errors.Is(err, os.ErrNotExist) || err == nil && target != wrapper {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect worker frontend %s: %w", link, err)
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove worker frontend %s: %w", link, err)
	}
	return nil
}

func removeWorkerFrontendSymlinks(frontends, wrappers map[string]string) error {
	var cleanupErrors []error
	for name, link := range frontends {
		if err := removeWorkerFrontendSymlink(link, wrappers[name]); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}
