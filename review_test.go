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

func TestReviewPresentation(t *testing.T) {
	files := reviewFiles([]change{
		{kind: changeUpdate, originalPath: "file.txt", path: "file.txt", original: "--old\n", content: "++new\n"},
		{kind: changeAdd, path: "new.txt", content: "new"},
		{kind: changeDelete, originalPath: "gone.txt", original: "gone\n"},
		{kind: changeAdd, path: "empty.txt"},
		{kind: changeDelete, originalPath: "empty-old.txt"},
		{kind: changeUpdate, originalPath: "old.txt", path: "renamed.txt", original: "same\n", content: "same\n"},
	})
	for i, want := range []string{
		"update \"file.txt\" +1 -1\n",
		"add \"new.txt\" +1 -0\n",
		"delete \"gone.txt\" +0 -1\n",
		"add \"empty.txt\" +0 -0\n",
		"delete \"empty-old.txt\" +0 -0\n",
		"move \"old.txt\" -> \"renamed.txt\" +0 -0\n",
	} {
		if got := files[i].Summary(); got != want {
			t.Errorf("summary %d = %q; want %q", i, got, want)
		}
		diff := files[i].UnifiedDiff()
		if i < 3 && !strings.HasPrefix(diff, "--- ") {
			t.Errorf("unified headers missing: %q", diff)
		}
		if i >= 3 && diff != files[i].Diff {
			t.Errorf("lost header-only change: %q", diff)
		}
		// Already compact records must render identically.
		file := files[i]
		file.Diff = diff
		if file.UnifiedDiff() != diff || file.Summary() != want {
			t.Errorf("presentation is not stable: %#v", file)
		}
	}
}
