package main

import (
	"fmt"
	"hpatch"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
)

const helpText = `Usage:
  hpatch [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch gain
  hpatch --help
  hpatch translate --help
  hpatch --version

Input and output:
  hpatch reads the complete editing script from standard input, validates and
  evaluates every command in memory, stages all changes, and only then commits.
  Normal-mode success is silent. translate never modifies files and writes one
  OpenAI apply_patch envelope to stdout. Diagnostics use stderr and nonzero status.

Agent workflow:
  1. Inspect the relevant source before constructing selectors.
  2. Submit one complete editing script directly as the free-form input of
     functions.hpatch.
  3. Do not call functions.apply_patch, tools.apply_patch, or functions.exec to
     perform an hpatch edit. Do not invoke hpatch translate as an editing transport.
  4. If functions.hpatch rejects the script, no staged edits were committed.
     Correct and resubmit the complete script against the unchanged file state.
  5. After success, run focused behavioral validation. For Go source changes, run
     gofmt before tests so structural errors are reported immediately.

Editing commands:
  in PATH                     select an existing file at cursor 0:0
  new PATH                    select a pending empty file at cursor 0:0
  mv PATH                     move the active pending file
  rm                          remove the active pending file and clear editor state
  sel LINE START:END          select inclusive one-based Unicode columns
  tsel LINE OCCURRENCE "TEXT" select a nonempty one-line literal; -1 is last
  bsel "START" "END"         select one uniquely anchored block in the search scope
  rsel START:END              select inclusive complete logical lines
  type "TEXT"                 replace the selection or insert at the cursor
  del                         delete the selection
  dup                         duplicate the selection and select the copy

Editor state:
  Commands observe all preceding in-memory edits. Returning to a file with in resets
  its cursor to 0:0 but retains pending content. A selection command replaces the
  prior selection. type and del leave the cursor after effective inserted text or at
  the deletion start; dup selects the new copy.

  ` + "`tsel`" + ` occurrence must be nonzero: positive values count from the start, and
  negative values count from the end. Both ` + "`bsel`" + ` anchors must be nonempty and
  different.

  bsel searches inside the current selection when one exists; otherwise it searches
  from the current cursor to end-of-file. It never wraps to the file beginning.
  START and END must each occur exactly once within that scope, END must follow START
  without overlap, and the selected span includes both anchors.

  rsel owns selected line terminators. When type replaces a terminated linewise
  selection and TEXT has no final terminator, hpatch preserves the selected final
  LF, CRLF, or CR. An explicit final terminator is authoritative; an unterminated
  selected final line stays unterminated. del still removes complete selected lines.

  TEXT, START, and END are JSON strings. Use a JSON serializer for nontrivial
  operands rather than hand-escaping quotes, backslashes, newlines, or Unicode.
  type and bsel strings may contain encoded line terminators; tsel may not.

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

const translateHelpText = `Usage:
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT

Read and evaluate a complete editing script from standard input without modifying
files, then write one OpenAI apply_patch envelope to stdout. Successful stdout is
patch-only; diagnostics use stderr and nonzero status.

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
	if len(args) == 1 && args[0] == "gain" {
		return []string{"gain"}, "", "", true, nil
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
		output = helpText
		description = "help"
	case len(args) == 2 && args[0] == "translate" && args[1] == "--help":
		output = translateHelpText
		description = "translate help"
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
