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
	const references = "2:abcd type 1:ffff \"fixed\"\n"
	const want = "\n" + references
	if got := RecoveryGuidance(references); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}

func TestRecoveryGuidanceWithoutReferences(t *testing.T) {
	const want = "\n"
	if got := RecoveryGuidance(""); got != want {
		t.Fatalf("RecoveryGuidance() = %q, want %q", got, want)
	}
}
