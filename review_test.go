package mekugi

import (
	"strings"
	"testing"
)

func TestReviewFiles(t *testing.T) {
	files := reviewFiles([]change{
		{kind: changeDelete, originalPath: "old.txt", original: "removed\n"},
		{kind: changeAdd, path: "empty.txt"},
		{kind: changeUpdate, originalPath: "before.txt", path: "after.txt", original: "unchanged\n", content: "unchanged\n"},
		{kind: changeUpdate, originalPath: "end.txt", path: "end.txt", original: "a\r\nb", content: "a\nb\n"},
	})
	if len(files) != 4 {
		t.Fatalf("files = %#v", files)
	}
	for i, want := range []string{"-removed\n", `add "" -> "empty.txt"`, `move "before.txt" -> "after.txt"`, "-a\r\n-b\n\\ No newline at end of file\n+a\n+b\n"} {
		if !strings.Contains(files[i].Diff, want) {
			t.Errorf("file %d: %q missing %q", i, files[i].Diff, want)
		}
	}
	if strings.Contains(files[2].Diff, "@@") {
		t.Fatal("pure move should not invent content edits")
	}
}

func TestHostReviewCapturesFormattedState(t *testing.T) {
	root := t.TempDir()
	result, err := TranslateForHostAt(t.Context(), root, "new sample.go\ntype \"package sample\\nvar X=1\\n\"\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReviewFiles) != 1 || !strings.Contains(result.ReviewFiles[0].Diff, "+var X = 1\n") {
		t.Fatalf("review = %#v", result.ReviewFiles)
	}
	rejected, err := TranslateForHostAt(t.Context(), root, "new sample.go\ntype \"invalid go\"\n", "")
	if err == nil || len(rejected.ReviewFiles) != 0 {
		t.Fatalf("rejected review = %#v, err = %v", rejected.ReviewFiles, err)
	}
}
