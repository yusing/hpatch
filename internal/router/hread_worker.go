package router

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yusing/hpatch"
)

const (
	hreadWorkerEnvironment = "HPATCH_INTERNAL_HREAD_WORKER"
)

// RunHReadWorker handles the private child-process mode used by a routed
// session's hread wrapper. The environment gate keeps this out of the public
// hpatch-router command surface.
func RunHReadWorker(ctx context.Context, args []string, stdout, stderr io.Writer) (bool, int) {
	if os.Getenv(hreadWorkerEnvironment) != "1" {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		_, _ = fmt.Fprintln(stderr, "hread:", err)
		return true, 1
	}
	if len(args) != 1 {
		return fail(fmt.Errorf(`expected exactly one "PATH" or "PATH" START:END argument`))
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
	output, readErr := hpatch.ReadHashLines(ctx, hpatch.Workspace{Root: root, CWD: relativeCWD}, args[0])
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
