package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
	"github.com/yusing/hpatch"
)

func TestCalculateHPatchMetricRecordUsesExactCallerPayloads(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: /workspace/calc.go\n@@\n-old\n+new\n*** End Patch\n"
	inputs := hpatchMetricInputs{
		attempt:            hpatch.AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call", Attempt: 2, Correction: true},
		emittedScript:      "2: type 12:9645..18:4b7b \"replacement\"\n",
		report:             "in calc.go 2:9\n9645: return new\n",
		patch:              patch,
		sessionID:          "session",
		definition:         "hpatch definition\n\n",
		baselineDefinition: "apply_patch definition\n",
		successful:         true,
		correction: hpatchCorrectionStats{
			scope: "value-row", valueRowOperations: 1, baseValueRows: 24,
			baseCommands: []string{
				"type 1:a793..24:b1e9 <<PATCH\nlarge body\nPATCH\n",
				"type 30:cafe..31:beef <<PATCH\nsecond body\nPATCH\n",
			},
		},
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
	if record.Attempt != inputs.attempt {
		t.Fatalf("attempt metadata = %+v, want %+v", record.Attempt, inputs.attempt)
	}
	if record.correctionScope != "value-row" || record.valueRowOperations != 1 || record.baseValueRows != 24 {
		t.Fatalf("correction telemetry = %+v", record)
	}
	if got, want := record.baseCommandTokens, count(inputs.correction.baseCommands[0])+count(inputs.correction.baseCommands[1]); got != want {
		t.Fatalf("base command tokens = %d, want %d", got, want)
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
		emittedScript: "2: type 12:9645..18:4b7b \"replacement\"\n",
		diagnostic:    "hpatch: command 2 rejected\nrepair context\n" + hpatchCorrectionHint,
		rejections: []hpatch.HostRejection{{
			Command: 2, SourceLine: 2, Operation: "type", Target: "range",
			Reason: "language-syntax", Path: "calc.go", GeneratedLine: 8, GeneratedColumn: 3,
		}},
		sessionID: "session-without-definition-accounting",
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
	if record.SessionID != inputs.sessionID {
		t.Fatalf("session ID = %q, want %q", record.SessionID, inputs.sessionID)
	}
	if !reflect.DeepEqual(record.Rejections, inputs.rejections) {
		t.Fatalf("rejections = %#v, want %#v", record.Rejections, inputs.rejections)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.ReportInputTokens != 0 {
		t.Fatalf("failure record = %+v", record)
	}
}

func TestHPatchMetricDefinitionMatchesInstalledGrammarTools(t *testing.T) {
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
	transform, _, request, _ := newHPatchTestTransform(t, translator)
	if err := transform.Finish(false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("metric records = %d, want 1", len(records))
	}

	var tools []json.RawMessage
	if err := json.Unmarshal(request.fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	installed := make([]json.RawMessage, 0, 2)
	for _, raw := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if name := jsonString(tool, "name"); name == hpatchToolName || name == hreadToolName {
			installed = append(installed, raw)
		}
	}
	encoded, err := json.Marshal(installed)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	want, err := codec.Count(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if got := records[0].DefinitionInputTokens; got != uint64(want) {
		t.Fatalf("definition tokens = %d, want exact installed definitions %d", got, want)
	}
}

func TestHPatchMetricPersistenceFailuresDoNotChangeToolResults(t *testing.T) {
	metricErr := errors.New("metrics unavailable")
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			return []byte(testTranslatedPatch), nil
		},
		record: func(context.Context, hpatchMetricRecord) error {
			return metricErr
		},
	}

	t.Run("successful JSON call", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, translator)
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": []any{testHPatchItem()},
		}))
		if err != nil || !bytes.Contains(visible, []byte("*** Begin Patch")) {
			t.Fatalf("visible = %q, error %v", visible, err)
		}
	})

	t.Run("rejected call", func(t *testing.T) {
		rejected := translator
		rejected.translate = func(context.Context, routingWorkspace, string) ([]byte, error) {
			return nil, errors.New("rejected")
		}
		transform, _, _, _ := newHPatchTestTransform(t, rejected)
		history, err := transform.translate("call-rejected", testHPatchScript, nil)
		if err != nil || !strings.Contains(history.translationError, "rejected") {
			t.Fatalf("history = %+v, error %v", history, err)
		}
	})

	t.Run("hread call", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, translator)
		history, err := transform.translateHRead("call-read", `"file.txt"`, nil)
		if err != nil || history.carrierInput() != hreadExecInput(`"file.txt"`) {
			t.Fatalf("history = %+v, error %v", history, err)
		}
	})

	t.Run("overhead only", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, translator)
		if err := transform.Finish(false); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}
	})

	t.Run("stream call", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, translator)
		event := mustTestJSON(t, map[string]any{
			"type": "response.output_item.done",
			"item": testHPatchItem(),
		})
		visible, err := transform.TransformSSE(event)
		if err != nil || len(visible) != 1 || !bytes.Contains(visible[0], []byte("*** Begin Patch")) {
			t.Fatalf("visible = %q, error %v", visible, err)
		}
	})
}

func TestHPatchMetricFailureStillPropagatesRequestCancellation(t *testing.T) {
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			return nil, nil
		},
		record: func(context.Context, hpatchMetricRecord) error {
			return errors.New("metrics unavailable")
		},
	}
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transform.ctx = ctx
	if err := transform.Finish(false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Finish() error = %v, want cancellation", err)
	}
}

func TestHReadOnlyCallClaimsCombinedDefinitionAccountingOnce(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("hread reached hpatch translation")
			return nil, nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, _, _, workspace := newHPatchTestTransform(t, translator)
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := transform.translateHRead("call-R", `"file.txt"`, nil); err != nil {
		t.Fatal(err)
	}
	if err := transform.Finish(false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("metric records = %d, want 1", len(records))
	}
	record := records[0]
	if record.DefinitionRequests != 1 || record.DefinitionInputTokens == 0 || record.RemovedDefinitionInputTokens == 0 {
		t.Fatalf("hread-only accounting = %+v", record)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 ||
		record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 ||
		record.ReportInputTokens != 0 || record.DiagnosticInputTokens != 0 {
		t.Fatalf("hread-only accounting contains patch tokens: %+v", record)
	}
}

func TestHReadTranslationProducesNoSyntheticResultMetrics(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("hread reached hpatch translation")
			return nil, nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	history, err := transform.translateHRead("call-R", `"missing.txt"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transform.Finish(false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("metric records = %d, want 1", len(records))
	}
	record := records[0]
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 ||
		record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 ||
		record.ReportInputTokens != 0 || record.DiagnosticInputTokens != 0 {
		t.Fatalf("hread accounting contains synthetic result tokens: %+v", record)
	}
	if strings.Contains(history.carrierInput(), "missing.txt:") {
		t.Fatalf("router fabricated a reader diagnostic: %q", history.carrierInput())
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
	if records[1].DefinitionRequests != 0 || records[1].SessionID != "session-1" || records[1].DefinitionInputTokens != 0 || records[1].RemovedDefinitionInputTokens != 0 {
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
