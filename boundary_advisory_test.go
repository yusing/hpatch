package mekugi

import (
	"strings"
	"testing"
)

func TestBoundaryAdvisoriesPreserveAuthoredBytes(t *testing.T) {
	for _, test := range []struct {
		name, before, edit, want, advisory string
	}{
		{"chomped empty row", "old\nnext\n", "type " + row(1, "old") + " <<PATCH-\n\nPATCH\n", "next\n",
			"owned=LF value=empty mode=<<PATCH- deletes=1 removes-ending=1"},
		{"text chomped empty row", "old\nnext\n", "type " + row(1, "old") + " <<TEXT-\n|\nTEXT\n", "next\n",
			"owned=LF value=empty mode=<<TEXT- deletes=1 removes-ending=1"},
		{"explicit deletion", "old\nnext\n", "type " + row(1, "old") + ` ""`, "next\n",
			"owned=LF value=empty deletes=1 removes-ending=1"},
		{"row preservation", "old\nnext\n", "type " + row(1, "old") + ` "new"`, "new\nnext\n",
			"owned=LF value=none preserves-ending=1"},
		{"range preservation", "old\r\nlast\r\nnext\r\n", "type " + row(1, "old") + ".." + row(2, "last") + ` "new"`, "new\r\nnext\r\n",
			"owned=CRLF value=none preserves-ending=1"},
		{"CR preservation", "old\rnext\r", "type " + row(1, "old") + ` "new"`, "new\rnext\r",
			"owned=CR value=none preserves-ending=1"},
		{"unterminated row", "old", "type " + row(1, "old") + " <<TEXT-\n|new\nTEXT\n", "new",
			"owned=none value=none mode=<<TEXT-"},
		{"literal newline removal", "old\nnext\n", `type "old\n" "new"`, "newnext\n",
			"owned=LF value=none removes-ending=1"},
		{"literal trailing newline", "old\nnext\n", "type \"old\" <<PATCH\nnew\nPATCH\n", "new\n\nnext\n",
			"owned=none value=LF mode=<<PATCH blank-after=1"},
		{"anchored literal", "old\nnext\n", "type " + row(1, "old") + " \"old\" <<TEXT\n|new\nTEXT\n", "new\n\nnext\n",
			"owned=none value=LF mode=<<TEXT blank-after=1"},
		{"insert before blank separator", "before\n \t\nnext\n", "add " + row(2, " \t") + ` "new\n"`, "before\nnew\n \t\nnext\n",
			"owned=none value=LF blank-after=1"},
		{"insert leading blank", "before\nnext\n", "add " + row(2, "next") + ` "\nnew\n"`, "before\n\nnew\nnext\n",
			"owned=none value=LF blank-before=1"},
		{"EOF joins unterminated line", "before", `add EOF "after"`, "beforeafter",
			"owned=none value=none joins-left=1"},
		{"multiple occurrences", "old\nold\n", `type "old" 2 "new\n"`, "new\n\nnew\n\n",
			"owned=none value=LF blank-after=2"},
		{"ordinary inline", "old\n", `type "old" "new"`, "new\n", ""},
		{"supplied row newline", "old\n", "type " + row(1, "old") + ` "new\n"`, "new\n", ""},
		{"unchanged row", "old\n", "type " + row(1, "old") + ` "old"`, "old\n", ""},
		{"split CRLF", "old\n", `type "old" "new\r"`, "new\r\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.before, 0o644)
			script := "in file.txt\n" + test.edit
			translated, err := translateForHostAtTest(t, root, script, "")
			if err != nil {
				t.Fatalf("translate: %v, %s", err, translated.Diagnostic)
			}
			applied, err := applyForHostAtTest(t, root, script, "")
			if err != nil || readTestFile(t, root, "file.txt") != test.want {
				t.Fatalf("apply: %v, %s; got %q, want %q", err, applied.Diagnostic, readTestFile(t, root, "file.txt"), test.want)
			}
			if translated.Report != applied.Report {
				t.Fatalf("apply/translate reports differ:\n%s\n%s", applied.Report, translated.Report)
			}
			if test.advisory == "" {
				if strings.Contains(applied.Report, "advisory ") {
					t.Fatalf("unexpected advisory: %s", applied.Report)
				}
			} else if !strings.Contains(applied.Report, ": baseline-boundary "+test.advisory+"\n") || strings.Count(applied.Report, "advisory ") != 1 {
				t.Fatalf("report lacks one expected advisory %q:\n%s", test.advisory, applied.Report)
			}
		})
	}
}

func TestBoundaryAdvisoriesFollowSuccessAndFinalPaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	script := "in file.txt\ntype " + row(1, "old") + " \"new\"\nmv moved.txt\n"
	result, err := translateForHostAtTest(t, root, script, "")
	if err != nil || !strings.Contains(result.Report, "advisory 2 type moved.txt:") {
		t.Fatalf("moved report: %v, %s", err, result.Report)
	}
	failed, err := translateForHostAtTest(t, root, script+"type \"missing\" \"value\"\n", "")
	if err == nil || failed.Report != "" || len(failed.Patch) != 0 {
		t.Fatalf("failure published success evidence: %v, %+v", err, failed)
	}
}

func TestBoundaryAdvisoriesRemainBaselineEvidence(t *testing.T) {
	for _, test := range []struct {
		name, path, before, script, want string
	}{
		{
			name: "neighbor removes observed separator", path: "file.txt",
			before: "old\nnext\n",
			script: "in file.txt\ntype \"old\" \"new\\n\"\ntype \"\\n\" \"\"\n",
			want:   "new\nnext\n",
		},
		{
			name: "formatter removes observed separator", path: "file.go",
			before: "package p\n\nvar old=1\n",
			script: "in file.go\ntype \"var old=1\" \"var newer=2\\n\"\n",
			want:   "package p\n\nvar newer = 2\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, test.path, test.before, 0o644)
			result, err := applyForHostAtTest(t, root, test.script, "")
			if err != nil || readTestFile(t, root, test.path) != test.want {
				t.Fatalf("apply: %v, %s; want %q", err, result.Diagnostic, test.want)
			}
			if !strings.Contains(result.Report, "advisory 2 type "+test.path+": baseline-boundary owned=none value=LF blank-after=1\n") {
				t.Fatalf("lost authored baseline observation: %s", result.Report)
			}
		})
	}
}

func TestEmptyMultilineInitializerIsNotReportedAsDeletion(t *testing.T) {
	root := t.TempDir()
	result, err := applyForHostAtTest(t, root, "new file.txt\ntype <<TEXT-\n|\nTEXT\n", "")
	if err != nil || readTestFile(t, root, "file.txt") != "" || strings.Contains(result.Report, "advisory ") {
		t.Fatalf("empty initializer: %v, %+v", err, result)
	}
}

func TestBlankBoundaryRecognizesTerminators(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        bool
	}{
		{"a\n", "\n", true}, {"a\r\n", " \t\r\n", true},
		{"a\r", "\r", true}, {"a\r", "\n", false},
		{"a\r", " \n", true}, {"a\n", " \tvalue\n", false},
		{"a", "\n", false}, {"a\n", "", false},
	} {
		if got := blankBoundary(test.left, test.right); got != test.want {
			t.Errorf("blankBoundary(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
	}
}
