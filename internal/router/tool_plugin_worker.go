package router

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yusing/hpatch/internal/router/toolplugin"
)

const maxToolWorkerManifestBytes = 32 << 20

// RunToolPluginWorker handles the private child-process mode used by a
// process-scoped contributed-tool wrapper.
func RunToolPluginWorker(ctx context.Context, argv0 string, args []string, stdout, stderr io.Writer) (bool, int) {
	wrapper, err := filepath.Abs(argv0)
	if err != nil {
		return false, 0
	}
	info, err := os.Lstat(wrapper)
	directory := filepath.Dir(wrapper)
	expectedRegistryID, recognized := toolRegistryIDFromDirectory(directory)
	if err != nil || info.Mode()&os.ModeSymlink == 0 || !recognized {
		return false, 0
	}
	fail := func(err error) (bool, int) {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", filepath.Base(wrapper), err)
		return true, 1
	}

	executable, err := os.Executable()
	if err != nil {
		return fail(fmt.Errorf("locate hpatch-router executable: %w", err))
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fail(fmt.Errorf("resolve hpatch-router executable: %w", err))
	}
	target, err := filepath.EvalSymlinks(wrapper)
	if err != nil {
		return fail(fmt.Errorf("resolve tool wrapper: %w", err))
	}
	if target != executable {
		return fail(errors.New("tool wrapper does not target the running hpatch-router executable"))
	}

	manifest, err := readToolWorkerManifest(filepath.Join(directory, toolPluginManifestFilename))
	if err != nil {
		return fail(err)
	}
	if manifest.Version != 1 || manifest.RuntimeRoot != "runtime" || manifest.NodeExecutable == "" {
		return fail(errors.New("tool worker manifest is inconsistent"))
	}
	if manifest.RegistryID != expectedRegistryID {
		return fail(errors.New("tool worker registry identity mismatch"))
	}
	runtimeRoot := filepath.Join(directory, manifest.RuntimeRoot)
	identity, err := toolRegistryIdentity(manifest, runtimeRoot)
	if err != nil {
		return fail(err)
	}
	if identity != expectedRegistryID {
		return fail(errors.New("tool worker registry identity mismatch"))
	}

	name := filepath.Base(wrapper)
	var contribution *toolContribution
	for index := range manifest.Tools {
		if manifest.Tools[index].Name == name {
			if contribution != nil {
				return fail(fmt.Errorf("tool %q appears more than once in worker manifest", name))
			}
			contribution = &manifest.Tools[index]
		}
	}
	if contribution == nil || contribution.Builtin || !contribution.Executor ||
		contribution.Module == "" || contribution.PluginID == "" {
		return fail(fmt.Errorf("tool %q is unavailable in worker manifest", name))
	}
	if err := validateToolContribution(*contribution); err != nil {
		return fail(err)
	}

	execution, err := toolplugin.Execute(
		ctx,
		manifest.NodeExecutable,
		runtimeRoot,
		contribution.Module,
		contribution.ModuleIndex,
		args,
	)
	if err != nil {
		return fail(fmt.Errorf("execute tool plugin: %w", err))
	}
	if _, err := io.WriteString(stdout, execution.Stdout); err != nil {
		return fail(fmt.Errorf("write plugin stdout: %w", err))
	}
	if _, err := io.WriteString(stderr, execution.Stderr); err != nil {
		return fail(fmt.Errorf("write plugin stderr: %w", err))
	}
	return true, execution.ExitCode
}

func toolRegistryIDFromDirectory(directory string) (string, bool) {
	base := filepath.Base(directory)
	if !strings.HasPrefix(base, "hpatch-router-tools-") {
		return "", false
	}
	separator := strings.LastIndexByte(base, '-')
	if separator < 0 || separator == len(base)-1 {
		return "", false
	}
	registryID := base[separator+1:]
	decoded, err := hex.DecodeString(registryID)
	return registryID, err == nil && len(decoded) == 32
}

func readToolWorkerManifest(path string) (manifest toolWorkerManifest, err error) {
	file, err := os.Open(path)
	if err != nil {
		return toolWorkerManifest{}, fmt.Errorf("open tool worker manifest: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	content, err := io.ReadAll(io.LimitReader(file, maxToolWorkerManifestBytes+1))
	if err != nil {
		return toolWorkerManifest{}, fmt.Errorf("read tool worker manifest: %w", err)
	}
	if len(content) > maxToolWorkerManifestBytes {
		return toolWorkerManifest{}, fmt.Errorf("tool worker manifest exceeds %d bytes", maxToolWorkerManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return toolWorkerManifest{}, fmt.Errorf("decode tool worker manifest: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return toolWorkerManifest{}, errors.New("tool worker manifest contains trailing data")
	}
	return manifest, nil
}
