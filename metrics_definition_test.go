package hpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestDefinitionCountsOncePerCallerSession(t *testing.T) {
	dataDirectory := t.TempDir()
	invocation := invocationMetrics{}
	record := HostMetricRecord{
		Invocation:                              InvocationMetrics{value: invocation},
		SessionID:                               "session-one",
		DefinitionRequests:                      1,
		DefinitionInputTokens:                   17,
		RemovedDefinitionInputTokens:            11,
		RemovedExecCommandDefinitionInputTokens: 7,
		ToolMetrics:                             []ToolMetricRecord{{PluginID: "builtin.hpatch", ToolName: "hpatch", DefinitionInputTokens: 17}},
	}
	for range 3 {
		recordHostMetricForTest(t, dataDirectory, record)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 1 || got.DefinitionRequests != 3 || got.DefinitionInputTokens != 17 || got.RemovedDefinitionInputTokens != 11 || got.RemovedExecCommandDefinitionInputTokens != 7 {
		t.Fatalf("first session definition metrics = %+v", got)
	}

	record.SessionID = "session-two"
	recordHostMetricForTest(t, dataDirectory, record)
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 2 || got.DefinitionRequests != 4 || got.DefinitionInputTokens != 34 || got.RemovedDefinitionInputTokens != 22 || got.RemovedExecCommandDefinitionInputTokens != 14 {
		t.Fatalf("second session definition metrics = %+v", got)
	}
}

func TestInterruptedSessionClaimRemainsFreshUntilMetricsCommit(t *testing.T) {
	dataDirectory := t.TempDir()
	fresh, err := claimSession(dataDirectory, "session", 0, 1, false)
	if err != nil || !fresh {
		t.Fatalf("initial claim = %t, error %v", fresh, err)
	}
	fresh, err = claimSession(dataDirectory, "session", 0, 1, false)
	if err != nil || !fresh {
		t.Fatalf("interrupted claim retry = %t, error %v", fresh, err)
	}
	fresh, err = claimSession(dataDirectory, "session", 1, 2, false)
	if err != nil || fresh {
		t.Fatalf("durable claim retry = %t, error %v", fresh, err)
	}
}

func TestHPATCH25DefinitionMetricsResetBeforeNewCounter(t *testing.T) {
	dataDirectory := t.TempDir()
	prior := encodeMetricsSlot(metrics{
		Sessions:                     3,
		DefinitionRequests:           5,
		DefinitionInputTokens:        17,
		RemovedDefinitionInputTokens: 11,
	}, 1)
	copy(prior[:8], "HPATCH25")
	checksum := sha256.Sum256(prior[:metricsChecksumOffset])
	copy(prior[metricsChecksumOffset:], checksum[:])
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), prior[:], 0o600); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256([]byte("session"))
	legacyDirectory := filepath.Join(dataDirectory, sessionMarkerDirectory, "HPATCH25")
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyMarker := filepath.Join(legacyDirectory, hex.EncodeToString(digest[:])+".seen")
	if err := os.WriteFile(legacyMarker, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recordHostMetricForTest(t, dataDirectory, HostMetricRecord{
		SessionID:                               "session",
		DefinitionRequests:                      1,
		RemovedExecCommandDefinitionInputTokens: 7,
	})
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := metrics{
		Sessions:                                1,
		DefinitionRequests:                      1,
		RemovedExecCommandDefinitionInputTokens: 7,
	}
	if got != want {
		t.Fatalf("metrics after HPATCH25 reset = %+v, want %+v", got, want)
	}
}

func TestDefinitionReportDisclosesMissingCallerSession(t *testing.T) {
	if got := (metrics{}).gainMetrics().DefinitionSources; got != "not measured (missing caller session)" {
		t.Fatalf("definition sources = %q", got)
	}
}

func TestFailedOutputBelongsOnlyToHPatch(t *testing.T) {
	value := metrics{
		HPatchTokens:            40,
		ApplyPatchTokens:        100,
		IneffectiveHPatchTokens: 30,
		FailedApplyPatchTokens:  10,
	}
	// (100 + 10 - 40 - 30) / (100 + 10) = 36.36%.
	if got := value.overallReduction(); got != "36.4" {
		t.Fatalf("overall reduction = %s, want 36.4", got)
	}
	gain := value.gainMetrics()
	if len(gain.Tools) != 2 || !gain.Tools[1].Failed || gain.Tools[1].EmittedTokens != 30 ||
		gain.Tools[1].TranslatedTokens != 10 || gain.AllTools.Reduction != "36.4" {
		t.Fatalf("structured gain = %+v", gain)
	}
}

func TestCommandReasonsAttributeErrorsToCommands(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, applyErr := applyForHostAtTest(t, root, "in note.txt\ntype 1:ffff \"x\"\n", dataDirectory)
	if applyErr == nil {
		t.Fatal("stale row unexpectedly succeeded")
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, HostMetricRecord{Invocation: result.Invocation}); err != nil {
		t.Fatal(err)
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommandReasons[commandOperationIndex("type")][reasonRowStale] != 1 {
		t.Fatalf("type row-stale = %d, want 1", got.CommandReasons[commandOperationIndex("type")][reasonRowStale])
	}
	projected := got.gainMetrics()
	if len(projected.CommandReasons) != 1 || projected.CommandReasons[0] != (CommandReasonMetric{Command: "type", Reason: "row-stale", Errors: 1}) {
		t.Fatalf("command reason rows = %+v", projected.CommandReasons)
	}
	if empty := (metrics{}).gainMetrics().CommandReasons; len(empty) != 1 || empty[0] != (CommandReasonMetric{Command: "none", Reason: "none"}) {
		t.Fatalf("empty command reason rows = %+v", empty)
	}
}
