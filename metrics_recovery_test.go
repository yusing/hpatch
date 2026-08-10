package hpatch

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRecoveryCountersPersistAggregateAndProject(t *testing.T) {
	first := metrics{}
	first.Recoveries = [recoveryKindCount]uint64{1, 2, 3}
	encoded := encodeMetricsSlot(first, 7)
	got, generation, ok := decodeMetricsSlot(encoded)
	if !ok || generation != 7 || got.Recoveries != first.Recoveries {
		t.Fatalf("round trip = (%v, %d, %t)", got.Recoveries, generation, ok)
	}

	// Current-format slots written before these reserved bytes were assigned decode as zero.
	for index := range recoveryKindCount {
		encoded[metricsRecoveryOffset+index*8] = 0
	}
	checksum := sha256.Sum256(encoded[:metricsChecksumOffset])
	copy(encoded[metricsChecksumOffset:], checksum[:])
	old, _, ok := decodeMetricsSlot(encoded)
	if !ok || old.Recoveries != [recoveryKindCount]uint64{} {
		t.Fatalf("old current-format recoveries = %v, valid %t", old.Recoveries, ok)
	}

	dataDirectory := t.TempDir()
	if err := updateMetrics(dataDirectory, first); err != nil {
		t.Fatal(err)
	}
	if err := updateMetrics(dataDirectory, metrics{invocationMetrics: invocationMetrics{
		Recoveries: [recoveryKindCount]uint64{4, 5, 6},
	}}); err != nil {
		t.Fatal(err)
	}
	aggregate, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Recoveries != [recoveryKindCount]uint64{5, 7, 9} {
		t.Fatalf("aggregate recoveries = %v", aggregate.Recoveries)
	}
	recoveries := aggregate.gainMetrics().Recoveries
	wantRecoveries := []NamedCount{
		{Name: "white-space error", Count: 5},
		{Name: "indentation shift", Count: 7},
		{Name: "luna misuse", Count: 9},
	}
	if len(recoveries) != len(wantRecoveries) {
		t.Fatalf("projected recoveries = %+v", recoveries)
	}
	for index, want := range wantRecoveries {
		if recoveries[index] != want {
			t.Fatalf("projected recoveries = %+v", recoveries)
		}
	}
	compactReport := strings.Join(strings.Fields(gainReport(aggregate)), " ")
	for _, want := range []string{
		"recoveries: recoveries count",
		"white-space error 5",
		"indentation shift 7",
		"luna misuse 9",
	} {
		if !strings.Contains(compactReport, want) {
			t.Fatalf("gain report has no per-action recovery table: %q", compactReport)
		}
	}
	overflow := metrics{invocationMetrics: invocationMetrics{Recoveries: [recoveryKindCount]uint64{^uint64(0), 0, 0}}}
	if err := overflow.add(metrics{invocationMetrics: invocationMetrics{
		Recoveries: [recoveryKindCount]uint64{1, 0, 0},
	}}); err == nil || overflow.Recoveries[recoveryWhitespace] != ^uint64(0) {
		t.Fatalf("recovery overflow = %v, error %v", overflow.Recoveries, err)
	}
}

func TestEvaluatorMetersConcreteRecoveriesIncludingRejectedResult(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "one.txt", "old\n", 0o600)
	writeTestFile(t, rootPath, "two.txt", "old\n", 0o600)
	writeTestFile(t, rootPath, "file.py", "header\n    first\n    second\n", 0o600)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	script := fmt.Sprintf("in one.txt\ntype %s %q\nin two.txt\ntype %s %q\nin file.py\ntype %s %q\ntype %s %q\n",
		row(1, "old"), "new  ", row(1, "old"), "new\t ",
		row(2, "    first"), "first\n", row(3, "    second"), "second\n")
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Invocation.value.Recoveries; got != [recoveryKindCount]uint64{2, 2, 0} {
		t.Fatalf("successful recoveries = %v", got)
	}
	if treeSitterIndentationAvailable {
		writeTestFile(t, rootPath, "wrapper.py",
			"def f():\n    if ready:\n        existing()\n    return\n", 0o600)
		wrapper, err := TranslateForHost(t.Context(), Workspace{Root: root},
			fmt.Sprintf("in wrapper.py\ntype %s %q\n", row(3, "        existing()"),
				"        if ready:\n        existing()\n"), t.TempDir())
		if err != nil || wrapper.Invocation.value.Recoveries[recoveryIndentation] != 1 {
			t.Fatalf("wrapper recovery = %v, error %v", wrapper.Invocation.value.Recoveries, err)
		}
	}

	writeTestFile(t, rootPath, "bad.js", "const old = 1;\n", 0o600)
	rejected, err := TranslateForHost(t.Context(), Workspace{Root: root},
		fmt.Sprintf("in bad.js\ntype %s %q\n", row(1, "const old = 1;"), "const = 1;  "), t.TempDir())
	if err == nil || rejected.Invocation.value.Recoveries[recoveryWhitespace] != 1 {
		t.Fatalf("rejected recovery = %v, error %v", rejected.Invocation.value.Recoveries, err)
	}
	writeTestFile(t, rootPath, "unsupported.txt", "    keep\n", 0o600)
	notApplied, err := TranslateForHost(t.Context(), Workspace{Root: root},
		fmt.Sprintf("in unsupported.txt\ntype %s %q\n", row(1, "    keep"), "keep\n"), t.TempDir())
	if err == nil || notApplied.Invocation.value.Recoveries != [recoveryKindCount]uint64{} {
		t.Fatalf("non-applied recovery = %v, error %v", notApplied.Invocation.value.Recoveries, err)
	}
}

func TestHostRecoveryMarkerValidationAndCombination(t *testing.T) {
	invocation := invocationMetrics{}
	invocation.Recoveries[recoveryWhitespace] = 2
	record, err := ClassifyHostMetrics(HostMetricInput{
		Invocation: InvocationMetrics{value: invocation},
		ToolCall:   &HostToolCall{PluginID: "builtin.shell", ToolName: "shell", Recovery: HostToolRecoveryCodeModeShell},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Invocation.value.Recoveries != [recoveryKindCount]uint64{2, 0, 0} ||
		record.hostRecoveries != [recoveryKindCount]uint64{0, 0, 1} {
		t.Fatalf("recovery origins were combined early: invocation %v, host %v",
			record.Invocation.value.Recoveries, record.hostRecoveries)
	}
	entry, err := record.entry()
	if err != nil || entry.Recoveries != [recoveryKindCount]uint64{2, 0, 1} {
		t.Fatalf("combined recoveries = %v, error %v", entry.Recoveries, err)
	}
	for _, call := range []*HostToolCall{
		{PluginID: "builtin.hpatch", ToolName: "hpatch", Recovery: HostToolRecoveryCodeModeShell},
		{PluginID: "builtin.shell", ToolName: "shell", Recovery: HostToolRecovery(99)},
	} {
		if _, err := ClassifyHostMetrics(HostMetricInput{ToolCall: call}); err == nil {
			t.Fatalf("accepted invalid recovery marker %+v", call)
		}
	}
}
