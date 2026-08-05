package hpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionCountsOncePerCallerSession(t *testing.T) {
	dataDirectory := t.TempDir()
	invocation := invocationMetrics{}
	record := HostMetricRecord{
		Invocation:                   InvocationMetrics{value: invocation},
		SessionID:                    "session-one",
		DefinitionRequests:           1,
		DefinitionInputTokens:        17,
		RemovedDefinitionInputTokens: 11,
	}
	for range 3 {
		recordHostMetricForTest(t, dataDirectory, record)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 1 || got.DefinitionRequests != 3 || got.DefinitionInputTokens != 17 || got.RemovedDefinitionInputTokens != 11 {
		t.Fatalf("first session definition metrics = %+v", got)
	}

	record.SessionID = "session-two"
	recordHostMetricForTest(t, dataDirectory, record)
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 2 || got.DefinitionRequests != 4 || got.DefinitionInputTokens != 34 || got.RemovedDefinitionInputTokens != 22 {
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

func TestDefinitionSessionClaimsAreScopedToMetricsRevision(t *testing.T) {
	dataDirectory := t.TempDir()
	digest := sha256.Sum256([]byte("session"))
	legacyDirectory := filepath.Join(dataDirectory, sessionMarkerDirectory)
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyMarker := filepath.Join(legacyDirectory, hex.EncodeToString(digest[:])+".seen")
	if err := os.WriteFile(legacyMarker, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fresh, err := claimSession(dataDirectory, "session", 1, 2, false)
	if err != nil || !fresh {
		t.Fatalf("claim with prior-revision marker = %t, error %v", fresh, err)
	}
	fresh, err = claimSession(dataDirectory, "session", 2, 3, false)
	if err != nil || fresh {
		t.Fatalf("claim within current revision = %t, error %v", fresh, err)
	}
}

func TestDefinitionReportDisclosesMissingCallerSession(t *testing.T) {
	report := strings.Join(strings.Fields(gainReport(metrics{})), " ")
	if !strings.Contains(report, "not measured (missing caller session)") {
		t.Fatalf("gain does not disclose unmeasured definitions: %q", report)
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
	report := gainReport(value)
	for _, want := range []string{
		"failed      30      10           n/a\n",
		"all         70      110          36.4%\n",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("gain report %q does not contain %q", report, want)
		}
	}
	if !strings.Contains(report, "failed apply_patch output uses the empty-patch semantic baseline.") {
		t.Fatalf("gain report does not identify the failed-call baseline: %q", report)
	}
}

func TestCommandReasonsAttributeErrorsToCommands(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exitCode := Run(nil, strings.NewReader("in note.txt\ntype 1:ffff \"x\"\n"), &bytes.Buffer{}, &bytes.Buffer{}, root, dataDirectory); exitCode == 0 {
		t.Fatal("stale row unexpectedly succeeded")
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommandReasons[commandOperationIndex("type")][reasonRowStale] != 1 {
		t.Fatalf("type row-stale = %d, want 1", got.CommandReasons[commandOperationIndex("type")][reasonRowStale])
	}
	report := gainReport(got)
	if !strings.Contains(report, "type     row-stale  1") {
		t.Fatalf("gain report lacks attributed error row: %q", report)
	}
	if !strings.Contains(gainReport(metrics{}), "none     none    0") { //nolint:dupword // Both empty-state columns read "none".
		t.Fatalf("empty gain report lacks none row: %q", gainReport(metrics{}))
	}
}
