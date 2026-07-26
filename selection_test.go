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

func TestBlockSelectionUsesImmutableBaseline(t *testing.T) {
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
		name, content, script, wantFragment, wantAbsent string
	}{
		{name: "missing start", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"absent\" \"END\"", wantFragment: "start literal \"absent\" occurs 0 times", wantAbsent: "horizontal whitespace ignored"},
		{name: "missing end", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" \"absent\"", wantFragment: "end literal \"absent\" occurs 0 times"},
		{name: "unrelated duplicate start", content: "BEGIN\nEND\nunrelated BEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "start literal \"BEGIN\" occurs 2 times"},
		{name: "duplicate end", content: "BEGIN\nEND\nunrelated END\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "end literal \"END\" occurs 2 times"},
		{name: "overlapping duplicate anchor is ambiguous", content: "aaa\nEND\n", script: "in file.txt\nbsel \"aa\" \"END\"", wantFragment: "start literal \"aa\" occurs 2 times"},
		{name: "end before start is ignored", content: "END\nBEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"END\"", wantFragment: "end literal \"END\" occurs 0 times"},
		{name: "end inside start is ignored", content: "BEGIN marker\n", script: "in file.txt\nbsel \"BEGIN marker\" \"marker\"", wantFragment: "end literal \"marker\" occurs 0 times"},
		{name: "same literals", content: "BEGIN\n", script: "in file.txt\nbsel \"BEGIN\" \"BEGIN\"", wantFragment: "bsel literals must differ"},
		{name: "empty literal", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"\" \"END\"", wantFragment: "bsel literals must not be empty"},
		{name: "invalid JSON", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" nope", wantFragment: "invalid bsel JSON strings"},
		{name: "non-string JSON", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" 7", wantFragment: "invalid bsel JSON strings"},
		{name: "missing operand", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\"", wantFragment: "invalid bsel JSON strings"},
		{name: "trailing operand", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\" \"END\" \"extra\"", wantFragment: "invalid bsel JSON strings"},
		{name: "missing separator", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN\"\"END\"", wantFragment: "invalid bsel JSON strings"},
		{name: "bsel_next invalid JSON", content: "BEGIN\nEND\n", script: "in file.txt\nbsel_next \"BEGIN\" nope", wantFragment: "invalid bsel_next JSON strings"},
		{name: "whitespace fallback ambiguity", content: "\tBEGIN\nEND\n    BEGIN\nEND\n", script: "in file.txt\nbsel \" \\tBEGIN\" \"END\"", wantFragment: "occurs 2 times with horizontal whitespace ignored"},
		{name: "horizontal whitespace cannot be absent", content: "BEGINEND\n", script: "in file.txt\nbsel \"BEGIN END\" \"absent\"", wantFragment: "start literal \"BEGIN END\" occurs 0 times"},
		{name: "horizontal whitespace cannot cross line", content: "BEGIN\nEND\n", script: "in file.txt\nbsel \"BEGIN END\" \"absent\"", wantFragment: "start literal \"BEGIN END\" occurs 0 times"},
		{name: "unknown near alias", content: "BEGIN\nEND\n", script: "in file.txt\nbselect \"BEGIN\" \"END\"", wantFragment: "unknown or malformed command"},
		{name: "no active file", content: "BEGIN\nEND\n", script: "bsel \"BEGIN\" \"END\"", wantFragment: "bsel requires an active file"},
		{name: "bsel_next no active file", content: "BEGIN\nEND\n", script: "bsel_next \"BEGIN\" \"END\"", wantFragment: "bsel_next requires an active file"},
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
			if test.wantAbsent != "" && strings.Contains(stderr, test.wantAbsent) {
				t.Fatalf("stderr %q contains %q", stderr, test.wantAbsent)
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

func TestBlockSelectionSearchesWholeFileIndependentOfCursor(t *testing.T) {
	root := t.TempDir()
	initial := "BEGIN old\nbody\nEND old\npivot\nBEGIN later\nbody\nEND later\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"tsel 4 1 \"pivot\"",
		"type \"pivot\"",
		"bsel \"BEGIN old\" \"END old\"",
		"type \"replacement\"",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	want := "replacement\npivot\nBEGIN later\nbody\nEND later\n"
	if got := readTestFile(t, root, "file.txt"); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestBlockSelectionNextSearchesForwardFromCursor(t *testing.T) {
	root := t.TempDir()
	initial := "BEGIN old\nbody\nEND\npivot\nBEGIN target\nbody\nEND\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"tsel 4 1 \"pivot\"",
		"type \"pivot\"",
		"bsel_next \"BEGIN\" \"END\"",
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

func TestBlockSelectionNextSearchesWithinCurrentSelection(t *testing.T) {
	root := t.TempDir()
	initial := "outside BEGIN\nEND\nbefore\nBEGIN target\nEND\nafter\nBEGIN outside\nEND\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"rsel 3:6",
		"bsel_next \"BEGIN target\" \"END\"",
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

func TestBlockSelectionIgnoresEndBeforeStart(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "END target\nBEGIN target\nbody\nEND target\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, "in file.txt\nbsel \"BEGIN target\" \"END target\"\ntype \"replacement\"\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "END target\nreplacement\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestBlockSelectionToleratesHorizontalWhitespace(t *testing.T) {
	root := t.TempDir()
	initial := "before\n\tcase gain:\n\t\treturn nil\n\tcase next:\nafter\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := "in file.txt\nbsel \"    case gain:\" \"    case next:\"\ntype \"\\tcase done:\"\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "before\n\tcase done:\nafter\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestBlockSelectionExactMatchPrecedesWhitespaceFallback(t *testing.T) {
	root := t.TempDir()
	initial := "\tBEGIN\nEND tab\n    BEGIN\nEND spaces\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	stdout, stderr, exitCode := runForTest(root, nil, "in file.txt\nbsel \"    BEGIN\" \"END spaces\"\ntype \"replacement\"\n")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "\tBEGIN\nEND tab\nreplacement\n"; got != want {
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

func TestSelectorsUseStableBaselineCoordinates(t *testing.T) {
	initial := "alpha\nbeta\ngamma\ndelta\n"
	scripts := map[string]string{
		"top edit first":    "in file.txt\ntsel 1 1 \"alpha\"\ntype \"alpha\\ninserted\"\ntsel 3 1 \"gamma\"\ntype \"G\"\n",
		"bottom edit first": "in file.txt\ntsel 3 1 \"gamma\"\ntype \"G\"\ntsel 1 1 \"alpha\"\ntype \"alpha\\ninserted\"\n",
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", initial, 0o644)
			stdout, stderr, exitCode := runForTest(root, nil, script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got, want := readTestFile(t, root, "file.txt"), "alpha\ninserted\nbeta\nG\ndelta\n"; got != want {
				t.Fatalf("file = %q, want %q", got, want)
			}
		})
	}
}

func TestInsertedTextDoesNotAffectLaterSelectors(t *testing.T) {
	root := t.TempDir()
	initial := "HEAD\nBEGIN\nbody\nEND\nTAIL\n"
	writeTestFile(t, root, "file.txt", initial, 0o644)
	script := strings.Join([]string{
		"in file.txt",
		"tsel 5 1 \"TAIL\"",
		"type \"BEGIN injected END\"",
		"in file.txt",
		"bsel \"BEGIN\" \"END\"",
		"type \"REPLACED\"",
	}, "\n")
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "HEAD\nREPLACED\nBEGIN injected END\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestInsertedTextCannotBeSelected(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	before := readTree(t, root)
	script := "in file.txt\ntsel 1 1 \"old\"\ntype \"future\"\nin file.txt\ntsel 1 1 \"future\"\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "occurrence 1 of \"future\" not found") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
	}
}

func TestConflictingBaselineEditsAreAtomic(t *testing.T) {
	tests := []struct {
		name, script, want string
	}{
		{
			name:   "overlapping ranges",
			script: "in file.txt\nrsel 2:3\ntype \"A\"\nrsel 3:4\ntype \"B\"\n",
			want:   "selection conflicts with edit from command 3 (source line 3, operation \"type\"): baseline line 3 was already modified",
		},
		{
			name:   "nested selection",
			script: "in file.txt\nrsel 2:4\ntype \"A\"\ntsel 3 1 \"three\"\ndel\n",
			want:   "selection conflicts with edit from command 3 (source line 3, operation \"type\"): baseline line 3 was already modified",
		},
		{
			name:   "insertion inside replacement",
			script: "in file.txt\ntsel 2 1 \"t\"\ndup\nrsel 2:2\ntype \"whole\"\n",
			want:   "conflicts with edit from command 3 (source line 3, operation \"dup\"): baseline line 2 is both replaced and inserted into",
		},
		{
			name:   "same existing-file insertion position",
			script: "in file.txt\ntype \"A\"\ntype \"B\"\n",
			want:   "baseline line 1 receives multiple insertions",
		},
		{
			name:   "new file accepts one complete write",
			script: "new note.txt\ntype \"A\"\ntype \"B\"\n",
			want:   "baseline line 1 receives multiple insertions",
		},
		{
			name:   "remove after edit",
			script: "in file.txt\ntsel 1 1 \"one\"\ntype \"ONE\"\nrm\n",
			want:   "cannot remove a baseline file after content edit from command 3",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "one\ntwo\nthree\nfour\n", 0o644)
			before := readTree(t, root)
			stdout, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestInsertionAtReplacementBoundaryIsUnambiguous(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "one\ntwo\n", 0o644)
	script := "in file.txt\ntsel 2 1 \"t\"\ntype \"T\"\ntype \"!\"\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "one\nT!wo\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestRelativeLineSelectorsResolveFromCursor(t *testing.T) {
	tests := []struct {
		name, content, script, want string
	}{
		{
			name:    "sel positive offset",
			content: "one\ntwo\nthree\n",
			script:  "in file.txt\ntsel 1 1 \"one\"\ntype \"ONE\"\nsel +1 1:3\ntype \"TWO\"\n",
			want:    "ONE\nTWO\nthree\n",
		},
		{
			name:    "tsel negative offset",
			content: "one\ntwo\nthree\n",
			script:  "in file.txt\ntsel 3 1 \"three\"\ntype \"THREE\"\ntsel -1 1 \"two\"\ntype \"TWO\"\n",
			want:    "one\nTWO\nTHREE\n",
		},
		{
			name:    "rsel plus zero follows line boundary",
			content: "one\ntwo\nthree\nfour\n",
			script:  "in file.txt\nrsel 1:1\ntype \"ONE\\n\"\nrsel +0:+1\ntype \"REST\"\n",
			want:    "ONE\nREST\nfour\n",
		},
		{
			name:    "plus zero at eof uses final line",
			content: "one\ntwo",
			script:  "in file.txt\ntsel 2 1 \"two\"\ndup\ntsel +0 1 \"two\"\ntype \"SECOND\"\n",
			want:    "one\nSECONDtwo",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.content, 0o644)
			stdout, stderr, exitCode := runForTest(root, nil, test.script)
			if exitCode != 0 || stdout != "" || stderr != "" {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if got := readTestFile(t, root, "file.txt"); got != test.want {
				t.Fatalf("file = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRelativeLineSelectorFailuresAreAtomic(t *testing.T) {
	tests := []struct {
		name, content, script, want string
	}{
		{name: "active selection", content: "one\ntwo\n", script: "in file.txt\ntsel 1 1 \"one\"\nsel +0 1:1", want: "relative line reference requires cursor state; a selection is active"},
		{name: "before first line", content: "one\ntwo\n", script: "in file.txt\nsel -1 1:1", want: "line 0 is outside the file"},
		{name: "after final line", content: "one\ntwo\n", script: "in file.txt\nsel +2 1:1", want: "line 3 is outside the file"},
		{name: "reversed resolved range", content: "one\ntwo\n", script: "in file.txt\nrsel +1:+0", want: "resolved line range start 2 exceeds end 1"},
		{name: "mixed range", content: "one\ntwo\n", script: "in file.txt\nrsel +0:2", want: "rsel endpoints must both be absolute or both be relative"},
		{name: "zero absolute", content: "one\n", script: "in file.txt\nsel 0 1:1", want: "invalid line reference \"0\""},
		{name: "negative zero", content: "one\n", script: "in file.txt\ntsel -0 1 \"one\"", want: "invalid line reference \"-0\""},
		{name: "incomplete signed", content: "one\n", script: "in file.txt\nsel + 1:1", want: "invalid line reference \"+\""},
		{name: "empty baseline", content: "", script: "in file.txt\nsel +0 1:1", want: "relative line reference requires a nonempty baseline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.content, 0o644)
			before := readTree(t, root)
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, test.script)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestRelativeLineSyntaxCanBeDisabled(t *testing.T) {
	t.Setenv("HPATCH_DISABLE_RELATIVE_LINES", "1")
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "one\ntwo\n", 0o644)
	before := readTree(t, root)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in file.txt\nsel +0 1:1")
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "relative line references are disabled by HPATCH_DISABLE_RELATIVE_LINES=1") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
	}

	stdout, stderr, exitCode = runForTest(root, []string{"translate"}, "in file.txt\nsel 1 1:1\ntype \"O\"")
	if exitCode != 0 || stdout == "" || stderr != "" {
		t.Fatalf("absolute selector = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestOnlyEnvironmentValueOneDisablesRelativeLines(t *testing.T) {
	t.Setenv("HPATCH_DISABLE_RELATIVE_LINES", "true")
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "one\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in file.txt\nsel +0 1:1\ntype \"O\"")
	if exitCode != 0 || stdout == "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
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
