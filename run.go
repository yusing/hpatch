package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Workspace is the filesystem authority for one hpatch operation. Root should
// be opened from its canonical absolute path; absolute script paths are matched
// against that name. CWD is root-relative and defaults to ".".
type Workspace struct {
	Root *os.Root
	CWD  string
}

// Run executes the hpatch command-line contract with workingDirectory as the
// workspace root. New callers that already own a root capability should use
// RunWorkspace, Apply, or Translate.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, workingDirectory, dataDirectory string) int {
	if len(args) == 1 && args[0] == "gain" {
		return RunWorkspace(args, stdin, stdout, stderr, Workspace{}, dataDirectory)
	}
	rootPath, err := filepath.Abs(workingDirectory)
	if err != nil {
		return fail(stderr, fmt.Sprintf("canonicalizing workspace root: %v", err))
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fail(stderr, fmt.Sprintf("canonicalizing workspace root: %v", err))
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fail(stderr, fmt.Sprintf("opening workspace root: %v", err))
	}
	defer root.Close()
	return RunWorkspace(args, stdin, stdout, stderr, Workspace{Root: root, CWD: "."}, dataDirectory)
}

// RunWorkspace executes the command-line contract within workspace.
func RunWorkspace(args []string, stdin io.Reader, stdout, stderr io.Writer, workspace Workspace, dataDirectory string) int {
	translateMode := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "translate":
		translateMode = true
	case len(args) == 1 && args[0] == "gain":
		metrics, err := readMetrics(dataDirectory)
		if err != nil {
			return fail(stderr, err.Error())
		}
		if _, err := io.WriteString(stdout, gainReport(metrics)); err != nil {
			return fail(stderr, fmt.Sprintf("writing gain report: %v", err))
		}
		return 0
	default:
		return fail(stderr, "expected no arguments or exactly: translate or gain")
	}

	script, err := io.ReadAll(stdin)
	if err != nil {
		return failWithIneffectiveMetrics(stderr, fmt.Sprintf("reading script: %v", err), dataDirectory, string(script), commandMetrics{})
	}
	scriptText := string(script)
	changes, filesystem, commands, err := evaluateScript(context.TODO(), workspace, scriptText)
	if err != nil {
		return failWithIneffectiveMetrics(stderr, err.Error(), dataDirectory, scriptText, commands)
	}
	if !translateMode && len(changes) == 0 {
		if dataDirectory != "" {
			if err := recordCommandMetrics(dataDirectory, commands); err != nil {
				warn(stderr, err.Error())
			}
		}
		return 0
	}
	if translateMode {
		patch, err := translate(changes)
		if err != nil {
			return failWithIneffectiveMetrics(stderr, err.Error(), dataDirectory, scriptText, commands)
		}
		if _, err := io.WriteString(stdout, patch); err != nil {
			return failWithIneffectiveMetrics(stderr, fmt.Sprintf("writing patch: %v", err), dataDirectory, scriptText, commands)
		}
		if dataDirectory != "" {
			if err := recordMetrics(dataDirectory, scriptText, patch, commands); err != nil {
				warn(stderr, err.Error())
			}
		}
		return 0
	}
	if err := commitChanges(changes, rootFileOperations{root: filesystem.root}); err != nil {
		return failWithIneffectiveMetrics(stderr, fmt.Sprintf("changing %s: %v", describePaths(changes), err), dataDirectory, scriptText, commands)
	}
	if dataDirectory != "" {
		patch, err := translate(changes)
		if err != nil {
			warn(stderr, "collecting metrics: "+err.Error())
			if err := recordCommandMetrics(dataDirectory, commands); err != nil {
				warn(stderr, err.Error())
			}
		} else if err := recordMetrics(dataDirectory, scriptText, patch, commands); err != nil {
			warn(stderr, err.Error())
		}
	}
	return 0
}

// Apply evaluates and atomically applies script within workspace.
func Apply(ctx context.Context, workspace Workspace, script string) error {
	changes, filesystem, _, err := evaluateScript(ctx, workspace, script)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commitChanges(changes, rootFileOperations{root: filesystem.root})
}

// Translate evaluates script without mutation and returns an apply_patch envelope
// whose paths are relative to workspace.Root.
func Translate(ctx context.Context, workspace Workspace, script string) ([]byte, error) {
	changes, _, _, err := evaluateScript(ctx, workspace, script)
	if err != nil {
		return nil, err
	}
	patch, err := translate(changes)
	if err != nil {
		return nil, err
	}
	return []byte(patch), nil
}

type filesystemWorkspace struct {
	root *os.Root
	cwd  string
}

func evaluateScript(ctx context.Context, workspace Workspace, script string) ([]change, filesystemWorkspace, commandMetrics, error) {
	filesystem, err := validateWorkspace(ctx, workspace)
	if err != nil {
		return nil, filesystemWorkspace{}, commandMetrics{}, err
	}
	program, err := parse(script)
	if err != nil {
		var commands commandMetrics
		if sourceError, ok := errors.AsType[*commandError](err); ok {
			commands.invokeFailure(sourceError.Operation)
		}
		return nil, filesystemWorkspace{}, commands, err
	}
	load := func(path string) (loadedFile, error) {
		if err := ctx.Err(); err != nil {
			return loadedFile{}, err
		}
		file, err := filesystem.root.Open(path)
		if err != nil {
			return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return loadedFile{}, fmt.Errorf("%s is not a regular file", path)
		}
		content, err := io.ReadAll(file)
		if err != nil {
			return loadedFile{}, fmt.Errorf("reading %s: %w", path, err)
		}
		if !utf8.Valid(content) {
			return loadedFile{}, fmt.Errorf("%s is not UTF-8", path)
		}
		return loadedFile{content: string(content), mode: info.Mode()}, nil
	}
	exists := func(path string) (fs.FileMode, bool, error) {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		info, err := filesystem.root.Stat(path)
		if err == nil {
			return info.Mode(), true, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	changes, commands, err := program.evaluate(filesystem.resolvePath, load, exists)
	if err != nil {
		return nil, filesystemWorkspace{}, commands, err
	}
	return changes, filesystem, commands, nil
}

func validateWorkspace(ctx context.Context, workspace Workspace) (filesystemWorkspace, error) {
	if ctx == nil {
		return filesystemWorkspace{}, fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return filesystemWorkspace{}, err
	}
	if workspace.Root == nil {
		return filesystemWorkspace{}, fmt.Errorf("workspace root is nil")
	}
	cwd := workspace.CWD
	if cwd == "" {
		cwd = "."
	}
	cwd = filepath.Clean(cwd)
	if !filepath.IsLocal(cwd) {
		return filesystemWorkspace{}, fmt.Errorf("workspace cwd %q is not root-relative", workspace.CWD)
	}
	info, err := workspace.Root.Stat(cwd)
	if err != nil {
		return filesystemWorkspace{}, fmt.Errorf("validating workspace cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return filesystemWorkspace{}, fmt.Errorf("workspace cwd %q is not a directory", cwd)
	}
	return filesystemWorkspace{root: workspace.Root, cwd: cwd}, nil
}

func (w filesystemWorkspace) resolvePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		if !filepath.IsAbs(w.root.Name()) {
			return "", fmt.Errorf("absolute path requires an absolute workspace root")
		}
		relative, err := filepath.Rel(w.root.Name(), filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("resolving path against workspace root: %w", err)
		}
		path = relative
	} else {
		path = filepath.Join(w.cwd, path)
	}
	path = filepath.Clean(path)
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("path resolves outside workspace root")
	}
	return path, nil
}

func sanitizeDiagnostic(message string) string {
	var sanitized strings.Builder
	for _, character := range message {
		switch {
		case character == '\n':
			sanitized.WriteString("; ")
		case unicode.IsControl(character):
			escaped := strconv.QuoteRune(character)
			sanitized.WriteString(escaped[1 : len(escaped)-1])
		default:
			sanitized.WriteRune(character)
		}
	}
	return sanitized.String()
}

func fail(stderr io.Writer, message string) int {
	message = sanitizeDiagnostic(message)
	_, _ = fmt.Fprintf(stderr, "hpatch: %s\n", message)
	return 1
}

func failWithIneffectiveMetrics(stderr io.Writer, message, dataDirectory, script string, commands commandMetrics) int {
	exitCode := fail(stderr, message)
	if dataDirectory != "" {
		if err := recordIneffectiveMetrics(dataDirectory, script, commands); err != nil {
			warn(stderr, err.Error())
		}
	}
	return exitCode
}

func warn(stderr io.Writer, message string) {
	message = sanitizeDiagnostic(message)
	_, _ = fmt.Fprintf(stderr, "hpatch: warning: %s\n", message)
}
