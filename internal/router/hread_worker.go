package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yusing/hpatch"
)

const hreadExecutableName = "hread"

func conciseHReadError(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil || err.Error() != next.Error() {
			break
		}
		err = next
	}

	next := errors.Unwrap(err)
	if next == nil {
		return err.Error()
	}
	prefix, ok := strings.CutSuffix(err.Error(), ": "+next.Error())
	if !ok || prefix == "" {
		return err.Error()
	}

	root := next
	for {
		next = errors.Unwrap(root)
		if next == nil {
			break
		}
		root = next
	}
	return prefix + ": " + root.Error()
}

// RunHReadWorker handles the private child-process mode used by a routed
// session's hread executable. The argv0 gate keeps this out of the public
// hpatch-router command surface.
func RunHReadWorker(ctx context.Context, argv0 string, args []string, stdout, stderr io.Writer) (bool, int) {
	if filepath.Base(argv0) != hreadExecutableName {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		_, _ = fmt.Fprintln(stderr, "hread:", conciseHReadError(err))
		return true, 1
	}

	if len(args) < 1 || len(args) > 2 {
		return fail(errors.New("expected PATH and optional START:END arguments"))
	}
	input := args[0]
	if len(args) == 2 {
		encodedPath, _ := json.Marshal(args[0]) // Strings are always JSON-encodable.
		input = string(encodedPath) + " " + args[1]
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
