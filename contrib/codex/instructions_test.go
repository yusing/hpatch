package codexinstructions

import (
	"strings"
	"testing"
)

func TestHPatchToolDescriptionStaysNonInstructional(t *testing.T) {
	const want = "Atomic HPATCH/2 edit-script application. Rejection or cancellation leaves the workspace unchanged."
	if HPatchToolDescription != want {
		t.Fatalf("HPatchToolDescription = %q, want %q", HPatchToolDescription, want)
	}
}

func TestInstructionsOwnCTP2Representation(t *testing.T) {
	for _, required := range []string{
		"## CTP/2 transport",
		"CTP/2 is an inline representation used in some model-visible strings",
		"CTP itself requires no inspection or tool call",
		"including all CTP/1 text",
		"A content-local dictionary and its reference body occupy one string",
		"Each `ID=VALUE` line defines",
		"Expand `@{ID}`",
		"`@@{ID}` is literal",
		"`@{ID}`, and every other `@` is literal",
		"The dictionary is local to that one string",
		"A visible-line representation may reuse exact lines",
		"`=SUFFIX,START,COUNT`",
		"`+JSON_STRING`",
		"compaction removes sources that are no longer visible",
		"`!ctp2 L` plus a line feed starts literal text",
		"Newly emitted tool names, tool inputs, and function arguments are literal",
		"`functions.shell`, omit `workdir`",
		"fully expanded existing absolute path, never a reference or placeholder",
		"Every decoded byte is final text",
	} {
		if !strings.Contains(Instructions(), required) {
			t.Errorf("Instructions() omits CTP representation rule %q", required)
		}
	}
}

func TestNativeInstructionsOmitOnlyCTPRepresentation(t *testing.T) {
	native := NativeInstructions()
	if strings.Contains(native, "## CTP/2 transport") || strings.Contains(native, "!ctp2") || strings.Contains(native, "!V=") {
		t.Fatal("native instructions contain CTP guidance")
	}
	for _, required := range []string{
		"<!-- hpatch-model-instructions:start -->",
		"## File editing",
		"## Shell execution",
		"<!-- hpatch-model-instructions:end -->",
	} {
		if !strings.Contains(native, required) {
			t.Errorf("NativeInstructions() omits %q", required)
		}
	}
}

func TestInstructionsBindCommentaryToSupportedTools(t *testing.T) {
	for _, test := range []struct {
		name         string
		instructions string
	}{
		{name: "CTP", instructions: Instructions()},
		{name: "native", instructions: NativeInstructions()},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, required := range []string{
				"Attach progress commentary only to a supported tool call",
				"When no available tool supports commentary, continue",
				"Never emit a standalone assistant message with\n`phase: \"commentary\"`",
				"standalone commentary messages are router-owned",
			} {
				if !strings.Contains(test.instructions, required) {
					t.Errorf("instructions omit commentary rule %q", required)
				}
			}
		})
	}
}

func TestInstructionsOwnCompleteShellWorkflow(t *testing.T) {
	for _, required := range []string{
		"Submit one free-form script without an outer heredoc",
		"The default interpreter is Bash.",
		"rather than `/usr/bin/env`",
		"accepts exactly one `{.}` placeholder",
		"`#!params=<JSON object>`",
		"`hread @shell/<reference>`",
		"only `#!script=@shell/<reference>`",
		"never mix retained scripts and workspace files",
		"PTY-backed, interactive, and long-running programs",
	} {
		if !strings.Contains(Instructions(), required) {
			t.Errorf("Instructions() omits shell workflow %q", required)
		}
	}
}

func TestInstructionsAcquireTargetContextOnce(t *testing.T) {
	for _, required := range []string{
		"Acquire target-bearing context once before editing.",
		"use hgrep first; use `-F` with repeated `-e` literals",
		"`hsymbol refs PATH LINE:HASH SYMBOL [N]`",
		"Use `hsymbol def PATH LINE:HASH SYMBOL [N]`",
		"Avoid bare whole-file hread unless the complete file",
		"After a successful hpatch, do not use hread,\nhgrep, hsymbol, or `git diff` on a changed file",
		"inspect, verify, or locate a follow-up target",
		"unknown or ambiguous\nin an unchanged file justifies a focused read",
		"Existing-file edits require a target.",
		"Targetless `type VALUE` is valid only immediately after",
		"unchanged saved rows remain valid even when edits shifted their line numbers",
		"On later calls, target previously changed content with a returned final-state row, a\nconfirmed mapping, or exact unanchored current text; never reconstruct a row or range endpoint.",
		`type "return oldResult, nil" "return newResult, nil"`,
		`C3:bcde "return oldResult, nil"`,
		"exact known target text spans logical lines or includes a trailing LF",
	} {
		if !strings.Contains(Instructions(), required) {
			t.Errorf("Instructions() omits target acquisition rule %q", required)
		}
	}
}

func TestInstructionsStayWithinHPatchAndPrivateTools(t *testing.T) {
	for _, excluded := range []string{
		"behavioral validation",
		"behavior-defining helper or callee semantics",
		"trace one concrete boundary",
		"imports inside the existing import declaration",
	} {
		if strings.Contains(Instructions(), excluded) {
			t.Errorf("Instructions() contains general project guidance %q", excluded)
		}
	}
}

func TestRecoveryGuidanceRendersDynamicReferences(t *testing.T) {
	const references = "Rejected target commands:\n"
	const want = "\nRepair only the stale targets in the retained rejected script. Each line is a current `C...` command handle followed directly by one different ordinary HPATCH/2 target. Submit every listed correction in one atomic payload. Recovery preserves operations, values, command order, and file context. A re-rejection changes no workspace file and makes every earlier handle stale. Use a complete script through functions.hpatch for every other correction.\n\n" + references
	if got := RecoveryGuidance(references); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}

func TestRecoveryGuidanceWithoutReferences(t *testing.T) {
	const want = "\nRepair only the stale targets in the retained rejected script. Each line is a current `C...` command handle followed directly by one different ordinary HPATCH/2 target. Submit every listed correction in one atomic payload. Recovery preserves operations, values, command order, and file context. A re-rejection changes no workspace file and makes every earlier handle stale. Use a complete script through functions.hpatch for every other correction.\n\n"
	if got := RecoveryGuidance(""); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}
