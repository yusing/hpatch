package hpatch

import (
	"strconv"
	"strings"
	"testing"
)

func TestWhitespaceAutofixCleansChangedTrailingWhitespaceOnly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "pre-existing  \nold\n", 0o644)

	script := "in file.txt\ntype " + row(2, "old") + " " + strconv.Quote("new \t  ")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "pre-existing  \nnew\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixPreservesAddedBinaryContent(t *testing.T) {
	root := t.TempDir()
	script := "new data.bin\ntype \"binary\\u0000payload  \\n\""

	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "data.bin"), "binary\x00payload  \n"; got != want {
		t.Fatalf("content = %q, want byte-exact %q", got, want)
	}
}

func TestWhitespaceAutofixPreservesUpdatedBinaryContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "data.bin", "\x00binary\nold\n", 0o644)

	script := "in data.bin\ntype " + row(2, "old") + " " + strconv.Quote("new  ")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "data.bin"), "\x00binary\nnew  \n"; got != want {
		t.Fatalf("content = %q, want byte-exact %q", got, want)
	}
}

func TestGitDefaultBinaryProbeUsesInitialEightThousandBytes(t *testing.T) {
	if content := strings.Repeat("x", gitBinaryProbeSize-1) + "\x00"; !isGitDefaultBinary(content) {
		t.Fatal("NUL at the end of the initial probe was not classified as binary")
	}
	if content := strings.Repeat("x", gitBinaryProbeSize) + "\x00"; isGitDefaultBinary(content) {
		t.Fatal("NUL after the initial probe was classified as binary")
	}
}

func TestWhitespaceAutofixCleansReorderedReplacementLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "X  \nA\n", 0o644)

	script := "in file.txt\ntype " + row(1, "X  ") + ".." + row(2, "A") + " " + strconv.Quote("A\nX  \n")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "A\nX\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixCleansInsertedDuplicateOnly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "X  \n", 0o644)

	script := "in file.txt\ntype- " + row(1, "X  ") + " " + strconv.Quote("A\nX  \n")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "A\nX\nX  \n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixCleansLineChangedByInlineDeletion(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "A  X\nuntouched  \n", 0o644)

	script := "in file.txt\ntype " + row(1, "A  X") + " \"X\" \"\""
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "A\nuntouched  \n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixLineDeletionDoesNotCleanRemainingLine(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "remove\nuntouched  \n", 0o644)

	script := "in file.txt\ntype " + row(1, "remove") + " \"\""
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "untouched  \n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixDoesNotCleanBetweenMultiOccurrenceReplacements(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\nuntouched  \nold\n", 0o644)

	script := "in file.txt\ntype " + row(1, "old") + " \"old\" 2 " + strconv.Quote("new  ")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "new\nuntouched  \nnew\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixRemovesSpaceBeforeTabInChangedIndent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)

	script := "in file.txt\ntype " + row(1, "old") + " " + strconv.Quote("  \tnew")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "\tnew\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixRemovesNewBlankLinesAtEOF(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)

	script := "in file.txt\ntype+ " + row(1, "alpha") + " " + strconv.Quote("beta\n \t\n")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "alpha\nbeta\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixPreservesCRLFLineTerminators(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\r\nkeep\r\n", 0o644)

	script := "in file.txt\ntype " + row(1, "old") + " " + strconv.Quote("new \t ")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got, want := readTestFile(t, root, "file.txt"), "new\r\nkeep\r\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWhitespaceAutofixCanMakeEditNoOp(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\n", 0o644)

	script := "in file.txt\ntype " + row(1, "alpha") + " " + strconv.Quote("alpha \t")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	if got := readTestFile(t, root, "file.txt"); got != "alpha\n" {
		t.Fatalf("content = %q, want unchanged", got)
	}
	if !strings.Contains(result.Report, "files add=0 update=0 move=0 delete=0\n") {
		t.Fatalf("report = %q, want no file change", result.Report)
	}
}

func TestWhitespaceAutofixFinalReferencesUseCleanedContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "alpha\nold\ngamma\n", 0o644)

	script := "in file.txt\ntype " + row(2, "old") + " " + strconv.Quote("  \tnew  ")
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, report %q", err, result.Report)
	}
	want := "2:" + hashLine("\tnew") + ` \tnew` + "\n"
	if !strings.Contains(result.Report, want) {
		t.Fatalf("report %q lacks cleaned final reference %q", result.Report, want)
	}
}
