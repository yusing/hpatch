package hpatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/yusing/hpatch/internal/hpatchsyntax"
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
			(command.operation != "type" && command.operation != "add") {
			return "", textEditCommandError(
				command,
				index+1,
				reasonSyntax,
				"text edit accepts only target-bearing type or add",
			)
		}
		origin := editOrigin{
			command:        index + 1,
			line:           command.line,
			operation:      command.operation,
			target:         command.target.variant(),
			targetSpec:     command.target,
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
		Target:    command.target.variant(),
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

// TargetAlias maps a target from one successful script to the rendered region
// that replaced it. Hosts may retain aliases only after the translated patch
// was applied successfully.
type TargetAlias struct {
	Path   string
	Before string
	After  string
}

// TargetAliasRelation describes an emitted row target's coordinate relation to
// a confirmed prior replacement target on the same path. It contains no row
// hashes or target text.
type TargetAliasRelation string

const (
	TargetAliasRelationNone      TargetAliasRelation = "none"
	TargetAliasRelationExact     TargetAliasRelation = "exact"
	TargetAliasRelationContains  TargetAliasRelation = "contains"
	TargetAliasRelationContained TargetAliasRelation = "contained"
	TargetAliasRelationOverlap   TargetAliasRelation = "overlap"
)

// TargetAliasDiagnostic is transient alias-rewrite evidence for one row-target
// command. Rewritten is true only when the complete target, including hashes,
// followed a confirmed alias. Relation compares inclusive row coordinates only.
type TargetAliasDiagnostic struct {
	Command   int
	Rewritten bool
	Relation  TargetAliasRelation
}

// RewriteTargetAliases updates exact line and range targets through successful
// prior replacements. It preserves values and framing and performs no filesystem access.
func RewriteTargetAliases(script string, aliases []TargetAlias) (string, error) {
	rewritten, _, err := RewriteTargetAliasesWithCommands(script, aliases)
	return rewritten, err
}

// RewriteTargetAliasesWithCommands also returns the 1-based command numbers
// whose targets changed. The command numbers let hosts attribute evaluator
// rejections without retaining target content.
func RewriteTargetAliasesWithCommands(script string, aliases []TargetAlias) (string, []int, error) {
	rewritten, diagnostics, err := RewriteTargetAliasesWithDiagnostics(script, aliases)
	if err != nil {
		return "", nil, err
	}
	commands := make([]int, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Rewritten {
			commands = append(commands, diagnostic.Command)
		}
	}
	return rewritten, commands, nil
}

// RewriteTargetAliasesWithDiagnostics also returns privacy-safe, transient
// coordinate relations for row-target commands. It does not retain paths,
// targets, hashes, values, or other script content.
func RewriteTargetAliasesWithDiagnostics(script string, aliases []TargetAlias) (string, []TargetAliasDiagnostic, error) {
	if len(aliases) == 0 {
		return script, nil, nil
	}
	program, err := parse(script)
	if err != nil {
		return "", nil, err
	}
	lines := hpatchsyntax.SplitPhysicalLines(script)
	activePath := ""
	var diagnostics []TargetAliasDiagnostic
	for commandIndex, command := range program.instructions {
		switch command.operation {
		case "in", "new":
			activePath = command.path
		case "mv":
			activePath = command.path
		case "rm":
			activePath = ""
		case "type", "add":
			if command.target.kind != targetLine && command.target.kind != targetRange {
				continue
			}
			diagnostic := TargetAliasDiagnostic{
				Command:  commandIndex + 1,
				Relation: targetAliasRelation(activePath, command.target, aliases),
			}
			before := renderRowTarget(command.target)
			after := before
			for _, alias := range aliases {
				if alias.Path == activePath && alias.Before == after {
					after = alias.After
				}
			}
			if after == before {
				diagnostics = append(diagnostics, diagnostic)
				continue
			}
			diagnostic.Rewritten = true
			lineIndex := command.line - 1
			if lineIndex < 0 || lineIndex >= len(lines) {
				return "", nil, fmt.Errorf("command source line %d is outside script", command.line)
			}
			header := lines[lineIndex].Text
			operationEnd := len(command.operation)
			if len(header) <= operationEnd || header[operationEnd] != ' ' {
				return "", nil, fmt.Errorf("command source line %d has unexpected framing", command.line)
			}
			operand := operationEnd + 1
			if !strings.HasPrefix(header[operand:], before) {
				return "", nil, fmt.Errorf("command source line %d target changed during parsing", command.line)
			}
			boundary := operand + len(before)
			if boundary < len(header) && header[boundary] != ' ' && header[boundary] != '\t' {
				return "", nil, fmt.Errorf("command source line %d target boundary is invalid", command.line)
			}
			lines[lineIndex].Text = header[:operand] + after + header[boundary:]
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	var rewritten strings.Builder
	for _, line := range lines {
		rewritten.WriteString(line.Text)
		rewritten.WriteString(line.Terminator)
	}
	if _, err := parse(rewritten.String()); err != nil {
		return "", nil, fmt.Errorf("rewriting target aliases: %w", err)
	}
	return rewritten.String(), diagnostics, nil
}

func targetAliasRelation(path string, target targetSpec, aliases []TargetAlias) TargetAliasRelation {
	relation := TargetAliasRelationNone
	for _, alias := range aliases {
		if alias.Path != path {
			continue
		}
		prior, trailing, err := parseTarget(1, alias.Before, false)
		if err != nil || strings.TrimSpace(trailing) != "" ||
			(prior.kind != targetLine && prior.kind != targetRange) {
			continue
		}
		candidate := rowSpanRelation(target, prior)
		if targetAliasRelationRank(candidate) > targetAliasRelationRank(relation) {
			relation = candidate
		}
	}
	return relation
}

func rowSpanRelation(target, prior targetSpec) TargetAliasRelation {
	targetStart, targetEnd := target.start.line, target.start.line
	if target.kind == targetRange {
		targetEnd = target.end.line
	}
	priorStart, priorEnd := prior.start.line, prior.start.line
	if prior.kind == targetRange {
		priorEnd = prior.end.line
	}
	if targetEnd < targetStart || priorEnd < priorStart || targetEnd < priorStart || priorEnd < targetStart {
		return TargetAliasRelationNone
	}
	switch {
	case targetStart == priorStart && targetEnd == priorEnd:
		return TargetAliasRelationExact
	case targetStart >= priorStart && targetEnd <= priorEnd:
		return TargetAliasRelationContained
	case targetStart <= priorStart && targetEnd >= priorEnd:
		return TargetAliasRelationContains
	default:
		return TargetAliasRelationOverlap
	}
}

func targetAliasRelationRank(relation TargetAliasRelation) int {
	switch relation {
	case TargetAliasRelationExact:
		return 4
	case TargetAliasRelationContained:
		return 3
	case TargetAliasRelationContains:
		return 2
	case TargetAliasRelationOverlap:
		return 1
	default:
		return 0
	}
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

// HostRejection is the non-sensitive, structured identity of one rejected
// command. It intentionally excludes source text, diagnostics, and repair
// context so hosts can retain it as telemetry without retaining edit content.
type HostRejection struct {
	Command             int                 `json:"command"`
	SourceLine          int                 `json:"source_line"`
	Operation           string              `json:"operation"`
	Target              string              `json:"target,omitempty"`
	TargetAliasRelation TargetAliasRelation `json:"target_alias_relation,omitempty"`
	Reason              string              `json:"reason"`
	Path                string              `json:"path,omitempty"`
	GeneratedLine       int                 `json:"generated_line,omitempty"`
	GeneratedColumn     int                 `json:"generated_column,omitempty"`
	ValueLine           int                 `json:"value_line,omitempty"`
}

// HostOutcome identifies the furthest lifecycle stage reached by one host request.
type HostOutcome struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

// HostChange summarizes the requested workspace effect.
type HostChange struct {
	Files            int  `json:"files"`
	AlreadySatisfied bool `json:"already_satisfied"`
	Applied          bool `json:"applied"`
}

// HostFailure is actionable host-facing failure context. Unlike HostRejection,
// it may contain bounded repair text and is not suitable for durable telemetry.
type HostFailure struct {
	Command    int    `json:"command,omitempty"`
	Path       string `json:"path,omitempty"`
	Reason     string `json:"reason"`
	Scope      string `json:"scope"`
	Suggestion string `json:"suggestion,omitempty"`
}

// HostPatchSummary describes a translated patch without duplicating its content.
type HostPatchSummary struct {
	Files int `json:"files"`
	Bytes int `json:"bytes"`
}

// HostTranslation contains the complete result needed by an in-process host.
// Diagnostic contains a rejection diagnostic or non-fatal hook warnings.
type HostTranslation struct {
	Patch         []byte
	Report        string
	TargetAliases []TargetAlias
	Diagnostic    string
	Outcome       HostOutcome
	Change        HostChange
	Attempt       AttemptMetadata
	Failures      []HostFailure
	PatchSummary  HostPatchSummary
	Rejections    []HostRejection
}

// TranslateForHostAt evaluates a host script relative to directory without
// imposing filesystem confinement. The host executor remains responsible for
// authorizing the translated patch.
func TranslateForHostAt(ctx context.Context, directory, script, dataDirectory string) (HostTranslation, error) {
	changes, _, report, aliases, err := evaluateScriptAt(ctx, directory, script)
	result := hostTranslationResult(changes, report, aliases, err == nil)
	failureStage := ""
	if err != nil {
		failureStage = "evaluated"
	} else if err = translateHostResult(ctx, changes, &result); err != nil {
		failureStage = "translated"
	}
	return finishHostChange(ctx, dataDirectory, script, result, failureStage, err, false)
}

// ApplyForHost evaluates and atomically applies script while returning host diagnostics.
func ApplyForHost(ctx context.Context, workspace Workspace, script, dataDirectory string) (HostTranslation, error) {
	changes, filesystem, report, aliases, err := evaluateScript(ctx, workspace, script)
	result := hostTranslationResult(changes, report, aliases, err == nil)
	failureStage := ""
	if err != nil {
		failureStage = "evaluated"
	} else if err = ctx.Err(); err == nil && len(changes) != 0 {
		if err = commitChanges(changes, rootFileOperations{root: filesystem.root}); err != nil {
			err = fmt.Errorf("changing %s: %w", describePaths(changes), err)
			failureStage = "applied"
		}
	}
	return finishHostChange(ctx, dataDirectory, script, result, failureStage, err, true)
}

// ApplyForHostRoot evaluates and applies a script within root. It is intended
// for hosts that own a confined private filesystem.
func ApplyForHostRoot(ctx context.Context, root *os.Root, script, dataDirectory string) (HostTranslation, error) {
	return ApplyForHost(ctx, Workspace{Root: root}, script, dataDirectory)
}

func finishHostChange(ctx context.Context, dataDirectory, script string, result HostTranslation, failureStage string, err error, applied bool) (HostTranslation, error) {
	result.Attempt, _ = attemptMetadataFromContext(ctx)
	if err != nil {
		result.Rejections = hostRejectionsOf(err)
		result.Failures = hostFailuresOf(err, failureStage)
		status := "failed"
		if failureStage == "evaluated" {
			status = "rejected"
		}
		result.Outcome = HostOutcome{Stage: failureStage, Status: status}

		if ctx.Err() == nil {
			result.Diagnostic = evaluationDiagnostic(ctx, err, dataDirectory)
			if contextErr := ctx.Err(); contextErr != nil {
				result.Diagnostic = ""
				return result, contextErr
			}
			for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, failureStage, status, script, nil, errorHooksTimeout) {
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
	stage, status := "translated", "succeeded"
	if result.Change.AlreadySatisfied {
		stage, status = "evaluated", "already-satisfied"
	} else if applied {
		stage, status = "applied", "succeeded"
		result.Change.Applied = true
	}
	result.Outcome = HostOutcome{Stage: stage, Status: status}
	for _, hookErr := range runOutcomeHooks(ctx, dataDirectory, stage, status, script, result.Patch, errorHooksTimeout) {
		result.Diagnostic += warningDiagnostic(hookErr.Error())
	}
	if err := ctx.Err(); err != nil {
		result.Diagnostic = ""
		return result, err
	}
	return result, nil
}

func hostTranslationResult(changes []change, report string, aliases []TargetAlias, evaluated bool) HostTranslation {
	files := len(changes)
	return HostTranslation{
		Report:        report,
		TargetAliases: slices.Clone(aliases),
		Change:        HostChange{Files: files, AlreadySatisfied: evaluated && files == 0},
	}
}

func translateHostResult(ctx context.Context, changes []change, result *HostTranslation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	patch, err := translate(changes)
	if err != nil {
		return err
	}
	result.Patch = []byte(patch)
	result.PatchSummary = HostPatchSummary{Files: len(changes), Bytes: len(patch)}
	return nil
}

type filesystemWorkspace struct {
	root *os.Root
	cwd  string
}

func evaluateScript(ctx context.Context, workspace Workspace, script string) ([]change, filesystemWorkspace, string, []TargetAlias, error) {
	filesystem, err := validateWorkspace(ctx, workspace)
	if err != nil {
		return nil, filesystemWorkspace{}, "", nil, err
	}
	return evaluateScriptInFilesystem(ctx, filesystem, script)
}

func evaluateScriptAt(ctx context.Context, directory, script string) ([]change, filesystemWorkspace, string, []TargetAlias, error) {
	filesystem, err := validateHostDirectory(ctx, directory)
	if err != nil {
		return nil, filesystemWorkspace{}, "", nil, err
	}
	return evaluateScriptInFilesystem(ctx, filesystem, script)
}

func evaluateScriptInFilesystem(ctx context.Context, filesystem filesystemWorkspace, script string) ([]change, filesystemWorkspace, string, []TargetAlias, error) {
	program, err := parse(script)
	if err != nil {
		return nil, filesystemWorkspace{}, "", nil, err
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
	changes, report, aliases, err := program.evaluate(ctx, filesystem.resolvePath, load, exists)
	if err != nil {
		return nil, filesystemWorkspace{}, "", nil, err
	}
	return changes, filesystem, report, aliases, nil
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
			output.WriteString(warningDiagnostic(hookErr.Error()))
		}
	}
	return output.String()
}

func warningDiagnostic(message string) string {
	message = sanitizeDiagnostic(message)
	return fmt.Sprintf("hpatch: warning: %s\n", message)
}
