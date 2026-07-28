package router

import (
	"context"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestCalculateHPatchMetricRecordUsesExactCallerPayloads(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: /workspace/calc.go\n@@\n-old\n+new\n*** End Patch\n"
	inputs := hpatchMetricInputs{
		emittedScript:      "2: sel 2 9:13\n",
		report:             "in calc.go 2:9\n2 return new\n",
		patch:              patch,
		sessionID:          "session",
		definition:         "hpatch definition\n\n",
		baselineDefinition: "apply_patch definition\n",
		successful:         true,
	}
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
	applyPatchPayload := applyPatchMetricPayload(patch)
	if got, want := record.ApplyPatchTokens, count(applyPatchPayload); got != want {
		t.Fatalf("apply_patch tokens = %d, want %d", got, want)
	}
	if got, want := record.ReportInputTokens, count(inputs.report); got != want {
		t.Fatalf("report tokens = %d, want %d", got, want)
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
	if record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 || record.DiagnosticInputTokens != 0 {
		t.Fatalf("successful record = %+v", record)
	}
}

func TestCalculateHPatchMetricRecordUsesEmptyFailureBaseline(t *testing.T) {
	inputs := hpatchMetricInputs{
		emittedScript: "2: sel 2 9:13\n",
		diagnostic:    "hpatch: command 2 rejected\nrepair context\n" + hpatchCorrectionHint,
	}
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
	failedPayload := applyPatchMetricPayload(failedApplyPatch)
	if got, want := record.FailedApplyPatchTokens, count(failedPayload); got != want {
		t.Fatalf("failed apply_patch tokens = %d, want %d", got, want)
	}
	if got, want := record.DiagnosticInputTokens, count(inputs.diagnostic); got != want {
		t.Fatalf("diagnostic tokens = %d, want %d", got, want)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.ReportInputTokens != 0 {
		t.Fatalf("failure record = %+v", record)
	}
}

func TestHPatchRequestDefinitionAccountingIsClaimedOnce(t *testing.T) {
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
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	if _, err := transform.translate("call-1", testHPatchScript, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transform.translate("call-2", testHPatchScript, nil); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("metric records = %d, want 2", len(records))
	}
	if records[0].DefinitionRequests != 1 || records[0].SessionID != "session-1" || records[0].DefinitionInputTokens == 0 || records[0].RemovedDefinitionInputTokens == 0 {
		t.Fatalf("first request accounting = %+v", records[0])
	}
	if records[1].DefinitionRequests != 0 || records[1].SessionID != "" || records[1].DefinitionInputTokens != 0 || records[1].RemovedDefinitionInputTokens != 0 {
		t.Fatalf("second call repeated request accounting = %+v", records[1])
	}
	if records[0].ApplyPatchTokens == 0 || records[1].ApplyPatchTokens == 0 {
		t.Fatalf("per-call patch metrics = %+v", records)
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
	if record.DefinitionRequests != 1 || record.DefinitionInputTokens == 0 || record.RemovedDefinitionInputTokens == 0 {
		t.Fatalf("request overhead = %+v", record)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 {
		t.Fatalf("overhead-only record contains call tokens: %+v", record)
	}
}
