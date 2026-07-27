package hpatch

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baselineDefinition = "Apply a patch. Uses *** Begin Patch context hunks."

// definitionEnvironment values are read from the process environment, so tests
// set them through t.Setenv and rely on its per-test restore.
func withDefinitionEnvironment(t *testing.T, session string) {
	t.Helper()
	t.Setenv(sessionEnvironment, session)
	t.Setenv(definitionEnvironment, "hpatch tool definition text that a host installs")
	t.Setenv(baselineDefinitionEnvironment, baselineDefinition)
}

func TestDefinitionCountsOncePerSession(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withDefinitionEnvironment(t, "session-one")

	script := "in note.txt\nrsel 1:1\ntype \"beta\\n\"\n"
	for attempt := range 3 {
		var stdout, stderr bytes.Buffer
		if exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
			t.Fatalf("attempt %d = exit %d, stderr %q", attempt, exitCode, stderr.String())
		}
	}

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1 across repeated invocations", got.Sessions)
	}
	single, baseline, err := definitionTokens(os.Getenv(definitionEnvironment), baselineDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefinitionInputTokens != single || got.BaselineDefinitionInputTokens != baseline {
		t.Fatalf("definition tokens = (%d, %d), want (%d, %d)", got.DefinitionInputTokens, got.BaselineDefinitionInputTokens, single, baseline)
	}

	// A distinct session pays the definition again.
	t.Setenv(sessionEnvironment, "session-two")
	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("second session = exit %d, stderr %q", exitCode, stderr.String())
	}
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 2 || got.DefinitionInputTokens != 2*single {
		t.Fatalf("second session = (%d sessions, %d tokens), want (2, %d)", got.Sessions, got.DefinitionInputTokens, 2*single)
	}
}

func TestDefinitionUnmeasuredWithoutSession(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sessionEnvironment, "")
	t.Setenv(definitionEnvironment, "hpatch tool definition text")
	t.Setenv(baselineDefinitionEnvironment, baselineDefinition)

	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader("in note.txt\nrsel 1:1\ntype \"beta\\n\"\n"), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr.String())
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 0 || got.DefinitionInputTokens != 0 || got.definitionOverhead() != 0 {
		t.Fatalf("unmeasured definition = %+v", got)
	}
	if !strings.Contains(gainReport(got), "not measured; host set no "+sessionEnvironment) {
		t.Fatal("gain does not disclose that definitions were unmeasured")
	}
}

func TestDefinitionOverheadCreditsLargerBaseline(t *testing.T) {
	value := metrics{DefinitionInputTokens: 100, BaselineDefinitionInputTokens: 250}
	if got := value.definitionOverhead(); got != 0 {
		t.Fatalf("overhead = %d, want 0 when baseline definition is larger", got)
	}
	value = metrics{DefinitionInputTokens: 400, BaselineDefinitionInputTokens: 150}
	if got := value.definitionOverhead(); got != 250 {
		t.Fatalf("overhead = %d, want 250", got)
	}
}

func TestBaselineCreditOnlyForAnalogousFailures(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A selector that overruns the file has no apply_patch analogue.
	var stderr bytes.Buffer
	if exitCode := Run(nil, strings.NewReader("in note.txt\nsel 99 1:2\ntype \"x\"\n"), &bytes.Buffer{}, &stderr, root, dataDirectory); exitCode == 0 {
		t.Fatal("out-of-range selector unexpectedly succeeded")
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.AttributableFailures != 1 || got.BaselineFailures != 0 {
		t.Fatalf("selector failure = (%d attributable, %d baseline), want (1, 0)", got.AttributableFailures, got.BaselineFailures)
	}

	// A missing file would have failed a direct apply_patch call too.
	if exitCode := Run(nil, strings.NewReader("in absent.txt\nrsel 1:1\ntype \"x\"\n"), &bytes.Buffer{}, &stderr, root, dataDirectory); exitCode == 0 {
		t.Fatal("missing file unexpectedly succeeded")
	}
	got, err = readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.AttributableFailures != 1 || got.BaselineFailures != 1 {
		t.Fatalf("missing-file failure = (%d attributable, %d baseline), want (1, 1)", got.AttributableFailures, got.BaselineFailures)
	}
}

func TestBaselineCreditRaisesOverallReduction(t *testing.T) {
	// Two effective invocations averaging 50 baseline tokens each, plus one
	// analogous failure, credit the baseline one mean payload.
	value := metrics{
		HPatchTokens:         40,
		ApplyPatchTokens:     100,
		EffectiveInvocations: 2,

		IneffectiveHPatchTokens: 30,
		BaselineFailures:        1,
	}
	if got := value.meanApplyPatchTokens(); got != 50 {
		t.Fatalf("mean apply_patch = %f, want 50", got)
	}
	if got := value.baselineOutputTokens(); got != 150 {
		t.Fatalf("baseline output = %f, want 150", got)
	}
	// (150 - 70) / 150 = 53.3%, versus (100 - 70) / 100 = 30% uncredited.
	if got := value.overallReduction(); got < 53.3 || got > 53.4 {
		t.Fatalf("overall reduction = %f, want ~53.3", got)
	}

	uncredited := value
	uncredited.BaselineFailures = 0
	uncredited.AttributableFailures = 1
	if got := uncredited.overallReduction(); got < 29.9 || got > 30.1 {
		t.Fatalf("uncredited overall reduction = %f, want ~30", got)
	}
}

func TestWeightedReductionChargesDefinitionOverhead(t *testing.T) {
	value := metrics{
		HPatchTokens:         40,
		ApplyPatchTokens:     100,
		EffectiveInvocations: 1,
		ReportInputTokens:    10,

		Sessions:                      1,
		DefinitionInputTokens:         1000,
		BaselineDefinitionInputTokens: 0,
	}
	// (100 - (40 + 1010/5)) / 100 = -142.0%.
	if got := value.weightedOverallReduction(5); got < -142.1 || got > -141.9 {
		t.Fatalf("weighted reduction at 5:1 = %f, want ~-142", got)
	}
	// A host whose native tool costs the same yields no definition overhead.
	value.BaselineDefinitionInputTokens = 1000
	if got := value.weightedOverallReduction(5); got < 57.9 || got > 58.1 {
		t.Fatalf("weighted reduction with displaced definition = %f, want ~58", got)
	}
}

func TestCommandReasonsAttributeErrorsToCommands(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exitCode := Run(nil, strings.NewReader("in note.txt\nsel 99 1:2\ntype \"x\"\n"), &bytes.Buffer{}, &bytes.Buffer{}, root, dataDirectory); exitCode == 0 {
		t.Fatal("out-of-range selector unexpectedly succeeded")
	}
	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommandReasons[commandOperationIndex("sel")][reasonCoordinateBounds] != 1 {
		t.Fatalf("sel coordinate-bounds = %d, want 1", got.CommandReasons[commandOperationIndex("sel")][reasonCoordinateBounds])
	}
	report := gainReport(got)
	if !strings.Contains(report, "sel      coordinate-bounds  1") {
		t.Fatalf("gain report lacks attributed error row: %q", report)
	}
	if !strings.Contains(gainReport(metrics{}), "none     none    0") {
		t.Fatalf("empty gain report lacks none row: %q", gainReport(metrics{}))
	}
}
