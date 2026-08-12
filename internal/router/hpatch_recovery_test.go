package router

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

func TestIsHPatchRecoveryCandidateUsesFirstNonblankCommand(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "replace", payload: "type 2:abcd \"fixed\"", want: true},
		{name: "insert before", payload: "\n\ntype- 2:abcd \"line\\n\"", want: true},
		{name: "insert after", payload: "type+ 2:abcd \"line\\n\"", want: true},
		{name: "malformed recovery candidate", payload: "type \"initializer\"", want: true},
		{name: "mixed recovery candidate", payload: "type 2:abcd \"fixed\"\nin file.go", want: true},
		{name: "complete script", payload: "in file.go\ntype 2:abcd \"fixed\""},
		{name: "old replacement", payload: "2: type 2:abcd \"fixed\""},
		{name: "old accept", payload: "2: accept"},
		{name: "old value row", payload: "2.1: \"fixed\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isHPatchRecoveryCandidate(test.payload); got != test.want {
				t.Fatalf("isHPatchRecoveryCandidate() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHPatchRecoveryGuidanceUsesOrdinaryScriptRows(t *testing.T) {
	script := "in file.go\n" +
		"type 1:ffff <<PATCH\n" +
		"package main\n" +
		"func main() {\n" +
		")\n" +
		"}\n" +
		"PATCH\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", ValueLine: 3,
	}}
	guidance := hpatchRecoveryGuidance(script, rejections)
	for _, want := range []string{
		"Rejected script `LINE:HASH` rows:",
		hpatch.TextReferences(script, 2),
		hpatch.TextReferences(script, 5),
		hpatch.TextReferences(script, 7),
		"Use hpatch without `in` to patch the rejected script.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance does not contain %q:\n%s", want, guidance)
		}
	}
	if strings.Contains(guidance, "INDEX:") || strings.Contains(guidance, "accept") {
		t.Fatalf("guidance retains obsolete indexed recovery language:\n%s", guidance)
	}
}

func TestHPatchRecoveryRowsExposeMalformedFrameEnd(t *testing.T) {
	for _, script := range []string{
		"in file.go\ntype 1:ffff <<PATCH\nbad\n",
		"in file.go\ntype 1:ffff <<PATCH\nbad",
	} {
		rejections := []hpatch.HostRejection{{
			Command: 2, SourceLine: 2, Operation: "type",
		}}
		got := hpatch.TextReferences(script, hpatchRecoveryRows(script, rejections)...)
		for _, row := range []int{2, 3} {
			if want := hpatch.TextReferences(script, row); !strings.Contains(got, want) {
				t.Fatalf("references do not contain row %d:\n%s", row, got)
			}
		}
	}
}

func TestHPatchRecoveryRowsMapStandaloneCRToOrdinaryRows(t *testing.T) {
	script := "in file.go\n" +
		"type 1:ffff <<PATCH\n" +
		"package main\rvar broken =\n" +
		"PATCH\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", ValueLine: 1,
	}}
	got := hpatch.TextReferences(script, hpatchRecoveryRows(script, rejections)...)
	for _, row := range []int{2, 3, 4, 5} {
		if want := hpatch.TextReferences(script, row); !strings.Contains(got, want) {
			t.Fatalf("references do not contain ordinary row %d:\n%s", row, got)
		}
	}
}

// A rejection the conversation no longer shows must not stay recoverable. The
// model cannot see the rejected script after its turn is discarded, so a bare
// recovery has nothing to edit and must ask for a complete script instead.
func TestTruncatedRejectionIsNotRecoverable(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	rejected := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
		translationError: "rejected", evaluatorRejected: true, carrierName: "exec",
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-R": rejected}); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.recoverableHistory("session"); err != nil {
		t.Fatalf("rejection was not recoverable before truncation: %v", err)
	}

	// The next turn's input no longer carries call-R.
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "message", "role": "user", "content": "edited"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if _, ok := proxy.history("session", "call-R"); ok {
		t.Fatal("discarded call stayed retained")
	}
	_, err = proxy.recoverableHistory("session")
	if err == nil || !strings.Contains(err.Error(), "no rejected hpatch script to recover") {
		t.Fatalf("recovery after truncation = %v", err)
	}
}

// Truncation only removes a suffix, so a call the input still shows must keep
// replaying to the model's own hpatch script even when a newer retained call is
// pruned alongside it. The pruned bytes must leave both budgets exactly.
func TestReconcilePrunesOnlyCallsNewerThanTheInput(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	survivor := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, patch: testTranslatedPatch,
		carrierName: "exec", report: testHPatchReport, sequence: 1,
	}
	discarded := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
		translationError: "rejected", evaluatorRejected: true, carrierName: "exec", sequence: 2,
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-old": survivor, "call-new": discarded,
	}); err != nil {
		t.Fatal(err)
	}
	retained, ok := proxy.history("session", "call-new")
	if !ok {
		t.Fatal("newer call was not retained")
	}
	prunedBytes := retained.bytes
	sessionBytes := proxy.sessions["session"].bytes
	globalBytes := proxy.historyBytes

	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call-old", "input": survivor.carrierInput()},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call-old", "output": testHPatchReport},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}

	if _, ok := proxy.history("session", "call-new"); ok {
		t.Fatal("call newer than the input survived the prune")
	}
	if _, ok := proxy.history("session", "call-old"); !ok {
		t.Fatal("call the input still shows was pruned")
	}
	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if name := jsonString(replayed[0], "name"); name != hpatchToolName {
		t.Fatalf("surviving carrier replayed tool name %q", name)
	}
	if input := jsonString(replayed[0], "input"); input != testHPatchScript {
		t.Fatalf("surviving carrier replayed input %q", input)
	}
	if got := proxy.sessions["session"].bytes; got != sessionBytes-prunedBytes {
		t.Fatalf("session bytes = %d, want %d", got, sessionBytes-prunedBytes)
	}
	if got := proxy.historyBytes; got != globalBytes-prunedBytes {
		t.Fatalf("global history bytes = %d, want %d", got, globalBytes-prunedBytes)
	}
}

// A second in-flight turn commits its calls only at response completion, so
// this turn's input cannot show them and must not prune them.
func TestReconcileSkipsPruneWhileAnotherTurnIsActive(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	rejected := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
		translationError: "rejected", evaluatorRejected: true, carrierName: "exec",
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-R": rejected}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := proxy.activateSession("session"); err != nil {
			t.Fatal(err)
		}
		defer proxy.deactivateSession("session")
	}
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "message", "role": "user", "content": "concurrent"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if _, ok := proxy.history("session", "call-R"); !ok {
		t.Fatal("concurrent turn's retained call was pruned")
	}
}

// Pruning must not renumber the session. A call retained after a prune has to
// outrank every survivor, or recovery would resolve to a stale rejection whose
// script the model already replaced.
func TestPruneKeepsRetainedSequenceMonotonic(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	discarded := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
		translationError: "discarded rejection", evaluatorRejected: true, carrierName: "exec",
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-gone": discarded}); err != nil {
		t.Fatal(err)
	}
	pruned := mustHPatchHistory(t, proxy, "call-gone").sequence

	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "message", "role": "user", "content": "edited"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if bytes := proxy.sessions["session"].bytes; bytes != 0 || proxy.historyBytes != 0 {
		t.Fatalf("bytes after full prune = session %d, global %d, want 0 and 0", bytes, proxy.historyBytes)
	}

	fresh := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
		translationError: "fresh rejection", evaluatorRejected: true, carrierName: "exec",
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-fresh": fresh}); err != nil {
		t.Fatal(err)
	}
	if sequence := mustHPatchHistory(t, proxy, "call-fresh").sequence; sequence <= pruned {
		t.Fatalf("sequence after prune = %d, want above the pruned %d", sequence, pruned)
	}
	base, err := proxy.recoverableHistory("session")
	if err != nil || base.translationError != "fresh rejection" {
		t.Fatalf("recovery base = %q, error %v", base.translationError, err)
	}
	if bytes := proxy.sessions["session"].bytes; bytes != mustHPatchHistory(t, proxy, "call-fresh").bytes {
		t.Fatalf("session bytes = %d, want only the surviving call's bytes", bytes)
	}
}

// Discarded calls must release their call budget, not just their bytes.
// Otherwise they crowd out a surviving call, and its carrier replays as the
// generated apply_patch envelope instead of the model's own hpatch script.
func TestPruneReleasesCallBudgetForSurvivingReplay(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	survivor := hpatchHistory{
		toolName: hpatchToolName, script: testHPatchScript, patch: testTranslatedPatch,
		carrierName: "exec", report: testHPatchReport,
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-survivor": survivor}); err != nil {
		t.Fatal(err)
	}
	filler := make(map[string]hpatchHistory, maxSessionTurns-1)
	for index := range maxSessionTurns - 1 {
		filler["call-discarded-"+strconv.Itoa(index)] = hpatchHistory{
			toolName: hpatchToolName, script: testHPatchScript, evaluated: testHPatchScript,
			translationError: "discarded", evaluatorRejected: true, carrierName: "exec",
			sequence: uint64(index + 1),
		}
	}
	if err := proxy.rememberBatch("session", filler); err != nil {
		t.Fatal(err)
	}
	if calls := len(proxy.sessions["session"].calls); calls != maxSessionTurns {
		t.Fatalf("retained calls = %d, want the session at capacity %d", calls, maxSessionTurns)
	}

	// Only the oldest call survives the edit, so every filler call is newer.
	surviving := map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "call-survivor",
		"input": survivor.carrierInput(),
	}
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{surviving}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if calls := len(proxy.sessions["session"].calls); calls != 1 {
		t.Fatalf("retained calls after prune = %d, want 1", calls)
	}

	// Released capacity means the next turn's call cannot evict the survivor.
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-next": {
		toolName: hpatchToolName, script: testHPatchScript, carrierName: "exec", report: testHPatchReport,
	}}); err != nil {
		t.Fatal(err)
	}
	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{surviving}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&replay, "session"); err != nil {
		t.Fatal(err)
	}
	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if name := jsonString(replayed[0], "name"); name != hpatchToolName {
		t.Fatalf("surviving carrier replayed tool name %q, want %q", name, hpatchToolName)
	}
	if input := jsonString(replayed[0], "input"); input != testHPatchScript {
		t.Fatalf("surviving carrier replayed input %q", input)
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
