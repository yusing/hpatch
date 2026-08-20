package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yusing/hpatch"
)

func TestHPatchExactEvidenceDisabled(t *testing.T) {
	t.Setenv(hpatchExactEvidenceEnvironment, "")
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if proxy.exactEvidence != nil {
		t.Fatal("empty exact-evidence setting installed a recorder")
	}

	t.Setenv(hpatchExactEvidenceEnvironment, "relative/path")
	if recorder := newHPatchExactEvidenceRecorder(); recorder != nil {
		t.Fatal("relative exact-evidence setting installed a recorder")
	}
}

func TestHPatchExactEvidenceInitialRejection(t *testing.T) {
	directory := t.TempDir()
	t.Setenv(hpatchExactEvidenceEnvironment, directory)
	diagnostic := "type: command 2 rejected exactly\n"
	translator := hpatchResultTranslatorFunc(func(_ context.Context, _, _ string) (hpatchTranslationResult, error) {
		return hpatchTranslationResult{
			diagnostic: diagnostic,
			rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
		}, errors.New("rejected")
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	history, err := transform.translate("call-exact", testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := readSingleHPatchExactEvidence(t, directory)
	assertHPatchExactEvidence(t, record, hpatchExactAttemptEvidence{
		Schema:             hpatchExactEvidenceSchema,
		SessionID:          "session-1",
		CorrelationID:      "call-exact",
		CallID:             "call-exact",
		Attempt:            1,
		Model:              "gpt-test",
		ToolName:           hpatchToolName,
		Outcome:            "evaluator_rejected",
		EmittedPayload:     testHPatchScript,
		RenderedDiagnostic: history.translationError,
	})
	if !strings.HasPrefix(record.RenderedDiagnostic, diagnostic) {
		t.Fatalf("rendered diagnostic %q lost evaluator diagnostic %q", record.RenderedDiagnostic, diagnostic)
	}
}

func TestHPatchExactEvidenceRecoveryPayloadAndBytes(t *testing.T) {
	directory := t.TempDir()
	t.Setenv(hpatchExactEvidenceEnvironment, directory)
	base := "in file.txt\ntype 1:aaaa \"value\"\n"
	report := "final rows: 1:a793..2:π\n"
	calls := 0
	translator := hpatchResultTranslatorFunc(func(_ context.Context, _, _ string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			return hpatchTranslationResult{
				diagnostic: "stale target\n",
				rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type", Target: "line", Reason: "row-stale"}},
			}, errors.New("rejected")
		}
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch), report: report}, nil
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	if _, err := transform.translate("call-first", base, nil); err != nil {
		t.Fatal(err)
	}
	payload := recoveryCommands(base)[1].handle + " 2:bbbb\n"
	if _, err := transform.translateRecovery("call-recovery", payload, nil); err != nil {
		t.Fatal(err)
	}
	records := readHPatchExactEvidence(t, directory)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	var recovery hpatchExactAttemptEvidence
	for _, record := range records {
		if record.CallID == "call-recovery" {
			recovery = record
		}
	}
	assertHPatchExactEvidence(t, recovery, hpatchExactAttemptEvidence{
		Schema:         hpatchExactEvidenceSchema,
		SessionID:      "session-1",
		CorrelationID:  "call-first",
		CallID:         "call-recovery",
		Attempt:        2,
		Correction:     true,
		Model:          "gpt-test",
		ToolName:       hpatchRecoveryToolName,
		Outcome:        "successful",
		EmittedPayload: payload,
		RenderedReport: report,
	})
	if recovery.EmittedPayloadBytes != len(payload) {
		t.Fatalf("payload bytes = %d, want %d", recovery.EmittedPayloadBytes, len(payload))
	}
	if recovery.RenderedReport != report || recovery.RenderedReportBytes != len(report) {
		t.Fatalf("rendered report = %q (%d bytes), want %q (%d bytes)",
			recovery.RenderedReport, recovery.RenderedReportBytes, report, len(report))
	}
}

func TestHPatchExactEvidenceMalformedRecoveryIsRouterRejected(t *testing.T) {
	directory := t.TempDir()
	t.Setenv(hpatchExactEvidenceEnvironment, directory)
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	payload := "C1:ffff not-a-target\n"
	history, err := transform.translateRecovery("call-malformed", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := readSingleHPatchExactEvidence(t, directory)
	assertHPatchExactEvidence(t, record, hpatchExactAttemptEvidence{
		Schema:             hpatchExactEvidenceSchema,
		SessionID:          "session-1",
		CorrelationID:      "call-malformed",
		CallID:             "call-malformed",
		Attempt:            1,
		Correction:         true,
		Model:              "gpt-test",
		ToolName:           hpatchRecoveryToolName,
		Outcome:            "router_rejected",
		EmittedPayload:     payload,
		RenderedDiagnostic: history.translationError,
	})
}

func TestHPatchExactEvidenceConcurrentFilesAreAtomicJSON(t *testing.T) {
	directory := t.TempDir()
	recorder := &hpatchExactEvidenceRecorder{directory: directory}
	const count = 32
	var wait sync.WaitGroup
	for index := range count {
		wait.Go(func() {
			inputs := hpatchMetricInputs{
				attempt: hpatch.AttemptMetadata{
					SessionID: "session", CorrelationID: "correlation", CallID: fmt.Sprintf("call-%d", index),
					Attempt: 1, ToolName: hpatchToolName,
				},
				emittedScript: fmt.Sprintf("payload-%d", index),
				successful:    true,
			}
			if err := recorder.record(inputs); err != nil {
				t.Errorf("record %d: %v", index, err)
			}
		})
	}
	wait.Wait()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("files = %d, want %d", len(entries), count)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("incomplete file visible: %q", entry.Name())
		}
		var record hpatchExactAttemptEvidence
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil || json.Unmarshal(content, &record) != nil || record.Schema != hpatchExactEvidenceSchema {
			t.Fatalf("invalid completed file %q: read error %v, content %q", entry.Name(), err, content)
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file %q mode = %v, error %v", entry.Name(), info.Mode().Perm(), err)
		}
	}
}

func TestHPatchExactEvidenceExcludesOtherToolsAndFailureIsAuxiliary(t *testing.T) {
	directory := t.TempDir()
	recorder := &hpatchExactEvidenceRecorder{directory: directory}
	if err := recorder.record(hpatchMetricInputs{
		attempt:     hpatch.AttemptMetadata{SessionID: "session", CorrelationID: "c", CallID: "call", Attempt: 1, ToolName: "shell"},
		emittedTool: "shell", emittedScript: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("excluded tool wrote evidence: entries %v, error %v", entries, err)
	}

	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hpatchExactEvidenceEnvironment, blockingFile)
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	history, err := transform.translate("call-success", testHPatchScript, nil)
	if err != nil || history.patch != testTranslatedPatch {
		t.Fatalf("auxiliary evidence failure changed translation: history %+v, error %v", history, err)
	}
}

func readSingleHPatchExactEvidence(t *testing.T, directory string) hpatchExactAttemptEvidence {
	t.Helper()
	records := readHPatchExactEvidence(t, directory)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	return records[0]
}

func readHPatchExactEvidence(t *testing.T, directory string) []hpatchExactAttemptEvidence {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]hpatchExactAttemptEvidence, 0, len(entries))
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var record hpatchExactAttemptEvidence
		if err := json.Unmarshal(content, &record); err != nil {
			t.Fatalf("decode %q: %v", entry.Name(), err)
		}
		records = append(records, record)
	}
	return records
}

func assertHPatchExactEvidence(t *testing.T, got, want hpatchExactAttemptEvidence) {
	t.Helper()
	want.EmittedPayloadBytes = len(want.EmittedPayload)
	want.EmittedPayloadSHA256 = hpatchExactEvidenceSHA256(want.EmittedPayload)
	want.RenderedDiagnosticBytes = len(want.RenderedDiagnostic)
	want.RenderedDiagnosticSHA256 = hpatchExactEvidenceSHA256(want.RenderedDiagnostic)
	want.RenderedReportBytes = len(want.RenderedReport)
	want.RenderedReportSHA256 = hpatchExactEvidenceSHA256(want.RenderedReport)
	if got != want {
		t.Fatalf("exact evidence =\n%+v\nwant\n%+v", got, want)
	}
}
