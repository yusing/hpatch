package mekugi

import (
	"strings"
	"testing"

	"github.com/yusing/mekugi/internal/patchtest"
)

func TestTextFramingThroughPublicOperations(t *testing.T) {
	const body = "|type <<PATCH\n|PATCH\n|type <<TEXT-\n||body\n|TEXT\n"
	const value = "type <<PATCH\nPATCH\ntype <<TEXT-\n|body\nTEXT"
	for _, test := range []struct {
		name, script, want string
	}{
		{"initialize", "new file.txt\ntype <<TEXT-\n" + body + "TEXT\n", value},
		{"literal", "in file.txt\ntype \"old\" <<TEXT-\n" + body + "TEXT\n", value + "\nnext\n"},
		{"row", "in file.txt\ntype " + row(1, "old") + " <<TEXT\n" + body + "TEXT\n", value + "\nnext\n"},
		{"range", "in file.txt\ntype " + row(1, "old") + ".." + row(2, "next") + " <<TEXT-\n" + body + "TEXT\n", value + "\n"},
		{"anchored", "in file.txt\ntype " + row(1, "old") + " \"old\" <<TEXT-\n" + body + "TEXT\n", value + "\nnext\n"},
		{"insert", "in file.txt\nadd \"old\" <<TEXT\n" + body + "TEXT\n", value + "\nold\nnext\n"},
		{"append", "in file.txt\nadd EOF <<TEXT-\n" + body + "TEXT\n", "old\nnext\n" + value},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			before := map[string]string{}
			if test.name != "initialize" {
				before["file.txt"] = "old\nnext\n"
				writeTestFile(t, root, "file.txt", before["file.txt"], 0o644)
			}
			translated, err := translateForHostAtTest(t, root, test.script, "")
			if err != nil {
				t.Fatalf("translate: %v, %s", err, translated.Diagnostic)
			}
			tree, err := patchtest.Apply(before, string(translated.Patch))
			if err != nil || tree["file.txt"] != test.want {
				t.Fatalf("translated tree = %v, error %v; want %q", tree, err, test.want)
			}
			result, err := applyForHostAtTest(t, root, test.script, "")
			if err != nil || readTestFile(t, root, "file.txt") != test.want {
				t.Fatalf("apply: %v, %s; want %q", err, result.Diagnostic, test.want)
			}
		})
	}
}

func TestMalformedTextFrameRejectsWholeScript(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	script := "in file.txt\ntype \"old\" \"prepared\"\nadd EOF <<TEXT\n|first\nrm\nnew other.txt\ntype \"unintended\"\nTEXT\n"
	result, err := applyForHostAtTest(t, root, script, "")
	if err == nil || !strings.Contains(result.Diagnostic, "leading |") || strings.Count(result.Diagnostic, ": command") != 1 {
		t.Fatalf("expected one header-owned rejection: %v, %s", err, result.Diagnostic)
	}
	if tree := readTree(t, root); len(tree) != 1 || readTestFile(t, root, "file.txt") != "old\n" {
		t.Fatalf("rejection changed tree: %v", tree)
	}
}

