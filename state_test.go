package hpatch

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFinalStateReportMatchesNormalAndTranslateResults(t *testing.T) {
	const script = "in file.txt\ntsel f44e \"beta\"\ntype \"B\"\n"
	const wantReport = "in file.txt 2:2\nlast edit in file.txt: type 1 tsel match: 2:1-2:2\n#|alpha\n#|B\n#|gamma\n"
	for _, args := range [][]string{nil, {"translate"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
			var stdout, stderr bytes.Buffer
			exitCode := Run(args, strings.NewReader(script), &stdout, &stderr, root, "")
			if exitCode != 0 || normalizeHashlineRows(stderr.String()) != wantReport {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
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
		{name: "selection", content: "alpha\nβeta\ngamma\n", script: "in file.txt\ntsel " + hashLine("βeta") + " \"βeta\"", want: "in file.txt 2:1-2:5\nfiles: no changes\n#|alpha\n#|βeta\n#|gamma\n"},
		{name: "moved path", content: "alpha\n", script: "in file.txt\nmv moved.txt", want: "in moved.txt 1:1\nfiles: 1 moved\n#|alpha\n#|\n"},
		{name: "removed active file", content: "alpha\n", script: "in file.txt\nrm", want: "no active file\nfiles: 1 deleted\n"},
		{name: "new empty file", script: "new empty.txt", want: "in empty.txt 1:1\nfiles: 1 added\n#|\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.content != "" {
				writeTestFile(t, root, "file.txt", test.content, 0o644)
			}
			var stdout, stderr bytes.Buffer
			if exitCode := Run(nil, strings.NewReader(test.script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 || normalizeHashlineRows(stderr.String()) != test.want {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()), test.want)
			}
		})
	}
}

func TestFinalStateCursorAffinity(t *testing.T) {
	tests := []struct {
		name, script, wantHeader, wantLine string
	}{
		{name: "type insertion", script: "in file.txt\ntype \"X\"", wantHeader: "in file.txt 1:2", wantLine: "#|Xabc"},
		{name: "type replacement", script: "in file.txt\ntsel " + hashLine("abc") + " \"b\"\ntype \"XYZ\"", wantHeader: "in file.txt 1:5", wantLine: "#|aXYZc"},
		{name: "delete join", script: "in file.txt\ntsel " + hashLine("abc") + " \"b\"\ndel", wantHeader: "in file.txt 1:2", wantLine: "#|ac"},
		{name: "after copy and paste", script: "in file.txt\ntsel " + hashLine("abc") + " \"b\"\ncopy\npaste", wantHeader: "in file.txt 1:4", wantLine: "#|abbc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "abc\n", 0o644)
			var stdout, stderr bytes.Buffer
			if exitCode := Run(nil, strings.NewReader(test.script), &stdout, &stderr, root, ""); exitCode != 0 {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
			}
			if !strings.HasPrefix(normalizeHashlineRows(stderr.String()), test.wantHeader+"\n") || !strings.Contains(normalizeHashlineRows(stderr.String()), test.wantLine+"\n") {
				t.Fatalf("report %q does not contain %q and %q", normalizeHashlineRows(stderr.String()), test.wantHeader, test.wantLine)
			}
		})
	}
}

func TestEmptyTypePreservesAdjacentEditCursorAffinity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "ab\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine("ab") + " \"b\"\ncopy\npaste\ntsel " + hashLine("ab") + " \"b\"\ntype \"X\"\ntype \"\""
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	if want := "in file.txt 1:3\nlast edit in file.txt: type 1 tsel match: 1:2-1:3\n#|aXb\n#|\n"; normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStatePreviewWindowTruncationAndControls(t *testing.T) {
	root := t.TempDir()
	longLine := "\t" + strings.Repeat("界", 70)
	writeTestFile(t, root, "file.txt", "one\ntwo\nthree\nfour\n"+longLine, 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine(longLine) + " " + jsonString(t, longLine) + "\ncopy\npaste"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 5:143\nlast edit in file.txt: paste 1 tsel match: 5:72-5:143\n#|three\n#|four\n#|\t" + strings.Repeat("界", 63) + "\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportSummarizesDisjointTextEdits(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "func foo() {\n  bar := 0\n  middle()\n  baz := 0\n}\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine("func foo() {") + " \":= 0\" 2\ntype \"=\"\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 4:8\n" +
		"last edit in file.txt: type 2 tsel matches: 2:7-2:8, 4:7-4:8\n" +
		"#|  bar =\n" +
		"#|  baz =\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportBoundsEditLocations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x 1\nx 2\nx 3\nx 4\nx 5\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine("x 1") + " \"x\" 5\ntype \"y\"\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 5:2\n" +
		"last edit in file.txt: type 5 tsel matches: 1:1-1:2, 2:1-2:2, 3:1-3:2, … +2\n" +
		"#|y 1\n" +
		"#|y 2\n" +
		"#|y 3\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportUsesEditLabelWithoutTextSelector(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "abc\n", 0o644)
	var stdout, stderr bytes.Buffer
	anchor := hashLine("abc")
	script := "in file.txt\nrsel " + anchor + " " + anchor + "\ndel\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 1:1\nlast edit in file.txt: del 1 edit: 1:1\n#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportSummarizesChangedNonActiveFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "changed.txt", "old\n", 0o644)
	writeTestFile(t, root, "active.txt", "unchanged\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in changed.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\nin active.txt\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in active.txt 1:1\n" +
		"last edit in changed.txt: type 1 tsel match: 1:1-1:4\n" +
		"files: 1 updated\n" +
		"#|new\n" +
		"#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportSummarizesNetFileActions(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"updated.txt", "moved.txt", "both.txt", "deleted.txt"} {
		writeTestFile(t, root, path, "old\n", 0o644)
	}
	var stdout, stderr bytes.Buffer
	script := "in updated.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\n" +
		"in moved.txt\nmv moved-final.txt\n" +
		"in both.txt\nmv both-final.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\n" +
		"new added.txt\ntype \"new\\n\"\n" +
		"in deleted.txt\nrm\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "no active file\n" +
		"last edit in added.txt: type 1 edit: 1:1-2:1\n" +
		"files: 1 updated, 1 moved, 1 moved+updated, 1 added, 1 deleted\n" +
		"#|new\n" +
		"#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportPreservesEditAcrossCommitAndMove(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\ncommit\nmv moved.txt\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in moved.txt 1:1\n" +
		"last edit in moved.txt: type 1 tsel match: 1:1-1:4\n" +
		"files: 1 moved+updated\n" +
		"#|new\n" +
		"#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportProjectsLastEditThroughPriorEdit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "top\nx\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in file.txt\ntsel " + hashLine("top") + " \"top\"\ntype \"top\\nextra\"\ntsel " + hashLine("x") + " \"x\"\ntype \"y\"\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "in file.txt 3:2\n" +
		"last edit in file.txt: type 1 tsel match: 3:1-3:2\n" +
		"#|extra\n" +
		"#|y\n" +
		"#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportFallsBackAfterLatestEditedFileIsRemoved(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "kept.txt", "old\n", 0o644)
	var stdout, stderr bytes.Buffer
	script := "in kept.txt\ntsel " + hashLine("old") + " \"old\"\ntype \"new\"\n" +
		"new discarded.txt\ntype \"discarded\"\nrm\n"
	if exitCode := Run(nil, strings.NewReader(script), &stdout, &stderr, root, ""); exitCode != 0 || stdout.Len() != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
	want := "no active file\n" +
		"last edit in kept.txt: type 1 tsel match: 1:1-1:4\n" +
		"files: 1 updated\n" +
		"#|new\n" +
		"#|\n"
	if normalizeHashlineRows(stderr.String()) != want {
		t.Fatalf("report = %q, want %q", normalizeHashlineRows(stderr.String()), want)
	}
}

func TestFinalStateReportWriteFailureDoesNotReverseEffect(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	var stdout bytes.Buffer
	exitCode := Run(nil, strings.NewReader("in file.txt\ntsel "+hashLine("old")+" \"old\"\ntype \"new\""), &stdout, stateReportErrorWriter{}, root, "")
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
	exitCode := Run([]string{"translate"}, strings.NewReader("in file.txt\ntsel "+hashLine("old")+" \"missing\""), &stdout, &stderr, root, "")
	if exitCode != 1 || stdout.Len() != 0 || !strings.HasPrefix(normalizeHashlineRows(stderr.String()), "hpatch:") || strings.Contains(normalizeHashlineRows(stderr.String()), "\nin ") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), normalizeHashlineRows(stderr.String()))
	}
}

func TestFinalStateReportUsesHashlineRows(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)
	var stdout, stderr bytes.Buffer
	if exitCode := Run(nil, strings.NewReader("in file.txt\n"), &stdout, &stderr, root, ""); exitCode != 0 {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	const want = "in file.txt 1:1\nfiles: no changes\n8ed3: alpha\ne3b0: \n"
	if stderr.String() != want {
		t.Fatalf("report = %q, want %q", stderr.String(), want)
	}
}

type stateReportErrorWriter struct{}

func (stateReportErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("report write failed")
}

var _ io.Writer = stateReportErrorWriter{}
