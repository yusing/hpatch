package codexinstructions

import (
	"strings"
	"testing"
)

func TestHPatchToolHelpComesFromCentralInstructions(t *testing.T) {
	help := HPatchToolHelp()
	if !strings.HasPrefix(help, "HPATCH/2 applies one complete target-bearing edit script atomically.") {
		t.Fatalf("HPatchToolHelp() starts with %q", help)
	}
	if strings.Contains(help, "## File reading") {
		t.Fatalf("HPatchToolHelp() includes the next instruction section:\n%s", help)
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

func TestRecoveryGuidanceRendersDynamicReferences(t *testing.T) {
	const references = "Current rejected-script command manifest (complete):\n"
	const want = "\nRepair the retained rejected script with the smallest handle-local operations. Submit every known independent correction in one atomic recovery payload, one operation per line. A re-rejection changes no workspace file: it retains successful corrections only in a new rejected-script baseline, emits that baseline's authoritative manifest, and makes every earlier handle stale. Resubmit a complete script through functions.hpatch only when the diagnostic requires a new script or transaction.\n\n" + references
	if got := RecoveryGuidance(references); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}

func TestRecoveryGuidanceWithoutReferences(t *testing.T) {
	const want = "\nRepair the retained rejected script with the smallest handle-local operations. Submit every known independent correction in one atomic recovery payload, one operation per line. A re-rejection changes no workspace file: it retains successful corrections only in a new rejected-script baseline, emits that baseline's authoritative manifest, and makes every earlier handle stale. Resubmit a complete script through functions.hpatch only when the diagnostic requires a new script or transaction.\n\n"
	if got := RecoveryGuidance(""); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}
