package main

import (
	"fmt"
	"hpatch"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
)

const helpTextBase = `Usage:
  hpatch [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch gain
  hpatch --help
  hpatch translate --help
  hpatch --tool-help
  hpatch --version

Input and output:
  hpatch reads the complete editing script from standard input, validates and
  evaluates every command in memory, stages all changes, and only then commits.
  Normal-mode success writes the final active-file state report to stderr. translate
  never modifies files, writes one OpenAI apply_patch envelope to stdout, and then
  writes the pending final-state report to stderr. Failures use stderr and nonzero status.

Agent workflow:
  1. Inspect the relevant source before constructing selectors.
  2. Build selectors against each existing file's immutable baseline.
  3. Send one complete editing script directly as functions.hpatch's free-form
     input. Invoke hpatch once per attempt; do not encode, shell-wrap, or route it
     through functions.exec, apply_patch, or hpatch translate.
  4. If functions.hpatch rejects the script, no staged edits were committed.
     A rejection that reached an existing file prints repair context after its
     diagnostic: the addressed line's column count, that line's token column spans,
     anchor occurrence lines, or the baseline lines earlier commands already claim.
     Correct from that context and resubmit the complete script against the unchanged
     file state; do not reread the file to recount columns.
  5. After success, run focused behavioral validation. For Go source changes, run
     gofmt before tests so structural errors are reported immediately. Success means
     every selector resolved, not that it resolved where you intended: a selector
     matching an existing but unintended span commits and reports success. Treat the
     final-state report and a parser or formatter as the check on placement.

Editing commands:
  in PATH                             select or reselect an existing file baseline
  new PATH                            select a pending empty file at cursor 0:0
  mv PATH                             move the active pending file without changing its baseline
  rm                                  remove the active file and clear editor state
  sel LINE START:END                  select inclusive one-based rune columns
  tsel LINE OCCURRENCE "TEXT" [N]     select N occurrences; N defaults to 1
  bsel "START" "END"                  select one whole-file uniquely anchored block
  bsel_next "START" "END"             select one state-scoped uniquely anchored block
  rsel START:END                      select inclusive complete logical lines
  type "TEXT"                         record replacement or insertion at baseline coordinates
  del                                 record deletion of the selection
  dup                                 copy the baseline selection immediately after it

Baseline editor state:
  The first in for an existing file captures an immutable baseline. Every selector
  for that file resolves against that baseline, regardless of prior edits or command
  order. Returning with in resets the baseline cursor to 0:0 and clears the selection,
  but retains recorded edits. mv preserves baseline identity. Text introduced by an
  earlier command is not selectable. A selector that overlaps baseline content already
  replaced or deleted by an earlier edit is rejected.

  Cursors and selections are baseline positions. A selection command replaces the
  prior selection. After type, the cursor is at the selected baseline span's end; a
  cursor insertion stays at that baseline position. del leaves it at the selection
  start. dup leaves it at the selection end and does not select the inserted copy.

  Disjoint baseline edits are applied together after complete validation. Replacements
  or deletions that overlap, insertions inside a replaced span, and multiple insertions
  at one baseline position are conflicts and reject the complete script. An insertion
  exactly at a replacement boundary is unambiguous and permitted. A new file has an
  empty baseline and accepts at most one effective type write. rm rejects an existing
  baseline file that already has content edits.

  ` + "`sel`" + ` columns count Unicode code points, not display width: one tab is one
  column, so a rendered editor column does not match a sel column on an indented line.
  Both endpoints are inclusive. Prefer tsel or rsel when the target has a usable text
  anchor or is a whole line; a sel range that resolves to an unintended but valid span
  commits silently. A rejected range prints the line's column count and each token's
  column span, which is enough to correct it without rereading the file.

  ` + "`tsel`" + ` occurrence must be nonzero: positive values count from the start, and
  negative values count from the end. Its optional count must be a positive integer and
  selects consecutive nonoverlapping occurrences, including intervening source text.
  Both ` + "`bsel`" + ` and ` + "`bsel_next`" + ` anchors must be nonempty and different.

  bsel searches the complete active-file baseline, independent of cursor or selection.
  bsel_next searches inside the current baseline selection when one exists; otherwise
  it searches from the current baseline cursor to end-of-file and never wraps. Each
  command resolves START uniquely in its scope, then resolves END uniquely only after
  START. Exact anchors are authoritative; when an anchor has no exact occurrence,
  nonempty ASCII space and tab runs match interchangeably. The selected span includes
  both anchors, so replacement TEXT must reproduce whatever END covers. An END anchor
  stopping mid-expression leaves the rest of that expression in place, and TEXT that
  also supplies it duplicates the remainder. Use rsel to replace whole lines.

  rsel owns selected line terminators. When type replaces a terminated linewise
  selection and TEXT has no final terminator, hpatch preserves the selected final
  LF, CRLF, or CR. An explicit final terminator is authoritative; an unterminated
  selected final line stays unterminated. del still removes complete selected lines.

  TEXT, START, and END are JSON strings. Use a JSON serializer for nontrivial
  operands rather than hand-escaping quotes, backslashes, newlines, or Unicode.
  type, bsel, and bsel_next strings may contain encoded line terminators; tsel may not.

Final-state report:
  A successful report starts with the active path and rendered cursor or selection,
  followed by up to three nearby post-edit lines. Each preview contains at most 64
  Unicode code points and escapes control characters so it remains on one output line.
  Use the report to orient focused validation without rereading a successfully edited file.

Metrics:
  hpatch gain reads no script and reports caller-accounted hpatch and apply_patch
  output-token estimates separately from input-token estimates. Stable tables retain
  evaluator-owned command errors, absolute selectors, single and multiple
  tsel spans, exact and recovered block successes, and terminal failure reasons.

Error hook:
  Agent-correctable script evaluation failures run each command template in
  <user-config-directory>/hpatch/settings.json under hooks.error. Templates receive
  the failed command's number, source line, operation, category, path, input, failure,
  diagnostic, repair context, and a Markdown Body. The format_markdown function returns
  that Body, and shellquote safely quotes a string for the shell. Hook failures are
  warnings and never replace the original hpatch failure. All hooks share one 10-second
  execution deadline.

Paths and patch boundary:
  --root selects the trusted workspace boundary and defaults to hpatch's current
  directory. --cwd selects an existing directory within that root and defaults to
  ".". Relative script paths resolve from cwd. Absolute script paths must use root's
  canonical spelling and stay within it. Paths that escape root, including through
  symlinks, are rejected.
  Normal edits stay within root, and translate always emits root-relative paths.
  Apply translated output from the same root. Keep translated stdout patch-only and
  internal.
`

const toolHelpTextBase = `Edit workspace files atomically with one free-form script. Submit the complete
script in one call. A rejected script changes nothing.

Commands:
  in PATH                              select an existing file baseline
  new PATH                             select a pending empty file
  mv PATH                              move the active pending file
  rm                                   remove the active file
  sel LINE START:END                   select inclusive one-based rune columns
  tsel LINE OCCURRENCE "TEXT" [N]      select matching text; N defaults to 1
  bsel "START" "END"                   select one unique whole-file block
  bsel_next "START" "END"              select one unique block in the current scope
  rsel START:END                       select inclusive complete logical lines
  type "TEXT"                          replace the selection or insert at the cursor
  del                                  delete the selection
  dup                                  duplicate the selected baseline text

State and selectors:
  The first in for an existing file captures an immutable baseline. Every
  selector and edit for that file uses baseline coordinates even after earlier
  commands. Returning with in resets its cursor and selection but keeps recorded
  edits. mv preserves baseline identity. Text introduced by an edit is not selectable.

  sel columns count Unicode code points, including tabs, and both endpoints are
  inclusive. tsel occurrences are nonzero: positive from the start, negative
  from the end. Its optional N is positive and spans consecutive nonoverlapping
  matches. Prefer tsel or rsel when possible because a valid sel range may still
  target unintended text.

  bsel searches the whole baseline. bsel_next searches the active selection, or
  from the cursor to end of file when there is no selection, and never wraps.
  Anchors must be nonempty and different. Each anchor must resolve uniquely in
  its scope; if exact matching fails, nonempty ASCII space and tab runs are
  interchangeable. A block includes both anchors.

Edits:
  type, bsel, and bsel_next operands are JSON strings and may encode line
  terminators; tsel text may not. rsel owns line terminators: replacing a
  terminated selection without an explicit terminator preserves the existing
  LF, CRLF, or CR. del removes selected logical lines completely.

  Disjoint edits commit together after the whole script validates. Overlapping
  replacements or deletions, insertions inside replacements, and multiple insertions
  at one baseline position are conflicts. Boundary insertions are allowed. A new
  file accepts one effective type; rm conflicts with recorded edits to an existing
  file.

Final-state report:
  Use workspace-relative paths within the workspace. Parent directories for new
  files must already exist. Success reports the active path and up to three
  nearby post-edit lines; use that report plus a parser or formatter to verify
  placement. A rejection reports repair context when available; retry against
  the unchanged baseline.
`

const hostMetricsMarker = "Host metrics schema: caller-v2\n"

func toolHelpText() string {
	return toolHelpTextBase + hostMetricsMarker
}

const translateHelpText = `Usage:
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT

Read and evaluate a complete editing script from standard input without modifying
files, then write one OpenAI apply_patch envelope to stdout and the pending final-state
report to stderr. Successful stdout is patch-only; failures use stderr and nonzero
status.

Attach SCRIPT through the execution interface's native non-PTY stdin field. Do not
use Python, printf, an encoding helper, a shell pipeline, or any wrapper around
hpatch translate. Run hpatch --help for the complete editing and agent workflow.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if exitCode, handled := runInformational(args, stdout, stderr); handled {
		return exitCode
	}

	engineArgs, rootPath, cwd, gainMode, err := parseInvocation(args)
	if err != nil {
		_, _ = io.WriteString(stderr, "hpatch: "+err.Error()+"\n")
		return 1
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		if gainMode {
			_, _ = io.WriteString(stderr, "hpatch: determining user config directory: "+err.Error()+"\n")
			return 1
		}
		_, _ = io.WriteString(stderr, "hpatch: warning: determining user config directory: "+err.Error()+"\n")
		return runEngine(engineArgs, stdin, stdout, stderr, rootPath, cwd, "")
	}
	if gainMode {
		return hpatch.Run(engineArgs, stdin, stdout, stderr, "", filepath.Join(configDirectory, "hpatch"))
	}
	return runEngine(engineArgs, stdin, stdout, stderr, rootPath, cwd, filepath.Join(configDirectory, "hpatch"))
}

func parseInvocation(args []string) (engineArgs []string, rootPath, cwd string, gain bool, err error) {
	if len(args) == 1 && (args[0] == "gain" || args[0] == "record-metrics") {
		return args, "", "", true, nil
	}
	if len(args) > 0 && args[0] == "translate" {
		engineArgs = []string{"translate"}
		args = args[1:]
	}
	for len(args) > 0 {
		if len(args) < 2 || (args[0] != "--root" && args[0] != "--cwd") {
			return nil, "", "", false, fmt.Errorf("expected no arguments or exactly: [--root ROOT] [--cwd CWD], translate [--root ROOT] [--cwd CWD], or gain")
		}
		value := args[1]
		if value == "" {
			return nil, "", "", false, fmt.Errorf("%s requires a nonempty value", args[0])
		}
		switch args[0] {
		case "--root":
			if rootPath != "" {
				return nil, "", "", false, fmt.Errorf("--root may be specified only once")
			}
			rootPath = value
		case "--cwd":
			if cwd != "" {
				return nil, "", "", false, fmt.Errorf("--cwd may be specified only once")
			}
			cwd = value
		}
		args = args[2:]
	}
	if rootPath == "" {
		rootPath, err = os.Getwd()
		if err != nil {
			return nil, "", "", false, fmt.Errorf("determining working directory: %w", err)
		}
	} else if !filepath.IsAbs(rootPath) {
		return nil, "", "", false, fmt.Errorf("workspace root must be absolute")
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, "", "", false, fmt.Errorf("canonicalizing workspace root: %w", err)
	}
	if cwd == "" {
		cwd = "."
	}
	cwdPath := cwd
	if !filepath.IsAbs(cwdPath) {
		cwdPath = filepath.Join(rootPath, cwdPath)
	}
	cwdPath, err = filepath.EvalSymlinks(cwdPath)
	if err != nil {
		return nil, "", "", false, fmt.Errorf("canonicalizing workspace cwd: %w", err)
	}
	cwdInfo, err := os.Stat(cwdPath)
	if err != nil {
		return nil, "", "", false, fmt.Errorf("validating workspace cwd: %w", err)
	}
	if !cwdInfo.IsDir() {
		return nil, "", "", false, fmt.Errorf("workspace cwd must be a directory")
	}
	cwd, err = filepath.Rel(rootPath, cwdPath)
	if err != nil || !filepath.IsLocal(cwd) {
		return nil, "", "", false, fmt.Errorf("workspace cwd must resolve within root")
	}
	return engineArgs, rootPath, cwd, false, nil
}

func runEngine(args []string, stdin io.Reader, stdout, stderr io.Writer, rootPath, cwd, dataDirectory string) int {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		_, _ = io.WriteString(stderr, "hpatch: opening workspace root: "+err.Error()+"\n")
		return 1
	}
	defer root.Close()
	return hpatch.RunWorkspace(args, stdin, stdout, stderr, hpatch.Workspace{Root: root, CWD: cwd}, dataDirectory)
}

func runInformational(args []string, stdout, stderr io.Writer) (int, bool) {
	var output, description string
	switch {
	case len(args) == 1 && args[0] == "--help":
		output = helpTextBase
		description = "help"
	case len(args) == 2 && args[0] == "translate" && args[1] == "--help":
		output = translateHelpText
		description = "translate help"
	case len(args) == 1 && args[0] == "--tool-help":
		output = toolHelpText()
		description = "tool help"
	case len(args) == 1 && args[0] == "--version":
		output = "hpatch " + buildVersion() + "\n"
		description = "version"
	default:
		return 0, false
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		_, _ = io.WriteString(stderr, "hpatch: writing "+description+": "+err.Error()+"\n")
		return 1, true
	}
	return 0, true
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "devel"
	}
	return info.Main.Version
}
