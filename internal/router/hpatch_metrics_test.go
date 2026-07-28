package router

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestCalculateHPatchMetricRecordUsesExactCallerPayloads(t *testing.T) {
	invocation := json.RawMessage(`{"commands":[{"invocations":1,"errors":0}]}`)
	patch := "*** Begin Patch\n*** Update File: /workspace/calc.go\n@@\n-old\n+new\n*** End Patch\n"
	inputs := hpatchMetricInputs{
		invocation:         invocation,
		emittedScript:      "workspace_id ws\n2: sel 2 9:13\n",
		report:             "in calc.go 2:9\n2 return new\n",
		carriedMetadata:    []string{"workspace_id ws\n", "workspace_id prior\n"},
		sessionID:          "session",
		definition:         "hpatch definition\n\n",
		baselineDefinition: "apply_patch definition\n",
		successful:         true,
	}
	inputs.carrier = hpatchApplyExecInput(patch, inputs.report)
	record, err := calculateHPatchMetricRecord(inputs)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	count := func(value string) uint64 {
		t.Helper()
		valueCount, countErr := codec.Count(value)
		if countErr != nil {
			t.Fatal(countErr)
		}
		return uint64(valueCount)
	}
	if got, want := record.HPatchTokens, count("functions.hpatch\n"+inputs.emittedScript); got != want {
		t.Fatalf("hpatch tokens = %d, want %d", got, want)
	}
	applyPatchPayload := "functions.exec\n" + inputs.carrier
	if got, want := record.ApplyPatchTokens, count(applyPatchPayload); got != want {
		t.Fatalf("apply_patch tokens = %d, want %d", got, want)
	}
	if got, want := record.ReportInputTokens, count(inputs.report); got != want {
		t.Fatalf("report tokens = %d, want %d", got, want)
	}
	if got, want := record.MetadataInputTokens, count(inputs.carriedMetadata[0])+count(inputs.carriedMetadata[1]); got != want {
		t.Fatalf("metadata tokens = %d, want %d", got, want)
	}
	if record.SessionID != inputs.sessionID || record.DefinitionRequests != 1 {
		t.Fatalf("definition attribution = %+v", record)
	}
	if got, want := record.DefinitionInputTokens, count("hpatch definition"); got != want {
		t.Fatalf("definition tokens = %d, want %d", got, want)
	}
	if got, want := record.RemovedDefinitionInputTokens, count("apply_patch definition"); got != want {
		t.Fatalf("removed definition tokens = %d, want %d", got, want)
	}
	if string(record.Invocation) != string(invocation) || record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 || record.DiagnosticInputTokens != 0 {
		t.Fatalf("successful record = %+v", record)
	}
}

func TestCalculateHPatchMetricRecordUsesExactFailureDiagnostic(t *testing.T) {
	inputs := hpatchMetricInputs{
		emittedScript: "2: sel 2 9:13\n",
		diagnostic:    "hpatch: command 2 rejected\nrepair context\n" + hpatchCorrectionHint,
	}
	inputs.carrier = hpatchDiagnosticExecInput(inputs.diagnostic)
	record, err := calculateHPatchMetricRecord(inputs)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	count := func(value string) uint64 {
		valueCount, countErr := codec.Count(value)
		if countErr != nil {
			t.Fatal(countErr)
		}
		return uint64(valueCount)
	}
	if got, want := record.IneffectiveHPatchTokens, count("functions.hpatch\n"+inputs.emittedScript); got != want {
		t.Fatalf("ineffective hpatch tokens = %d, want %d", got, want)
	}
	failedPayload := "functions.exec\n" + inputs.carrier
	if got, want := record.FailedApplyPatchTokens, count(failedPayload); got != want {
		t.Fatalf("rejected carrier tokens = %d, want %d", got, want)
	}
	if got, want := record.DiagnosticInputTokens, count(inputs.diagnostic); got != want {
		t.Fatalf("diagnostic tokens = %d, want %d", got, want)
	}
	if string(record.Invocation) != "{}" || record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.ReportInputTokens != 0 {
		t.Fatalf("failure record = %+v", record)
	}
}

func TestHPatchMetricsUseFinalRebasedCarrier(t *testing.T) {
	var recorded hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			return []byte(testTranslatedPatch), nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			recorded = record
			return nil
		},
	}
	first := t.TempDir()
	second := t.TempDir()
	transform, _, _ := newHPatchTestTransformForWorkspaces(t, translator, first, second)
	workspace := transform.workspaces[1]
	input := "workspace_id " + workspace.id + "\n" + testHPatchScript
	history, err := transform.translate("call-1", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(workspace.canonical, "created.txt"); !strings.Contains(history.patch, want) {
		t.Fatalf("rebased patch %q does not contain %q", history.patch, want)
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	count, err := codec.Count("functions.exec\n" + history.carrierInput())
	if err != nil {
		t.Fatal(err)
	}
	if recorded.ApplyPatchTokens != uint64(count) {
		t.Fatalf("apply_patch tokens = %d, want %d from final carrier", recorded.ApplyPatchTokens, count)
	}
}

func TestFinishRecordsDefinitionOverheadWithoutHPatchCall(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("unexpected hpatch translation")
			return nil, nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	if err := transform.Finish(false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("metric records = %d, want 1", len(records))
	}
	record := records[0]
	if record.DefinitionRequests != 1 || record.DefinitionInputTokens == 0 || record.RemovedDefinitionInputTokens == 0 || record.MetadataInputTokens == 0 {
		t.Fatalf("request overhead = %+v", record)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 {
		t.Fatalf("overhead-only record contains call tokens: %+v", record)
	}
}
