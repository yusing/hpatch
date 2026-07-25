package hpatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type fileOperations interface {
	CreateTemp(directory, pattern string) (*os.File, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
}

type osFileOperations struct{}

func (osFileOperations) CreateTemp(directory, pattern string) (*os.File, error) {
	return os.CreateTemp(directory, pattern)
}

func (osFileOperations) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osFileOperations) Remove(path string) error {
	return os.Remove(path)
}

type stagedChange struct {
	change

	originalFullPath string
	finalFullPath    string
	outputTemp       string
	backupPath       string
	installed        bool
	backedUp         bool
}

func commitChanges(root string, changes []change, operations fileOperations) error {
	staged, err := stageChanges(root, changes, operations)
	if err != nil {
		return err
	}
	fail := func(action string, cause error) error {
		rollbackErr := rollbackChanges(staged, operations)
		cleanupErr := cleanupStaging(staged, operations)
		return transactionError(action, cause, errors.Join(rollbackErr, cleanupErr))
	}

	for _, item := range staged {
		if item.kind == changeAdd {
			continue
		}
		if err := operations.Rename(item.originalFullPath, item.backupPath); err != nil {
			return fail("backing up "+item.originalPath, err)
		}
		item.backedUp = true
	}

	for _, item := range staged {
		if item.kind == changeDelete {
			continue
		}
		if err := operations.Rename(item.outputTemp, item.finalFullPath); err != nil {
			return fail("installing "+item.path, err)
		}
		item.outputTemp = ""
		item.installed = true
	}

	var cleanupErrors []error
	for _, item := range staged {
		if !item.backedUp {
			continue
		}
		if err := operations.Remove(item.backupPath); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("removing backup for %s: %w", item.originalPath, err))
			continue
		}
		item.backupPath = ""
		item.backedUp = false
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		return fmt.Errorf("changes committed but backup cleanup failed: %w", err)
	}
	if err := cleanupStaging(staged, operations); err != nil {
		return fmt.Errorf("changes committed but temporary cleanup failed: %w", err)
	}
	return nil
}

func stageChanges(root string, changes []change, operations fileOperations) ([]*stagedChange, error) {
	staged := make([]*stagedChange, 0, len(changes))
	for _, current := range changes {
		item := &stagedChange{change: current}
		staged = append(staged, item)
		if current.originalPath != "" {
			item.originalFullPath = resolveFilesystemPath(root, current.originalPath)
		}
		if current.path != "" {
			item.finalFullPath = resolveFilesystemPath(root, current.path)
		}

		if current.kind != changeAdd {
			backup, err := operations.CreateTemp(filepath.Dir(item.originalFullPath), "."+filepath.Base(item.originalFullPath)+".hpatch-backup-*")
			if err != nil {
				return nil, stagingError("creating backup reservation for "+current.originalPath, err, staged, operations)
			}
			item.backupPath = backup.Name()
			if err := backup.Close(); err != nil {
				return nil, stagingError("closing backup reservation for "+current.originalPath, err, staged, operations)
			}
			if err := operations.Remove(item.backupPath); err != nil {
				return nil, stagingError("releasing backup reservation for "+current.originalPath, err, staged, operations)
			}
		}

		if current.kind != changeDelete {
			output, err := operations.CreateTemp(filepath.Dir(item.finalFullPath), "."+filepath.Base(item.finalFullPath)+".hpatch-output-*")
			if err != nil {
				return nil, stagingError("creating output for "+current.path, err, staged, operations)
			}
			item.outputTemp = output.Name()
			if err := writeStagedFile(output, current.content, current.mode); err != nil {
				return nil, stagingError("writing output for "+current.path, err, staged, operations)
			}
		}
	}
	return staged, nil
}

func stagingError(action string, cause error, staged []*stagedChange, operations fileOperations) error {
	cleanupErr := cleanupStaging(staged, operations)
	if cleanupErr == nil {
		return fmt.Errorf("staging failed while %s: %w", action, cause)
	}
	return fmt.Errorf("staging failed while %s: %w; cleanup also failed: %w", action, cause, cleanupErr)
}

func writeStagedFile(file *os.File, content string, mode fs.FileMode) error {
	if err := file.Chmod(mode.Perm()); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func rollbackChanges(staged []*stagedChange, operations fileOperations) error {
	var rollbackErrors []error
	for index := len(staged) - 1; index >= 0; index-- {
		item := staged[index]
		if item.installed {
			if err := operations.Remove(item.finalFullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("removing installed %s: %w", item.path, err))
				continue
			}
			item.installed = false
		}
	}
	for index := len(staged) - 1; index >= 0; index-- {
		item := staged[index]
		if !item.backedUp {
			continue
		}
		if err := operations.Rename(item.backupPath, item.originalFullPath); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restoring %s: %w", item.originalPath, err))
			continue
		}
		item.backupPath = ""
		item.backedUp = false
	}
	return errors.Join(rollbackErrors...)
}

func cleanupStaging(staged []*stagedChange, operations fileOperations) error {
	var cleanupErrors []error
	for _, item := range staged {
		if item.outputTemp != "" {
			if err := operations.Remove(item.outputTemp); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("removing %s: %w", item.outputTemp, err))
			}
		}
		if item.backupPath != "" && !item.backedUp {
			if err := operations.Remove(item.backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("removing %s: %w", item.backupPath, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func transactionError(action string, commitErr, rollbackErr error) error {
	if rollbackErr == nil {
		return fmt.Errorf("commit failed while %s; rollback succeeded: %w", action, commitErr)
	}
	return fmt.Errorf("commit failed while %s: %w; rollback also failed: %w", action, commitErr, rollbackErr)
}

func describePaths(changes []change) string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.kind == changeDelete {
			paths = append(paths, change.originalPath)
			continue
		}
		paths = append(paths, change.path)
	}
	return strings.Join(paths, ", ")
}
