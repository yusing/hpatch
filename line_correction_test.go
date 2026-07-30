package hpatch

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestShiftedTextSelectionAutocorrectsWhenGloballyUnique(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\ntarget\nomega\n", 0o644)

	script := "in file.txt\ntsel 3 \"target\"\ntype \"fixed\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "alpha\nfixed\nomega\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	wantReport := "" +
		"in file.txt 2:6\n" +
		"last edit in file.txt: type 1 tsel match: 2:1-2:6\n" +
		"1|alpha\n" +
		"2|fixed\n" +
		"3|omega\n" +
		"repaired command 2 tsel line 3 to 2 in file.txt\n" +
		"1|alpha\n" +
		"2|fixed\n" +
		"3|omega\n"
	if stderr != wantReport {
		t.Fatalf("report = %q, want %q", stderr, wantReport)
	}
}

func TestShiftedTextSelectionAutocorrectsFromMissingLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\ntarget\n", 0o644)

	script := "in file.txt\ntsel 99 \"target\"\ntype \"fixed\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "alpha\nfixed\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if want := "repaired command 2 tsel line 99 to 2 in file.txt\n"; !strings.Contains(stderr, want) {
		t.Fatalf("report = %q, want substring %q", stderr, want)
	}
}

func TestShiftedMultiTextSelectionAutocorrectsOnlyCompleteUniqueSet(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\nmiddle\ntarget\n", 0o644)

	script := "in file.txt\ntsel 2 \"target\" 2\ntype \"fixed\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "fixed\nmiddle\nfixed\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	for _, want := range []string{
		"repaired command 2 tsel line 2 to 1 in file.txt\n",
		"1|fixed\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("report = %q, want substring %q", stderr, want)
		}
	}
}

func TestShiftedTextSelectionRemainsAtomicWhenAmbiguous(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\ntarget\nend\n", 0o644) //nolint:dupword // Repetition makes the shifted selection ambiguous.
	before := readTree(t, root)

	script := "in file.txt\ntsel 3 \"target\"\ntype \"fixed\"\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "found 0 of 1 requested matches") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
	}
	if strings.Contains(stderr, "repaired command") {
		t.Fatalf("ambiguous selection reported a repair: %q", stderr)
	}
}

func TestLineCorrectionContextMarksMultilineReplacementStart(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\nend\n", 0o644)

	script := "in file.txt\ntsel 2 \"target\"\ntype \"first\\nsecond\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "first\nsecond\nend\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	for _, want := range []string{
		"repaired command 2 tsel line 2 to 1 in file.txt\n",
		"1|first\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("report = %q, want substring %q", stderr, want)
		}
	}
	if !strings.Contains(stderr, "2|second\n") {
		t.Fatalf("report omitted the multiline replacement continuation: %q", stderr)
	}
}

func TestLineCorrectionContextSurvivesCommitAndLaterEdits(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "target\nend\n", 0o644)

	script := "in file.txt\ntsel 2 \"target\"\ntype \"fixed\"\ncommit\ntype \"prefix\\n\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "prefix\nfixed\nend\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
	for _, want := range []string{
		"repaired command 2 tsel line 2 to 1 in file.txt\n",
		"2|fixed\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("report = %q, want substring %q", stderr, want)
		}
	}
}

func TestLineCorrectionContextSurvivesFileSwitch(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "first.txt", "target\nend\n", 0o644)
	writeTestFile(t, root, "second.txt", "old\n", 0o644)

	script := "in first.txt\ntsel 2 \"target\"\ntype \"fixed\"\nin second.txt\ntsel 1 \"old\"\ntype \"new\"\n"
	stdout, stderr, exitCode := runWithStateReport(root, script)
	if exitCode != 0 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, want := range []string{
		"in second.txt 1:4\n",
		"repaired command 2 tsel line 2 to 1 in first.txt\n",
		"1|fixed\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("report = %q, want substring %q", stderr, want)
		}
	}
}

func runWithStateReport(root, script string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, "")
	return stdout.String(), stderr.String(), exitCode
}
