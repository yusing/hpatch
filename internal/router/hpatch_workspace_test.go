package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHPatchWorkspaceDirectiveSelectsDeclaredWorkspaceAndRebasesCarrier(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	var selected routingWorkspace
	var seen string
	translator := hpatchTranslatorFunc(func(_ context.Context, workspace routingWorkspace, script string) ([]byte, error) {
		selected = workspace
		seen = script
		return []byte(testTranslatedPatch), nil
	})
	transform, _, request := newHPatchTestTransformForWorkspaces(t, translator, first, second)
	target := transform.workspaces[1]
	input := "workspace_id " + target.id + "\n" + testHPatchScript

	history, err := transform.translate("call-1", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.canonical != target.canonical || seen != testHPatchScript {
		t.Fatalf("translation = workspace %q, script %q", selected.canonical, seen)
	}
	wantHeader := "*** Add File: " + filepath.Join(target.canonical, "created.txt") + "\n"
	if !strings.Contains(history.patch, wantHeader) {
		t.Fatalf("rebased patch = %q, want header %q", history.patch, wantHeader)
	}
	if history.workspaceID != target.id || history.evaluated != testHPatchScript {
		t.Fatalf("history = %+v", history)
	}
	if strings.Contains(transform.toolDescription, target.id) || strings.Contains(transform.toolDescription, target.declared) {
		t.Fatalf("tool description contains request-specific workspace data: %q", transform.toolDescription)
	}
	if !strings.Contains(string(request.fields["tools"]), "workspace_id WORKSPACE_ID") {
		t.Fatalf("request tool definition lacks stable workspace protocol: %s", request.fields["tools"])
	}
	if !strings.Contains(string(request.fields["input"]), target.id) || !strings.Contains(string(request.fields["input"]), target.declared) {
		t.Fatalf("request input lacks workspace metadata: %s", request.fields["input"])
	}
}

func TestHPatchMultipleWorkspacesRequireKnownWorkspaceID(t *testing.T) {
	translator := hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		t.Fatal("invalid workspace selection reached translator")
		return nil, nil
	})
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir(), t.TempDir())
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing", input: testHPatchScript, want: "requires workspace_id"},
		{name: "unknown", input: "workspace_id absent\n" + testHPatchScript, want: `unknown workspace_id "absent"`},
		{name: "malformed", input: "workspace_id\n" + testHPatchScript, want: "must be followed by one workspace ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			history, err := transform.translate("call-"+test.name, test.input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !history.unevaluated || !strings.Contains(history.translationError, test.want) {
				t.Fatalf("history = %+v, want diagnostic %q", history, test.want)
			}
		})
	}
}

func TestHPatchCorrectionRetainsRejectedWorkspace(t *testing.T) {
	type call struct {
		workspace string
		script    string
	}
	var calls []call
	translator := hpatchTranslatorFunc(func(_ context.Context, workspace routingWorkspace, script string) ([]byte, error) {
		calls = append(calls, call{workspace: workspace.id, script: script})
		return nil, hpatchCommandError{cause: errors.New("exit status 1"), diagnostic: "hpatch: rejected\n"}
	})
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir(), t.TempDir())
	target := transform.workspaces[1]
	base := "in calc.go\nsel 2 9:14\ntype \"a - b\"\n"
	if _, err := transform.translate("call-1", "workspace_id "+target.id+"\n"+base, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transform.translate("call-2", "2: sel 2 9:13\n", nil); err != nil {
		t.Fatal(err)
	}
	wantCorrected := "in calc.go\nsel 2 9:13\ntype \"a - b\"\n"
	if len(calls) != 2 || calls[0].workspace != target.id || calls[1] != (call{workspace: target.id, script: wantCorrected}) {
		t.Fatalf("translator calls = %+v", calls)
	}

	other := transform.workspaces[0]
	history, err := transform.translate("call-3", "workspace_id "+other.id+"\n2: sel 2 9:12\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !history.unevaluated || !strings.Contains(history.translationError, "must use the rejected script's workspace_id") || len(calls) != 2 {
		t.Fatalf("cross-workspace correction = %+v, calls %+v", history, calls)
	}
}

func TestRebaseHPatchPatchRewritesOnlyFileHeaders(t *testing.T) {
	root := t.TempDir()
	patch := "*** Begin Patch\n*** Update File: old.txt\n*** Move to: moved.txt\n@@\n-*** Add File: context.txt\n+changed\n*** Delete File: obsolete.txt\n*** End Patch\n"
	rebased, err := rebaseHPatchPatch([]byte(patch), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"old.txt", "moved.txt", "obsolete.txt"} {
		if !strings.Contains(string(rebased), filepath.Join(root, path)) {
			t.Fatalf("rebased patch lacks %q: %q", path, rebased)
		}
	}
	if !strings.Contains(string(rebased), "-*** Add File: context.txt\n") {
		t.Fatalf("hunk content was rewritten: %q", rebased)
	}
	if _, err := rebaseHPatchPatch([]byte("*** Begin Patch\n*** Update File: ../escape\n*** End Patch\n"), root); err == nil {
		t.Fatal("non-local translated path was accepted")
	}
}

func TestRoutingWorkspaceIDsAreStableAcrossRequests(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(base, "a-other")
	target := filepath.Join(base, "z-target")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	one := usableRoutingWorkspaces(map[string]json.RawMessage{target: nil})
	many := usableRoutingWorkspaces(map[string]json.RawMessage{target: nil, other: nil})
	defer closeRoutingWorkspaces(one)
	defer closeRoutingWorkspaces(many)
	if len(one) != 1 || len(many) != 2 {
		t.Fatalf("usable workspaces = %d and %d", len(one), len(many))
	}
	var targetID string
	for _, workspace := range many {
		if workspace.canonical == one[0].canonical {
			targetID = workspace.id
		}
	}
	if targetID == "" || targetID != one[0].id {
		t.Fatalf("workspace ID changed from %q to %q when the set changed", one[0].id, targetID)
	}
	instructions := hpatchWorkspaceInstructions(one)
	if !strings.Contains(instructions, "workspace_id "+one[0].id) {
		t.Fatalf("single-workspace instructions omit its ID: %q", instructions)
	}
}

func TestHPatchSingleExplicitWorkspaceRebasesCarrier(t *testing.T) {
	translator := hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return []byte(testTranslatedPatch), nil
	})
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir())
	workspace := transform.workspaces[0]
	history, err := transform.translate("call-1", "workspace_id "+workspace.id+"\n"+testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "*** Add File: " + filepath.Join(workspace.canonical, "created.txt")
	if !strings.Contains(history.patch, want) {
		t.Fatalf("explicit single-workspace carrier was not rebased: %q", history.patch)
	}
}

func TestHPatchWorkspaceReplayRestoresExactFramingAndAccounting(t *testing.T) {
	translator := hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return []byte(testTranslatedPatch), nil
	})
	transform, proxy, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir(), t.TempDir())
	workspace := transform.workspaces[1]
	framed := "workspace_id " + workspace.id + "\n" + testHPatchScript
	history, err := transform.translate("call-1", framed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.rememberBatch("session-1", map[string]hpatchHistory{"call-1": history}); err != nil {
		t.Fatal(err)
	}
	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{map[string]any{
		"type": "custom_tool_call", "name": history.carrierName, "call_id": "call-1", "input": history.carrierInput(),
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	carriedMetadata, err := proxy.restoreInputPrefixWithMetadata(&replay, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	wantMetadata := "workspace_id " + workspace.id + "\n"
	if len(carriedMetadata) != 1 || carriedMetadata[0] != wantMetadata {
		t.Fatalf("carried metadata = %q, want %q", carriedMetadata, wantMetadata)
	}
	if !bytes.Contains(replay.fields["input"], []byte(jsonQuoted(framed))) {
		t.Fatalf("replay did not restore exact framed input: %s", replay.fields["input"])
	}
	retained, _ := proxy.history("session-1", "call-1")
	encodedItem, err := json.Marshal(history.upstreamItem)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := len("session-1") + len("call-1") + len(history.script) + len(history.workspaceID) + len(history.evaluated) + len(history.patch) + len(history.carrierName) + len(history.report) + len(history.translationError) + len(encodedItem)
	if retained.bytes != wantBytes {
		t.Fatalf("history bytes = %d, want %d including workspace ID", retained.bytes, wantBytes)
	}
}

func TestHPatchRequestInputAccountingIsClaimedOnce(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			return []byte(testTranslatedPatch), nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir(), t.TempDir())
	transform.carriedMetadata = []string{"workspace_id retained\n"}
	workspace := transform.workspaces[0]
	input := "workspace_id " + workspace.id + "\n" + testHPatchScript
	if _, err := transform.translate("call-1", input, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transform.translate("call-2", input, nil); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("metric records = %d, want 2", len(records))
	}
	if records[0].DefinitionRequests != 1 || records[0].SessionID != "session-1" || records[0].DefinitionInputTokens == 0 || records[0].RemovedDefinitionInputTokens == 0 || records[0].MetadataInputTokens == 0 {
		t.Fatalf("first request accounting = %+v", records[0])
	}
	if records[1].DefinitionRequests != 0 || records[1].SessionID != "" || records[1].DefinitionInputTokens != 0 || records[1].RemovedDefinitionInputTokens != 0 || records[1].MetadataInputTokens != 0 {
		t.Fatalf("second call repeated request accounting = %+v", records[1])
	}
	if records[0].ApplyPatchTokens == 0 || records[1].ApplyPatchTokens == 0 {
		t.Fatalf("per-call patch metrics = %+v", records)
	}
}

func TestHPatchUnavailableCorrectionWorkspaceIsRejected(t *testing.T) {
	calls := 0
	transform, proxy, _ := newHPatchTestTransformForWorkspaces(t, testTranslator(t, &calls), t.TempDir())
	if err := proxy.rememberBatch("session-1", map[string]hpatchHistory{"call-old": {
		script: testHPatchScript, workspaceID: "workspace-unavailable", patch: "", carrierName: "exec",
		translationError: "rejected", sequence: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	history, err := transform.translate("call-new", "1: new repaired.txt\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !history.unevaluated || !strings.Contains(history.translationError, "workspace is no longer available") || calls != 0 {
		t.Fatalf("unavailable-workspace correction = %+v, translator calls %d", history, calls)
	}
}

func TestHPatchStreamingWorkspaceCarrierIsRebased(t *testing.T) {
	translator := hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return []byte(testTranslatedPatch), nil
	})
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir(), t.TempDir())
	workspace := transform.workspaces[1]
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""
	visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
	if err != nil || visible != nil {
		t.Fatalf("buffered added = %q, error %v", visible, err)
	}
	input := "workspace_id " + workspace.id + "\n" + testHPatchScript
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": input}))
	want := filepath.Join(workspace.canonical, "created.txt")
	if err != nil || len(visible) != 2 || !bytes.Contains(bytes.Join(visible, nil), []byte(want)) {
		t.Fatalf("streamed carrier = %q, error %v; want absolute path %q", visible, err, want)
	}
}

func TestHPatchRebasedCarrierStillHonorsPatchLimit(t *testing.T) {
	prefix := "*** Begin Patch\n*** Add File: a\n"
	suffix := "\n*** End Patch\n"
	patch := prefix + strings.Repeat("x", maxHPatchPatchBytes-len(prefix)-len(suffix)) + suffix
	translator := hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return []byte(patch), nil
	})
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, t.TempDir())
	workspace := transform.workspaces[0]
	_, err := transform.translate("call-1", "workspace_id "+workspace.id+"\n"+testHPatchScript, nil)
	if err == nil || !strings.Contains(err.Error(), "translation exceeds") {
		t.Fatalf("oversized rebased carrier error = %v", err)
	}
}
