package router

import (
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

func TestHPatchRecoveryGuidanceListsOnlyRowStaleTargetCommands(t *testing.T) {
	script := "in file.go\n" +
		"type 13:974b..16:d10b <<PATCH\n" +
		"replacement\n" +
		"broken\n" +
		"PATCH\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", Target: "range", Reason: "row-stale",
	}}
	guidance := hpatchRecoveryGuidance(script, rejections, true)
	command := recoveryCommands(script)[1]
	for _, want := range []string{
		"Rejected target commands:",
		command.handle,
		"This re-rejection changed no workspace file",
		"C... CURRENT_TARGET",
		"preserves every operation and value",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance does not contain %q:\n%s", want, guidance)
		}
	}
	for _, absent := range []string{
		recoveryCommands(script)[0].handle,
		"replacement",
		"broken",
	} {
		if strings.Contains(guidance, absent) {
			t.Fatalf("guidance contains unrelated %q:\n%s", absent, guidance)
		}
	}
}

func TestHPatchRecoveryGuidanceRequiresCompleteScriptForNonTargetFailure(t *testing.T) {
	script := "in file.go\n" + `type 1:abcd "sensitive replacement" trailing` + "\n"
	guidance := hpatchRecoveryGuidance(
		script,
		[]hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type", Reason: "language-syntax"}},
		false,
	)
	if strings.Contains(guidance, "sensitive replacement") ||
		!strings.Contains(guidance, "requires one complete corrected HPATCH/2 script") ||
		!strings.Contains(guidance, "hpatch_recover changes stale targets only") {
		t.Fatalf("non-target guidance = %q", guidance)
	}
}

func mustHPatchHistory(t *testing.T, proxy *hpatchProxy, callID string) hpatchHistory {
	t.Helper()
	history, ok := proxy.history("session", callID)
	if !ok {
		t.Fatalf("call %q is not retained", callID)
	}
	return history
}
