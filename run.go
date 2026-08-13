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

// EditText applies target-bearing HPATCH mutations to an in-memory immutable
// baseline. It performs no filesystem access, language validation, formatting,
// indentation correction, or whitespace cleanup.
func EditText(ctx context.Context, baseline, script string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	program, err := parse(script)
	if err != nil {
		return "", err
	}
	target := &editor{baseline: baseline}
	for index, command := range program.instructions {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if command.target.kind == targetNone ||
			(command.operation != "type" && command.operation != "type-" && command.operation != "type+") {
			return "", textEditCommandError(
				command,
				index+1,
				reasonSyntax,
				"text edit accepts only target-bearing type, type-, or type+",
			)
		}
		origin := editOrigin{
			command:        index + 1,
			line:           command.line,
			operation:      command.operation,
			target:         command.target.variant(),
			multilineValue: command.delimiter != "",
		}
		if err := target.applyMutation(command.operation, command.target, command.text, origin, command); err != nil {
			return "", textEditCommandError(command, index+1, reasonOf(err, reasonOther), err.Error())
		}
	}
	return target.content(), nil
}

func textEditCommandError(command instruction, index int, reason failureReason, message string) *commandError {
	return &commandError{
		Attempt:   command.attempt,
		Reason:    reason,
		Command:   index,
		Line:      command.line,
		Operation: command.operation,
		Category:  "edit",
		Source:    command.source,
		Message:   message,
	}
}

// TextLineCount returns the number of targetable logical rows in text.
func TextLineCount(text string) int {
	return len(logicalLines(text))
}

// TextReferences renders current LINE:HASH references for valid requested rows.
// Repeated and out-of-range row numbers are omitted.
func TextReferences(text string, rows ...int) string {
	lines := logicalLines(text)
	seen := make(map[int]struct{}, len(rows))
	var output strings.Builder
	for _, number := range rows {
		if number < 1 || number > len(lines) {
			continue
		}
		if _, exists := seen[number]; exists {
			continue
		}
		seen[number] = struct{}{}
		content := lineContent(text, lines[number-1])
		writeHashLine(&output, number, content, previewTextLimit(content, repairPreviewLimit))
	}
	return output.String()
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
	Patch      []byte
	Report     string
	Diagnostic string
	Rejections []HostRejection
	Invocation InvocationMetrics
}

// TranslateForHost evaluates script once without mutation and returns the
// translated patch, final-state report, and evaluator-owned invocation metrics.
// On an evaluation failure it also returns the command-line diagnostic and
// repair context, including configured error-hook warnings.
func TranslateForHost(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	return changeForHost(ctx, workspace, script, dataDirectory, false)
}

// TranslateForHostAt evaluates a host script relative to directory without
// imposing filesystem confinement. The host executor remains responsible for
// authorizing the translated patch.
func TranslateForHostAt(ctx context.Context, directory, script, dataDirectory string) (HostTranslation, error) {
	result, failureStage, err := translateDetailedAt(ctx, directory, script)
	return finishHostChange(ctx, dataDirectory, script, result, failureStage, err, false)
}

// ApplyForHost evaluates and atomically applies script while returning host diagnostics and metrics.
func ApplyForHost(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	return changeForHost(ctx, workspace, script, dataDirectory, true)
}

// ApplyForHostRoot evaluates and applies a script within root. It is intended
// for hosts that own a confined private filesystem.
func ApplyForHostRoot(ctx context.Context, root *os.Root, script, dataDirectory string) (HostTranslation, error) {
	return ApplyForHost(ctx, Workspace{Root: root}, script, dataDirectory)
}

func changeForHost(ctx context.Context, workspace Workspace, script, dataDirectory string, apply bool) (HostTranslation, error) {
	result, failureStage, err := changeDetailed(ctx, workspace, script, apply)
	return finishHostChange(ctx, dataDirectory, script, result, failureStage, err, apply)
}

func finishHostChange(ctx context.Context, dataDirectory, script string, result HostTranslation, failureStage string, err error, applied bool) (HostTranslation, error) {
	if err != nil {
		result.Rejections = hostRejectionsOf(err)

		if ctx.Err() == nil {
			result.Diagnostic = evaluationDiagnostic(ctx, err, dataDirectory)
			if contextErr := ctx.Err(); contextErr != nil {
				result.Diagnostic = ""
				return result, contextErr
			}
			outcome := "failed"
			if failureStage == "evaluated" {
				outcome = "rejected"
			}
			for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, failureStage, outcome, script, nil, errorHooksTimeout) {
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
	stage, outcome := "translated", "succeeded"
	if applied {
		stage, outcome = "applied", "succeeded"
	}
	for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, stage, outcome, script, result.Patch, errorHooksTimeout) {
		result.Diagnostic += warningDiagnostic(hookErr.Error())
	}
	if err := ctx.Err(); err != nil {
		result.Diagnostic = ""
		return result, err
	}
	return result, nil
}

func translateDetailed(ctx context.Context, workspace Workspace, script string) (HostTranslation, error) {
	result, _, err := changeDetailed(ctx, workspace, script, false)
	return result, err
}

func changeDetailed(ctx context.Context, workspace Workspace, script string, apply bool) (HostTranslation, string, error) {
	changes, filesystem, invocation, report, err := evaluateScript(ctx, workspace, script)
	if err != nil {
		result := HostTranslation{Report: report, Invocation: InvocationMetrics{value: invocation}}
		return result, "evaluated", err
	}
	if !apply {
		result, err := translatedEvaluation(ctx, changes, invocation, report, nil)
		if err != nil {
			return result, "translated", err
		}
		return result, "", nil
	}
	result := HostTranslation{Report: report, Invocation: InvocationMetrics{value: invocation}}
	if err := ctx.Err(); err != nil {
		return result, "", err
	}
	if err := commitChanges(changes, rootFileOperations{root: filesystem.root}); err != nil {
		return result, "applied", fmt.Errorf("changing %s: %w", describePaths(changes), err)
	}
	return result, "", nil
}

func translateDetailedAt(ctx context.Context, directory, script string) (HostTranslation, string, error) {
	changes, _, invocation, report, err := evaluateScriptAt(ctx, directory, script)
	if err != nil {
		result := HostTranslation{Report: report, Invocation: InvocationMetrics{value: invocation}}
		return result, "evaluated", err
	}
	result, err := translatedEvaluation(ctx, changes, invocation, report, nil)
	if err != nil {
		return result, "translated", err
	}
	return result, "", nil
}

func translatedEvaluation(ctx context.Context, changes []change, invocation invocationMetrics, report string, evaluationErr error) (HostTranslation, error) {
	result := HostTranslation{Report: report, Invocation: InvocationMetrics{value: invocation}}
	if evaluationErr != nil {
		return result, evaluationErr
	}
	if err := ctx.Err(); err != nil {
		return result, err
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
	return evaluateScriptInFilesystem(ctx, filesystem, script)
}

func evaluateScriptAt(ctx context.Context, directory, script string) ([]change, filesystemWorkspace, invocationMetrics, string, error) {
	filesystem, err := validateHostDirectory(ctx, directory)
	if err != nil {
		return nil, filesystemWorkspace{}, invocationMetrics{}, "", err
	}
	return evaluateScriptInFilesystem(ctx, filesystem, script)
}

func evaluateScriptInFilesystem(ctx context.Context, filesystem filesystemWorkspace, script string) ([]change, filesystemWorkspace, invocationMetrics, string, error) {
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
		info, err := filesystem.stat(path)
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

func validateHostDirectory(ctx context.Context, directory string) (filesystemWorkspace, error) {
	if ctx == nil {
		return filesystemWorkspace{}, fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return filesystemWorkspace{}, err
	}
	if directory == "" {
		return filesystemWorkspace{}, nil
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return filesystemWorkspace{}, fmt.Errorf("resolving host directory: %w", err)
	}
	directory = filepath.Clean(directory)
	info, err := os.Stat(directory)
	if err != nil {
		return filesystemWorkspace{}, fmt.Errorf("validating host directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return filesystemWorkspace{}, fmt.Errorf("host directory %q is not a directory", directory)
	}
	return filesystemWorkspace{cwd: directory}, nil
}

func (w filesystemWorkspace) resolvePath(path string) (string, error) {
	if w.root == nil {
		path = filepath.Clean(path)
		if w.cwd == "" && !filepath.IsAbs(path) {
			return "", fmt.Errorf("relative path requires a host directory")
		}
		return path, nil
	}
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

func (w filesystemWorkspace) hostPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(w.cwd, path)
}

func (w filesystemWorkspace) stat(path string) (fs.FileInfo, error) {
	if w.root == nil {
		return os.Stat(w.hostPath(path))
	}
	return w.root.Stat(path)
}

func (w filesystemWorkspace) open(path string) (*os.File, error) {
	if w.root == nil {
		return os.Open(w.hostPath(path))
	}
	return w.root.Open(path)
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
		diagnostic.WriteString(sanitizeDiagnostic(command.Error()))
		diagnostic.WriteByte('\n')
		diagnostic.WriteString(command.Repair)
	}
	output.WriteString(diagnostic.String())
	if _, routed := attemptMetadataFromContext(ctx); !routed {
		for _, hookErr := range runCommandErrorHooks(ctx, dataDirectory, commands, diagnostic.String(), errorHooksTimeout) {
			warn(&output, hookErr.Error())
		}
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
