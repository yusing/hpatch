package router

import (
	"context"
	"strings"
	"testing"
)

func TestHPatchRecoveryRejectsDifferentWorktree(t *testing.T) {
	calls := 0
	transform, proxy, _, workspace := newHPatchTestTransform(t, testTranslator(t, &calls))
	if err := proxy.rememberBatch(transform.historySessionID, map[string]hpatchHistory{"call-old": {
		toolName: hpatchToolName, script: testHPatchScript, root: workspace + "-other", carrierName: "exec",
		translationError: "rejected", sequence: 1,
		evaluatorRejected: true,
	}}); err != nil {
		t.Fatal(err)
	}
	history, err := transform.translate("call-new", `type 2:ffff "repaired"`+"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !history.unevaluated || !strings.Contains(history.translationError, "different worktree") || calls != 0 {
		t.Fatalf("cross-worktree recovery = %+v, translator calls %d", history, calls)
	}
}

func TestHPatchTranslationStillHonorsPatchLimit(t *testing.T) {
	prefix := "*** Begin Patch\n*** Add File: a\n"
	suffix := "\n*** End Patch\n"
	patch := prefix + strings.Repeat("x", maxHPatchPatchBytes-len(prefix)-len(suffix)+1) + suffix
	translator := hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
		return []byte(patch), nil
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	_, err := transform.translate("call-1", testHPatchScript, nil)
	if err == nil || !strings.Contains(err.Error(), "translation exceeds") {
		t.Fatalf("oversized carrier error = %v", err)
	}
}
