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

func TestDefinitionCountsEveryInvocationAndSessionOnce(t *testing.T) {
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
	if got.DefinitionInputTokens != 3*single || got.BaselineDefinitionInputTokens != 3*baseline {
		t.Fatalf("definition tokens = (%d, %d), want (%d, %d)", got.DefinitionInputTokens, got.BaselineDefinitionInputTokens, 3*single, 3*baseline)
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
	if got.Sessions != 2 || got.DefinitionInputTokens != 4*single {
		t.Fatalf("second session = (%d sessions, %d tokens), want (2, %d)", got.Sessions, got.DefinitionInputTokens, 4*single)
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
	if got.Sessions != 0 || got.DefinitionInputTokens != 0 {
		t.Fatalf("unmeasured definition = %+v", got)
	}
	if !strings.Contains(gainReport(got), "not measured (missing "+sessionEnvironment+")") {
		t.Fatal("gain does not disclose that definitions were unmeasured")
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
	if got := value.overallReduction(); got < 36.3 || got > 36.4 {
		t.Fatalf("overall reduction = %f, want ~36.36", got)
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
	if !strings.Contains(report, "failed apply_patch output is the empty carrier emitted by the router.") {
		t.Fatalf("gain report does not identify the failed-call carrier: %q", report)
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
