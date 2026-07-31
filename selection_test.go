package hpatch

import (
	"encoding/json"
	"github.com/yusing/hpatch/internal/patchtest"
	"reflect"
	"strings"
	"testing"
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

func TestRemovedBlockSelectorIsRejected(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "BEGIN\nbody\nEND\n", 0o644)
	before := readTree(t, root)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in file.txt\nbsel \"BEGIN\" \"END\"\n")
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "unknown or malformed command") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected command mutated tree: before %#v, after %#v", before, after)
	}
}

func TestSelectorsUseStableBaselineCoordinates(t *testing.T) {
	initial := "alpha\nbeta\ngamma\ndelta\n"
	scripts := map[string]string{
		"top edit first":    "in file.txt\ntsel 1 \"alpha\"\ntype \"alpha\\ninserted\"\ntsel 3 \"gamma\"\ntype \"G\"\n",
		"bottom edit first": "in file.txt\ntsel 3 \"gamma\"\ntype \"G\"\ntsel 1 \"alpha\"\ntype \"alpha\\ninserted\"\n",
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

func TestInsertedTextCannotBeSelected(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "old\n", 0o644)
	before := readTree(t, root)
	script := "in file.txt\ntsel 1 \"old\"\ntype \"future\"\nin file.txt\ntsel 1 \"future\"\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "found 0 of 1 requested matches of \"future\"") {
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
			script: "in file.txt\nrsel 2:4\ntype \"A\"\ntsel 3 \"three\"\ndel\n",
			want:   "selection conflicts with edit from command 3 (source line 3, operation \"type\"): baseline line 3 was already modified",
		},
		{
			name:   "insertion inside replacement",
			script: "in file.txt\ntsel 2 \"t\"\ncopy\npaste\nrsel 2:2\ntype \"whole\"\n",
			want:   "conflicts with edit from command 4 (source line 4, operation \"paste\"): baseline line 2 is both replaced and inserted into",
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
			script: "in file.txt\ntsel 1 \"one\"\ntype \"ONE\"\nrm\n",
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
	script := "in file.txt\ntsel 2 \"t\"\ntype \"T\"\ntype \"!\"\n"
	stdout, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got, want := readTestFile(t, root, "file.txt"), "one\nT!wo\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestTextSelectionCreatesSeparateMatches(t *testing.T) {
	tests := []struct {
		name, content, script, want string
	}{
		{
			name:    "matches across lines",
			content: "bar := 0\nbaz := 0\n",
			script:  "in file.txt\ntsel 1 \":= 0\" 2\ntype \"=\"",
			want:    "bar =\nbaz =\n",
		},
		{
			name:    "from line is an inclusive lower bound",
			content: "x\nx\nx\n", //nolint:dupword // Repetition is the multiple-match fixture.
			script:  "in file.txt\ntsel 2 \"x\" 2\ntype \"Y\"",
			want:    "x\nY\nY\n",
		},
		{
			name:    "explicit count one",
			content: "x, x, x\n", //nolint:dupword // Repetition is the explicit-count fixture.
			script:  "in file.txt\ntsel 1 \"x\" 1\ntype \"Y\"",
			want:    "Y, x, x\n",
		},
		{
			name:    "overlapping candidates are skipped",
			content: "aaaa\n",
			script:  "in file.txt\ntsel 1 \"aa\" 2\ntype \"Y\"",
			want:    "YY\n",
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

func TestNonOverlappingLiteralOffsetsStopsAtRequestedCount(t *testing.T) {
	got := nonOverlappingLiteralOffsets("x x x", "x", 1) //nolint:dupword // Repetition verifies early count termination.
	if want := []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offsets = %v, want %v", got, want)
	}
}

func TestTextSelectionSetSupportsEditActions(t *testing.T) {
	tests := []struct {
		name, action, want string
	}{
		{name: "delete", action: "del", want: "a=\nb=\n"},
		{name: "copy and paste", action: "copy\npaste", want: "a=xx\nb=xx\n"},
		{name: "cut", action: "cut", want: "a=\nb=\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "a=x\nb=x\n", 0o644)
			script := "in file.txt\ntsel 1 \"x\" 2\n" + test.action + "\n"
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

func TestTextSelectionFailuresAreAtomic(t *testing.T) {
	tests := []struct {
		name, content, operand, want string
	}{
		{name: "requested count incomplete", content: "x, x, x\n", operand: "\"x\" 4", want: "found 3 of 4 requested matches"}, //nolint:dupword // Repetition creates three available matches.
		{name: "overlapping match unavailable", content: "aaa\n", operand: "\"aa\" 2", want: "found 1 of 2 requested matches"},
		{name: "zero count", content: "x\n", operand: "\"x\" 0", want: "invalid tsel count"},
		{name: "negative count", content: "x\n", operand: "\"x\" -1", want: "invalid tsel count"},
		{name: "signed count", content: "x\n", operand: "\"x\" +1", want: "invalid tsel count"},
		{name: "leading zero count", content: "x\n", operand: "\"x\" 01", want: "invalid tsel count"},
		{name: "trailing operand", content: "x\n", operand: "\"x\" 2 extra", want: "invalid tsel count"},
		{name: "missing separator", content: "x\n", operand: "\"x\"2", want: "tsel count must be separated by whitespace"},
		{name: "integer overflow", content: "x\n", operand: "\"x\" 999999999999999999999999999999", want: "tsel count is out of range"},
		{name: "removed occurrence grammar", content: "x\n", operand: "1 \"x\"", want: "invalid quoted string for tsel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", test.content, 0o644)
			before := readTree(t, root)
			script := "in file.txt\ntsel 1 " + test.operand
			stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("Run() = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := readTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
			}
		})
	}
}

func TestMultiSelectionConflictRejectsWholeScript(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "file.txt", "x x x\n", 0o644) //nolint:dupword // Repetition creates overlapping multi-selection candidates.
	before := readTree(t, root)
	script := "in file.txt\ntsel 1 \"x\" 2\ntype \"Y\"\ntsel 1 \"x\" 3\n"
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "selection conflicts with edit from command 3") {
		t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := readTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failure mutated tree: before %#v, after %#v", before, after)
	}
}

func TestSelectorsRejectNonAbsoluteLinesAtomically(t *testing.T) {
	tests := []struct {
		name, script, want string
	}{
		{name: "tsel signed", script: "in file.txt\ntsel -1 \"one\"", want: `invalid line reference "-1"`},
		{name: "rsel signed", script: "in file.txt\nrsel +0:+1", want: `invalid line reference "+0"`},
		{name: "tsel negative zero", script: "in file.txt\ntsel -0 \"one\"", want: `invalid line reference "-0"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "file.txt", "one\ntwo\n", 0o644)
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

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
