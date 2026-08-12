package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
	"github.com/yusing/hpatch"
)

func TestCalculateHPatchMetricRecordUsesExactCallerPayloads(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: /workspace/calc.go\n@@\n-old\n+new\n*** End Patch\n"
	inputs := hpatchMetricInputs{
		attempt:                hpatch.AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call", Attempt: 2, Correction: true},
		emittedScript:          "type 12:9645..18:4b7b \"replacement\"\n",
		report:                 "in calc.go 2:9\n9645: return new\n",
		patch:                  patch,
		sessionID:              "session",
		definition:             "hpatch definition\n\n",
		baselineDefinition:     "apply_patch definition\n",
		execCommandDefinitions: []string{"exec_command definition\n", "second exec definition\n"},
		successful:             true,
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
	applyPatchPayload := "functions.exec\n" + applyPatchMetricProgram(patch)
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
	if got, want := record.DefinitionInputTokens, count(inputs.definition); got != want {
		t.Fatalf("definition tokens = %d, want %d", got, want)
	}
	if got, want := record.RemovedDefinitionInputTokens, count(inputs.baselineDefinition); got != want {
		t.Fatalf("removed definition tokens = %d, want %d", got, want)
	}
	if got, want := record.RemovedExecCommandDefinitionInputTokens, count(inputs.execCommandDefinitions[0])+count(inputs.execCommandDefinitions[1]); got != want {
		t.Fatalf("removed exec definition tokens = %d, want %d", got, want)
	}
	if record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 || record.DiagnosticInputTokens != 0 {
		t.Fatalf("successful record = %+v", record)
	}
}

func TestCalculateHPatchMetricRecordUsesEmptyFailureBaseline(t *testing.T) {
	inputs := hpatchMetricInputs{
		emittedScript: "type 12:9645..18:4b7b \"replacement\"\n",
		diagnostic:    "type: command 2, reason language-syntax: rejected\nrepair context\n\nUse hpatch without `in` to patch the rejected script.\n",
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
	failedPayload := "functions.exec\n" + applyPatchMetricProgram(failedApplyPatch)
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

// A recovery is charged the short payload the model emitted, never the complete
// script the router rebuilt from the rejected baseline. The comparison baseline
// stays the rebuilt script's full apply_patch program, so a recovered chain
// reports the real payload saving rather than the baseline's size twice.
func TestHPatchRecoveryChargesEmittedPayloadNotRebuiltScript(t *testing.T) {
	base := "new created.txt\ntype \"bad\"\n"
	rebuilt := "new created.txt\ntype \"payload\"\n"
	var records []hpatchMetricRecord
	calls := 0
	translator := hpatchResultObservingTranslator{
		translate: func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
			calls++
			if calls == 1 {
				return hpatchTranslationResult{
					diagnostic: "type: command 2, reason rejected: bad value\n",
					rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
				}, errors.New("rejected")
			}
			if script != rebuilt {
				t.Fatalf("evaluated script = %q, want %q", script, rebuilt)
			}
			return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, newManagedHPatchProxy(t, translator))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	payload := recoveryCommands(base)[1].handle + ` value "payload"` + "\n"
	if _, err := transform.translateRecovery("call-2", payload, nil); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("recorded attempts = %d, want 2", len(records))
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

	if got, want := records[0].IneffectiveHPatchTokens, count("functions.hpatch\n"+base); got != want {
		t.Fatalf("rejected attempt charge = %d, want %d", got, want)
	}
	if records[0].DiagnosticInputTokens == 0 {
		t.Fatal("rejection diagnostic carrying recovery guidance was not charged as model input")
	}
	if got, want := records[1].attemptHPatchTokens, count("functions.hpatch_recover\n"+payload); got != want {
		t.Fatalf("recovery attempt charge = %d, want %d for the emitted payload", got, want)
	}
	if got, want := records[1].HPatchTokens, count("functions.hpatch\n"+base)+count("functions.hpatch_recover\n"+payload); got != want {
		t.Fatalf("settled recovery chain = %d, want combined payloads %d", got, want)
	}
	if got := records[1].attemptHPatchTokens; got == count("functions.hpatch_recover\n"+rebuilt) {
		t.Fatalf("recovery charged the rebuilt script (%d) instead of the emitted payload", got)
	}
	if got, want := records[1].ApplyPatchTokens, count("functions.exec\n"+applyPatchMetricProgram(testTranslatedPatch)); got != want {
		t.Fatalf("recovery apply_patch baseline = %d, want %d", got, want)
	}
	if records[1].IneffectiveHPatchTokens != 0 || records[1].FailedApplyPatchTokens != 0 {
		t.Fatalf("successful recovery reported failure counters: %+v", records[1])
	}

	rejected, ok := hpatchAttemptMetricsOf(records[0])
	if !ok {
		t.Fatal("rejected attempt produced no session telemetry")
	}
	recovered, ok := hpatchAttemptMetricsOf(records[1])
	if !ok {
		t.Fatal("recovery attempt produced no session telemetry")
	}
	if rejected.Outcome != "rejected" || recovered.Outcome != "successful" {
		t.Fatalf("outcomes = %q and %q", rejected.Outcome, recovered.Outcome)
	}
	if rejected.Correction || !recovered.Correction {
		t.Fatalf("recovery markers = %v and %v", rejected.Correction, recovered.Correction)
	}
	if rejected.CorrelationID != recovered.CorrelationID || rejected.Attempt != 1 || recovered.Attempt != 2 {
		t.Fatalf("recovery chain = %+v and %+v", rejected, recovered)
	}
}

func TestEvictedRecoveryAttemptsRemainInChainSettlement(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	const correlationID = "chain"
	for attempt := 1; attempt <= maxSessionTurns+2; attempt++ {
		toolName := hpatchRecoveryToolName
		if attempt == 1 {
			toolName = hpatchToolName
		}
		history := hpatchHistory{
			toolName: toolName, script: "payload-" + strconv.Itoa(attempt),
			translationError: "rejected", evaluatorRejected: true,
			correlationID: correlationID, attempt: attempt,
		}
		if err := proxy.rememberBatch("session", map[string]hpatchHistory{
			"call-" + strconv.Itoa(attempt): history,
		}); err != nil {
			t.Fatal(err)
		}
	}
	settled := proxy.recoverySettlement("session", correlationID)
	survivors := proxy.recoveryChain("session", correlationID)
	record, err := calculateHPatchMetricRecord(hpatchMetricInputs{
		attempt: hpatch.AttemptMetadata{
			SessionID: "session", CorrelationID: correlationID,
			CallID: "success", Attempt: maxSessionTurns + 3, Correction: true,
		},
		emittedScript:   "successful-recovery",
		emittedTool:     hpatchRecoveryToolName,
		successful:      true,
		patch:           testTranslatedPatch,
		priorSettlement: settled,
		priorAttempts:   survivors,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wantEmitted uint64
	for attempt := 1; attempt <= maxSessionTurns+2; attempt++ {
		toolName := hpatchRecoveryToolName
		if attempt == 1 {
			toolName = hpatchToolName
		}
		prior, err := calculateHPatchMetricRecord(hpatchMetricInputs{
			emittedScript: "payload-" + strconv.Itoa(attempt),
			emittedTool:   toolName,
		})
		if err != nil {
			t.Fatal(err)
		}
		wantEmitted += prior.IneffectiveHPatchTokens
	}
	success, err := calculateHPatchMetricRecord(hpatchMetricInputs{
		emittedScript: "successful-recovery",
		emittedTool:   hpatchRecoveryToolName,
		successful:    true,
		patch:         testTranslatedPatch,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantEmitted += success.HPatchTokens
	if record.HPatchTokens != wantEmitted || record.IneffectiveHPatchTokens != 0 ||
		record.FailedApplyPatchTokens != 0 || record.ApplyPatchTokens != success.ApplyPatchTokens {
		t.Fatalf("settled evicted chain = %+v; want emitted %d and comparator %d", record, wantEmitted, success.ApplyPatchTokens)
	}
	if record.Compensation.FailedApplyPatchTokens == 0 {
		t.Fatal("settlement did not compensate the initial failed comparator")
	}
}

func TestHPatchMetricDefinitionMatchesInstalledGrammarTools(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
			t.Fatal("unexpected hpatch translation")
			return nil, nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	}
	transform, proxy, request, _ := newHPatchTestTransform(t, translator)
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
	installed := make([]json.RawMessage, 0, 3)
	var installedNames []string
	for _, raw := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if name := jsonString(tool, "name"); name != "" {
			if _, ok := proxy.registry.contribution(name); !ok {
				continue
			}
			installed = append(installed, raw)
			installedNames = append(installedNames, name)
		}
	}
	if want := []string{hpatchToolName, hpatchRecoveryToolName, "shell"}; !reflect.DeepEqual(installedNames, want) {
		t.Fatalf("metered tool definitions = %v, want %v", installedNames, want)
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
		translate: func(context.Context, string, string) ([]byte, error) {
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
		rejected.translate = func(context.Context, string, string) ([]byte, error) {
			return nil, errors.New("rejected")
		}
		transform, _, _, _ := newHPatchTestTransform(t, rejected)
		history, err := transform.translate("call-rejected", testHPatchScript, nil)
		if err != nil || !strings.Contains(history.translationError, "rejected") {
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
		translate: func(context.Context, string, string) ([]byte, error) {
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

func TestHPatchRequestDefinitionAccountingIsClaimedOnce(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
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
	if records[0].DefinitionRequests != 1 || records[0].SessionID != "session-1" || records[0].DefinitionInputTokens == 0 || records[0].RemovedDefinitionInputTokens == 0 || records[0].RemovedExecCommandDefinitionInputTokens == 0 {
		t.Fatalf("first request accounting = %+v", records[0])
	}
	if records[1].DefinitionRequests != 0 || records[1].SessionID != "session-1" || records[1].DefinitionInputTokens != 0 || records[1].RemovedDefinitionInputTokens != 0 || records[1].RemovedExecCommandDefinitionInputTokens != 0 {
		t.Fatalf("second call repeated request accounting = %+v", records[1])
	}
	if records[0].ApplyPatchTokens == 0 || records[1].ApplyPatchTokens == 0 {
		t.Fatalf("per-call patch metrics = %+v", records)
	}
}

func TestFinishRecordsDefinitionOverheadWithoutHPatchCall(t *testing.T) {
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
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
	if record.DefinitionRequests != 1 || record.DefinitionInputTokens == 0 || record.RemovedDefinitionInputTokens == 0 || record.RemovedExecCommandDefinitionInputTokens == 0 {
		t.Fatalf("request overhead = %+v", record)
	}
	if record.HPatchTokens != 0 || record.ApplyPatchTokens != 0 || record.IneffectiveHPatchTokens != 0 || record.FailedApplyPatchTokens != 0 {
		t.Fatalf("overhead-only record contains call tokens: %+v", record)
	}
}
