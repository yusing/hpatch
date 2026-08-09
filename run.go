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

	"golang.org/x/term"
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

func gainReportWidth(output io.Writer) int {
	file, ok := output.(interface{ Fd() uintptr })
	if !ok {
		return defaultGainReportWidth
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return defaultGainReportWidth
	}
	return width
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
		if _, err := io.WriteString(stdout, gainReportAtWidth(metrics, gainReportWidth(stdout))); err != nil {
			return fail(stderr, fmt.Sprintf("writing gain report: %v", err))
		}
		return 0
	default:
		return fail(stderr, "expected no arguments or exactly: translate or gain")
	}

	script, err := io.ReadAll(stdin)
	if err != nil {
		return fail(stderr, fmt.Sprintf("reading script: %v", err))
	}
	changes, filesystem, commands, report, err := evaluateScript(context.TODO(), workspace, string(script))
	if dataDirectory != "" {
		if metricsErr := updateMetrics(dataDirectory, metrics{invocationMetrics: commands}); metricsErr != nil {
			warn(stderr, metricsErr.Error())
		}
	}
	if err != nil {
		return failEvaluation(stderr, err, dataDirectory)
	}
	if !translateMode && len(changes) == 0 {
		_, _ = io.WriteString(stderr, report)
		return 0
	}
	if translateMode {
		patch, err := translate(changes)
		if err != nil {
			return fail(stderr, err.Error())
		}
		if _, err := io.WriteString(stdout, patch); err != nil {
			return fail(stderr, fmt.Sprintf("writing patch: %v", err))
		}
		_, _ = io.WriteString(stderr, report)
		return 0
	}
	if err := commitChanges(changes, rootFileOperations{root: filesystem.root}); err != nil {
		return fail(stderr, fmt.Sprintf("changing %s: %v", describePaths(changes), err))
	}
	_, _ = io.WriteString(stderr, report)
	return 0
}

// Apply evaluates and atomically applies script within workspace.
func Apply(ctx context.Context, workspace Workspace, script string) error {
	changes, filesystem, _, _, err := evaluateScript(ctx, workspace, script)
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
	result, err := translateDetailed(ctx, workspace, script)
	if err != nil {
		return nil, err
	}
	return result.Patch, nil
}

// CommandCorrection is an exact replacement for one rejected script command.
type CommandCorrection struct {
	Command     int
	Replacement string
}

// HostRejection is the non-sensitive, structured identity of one rejected
// command. It intentionally excludes source text, diagnostics, and repair
// context so hosts can retain it as telemetry without retaining edit content.
type HostRejection struct {
	Command         int    `json:"command"`
	SourceLine      int    `json:"source_line"`
	Operation       string `json:"operation"`
	Target          string `json:"target,omitempty"`
	Reason          string `json:"reason"`
	Path            string `json:"path,omitempty"`
	GeneratedLine   int    `json:"generated_line,omitempty"`
	GeneratedColumn int    `json:"generated_column,omitempty"`
	ValueLine       int    `json:"value_line,omitempty"`
}

// HostTranslation contains the complete result needed by an in-process host.
// Diagnostic contains a rejection diagnostic or non-fatal hook warnings.
type HostTranslation struct {
	Patch       []byte
	Report      string
	Diagnostic  string
	Corrections []CommandCorrection
	Rejections  []HostRejection
	Invocation  InvocationMetrics
}

// TranslateForHost evaluates script once without mutation and returns the
// translated patch, final-state report, and evaluator-owned invocation metrics.
// On an evaluation failure it also returns the command-line diagnostic and
// repair context, including configured error-hook warnings.
func TranslateForHost(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	return changeForHost(ctx, workspace, script, dataDirectory, false)
}

// ApplyForHost evaluates and atomically applies script while returning host diagnostics and metrics.
func ApplyForHost(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	return changeForHost(ctx, workspace, script, dataDirectory, true)
}

func changeForHost(ctx context.Context, workspace Workspace, script, dataDirectory string, apply bool) (HostTranslation, error) {
	result, err := changeDetailed(ctx, workspace, script, apply)
	if err != nil {
		result.Corrections = commandCorrectionsOf(err)
		result.Rejections = hostRejectionsOf(err)

		if ctx.Err() == nil {
			result.Diagnostic = evaluationDiagnostic(ctx, err, dataDirectory)
			if contextErr := ctx.Err(); contextErr != nil {
				result.Diagnostic = ""
				return result, contextErr
			}
			for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, "rejected", errorHooksTimeout) {
				warning := warningDiagnostic(hookErr.Error())
				if !strings.Contains(result.Diagnostic, warning) {
					result.Diagnostic += warning
				}
			}
		}
		if contextErr := ctx.Err(); contextErr != nil {
			result.Diagnostic = ""
			return result, contextErr
		}
		return result, err
	}
	outcome := "succeeded"
	if metadata, ok := attemptMetadataFromContext(ctx); ok && metadata.Correction {
		outcome = "corrected"
	}
	for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, outcome, errorHooksTimeout) {
		result.Diagnostic += warningDiagnostic(hookErr.Error())
	}
	if err := ctx.Err(); err != nil {
		result.Diagnostic = ""
		return result, err
	}
	return result, nil
}

func translateDetailed(ctx context.Context, workspace Workspace, script string) (HostTranslation, error) {
	return changeDetailed(ctx, workspace, script, false)
}

func changeDetailed(ctx context.Context, workspace Workspace, script string, apply bool) (HostTranslation, error) {
	changes, filesystem, invocation, report, err := evaluateScript(ctx, workspace, script)
	result := HostTranslation{Report: report, Invocation: InvocationMetrics{value: invocation}}
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if apply {
		if err := commitChanges(changes, rootFileOperations{root: filesystem.root}); err != nil {
			return result, fmt.Errorf("changing %s: %w", describePaths(changes), err)
		}
		return result, nil
	}
	patch, err := translate(changes)
	if err != nil {
		return result, err
	}
	result.Patch = []byte(patch)
	return result, nil
}

type filesystemWorkspace struct {
	root *os.Root
	cwd  string
}

func evaluateScript(ctx context.Context, workspace Workspace, script string) ([]change, filesystemWorkspace, invocationMetrics, string, error) {
	filesystem, err := validateWorkspace(ctx, workspace)
	if err != nil {
		return nil, filesystemWorkspace{}, invocationMetrics{}, "", err
	}
	program, err := parse(script)
	if err != nil {
		var events invocationMetrics
		for _, sourceError := range commandsOf(err) {
			events.invokeFailure(sourceError.Operation, sourceError.Attempt, sourceError.Reason)
		}
		return nil, filesystemWorkspace{}, events, "", err
	}
	load := func(path string) (loadedFile, error) {
		return filesystem.readFile(ctx, path)
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
	changes, commands, report, err := program.evaluate(ctx, filesystem.resolvePath, load, exists)
	if err != nil {
		return nil, filesystemWorkspace{}, commands, "", err
	}
	return changes, filesystem, commands, report, nil
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

func failureDiagnostic(message string) string {
	return fmt.Sprintf("hpatch: %s\n", sanitizeDiagnostic(message))
}

func fail(stderr io.Writer, message string) int {
	_, _ = io.WriteString(stderr, failureDiagnostic(message))
	return 1
}

func failEvaluation(stderr io.Writer, err error, dataDirectory string) int {
	_, _ = io.WriteString(stderr, evaluationDiagnostic(context.TODO(), err, dataDirectory))
	return 1
}

func evaluationDiagnostic(ctx context.Context, err error, dataDirectory string) string {
	commands := commandsOf(err)
	if len(commands) == 0 {
		return failureDiagnostic(err.Error())
	}

	var output strings.Builder
	var diagnostic strings.Builder
	for _, command := range commands {
		diagnostic.WriteString(failureDiagnostic(command.Error()))
		diagnostic.WriteString(command.Repair)
	}
	output.WriteString(diagnostic.String())
	for _, hookErr := range runCommandErrorHooks(ctx, dataDirectory, commands, diagnostic.String(), errorHooksTimeout) {
		warn(&output, hookErr.Error())
	}
	return output.String()
}

func warningDiagnostic(message string) string {
	message = sanitizeDiagnostic(message)
	return fmt.Sprintf("hpatch: warning: %s\n", message)
}

func warn(stderr io.Writer, message string) {
	_, _ = io.WriteString(stderr, warningDiagnostic(message))
}
