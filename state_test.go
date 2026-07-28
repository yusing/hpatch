package hpatch

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFinalStateReportMatchesNormalAndTranslateResults(t *testing.T) {
	const script = "in file.txt\ntsel 2 \"beta\"\ntype \"B\"\n"
	const wantReport = "in file.txt 2:2\n1 alpha\n2 B\n3 gamma\n"
	for _, args := range [][]string{nil, {"translate"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
			var stdout, stderr bytes.Buffer
			exitCode := Run(args, strings.NewReader(script), &stdout, &stderr, root, "")
			if exitCode != 0 || stderr.String() != wantReport {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
			if len(args) == 0 {
				if stdout.Len() != 0 || readTestFile(t, root, "file.txt") != "alpha\nB\ngamma\n" {
					t.Fatalf("normal result = stdout %q, file %q", stdout.String(), readTestFile(t, root, "file.txt"))
				}
			} else if !strings.Contains(stdout.String(), "*** Update File: file.txt\n") || readTestFile(t, root, "file.txt") != "alpha\nbeta\ngamma\n" {
				t.Fatalf("translate result = stdout %q, file %q", stdout.String(), readTestFile(t, root, "file.txt"))
			}
		})
	}
}

func TestFinalStateReportRepresentsSelectionMoveRemovalAndEmptyFile(t *testing.T) {
	tests := []struct {
		name, content, script, want string
	}{
		{name: "selection", content: "alpha\nβeta\ngamma\n", script: "in file.txt\ntsel 2 \"βeta\"", want: "in file.txt 2:1-2:5\n1 alpha\n2 βeta\n3 gamma\n"},
		{name: "moved path", content: "alpha\n", script: "in file.txt\nmv moved.txt", want: "in moved.txt 1:1\n1 alpha\n2 \n"},
		{name: "removed active file", content: "alpha\n", script: "in file.txt\nrm", want: "no active file\n"},
		{name: "new empty file", script: "new empty.txt", want: "in empty.txt 1:1\n1 \n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.content != "" {
				writeTestFile(t, root, "file.txt", test.content, 0o644)
			}
			var stdout, stderr bytes.Buffer
			if exitCode := Run(nil, strings.NewReader(test.script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 || stderr.String() != test.want {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestFinalStateCursorAffinity(t *testing.T) {
	tests := []struct {
		name, script, wantHeader, wantLine string
	}{
		{name: "type insertion", script: "in file.txt\ntype \"X\"", wantHeader: "in file.txt 1:2", wantLine: "1 Xabc"},
		{name: "type replacement", script: "in file.txt\ntsel 1 \"b\"\ntype \"XYZ\"", wantHeader: "in file.txt 1:5", wantLine: "1 aXYZc"},
		{name: "delete join", script: "in file.txt\ntsel 1 \"b\"\ndel", wantHeader: "in file.txt 1:2", wantLine: "1 ac"},
		{name: "after copy and paste", script: "in file.txt\ntsel 1 \"b\"\ncopy\npaste", wantHeader: "in file.txt 1:4", wantLine: "1 abbc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "abc\n", 0o644)
			var stdout, stderr bytes.Buffer
			if exitCode := Run(nil, strings.NewReader(test.script), &stdout, &stderr, root, ""); exitCode != 0 {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), test.wantHeader+"\n") || !strings.Contains(stderr.String(), test.wantLine+"\n") {
				t.Fatalf("report %q does not contain %q and %q", stderr.String(), test.wantHeader, test.wantLine)
			}
		})
	}
}

func TestEmptyTypePreservesAdjacentEditCursorAffinity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "ab\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel 1 \"b\"\ncopy\npaste\ntsel 1 \"b\"\ntype \"X\"\ntype \"\""
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if want := "in file.txt 1:3\n1 aXb\n2 \n"; stderr.String() != want {
		t.Fatalf("report = %q, want %q", stderr.String(), want)
	}
}

func TestFinalStatePreviewWindowTruncationAndControls(t *testing.T) {
	root := t.TempDir()
	longLine := "\t" + strings.Repeat("界", 70)
	writeTestFile(t, root, "file.txt", "one\ntwo\nthree\nfour\n"+longLine, 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel 5 " + jsonString(t, longLine) + "\ncopy\npaste"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	want := "in file.txt 5:143\n3 three\n4 four\n5 \\t" + strings.Repeat("界", 63) + "\n"
	if stderr.String() != want {
		t.Fatalf("report = %q, want %q", stderr.String(), want)
	}
}

func TestFinalStateReportWriteFailureDoesNotReverseEffect(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	var stdout bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in file.txt\ntsel 1 \"old\"\ntype \"new\""), &stdout, stateReportErrorWriter{}, root, "")
	if exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q", exitCode, stdout.String())
	}
	if got := readTestFile(t, root, "file.txt"); got != "new\n" {
		t.Fatalf("file = %q, want new", got)
	}
}

func TestFailureEmitsNoFinalStateReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"translate"}, strings.NewReader("in file.txt\ntsel 1 \"missing\""), &stdout, &stderr, root, "")
	if exitCode != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "hpatch:") || strings.Contains(stderr.String(), "\nin ") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

type stateReportErrorWriter struct{}

func (stateReportErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("report write failed")
}

var _ io.Writer = stateReportErrorWriter{}
