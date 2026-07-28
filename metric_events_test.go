package hpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuccessfulReportsDoNotInventCallerTokens(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("translate = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.HPatchTokens != 0 || got.ApplyPatchTokens != 0 || got.ReportInputTokens != 0 || got.DiagnosticInputTokens != 0 {
		t.Fatalf("standalone evaluation invented caller tokens: %+v", got)
	}
	if got.Commands[commandOperationIndex("new")].Invocations != 1 || got.Commands[commandOperationIndex("type")].Invocations != 1 {
		t.Fatalf("standalone evaluator counters = %+v", got.Commands)
	}
}

func TestPartialReportWriteDoesNotInventCallerTokens(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new note.txt\ntype \"hello\"\n"
	var stdout bytes.Buffer

	exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, partialMetricsWriter{}, root, dataDirectory)
	if exitCode != 0 || stdout.Len() == 0 {
		t.Fatalf("translate = exit %d, stdout %q", exitCode, stdout.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.HPatchTokens != 0 || got.ApplyPatchTokens != 0 || got.ReportInputTokens != 0 {
		t.Fatalf("metrics after partial report = %+v", got)
	}
}

type partialMetricsWriter struct{}

func (partialMetricsWriter) Write(value []byte) (int, error) {
	return min(3, len(value)), io.ErrClosedPipe
}

func TestSuccessfulNoopCountsEvaluatorCommandsOnly(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	script := "new transient.txt\ncommit\nrm\n"
	var stdout, stderr bytes.Buffer
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("normal = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.HPatchTokens != 0 || got.ApplyPatchTokens != 0 || got.IneffectiveHPatchTokens != 0 || got.ReportInputTokens != 0 {
		t.Fatalf("no-op token metrics = %+v", got)
	}
	if got.Commands[commandOperationIndex("new")].Invocations != 1 || got.Commands[commandOperationIndex("rm")].Invocations != 1 {
		t.Fatalf("no-op command metrics = %+v", got.Commands)
	}
	if got.Commands[commandOperationIndex("commit")].Invocations != 1 {
		t.Fatalf("commit metrics = %+v", got.Commands)
	}
}

func TestGainReportsOutputAndInputSeparately(t *testing.T) {
	value := metrics{HPatchTokens: 40, ApplyPatchTokens: 100, IneffectiveHPatchTokens: 10, FailedApplyPatchTokens: 5, ReportInputTokens: 50}
	if got := value.overallReduction(); got != "52.4" {
		t.Fatalf("output reduction = %s, want 52.4", got)
	}
	report := gainReport(value)
	if !strings.Contains(report, "all         50      105          52.4%\n") {
		t.Fatalf("gain report %q does not contain the output total", report)
	}
	input := strings.Join(strings.Fields(gainInputSection(t, report)), " ")
	if !strings.Contains(input, "state reports 50") || !strings.Contains(input, "net added input 50") {
		t.Fatalf("input gain report %q does not reconcile the state report", input)
	}
	if strings.Contains(report, "combined token-cost") || strings.Contains(report, "output:input cost") {
		t.Fatalf("gain report combines output and input tokens: %q", report)
	}
}

func TestMalformedSelectorAttributionRequiresRecognizableVariant(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   commandAttempt
		reason failureReason
	}{
		{name: "bare sel", script: "sel\n"},
		{name: "nonnumeric sel", script: "sel nope\n"},
		{name: "bare tsel", script: "tsel\n"},
		{name: "tsel without text", script: "tsel 1 1\n"},
		{name: "zero absolute line", script: "sel 0 1:1\n", want: commandAttempt{recognized: true}, reason: reasonSyntax},
		{name: "malformed signed line", script: "sel +x 1:1\n"},
		{name: "invalid multiple count", script: "tsel 1 1 \"x\" nope\n", want: commandAttempt{recognized: true, textSpan: textSpanMultiple}, reason: reasonInvalidCount},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parse(test.script)
			sourceError, ok := errors.AsType[*commandError](err)
			if !ok {
				t.Fatalf("parse() error = %T %v, want *commandError", err, err)
			}
			if sourceError.Attempt != test.want {
				t.Fatalf("attempt = %+v, want %+v", sourceError.Attempt, test.want)
			}
			if test.want.recognized && sourceError.Reason != test.reason {
				t.Fatalf("reason = %s, want %s", failureReasonNames[sourceError.Reason], failureReasonNames[test.reason])
			}
			var events invocationMetrics
			events.invokeFailure(sourceError.Operation, sourceError.Attempt, sourceError.Reason)
			if !validInvocationMetrics(events) {
				t.Fatalf("attributed events are inconsistent: %+v", events)
			}
			if !test.want.recognized && events != (invocationMetrics{}) {
				t.Fatalf("unrecognizable selector was attributed: %+v", events)
			}
		})
	}
}

func TestMetricsClassifyVariantsOutcomesAndReasons(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	content := "alpha alpha alpha\nfunc x() {\n\tbody\n}\n"
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(script string, success bool) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory)
		if (exitCode == 0) != success {
			t.Fatalf("script %q = exit %d, stdout %q, stderr %q", script, exitCode, stdout.String(), stderr.String())
		}
	}

	for _, script := range []string{
		"in sample.txt\nsel 1 1:1\ntype \"A\"\n",
		"in sample.txt\nrsel 1:1\ntype \"A\"\n",
		"in sample.txt\ntsel 1 1 \"alpha\"\ntype \"A\"\n",
		"in sample.txt\ntsel 1 1 \"alpha\" 1\ntype \"A\"\n",
		"in sample.txt\ntsel 1 1 \"alpha\" 2\ntype \"A\"\n",
		"in sample.txt\nbsel \"func x() {\" \"}\"\ntype \"A\"\n",
		"in sample.txt\nbsel \"func x() {\" \"    body\"\ntype \"A\"\n",
		"in sample.txt\nbsel_next \"func x() {\" \"}\"\ntype \"A\"\n",
		"in sample.txt\nbsel_next \"func x() {\" \"    body\"\ntype \"A\"\n",
	} {
		run(script, true)
	}
	for _, script := range []string{
		"in sample.txt\ntsel 1 9 \"alpha\"\n",
		"in sample.txt\ntsel 1 1 \"alpha\" nope\n",
		"in sample.txt\nbsel \"missing\" \"}\"\n",
		"in sample.txt\nbsel \"alpha\" \"}\"\n",
		"in sample.txt\nsel +x 1:1\n",
	} {
		run(script, false)
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantTextSpans := [textSpanVariantCount]commandMetric{
		{Invocations: 3, Errors: 1},
		{Invocations: 2, Errors: 1},
	}
	if got.TextSpans != wantTextSpans {
		t.Fatalf("tsel spans = %+v, want %+v", got.TextSpans, wantTextSpans)
	}
	wantBlockOutcomes := [blockOutcomeCount]uint64{1, 1, 1, 1}
	if got.BlockOutcomes != wantBlockOutcomes {
		t.Fatalf("block outcomes = %+v, want %+v", got.BlockOutcomes, wantBlockOutcomes)
	}
	for reason, want := range map[failureReason]uint64{
		reasonOccurrenceMissing: 1,
		reasonAnchorMissing:     1,
		reasonAnchorAmbiguous:   1,
		reasonInvalidCount:      1,
	} {
		if got.Reasons[reason] != want {
			t.Fatalf("reason %s = %d, want %d", failureReasonNames[reason], got.Reasons[reason], want)
		}
	}
	totalCommands, ok := got.Commands.total()
	if !ok {
		t.Fatal("aggregate command metrics overflow")
	}
	var totalReasons uint64
	for _, count := range got.Reasons {
		totalReasons += count
	}
	if totalReasons != totalCommands.Errors || totalReasons != 4 {
		t.Fatalf("reason total = %d, aggregate errors = %d", totalReasons, totalCommands.Errors)
	}
}

func TestMetricsSlotRoundTripsAllCounters(t *testing.T) {
	want := representativeMetrics()
	encoded := encodeMetricsSlot(want, 9)
	got, generation, ok := decodeMetricsSlot(encoded)
	if !ok || generation != 9 || got != want {
		t.Fatalf("decode = (%+v, %d, %t), want (%+v, 9, true)", got, generation, ok, want)
	}

	dataDirectory := t.TempDir()
	if err := updateMetrics(dataDirectory, want); err != nil {
		t.Fatal(err)
	}
	persisted, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if persisted != want {
		t.Fatalf("persisted metrics = %+v, want %+v", persisted, want)
	}
}

func representativeMetrics() metrics {
	value := metrics{HPatchTokens: 11, ApplyPatchTokens: 19, IneffectiveHPatchTokens: 7, FailedApplyPatchTokens: 5, ReportInputTokens: 5}
	value.Commands[commandOperationIndex("sel")] = commandMetric{Invocations: 3, Errors: 1}

	value.Commands[commandOperationIndex("tsel")] = commandMetric{Invocations: 2, Errors: 1}

	value.TextSpans[textSpanSingle-1] = commandMetric{Invocations: 1}
	value.TextSpans[textSpanMultiple-1] = commandMetric{Invocations: 1, Errors: 1}
	value.Commands[commandOperationIndex("bsel")] = commandMetric{Invocations: 2, Errors: 1}
	value.BlockOutcomes[blockOutcomeIndex("bsel", false)] = 1
	value.Commands[commandOperationIndex("bsel_next")] = commandMetric{Invocations: 1}
	value.BlockOutcomes[blockOutcomeIndex("bsel_next", true)] = 1
	value.Reasons[reasonSyntax] = 2
	value.Reasons[reasonAnchorMissing] = 1
	value.Commands[commandOperationIndex("paste")] = commandMetric{Invocations: 1, Errors: 1}
	value.Reasons[reasonClipboardEmpty] = 1
	// Attribute each reason to the command that raised it so the
	// cross-tabulation reconciles with both margins.
	value.CommandReasons[commandOperationIndex("sel")][reasonSyntax] = 1
	value.CommandReasons[commandOperationIndex("tsel")][reasonSyntax] = 1
	value.CommandReasons[commandOperationIndex("bsel")][reasonAnchorMissing] = 1
	value.CommandReasons[commandOperationIndex("paste")][reasonClipboardEmpty] = 1
	return value
}

func TestMetricsReportCounterOverflowFails(t *testing.T) {
	value := metrics{ReportInputTokens: ^uint64(0)}
	if err := value.add(metrics{ReportInputTokens: 1}); err == nil || !strings.Contains(err.Error(), "token count overflow") {
		t.Fatalf("add overflow error = %v", err)
	}
}

func TestHPATCH07MetricsReset(t *testing.T) {
	dataDirectory := t.TempDir()
	var prior [264]byte
	copy(prior[:8], "HPATCH07")
	binary.LittleEndian.PutUint64(prior[8:16], 3)
	binary.LittleEndian.PutUint64(prior[16:24], 99)
	checksum := sha256.Sum256(prior[:232])
	copy(prior[232:], checksum[:])
	if err := os.WriteFile(filepath.Join(dataDirectory, metricsFilename), prior[:], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != (metrics{}) {
		t.Fatalf("HPATCH07 metrics were not reset: %+v", got)
	}
}
