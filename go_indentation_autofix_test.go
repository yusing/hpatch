package hpatch

import (
	"os"
	"strings"
	"testing"
)

func TestGoIndentationOnlyReplacementIsFormatted(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "file.go", "package p\n\nfunc f() {\n    return\n}\n", 0o644)

	result, err := applyForHostAtTest(t, rootPath,
		"in file.go\ntype "+row(4, "    return")+" \"  return\\n\"", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, diagnostic %q", err, result.Diagnostic)
	}
	if got, want := readTestFile(t, rootPath, "file.go"), "package p\n\nfunc f() {\n\treturn\n}\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestGoIndentationOnlyReplacementFormatsAfterMove(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "source.txt", "package p\n\nfunc f() {\n    return\n}\n", 0o644)

	result, err := applyForHostAtTest(t, rootPath,
		"in source.txt\ntype "+row(4, "    return")+" \"  return\\n\"\nmv moved.go", "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, diagnostic %q", err, result.Diagnostic)
	}
	if got, want := readTestFile(t, rootPath, "moved.go"), "package p\n\nfunc f() {\n\treturn\n}\n"; got != want {
		t.Fatalf("moved file = %q, want %q", got, want)
	}
	if _, err := os.Stat(rootPath + "/source.txt"); !os.IsNotExist(err) {
		t.Fatalf("source.txt still exists, stat error %v", err)
	}
}

func TestNonGoIndentationOnlyReplacementRejectsWithoutSuggestion(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "script.sh", "header\n\texit \"$status\"\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	command := "type " + row(2, "\texit \"$status\"") + ` "exit \"$status\"\n"`
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, "in script.sh\n"+command, t.TempDir())
	if err == nil {
		t.Fatal("indentation-only replacement unexpectedly succeeded")
	}
	if !strings.Contains(result.Diagnostic, "indentation-only change to preserved text") {
		t.Fatalf("diagnostic = %q", result.Diagnostic)
	}
}

func TestIndentationCorrectionUsesEarliestCommandAcrossFiles(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "first.txt", "header\n\tfirst\n", 0o644)
	writeTestFile(t, rootPath, "second.sh", "header\n\tsecond\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	script := "in first.txt\nin second.sh\ntype " + row(2, "\tsecond") + ` "second\n"` +
		"\nin first.txt\ntype " + row(2, "\tfirst") + ` "first\n"`
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err == nil {
		t.Fatal("indentation-only replacements unexpectedly succeeded")
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Command != 3 {
		t.Fatalf("rejections = %#v, want earliest command 3", result.Rejections)
	}
}

func TestIndentationCorrectionPrecedesLaterPathResolutionFailure(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "script.sh", "header\n\texit\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	script := "in script.sh\ntype " + row(2, "\texit") + ` "exit\n"` + "\nin missing.sh"
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err == nil {
		t.Fatal("path failure unexpectedly succeeded")
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Command != 2 {
		t.Fatalf("rejections = %#v, want pending command 2", result.Rejections)
	}
}

func TestNonGoIndentationCorrectionKeepsMutationPathAfterMove(t *testing.T) {
	rootPath := t.TempDir()
	writeTestFile(t, rootPath, "source.sh", "header\n\texit\n", 0o644)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	script := "in source.sh\ntype " + row(2, "\texit") + ` "exit\n"` + "\nmv moved.sh"
	result, err := TranslateForHost(t.Context(), Workspace{Root: root}, script, t.TempDir())
	if err == nil {
		t.Fatal("indentation-only replacement unexpectedly succeeded")
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Command != 2 ||
		result.Rejections[0].Path != "source.sh" {
		t.Fatalf("rejections = %#v, want command 2 at source.sh", result.Rejections)
	}
	if !strings.Contains(result.Diagnostic, "indentation-only change to preserved text") {
		t.Fatalf("diagnostic = %q", result.Diagnostic)
	}
}
