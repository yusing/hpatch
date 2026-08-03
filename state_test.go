package hpatch

import (
	"strings"
	"testing"
)

func TestHPatch2FinalStateReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\ngamma\n", 0o644)
	script := "in file.txt\ntype " + row(2, "beta") + ` "B"`
	_, report, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	for _, fragment := range []string{
		"in file.txt\n",
		"last type file.txt 1 ranges 2:1-3:1\n",
		"files add=0 update=1 move=0 delete=0\n",
		"1:" + hashLine("alpha") + " alpha\n",
		"2:" + hashLine("B") + " B\n",
		"3:" + hashLine("gamma") + " gamma\n",
	} {
		if !strings.Contains(report, fragment) {
			t.Fatalf("report %q lacks %q", report, fragment)
		}
	}
}

func TestHPatch2FormattedPreviewTracksEditedContent(t *testing.T) {
	root := t.TempDir()
	before := "package p\n\nvar ( a=1; b=2; c=3; d=4 )\n\nvar filler1=1\nvar filler2=2\nvar target=1\n"
	writeTestFile(t, root, "file.go", before, 0o644)
	script := "in file.go\ntype " + row(7, "var target=1") + ` "var target=2"`
	_, report, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	want := "12:" + hashLine("var target = 2") + " var target = 2\n"
	if !strings.Contains(report, want) {
		t.Fatalf("formatted preview %q lacks edited row %q", report, want)
	}
}

func TestHPatch2FinalStateReportIsBounded(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x x x x\n", 0o644)
	script := "in file.txt\ntype " + row(1, "x x x x") + ` "x" 4 "y"`
	_, report, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	if !strings.Contains(report, "last type file.txt 4 ranges ") || !strings.Contains(report, " +1 more\n") {
		t.Fatalf("report = %q", report)
	}
}

func TestHPatch2InsertionReportNamesTargetRange(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nbeta\n", 0o644)
	script := "in file.txt\ntype+ " + row(1, "alpha") + ` "inserted\n"`
	_, report, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	if !strings.Contains(report, "last type+ file.txt 1 ranges 1:1-2:1\n") {
		t.Fatalf("report = %q", report)
	}
}

func TestHPatch2PreviewDoesNotInventTrailingEmptyLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)
	_, report, exitCode := runForTest(root, nil, "in file.txt")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	want := "in file.txt\nlast none\nfiles add=0 update=0 move=0 delete=0\n1:" + hashLine("alpha") + " alpha\n"
	if report != want {
		t.Fatalf("report = %q, want %q", report, want)
	}
}

func TestHPatch2EmptyNewFileReport(t *testing.T) {
	root := t.TempDir()
	_, report, exitCode := runForTest(root, nil, "new empty.txt")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	want := "in empty.txt\nlast none\nfiles add=1 update=0 move=0 delete=0\n1:" + hashLine("") + " \n"
	if report != want {
		t.Fatalf("report = %q, want %q", report, want)
	}
}

func TestHPatch2MovedMutationReportUsesFinalPath(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "old.txt", "alpha\n", 0o644)
	script := "in old.txt\ntype " + row(1, "alpha") + ` "beta"` + "\nmv new.txt"
	_, report, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	for _, want := range []string{
		"in new.txt\n",
		"last type new.txt 1 ranges 1:1-2:1\n",
		"files add=0 update=1 move=1 delete=0\n",
		"1:" + hashLine("beta") + " beta\n",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q lacks %q", report, want)
		}
	}
}

func TestHPatch2PreviewBoundsContent(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("界", 70) + "tail\n"
	writeTestFile(t, root, "file.txt", content, 0o644)
	_, report, exitCode := runForTest(root, nil, "in file.txt")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	rowText := strings.Split(strings.TrimSuffix(report, "\n"), "\n")[3]
	if strings.Count(rowText, "界") != 64 || strings.Contains(rowText, "tail") {
		t.Fatalf("bounded preview row = %q", rowText)
	}
}

func TestHPatch2PreviewEscapesControls(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "a\x01b\n", 0o644)
	_, report, exitCode := runForTest(root, nil, "in file.txt")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	if strings.ContainsRune(report, '\x01') || !strings.Contains(report, `a\x01b`) {
		t.Fatalf("escaped report = %q", report)
	}
}

func TestHPatch2FinalStateNoActiveFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)
	_, report, exitCode := runForTest(root, nil, "in file.txt\nrm")
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, report %q", exitCode, report)
	}
	want := "no active file\nlast none\nfiles add=0 update=0 move=0 delete=1\n"
	if report != want {
		t.Fatalf("report = %q, want %q", report, want)
	}
}
