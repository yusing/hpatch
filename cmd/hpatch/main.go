package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"hpatch"
)

const helpText = `Usage:
  hpatch < SCRIPT
  hpatch translate < SCRIPT
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
  1. Inspect the relevant source before selecting text.
  2. From the workspace root, invoke exactly hpatch translate and attach SCRIPT
     through the execution interface's native non-PTY stdin field.
  3. Do not use Python, printf, an encoding helper, a shell pipeline, or any other
     wrapper around hpatch translate. If native stdin is unavailable, stop instead
     of inventing another transport.
  4. If translation succeeds, pass its stdout directly and internally to the native
     apply_patch tool in the same orchestration boundary. Do not invoke a shell
     executable named apply_patch or return the translated patch to the model.
  5. Propagate failure from either boundary, then reread intended files and run
     focused validation. An opaque successful apply result is not verification.

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
  Relative paths resolve from hpatch's current directory; absolute paths remain
  absolute. Translation does not embed that working directory. The native patch
  tool independently chooses its application root, so use workspace-relative paths
  from the workspace root. Keep translated stdout patch-only and internal.
`

const translateHelpText = `Usage:
  hpatch translate < SCRIPT

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

	gainMode := len(args) == 1 && args[0] == "gain"
	workingDirectory := ""
	if !gainMode {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			_, _ = io.WriteString(stderr, "hpatch: determining working directory: "+err.Error()+"\n")
			return 1
		}
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		if gainMode {
			_, _ = io.WriteString(stderr, "hpatch: determining user config directory: "+err.Error()+"\n")
			return 1
		}
		_, _ = io.WriteString(stderr, "hpatch: warning: determining user config directory: "+err.Error()+"\n")
		return hpatch.Run(args, stdin, stdout, stderr, workingDirectory, "")
	}
	return hpatch.Run(args, stdin, stdout, stderr, workingDirectory, filepath.Join(configDirectory, "hpatch"))
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
