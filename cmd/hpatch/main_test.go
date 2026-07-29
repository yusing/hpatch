package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGainDoesNotRequireWorkingDirectory(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"gain"}, strings.NewReader("ignored"), &stdout, &stderr)
	wantPrefix := "output token estimates:\n"
	wantFragment := "\n\ncommand metrics:\n"
	if exitCode != 0 || !strings.HasPrefix(stdout.String(), wantPrefix) || !strings.Contains(stdout.String(), wantFragment) || stderr.Len() != 0 {
		t.Fatalf("gain = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestInformationalCommandsNeedNoEnvironmentOrStdin(t *testing.T) {
	removeCurrentDirectory(t)
	t.Setenv("XDG_CONFIG_HOME", "")

	tests := []struct {
		name         string
		args         []string
		want         string
		wantFragment string
	}{
		{name: "top-level help", args: []string{"--help"}, want: helpTextBase, wantFragment: "hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT"},
		{name: "translate help", args: []string{"translate", "--help"}, want: translateHelpText, wantFragment: "without modifying"},
		{name: "tool help", args: []string{"--tool-help"}, want: toolHelpText(), wantFragment: "HPATCH/1"},
		{name: "version", args: []string{"--version"}, want: "hpatch devel\n", wantFragment: "hpatch devel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(test.args, errorReader{}, &stdout, &stderr)
			if exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantFragment) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), test.wantFragment)
			}
		})
	}
}

func TestTopLevelHelpDescribesCompletePublicSurface(t *testing.T) {
	for _, fragment := range []string{
		"hpatch [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch translate [--root ROOT] [--cwd CWD] < SCRIPT",
		"hpatch gain",
		"standard input",
		"bsel \"START\" \"END\"",
		"tsel FROM_LINE \"TEXT\" [N]",
		"rsel START:END",
		"type <<TAG",
		"commit",
		"next immutable in-memory baseline",
		"Outside a type heredoc",
		"exact unindented",
		"functions.hpatch",
		"Build selectors against each existing file's immutable baseline.",
		"Invoke hpatch once per attempt; do not encode, shell-wrap, or route it",
		"immutable baseline",
		"Text introduced by an",
		"multiple insertions",
		"complete active-file baseline",
		"separate matches",
		"Prefer a broader TEXT instead of occurrence arithmetic",
		"selection sets",
		"ASCII space and tab runs match interchangeably",
		"preserves the selected final",
		"translate always emits root-relative paths",
		"caller-accounted hpatch",
		"absolute selectors",
		"terminal failure reasons",
		"settings.json under hooks.error",
		"format_markdown",
		"shellquote",
	} {
		if !strings.Contains(helpTextBase, fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}
}

func TestToolHelpIsCompactAgentContract(t *testing.T) {
	help := toolHelpText()
	for _, required := range []string{
		"HPATCH/1",
		"call=functions.hpatch(raw_complete_script)",
		"atomic(reject|cancel)=patch:none,workspace:unchanged",
		"lex.command=nonblank_physical_line;newline=command_end;exception=type_heredoc;recovery=open_quote_across_newline=>one_header_owned_rejected_frame",
		"lex.quote=JSON_double_quote;literal_tab=allow;physical_newline=never;linebreak_escape=\\n|\\r",
		"cmd=in PATH|new PATH|mv DESTINATION|rm|sel LINE START:END",
		"state.active=in|new=>select;mv DESTINATION=>rename_active_file(source_implicit);rm=>delete_active_file(no_operand)",
		"cursor:=BOF,selections:=none,active:=keep",
		"state.coords=file.baseline[generation]",
		"state.commit=all_live_files",
		"partial_filesystem_write=false;whole_script_atomic=true",
		"tsel=baseline[(FROM_LINE,col1)..EOF];TEXT=nonempty",
		"tsel.TEXT=copy_exact_baseline_substring;start_at_first_nonwhitespace;exclude_leading_indent",
		"tsel.repair=FROM_LINE_only;TEXT_unchanged",
		"bsel=START!=END;nonempty;START_count(file)==1&&END_count(suffix_after_START)==1",
		"selection=START.first_byte..END.last_byte;outside_bytes_preserved",
		"bsel.anchor_fallback=no_exact_match",
		"anchor=stable_nonwhitespace_content;never_include_leading_indent;whole_line|indent_edit=>rsel",
		"rsel=logical_line[START..END]",
		"numeric_selector.coords=fresh_nl_-ba",
		"heredoc.tag=[A-Za-z0-9_.-]{1,64}",
		"inline_linebreaks=type|bsel:encode_as_\\n|\\r;tsel:forbid_LF_CR",
		"edit.copy=clipboard:=first_selection_baseline_text",
		"nonlinewise_destination_may_split_line",
		"following_terminator_outside_selection_unless_encoded_in_END",
		"edit.conflict=same_generation",
		"result.success=active_path+cursor_or_selection_ranges<=3+last_effective_edit",
		"result.selection_ranges=individual;locations>3=>first_3+omitted_count",
		"result.file_actions=show_if lifecycle|multi_file|changed_file!=active",
		"source_codepoints_per_line<=64;truncation_marker=none",
		"result.noop=net_workspace_unchanged=>reject",
		"repair_context_not_match_candidate",
		"verify=inspect_reported_lines",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("tool help does not contain %q", required)
		}
	}
	for _, excluded := range []string{
		"Usage:",
		"Commands:",
		"Agent use:",
		"Final-state report:",
		"hpatch gain",
		"--root",
		"--cwd",
		"Metrics:",
		"INDEX: COMMAND",
	} {
		if strings.Contains(help, excluded) {
			t.Fatalf("tool help contains prose or non-tool text %q", excluded)
		}
	}
	body := strings.TrimSuffix(help, "\n")
	if strings.Contains(body, "\n\n") {
		t.Fatal("tool help contains layout-only blank lines")
	}
	for line := range strings.SplitSeq(body, "\n") {
		if line != strings.TrimSpace(line) {
			t.Fatalf("tool help line has layout whitespace: %q", line)
		}
	}
	if len(help) >= len(helpTextBase)/2 {
		t.Fatalf("tool help is not compact: %d bytes versus %d-byte CLI help", len(help), len(helpTextBase))
	}
}

func TestRootAndCWDOptionsTranslateRootRelativePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{"nested", filepath.Join(root, "nested")} {
		var stdout, stderr bytes.Buffer
		exitCode := run(
			[]string{"translate", "--root", root, "--cwd", cwd},
			strings.NewReader("in main.go\ntsel 1 \"package main\"\ntype \"package graph\"\n"),
			&stdout,
			&stderr,
		)
		if exitCode != 0 || !strings.HasPrefix(stderr.String(), "in nested/main.go ") {
			t.Fatalf("run(cwd %q) = exit %d, stdout %q, stderr %q", cwd, exitCode, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "*** Update File: nested/main.go\n") {
			t.Fatalf("translation for cwd %q is not root-relative:\n%s", cwd, stdout.String())
		}
	}
}

func TestWorkspaceOptionsRejectInvalidBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"--root", "relative"},
		{"--root", root, "--cwd", outside},
		{"--root", root, "--cwd", "escape"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(args, errorReader{}, &stdout, &stderr)
			if exitCode != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("run(%q) = exit %d, stdout %q, stderr %q", args, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestTopLevelHelpDescribesSelectionOperandConstraints(t *testing.T) {
	for _, fragment := range []string{
		"`tsel` starts at column 1 of FROM_LINE",
		"scans forward through EOF",
		"optional count defaults to one",
		"separate exact",
		"all requested matches",
		"Prefer a broader TEXT instead of occurrence arithmetic",
		"bsel searches the complete active-file baseline",
		"resolves START uniquely",
		"resolves END uniquely only after START",
	} {
		if !strings.Contains(helpTextBase, fragment) {
			t.Fatalf("help does not contain %q", fragment)
		}
	}
	if strings.Contains(translateHelpText, "`tsel` starts at column 1 of FROM_LINE") {
		t.Fatal("translate help duplicates top-level operand constraints")
	}
	if !strings.Contains(translateHelpText, "Run hpatch --help for the complete editing and agent workflow.") {
		t.Fatal("translate help does not point to top-level help")
	}
}

func TestHelpDoesNotLeakSourceTreeReferences(t *testing.T) {
	for _, output := range []string{helpTextBase, toolHelpText(), translateHelpText} {
		for _, stale := range []string{"doc/spec", "AGENT_INSTRUCTIONS.md"} {
			if strings.Contains(output, stale) {
				t.Fatalf("help contains source-tree reference %q", stale)
			}
		}
	}
}

func TestUnsupportedInformationalAliasesRemainErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tests := [][]string{
		{"-h"},
		{"help"},
		{"--help", "extra"},
		{"translate", "-h"},
		{"translate", "--help", "extra"},
		{"--tool-help", "extra"},
		{"translate", "--version"},
		{"gain", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := run(args, errorReader{}, &stdout, &stderr)
			if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "expected no arguments") {
				t.Fatalf("run(%q) = exit %d, stdout %q, stderr %q", args, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestInformationalWriteFailureIsReported(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run([]string{"--help"}, errorReader{}, errorWriter{}, &stderr)
	if exitCode != 1 || !strings.Contains(stderr.String(), "hpatch: writing help:") {
		t.Fatalf("run() = exit %d, stderr %q", exitCode, stderr.String())
	}
}

func removeCurrentDirectory(t *testing.T) {
	t.Helper()
	removedDirectory := t.TempDir()
	t.Chdir(removedDirectory)
	if err := os.Remove(removedDirectory); err != nil {
		t.Skipf("platform does not allow removing the current directory: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("stdin must not be read")
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
