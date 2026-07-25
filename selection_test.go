package hpatch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hpatch/internal/patchtest"
)

func TestLinewiseReplacementPreservesFinalTerminator(t *testing.T) {
	tests := []struct {
		name        string
		initial     string
		replacement string
		want        string
	}{
		{name: "LF", initial: "one\nold\nnext\n", replacement: "replacement", want: "one\nreplacement\nnext\n"},
		{name: "CRLF", initial: "one\r\nold\r\nnext\r\n", replacement: "replacement", want: "one\r\nreplacement\r\nnext\r\n"},
		{name: "standalone CR", initial: "one\rold\rnext\r", replacement: "replacement", want: "one\rreplacement\rnext\r"},
		{name: "unterminated final line", initial: "one\nold", replacement: "replacement", want: "one\nreplacement"},
		{name: "explicit terminator is not doubled", initial: "one\nold\nnext\n", replacement: "replacement\n", want: "one\nreplacement\nnext\n"},
		{name: "explicit terminator overrides source style", initial: "one\nold\nnext\n", replacement: "replacement\r\n", want: "one\nreplacement\r\nnext\n"},
		{name: "multiline replacement inherits final terminator", initial: "one\nold\nnext\n", replacement: "first\nsecond", want: "one\nfirst\nsecond\nnext\n"},
		{name: "empty replacement leaves an empty logical line", initial: "one\nold\nnext\n", replacement: "", want: "one\n\nnext\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.initial, 0o644)
			script := "in file.txt\nrsel 2:2\ntype " + jsonString(t, test.replacement) + "\n"
			stdout, stderr, exitCode := runForTest(root, nil, script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, root, "file.txt"); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLinewiseDeleteStillOwnsFinalTerminator(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "one\ntwo\nthree\nfour\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, "in file.txt\nrsel 2:3\ndel\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "one\nfour\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestLinewiseReplacementTranslateMatchesNormalResult(t *testing.T) {
	initial := map[string]string{"file.txt": "one\ntwo\nthree\nfour\n"}
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", initial["file.txt"], 0o644)
	script := "in file.txt\nrsel 2:3\ntype \"replacement\"\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "file.txt"); got != initial["file.txt"] {
		t.Fatalf("translate mutated source: %q", got)
	}
	got, err := patchtest.Apply(initial, stdout)
	if err != nil {
		t.Fatalf("applying translation: %v\n%s", err, stdout)
	}
	want := map[string]string{"file.txt": "one\nreplacement\nfour\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v", got, want)
	}
}

func TestBlockSelectionUsesCurrentUnambiguousContent(t *testing.T) {
	initial := map[string]string{"file.txt": "title\nBEGIN old\nbody\nEND\nfooter\n"}
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", initial["file.txt"], 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"tsel 1 1 \"title\"",
		"type \"title\\ninserted\"",
		"bsel \"BEGIN old\" \"END\"",
		"type \"BEGIN new\\nchanged\\nEND\"",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr)
	}
	if got := readTestFile(t, root, "file.txt"); got != initial["file.txt"] {
		t.Fatalf("translate mutated source: %q", got)
	}
	got, err := patchtest.Apply(initial, stdout)
	if err != nil {
		t.Fatalf("applying translation: %v\n%s", err, stdout)
	}
	want := map[string]string{"file.txt": "title\ninserted\nBEGIN new\nchanged\nEND\nfooter\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %#v, want %#v", got, want)
	}
}

func TestBlockSelectionFailuresAreAtomic(t *testing.T) {
	tests := []struct {
		name, content, script, wantFragment string
	}{
		{name: "missing start", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"absent\" \"END\"", wantFragment: "start literal \"absent\" occurs 0 times"},
		{name: "missing end", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" \"absent\"", wantFragment: "end literal \"absent\" occurs 0 times"},
		{name: "unrelated duplicate start", content: "BEGIN\nEND\nunrelated BEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "start literal \"BEGIN\" occurs 2 times"},
		{name: "duplicate end", content: "BEGIN\nEND\nunrelated END\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "end literal \"END\" occurs 2 times"},
		{name: "overlapping duplicate anchor is ambiguous", content: "aaa\nEND\n", script: "in file.txt\nbsel \"aa\" \"END\"", wantFragment: "start literal \"aa\" occurs 2 times"},
		{name: "end precedes start", content: "END\nBEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "precedes or overlaps"},
		{name: "end overlaps start", content: "BEGIN marker\n", script: "in file.txt\nbsel \"BEGIN marker\" \"marker\"", wantFragment: "precedes or overlaps"},
		{name: "same literals", content: "BEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"BEGIN\"", wantFragment: "bsel literals must differ"},
		{name: "empty literal", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"\" \"END\"", wantFragment: "bsel literals must not be empty"},
		{name: "invalid JSON", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" nope", wantFragment: "invalid bsel JSON strings"},
		{name: "non-string JSON", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" 7", wantFragment: "invalid bsel JSON strings"},
		{name: "missing operand", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\"", wantFragment: "invalid bsel JSON strings"},
		{name: "trailing operand", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" \"END\" \"extra\"", wantFragment: "invalid bsel JSON strings"},
		{name: "missing separator", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\"\"END\"", wantFragment: "invalid bsel JSON strings"},
		{name: "unknown near alias", content: "BEGIN\nEND\n", script: "in file.txt\nbselect \"BEGIN\" \"END\"", wantFragment: "unknown or malformed command"},
		{name: "no active file", content: "BEGIN\nEND\n", script: "bsel \"BEGIN\" \"END\"", wantFragment: "bsel requires an active file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.content, 0o644)
			before := readTree(t, root)
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, test.script)
			if exitCode == 0 || stdout != "" || !strings.Contains(stderr, test.wantFragment) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want diagnostic containing %q", exitCode, stdout, stderr, test.wantFragment)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestBlockSelectionFailureDiagnosticUsesSelectionCategory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "BEGIN\nEND\nBEGIN\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in file.txt\nbsel \"BEGIN\" \"END\"")
	if exitCode != 1 || stdout != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, fragment := range []string{"operation \"bsel\"", "path \"file.txt\"", "category selection"} {
		if !strings.Contains(stderr, fragment) {
			t.Fatalf("stderr %q does not contain %q", stderr, fragment)
		}
	}
}

func TestBlockSelectionSearchesForwardFromCursor(t *testing.T) {
	root := t.TempDir()
	initial := "BEGIN old\nbody\nEND\npivot\nBEGIN target\nbody\nEND\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"tsel 4 1 \"pivot\"",
		"type \"pivot\"",
		"bsel \"BEGIN\" \"END\"",
		"type \"replacement\"",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "BEGIN old\nbody\nEND\npivot\nreplacement\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestBlockSelectionSearchesWithinCurrentSelection(t *testing.T) {
	root := t.TempDir()
	initial := "outside BEGIN\nEND\nbefore\nBEGIN target\nEND\nafter\nBEGIN outside\nEND\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"rsel 3:6",
		"bsel \"BEGIN target\" \"END\"",
		"type \"replacement\"",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "outside BEGIN\nEND\nbefore\nreplacement\nafter\nBEGIN outside\nEND\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestBlockSelectionSupportsExistingEditActions(t *testing.T) {
	tests := []struct {
		name, action, want string
	}{
		{name: "delete", action: "del", want: "before\n\nafter\n"},
		{name: "duplicate", action: "dup", want: "before\nBEGIN\nbody\nENDBEGIN\nbody\nEND\nafter\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "before\nBEGIN\nbody\nEND\nafter\n", 0o644)
			script := "in file.txt\nbsel \"BEGIN\" \"END\"\n" + test.action + "\n"
			stdout, stderr, exitCode := runForTest(root, nil, script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, root, "file.txt"); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
