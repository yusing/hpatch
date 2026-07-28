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
	exitCode := Run(nil, strings.NewReader("in file.txt\ntsel 1 \"x\" 2\ncopy\n"), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	want := "in file.txt 2 selections 1:3-2:4\n1 a=x\n2 b=x\n3 \n"
	if stderr.String() != want {
		t.Fatalf("report = %q, want %q", stderr.String(), want)
	}
}

func TestMultiSelectionLinewisePaste(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "source.txt", "tail", 0o644)
	writeTestFile(t, root, "destination.txt", "beforeafter beforeafter\n", 0o644)

	script := "in source.txt\nrsel 1:1\ncopy\nin destination.txt\ntsel 1 \"before\" 2\npaste\n"
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

	script := "in file.txt\ntsel 1 \"top\"\ntype \"top\\nextra\"\ntsel 2 \"x\" 2\ncopy\n"
	var stdout, stderr bytes.Buffer
	exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	want := "in file.txt 2 selections 3:1-5:2\n2 extra\n3 x\n4 middle\n"
	if stderr.String() != want {
		t.Fatalf("report = %q, want %q", stderr.String(), want)
	}
}
