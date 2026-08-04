package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yusing/hpatch"
)

const hreadExecutableName = "hread"

// RunHReadWorker handles the private child-process mode used by a routed
// session's hread executable. The argv0 gate keeps this out of the public
// hpatch-router command surface.
func RunHReadWorker(ctx context.Context, argv0 string, args []string, stdout, stderr io.Writer) (bool, int) {
	if filepath.Base(argv0) != hreadExecutableName {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		_, _ = fmt.Fprintln(stderr, "hread:", err)
		return true, 1
	}
	if len(args) < 1 || len(args) > 2 {
		return fail(fmt.Errorf(`expected PATH and optional START:END arguments`))
	}
	encodedPath, _ := json.Marshal(args[0]) // Strings are always JSON-encodable.
	input := string(encodedPath)
	if len(args) == 2 {
		input += " " + args[1]
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(fmt.Errorf("determine working directory: %w", err))
	}
	rootPath := filepath.VolumeName(cwd) + string(os.PathSeparator)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fail(fmt.Errorf("opening filesystem root: %w", err))
	}
	relativeCWD, err := filepath.Rel(rootPath, cwd)
	if err != nil || !filepath.IsLocal(relativeCWD) {
		_ = root.Close()
		if err != nil {
			return fail(fmt.Errorf("resolve working directory: %w", err))
		}
		return fail(fmt.Errorf("working directory is unavailable"))
	}
	output, readErr := hpatch.ReadHashLines(ctx, hpatch.Workspace{Root: root, CWD: relativeCWD}, input)
	closeErr := root.Close()
	if readErr != nil {
		return fail(readErr)
	}
	if closeErr != nil {
		return fail(fmt.Errorf("closing trusted workspace: %w", closeErr))
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		return fail(fmt.Errorf("writing result: %w", err))
	}
	return true, 0
}

func ensureHReadSymlink() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate hread executable: %w", err)
	}
	return ensureHReadSymlinkForExecutable(executable)
}

func ensureHReadSymlinkForExecutable(executable string) (string, error) {
	link := filepath.Join(filepath.Dir(executable), hreadExecutableName)
	if _, err := os.Lstat(link); err == nil {
		return link, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect hread symlink: %w", err)
	}
	if err := os.Symlink(executable, link); err != nil {
		if errors.Is(err, os.ErrExist) {
			return link, nil
		}
		return "", fmt.Errorf("create hread symlink: %w", err)
	}
	return link, nil
}
