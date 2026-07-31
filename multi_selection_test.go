package hpatch

import (
	"bytes"
	"strings"
	"testing"
)

func TestMultiSelectionFinalStateReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "a=x\nb=x\n", 0o644)

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in file.txt\ntsel "+hashLine("a=x")+" \"x\" 2\ncopy\n"), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 2 selections: 1:3-1:4, 2:3-2:4\nfiles: no changes\n#|a=x\n#|b=x\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestMultiSelectionFinalStateReportBoundsLocations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x 1\nx 2\nx 3\nx 4\nx 5\n", 0o644)

	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in file.txt\ntsel "+hashLine("x 1")+" \"x\" 5\ncopy\n"), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 5 selections: 1:1-1:2, 2:1-2:2, 3:1-3:2, … +2\n" +
		"files: no changes\n" +
		"#|x 1\n" +
		"#|x 2\n" +
		"#|x 3\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestMultiSelectionLinewisePaste(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "tail", 0o644)
	writeTestFile(t, root, "destination.txt", "beforeafter beforeafter\n", 0o644)

	script := "in source.txt\nrsel " + hashLine("tail") + " " + hashLine("tail") + "\ncopy\nin destination.txt\ntsel " + hashLine("beforeafter beforeafter") + " \"before\" 2\npaste\n" //nolint:dupword // A one-line rsel repeats its unique endpoint hash.
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "destination.txt"), "before\ntail\nafter before\ntail\nafter\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestMultiSelectionReportProjectsPriorDisjointEdit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "top\nx\nmiddle\nx\nbottom\n", 0o644)

	script := "in file.txt\ntsel " + hashLine("top") + " \"top\"\ntype \"top\\nextra\"\ntsel " + hashLine("top") + " \"x\" 2\ncopy\n"
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 2 selections: 3:1-3:2, 5:1-5:2\n" +
		"last edit in file.txt: type 1 tsel match: 1:1-2:6\n" +
		"#|x\n" +
		"#|x\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}
