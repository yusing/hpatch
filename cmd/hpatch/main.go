package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/yusing/hpatch"
)

const helpTextBase = `Usage:
  hpatch [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT
  hpatch gain
  hpatch --help
  hpatch translate --help
  hpatch --tool-help
  hpatch --version

HPATCH/2:
  hpatch reads one complete script from standard input, evaluates every command
  against immutable invocation baselines, stages the complete change set, and
  commits only after all parsing, target verification, conflict checks, and
  language validation succeed. Rejection or cancellation changes nothing.

  translate performs the same evaluation without modifying files. It writes one
  OpenAI apply_patch envelope to stdout and the pending final-state report to
  stderr. Normal mode leaves stdout empty and writes the report after commit.

Commands:
  in PATH
  new PATH
  mv PATH
  rm
  type TARGET VALUE
  type- TARGET VALUE
  type+ TARGET VALUE
  type VALUE

Targets:
  LINE:HASH                         complete logical line
  LINE:HASH..LINE:HASH              inclusive complete-line range
  LINE:HASH "TEXT" [COUNT]          anchored exact literal occurrence(s)

  Copy complete LINE:HASH references from hread output. LINE is a positive
  one-based logical line and HASH is exactly four lowercase hexadecimal digits
  over that line's exact content, including indentation. The line number chooses
  the line; the hash rejects stale content. Hpatch never searches for a nearby
  hash or substitutes another matching line.

  A text target verifies its anchor row, then finds the first COUNT non-overlapping
  exact matches from that row's column 1 through EOF. COUNT defaults to one.
  TEXT must be nonempty and remain within one logical line. Every requested match
  must exist.

Mutations:
  type replaces every target span. An empty target-bearing type value deletes every
  span, including terminators owned by complete-line and range targets. type- inserts
  immediately before every span; type+ inserts immediately after every span. Before
  and after insertion preserve the target and never synthesize a newline; include \n
  when the inserted value must form a complete line.

  Nonempty complete-line and range replacement preserve the target's final LF, CRLF,
  or standalone CR when VALUE omits a terminator. An explicit terminator is
  authoritative.

Values and framing:
  Use an inline JSON-compatible quoted string for short or single-line values.
  Inline strings also accept literal horizontal tabs. Escape quotes, backslashes,
  line terminators, NUL, and other controls.

  Use only the fixed <<PATCH frame for multiline or escape-heavy values:

    type 20:2ff7..28:d10b <<PATCH
    replacement
    text
    PATCH

  The unindented closing line must be exactly PATCH. There is no interpolation,
  dedent, escaping, or alternate delimiter. A physical body line equal to PATCH
  must instead be represented by an inline escaped value.

Baselines and conflict safety:
  Use search to locate relevant regions. For any region likely to be edited, use
  hread for its first content read. Issue every independent hread call for
  already-known files or ranges together; do not serialize them.
  Every existing file has one immutable baseline for the complete invocation.
  Pending edits do not move later targets, and introduced content is not targetable
  in that call. When inspected files are ready, batch all short supporting edits
  that share a failure domain into one call with repeated in PATH sections; do not
  issue one call per file. Keep unrelated large <<PATCH values in separate calls,
  with at most one syntax-sensitive multiline Go declaration or function replacement
  per call; short supporting edits for that same change may remain with it. Prefer the
  smallest mutation that expresses the semantic change. When a formatter owns formatting,
  alignment, or indentation, do not replace surrounding lines merely to reproduce its
  output; let the formatter apply those changes. For example, add one struct field with one
  insertion rather than replacing the declaration. Preserve required indentation prefixes
  in indentation-sensitive languages such as Python. Before a later invocation targets a
  file changed by a successful call, discard its saved references and hread only the
  required region again; do not reread a file that needs no further edit.

  Replacements and deletions may not overlap. An insertion strictly inside either
  one conflicts. Insertions at a destructive span boundary are valid. Multiple
  insertions at the same baseline boundary render in script order. A multi-match
  mutation is all-or-nothing.

File lifecycle:
  in selects an existing regular UTF-8 file. Returning to a touched file reuses
  its baseline and pending edits. new selects a pending empty file; its immediately
  following nonblank command may be one targetless type VALUE initializer. Any
  intervening command closes that opportunity. New-file content cannot be targeted
  until a successful invocation and fresh read.

  mv moves the active logical file and preserves its baseline and pending edits.
  rm deletes the active file and clears the active file. Removing an existing file
  after a content mutation is a conflict. Parents for new and mv must already
  exist; hpatch does not create directories.

Agent workflow:
  1. Use search to locate relevant regions, then hread for the first content read
     of likely edit regions. Issue every independent hread call for already-known
     files or ranges together; do not serialize them. Copy complete LINE:HASH rows
     only from current output for that exact path.
  2. Put a line, range, or anchored literal target directly in each mutation.
  3. Use type to replace or delete, type- to insert before, and type+ to insert after.
     HPATCH/1 selection, clipboard, script commit, and del commands are invalid.
  4. Batch all ready short supporting edits that share a failure domain into one call
     with repeated in PATH sections; do not issue one call per file. Put unrelated
     large <<PATCH values in separate calls, with at most one syntax-sensitive multiline
     Go declaration or function replacement in each failure-domain call.
  5. Prefer the smallest mutation that expresses the semantic change. When a formatter
     owns formatting, alignment, or indentation, do not replace surrounding lines merely
     to reproduce its output; let the formatter apply those changes. For example, add one
     struct field with one insertion rather than replacing the declaration. Preserve
     required indentation prefixes in indentation-sensitive languages such as Python.
     Before a later invocation targets a changed file, discard its saved references and
     hread only the required region; do not reread files needing no edit.
  6. Prefer inline single-line values; reserve <<PATCH for multiline or escape-heavy text.
  7. After rejection, use a router indexed command or multiline-value-row correction
     only while the referenced rows still belong to the same baseline. Reread stale
     rows instead of guessing.
  8. Changed Go files are parsed and formatted with Go's standard library before
     success. Syntax rejection identifies the implicated command and shows at most
     five generated-source lines. Do not run redundant gofmt. Other languages receive
     no validation.

Final-state report:
  Success reports the active final path or "no active file", the last effective
  mutation and at most three immutable-baseline Unicode ranges, net add/update/
  move/delete counts, and at most three final preview rows as LINE:HASH TEXT.
  Preview content is bounded and controls are escaped. The report describes only
  the completed invocation and carries no target or editor state into a later call.

Failures and repair:
  Failures use stderr, nonzero status, and no patch or final-state report. Script
  diagnostics identify command index, source line, operation, path when known,
  category, and stable reason. Stale rows show verified current rows; incomplete
  literal targets show anchor context; conflicts identify the prior mutation.
  Missing rows do not guess. A malformed heredoc is one header-owned failure.

Metrics:
  hpatch gain reads no script and reports separate output-token estimates, input
  overhead, command counters for in/new/mv/rm/type/type-/type+, target counters
  for line/range/text-single/text-multiple, and stable failure reasons. Gain does
  not inspect or change the workspace.

Hooks:
  Agent-correctable evaluation failures run commands configured under hooks.error
  in <user-config-directory>/hpatch/settings.json. Router attempts also run
  hooks.outcome. Templates receive structured command, diagnostic, repair, attempt,
  and outcome data; format_markdown renders the Markdown body and shellquote quotes
  shell data. Hook failures are warnings and do not replace the hpatch result.

Workspace boundary:
  --root selects the trusted workspace and defaults to the current directory.
  --cwd selects an existing directory beneath root and defaults to ".". Relative
  script paths resolve from cwd. Absolute paths must use root's canonical spelling
  and remain beneath it. Lexical and symlink escapes reject. Translation always
  emits root-relative paths.
`

func toolHelpText() string {
	return hpatch.ToolDescription()
}

const translateHelpText = `Usage:
  hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT

Read and evaluate a complete HPATCH/2 script from standard input without modifying
files. Write one OpenAI apply_patch envelope to stdout and the pending final-state
report to stderr. Successful stdout is patch-only; failures use stderr and nonzero
status. Run hpatch --help for editing, target, safety, and workflow guidance.
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
