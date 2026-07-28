package router

import (
	"reflect"
	"strings"
	"testing"
)

func TestHPatchCorrectionDetectionDistinguishesScripts(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		payload string
		want    bool
	}{
		{"correction", "5: sel 2 6:15\n", true},
		{"correction after blank lines", "\n\n5: sel 2 6:15\n", true},
		{"multiple corrections", "5: sel 2 6:15\n10: rsel 3:3\n", true},
		{"deletion", "-5\n", true},
		{"insert before", "+5: copy\n", true},
		{"insert after", "5+: paste\n", true},
		{"script", "in calc.go\nsel 2 6:15\ntype \"x\"\n", false},
		{"new file script", "new calc.go\ntype \"x\"\n", false},
		{"empty", "", false},
		{"blank only", "\n \n", false},
	} {
		if got := isHPatchCorrection(testCase.payload); got != testCase.want {
			t.Errorf("%s: isHPatchCorrection = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestHPatchCorrectionParsesEntries(t *testing.T) {
	corrections, err := parseHPatchCorrections("2: sel 2 6:15\n\n10: bsel \"func bar() {\" \"return 0\\n}\"\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("parsed %d corrections, want 2", len(corrections))
	}
	if corrections[0] != (hpatchCorrection{command: 2, replacement: "sel 2 6:15"}) {
		t.Errorf("first correction = %+v", corrections[0])
	}
	if corrections[1].command != 10 || corrections[1].replacement != `bsel "func bar() {" "return 0\n}"` {
		t.Errorf("second correction = %+v", corrections[1])
	}
}

func TestHPatchCorrectionParsesHeredocAsOneEntry(t *testing.T) {
	corrections, err := parseHPatchCorrections("2: type <<BODY\n5: rm\nBODY\n3: rm\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("parsed %d corrections, want 2", len(corrections))
	}
	if got := corrections[0]; got.command != 2 || got.replacement != "type <<BODY\n5: rm\nBODY" {
		t.Errorf("heredoc correction = %+v", got)
	}
	if got := corrections[1]; got != (hpatchCorrection{command: 3, replacement: "rm"}) {
		t.Errorf("following correction = %+v", got)
	}
}

func TestHPatchCorrectionIndexesAndReplacesCompleteHeredocFrames(t *testing.T) {
	base := "new file.txt\ntype <<BODY\nraw\nBODY\nrm\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 3, replacement: `type "tail"`}})
	if err != nil {
		t.Fatalf("replace after heredoc: %v", err)
	}
	if corrected != "new file.txt\ntype <<BODY\nraw\nBODY\ntype \"tail\"\n" {
		t.Errorf("replacement after heredoc = %q", corrected)
	}

	corrected, err = applyHPatchCorrections(base, []hpatchCorrection{{command: 2, replacement: `type "short"`}})
	if err != nil {
		t.Fatalf("replace heredoc: %v", err)
	}
	if corrected != "new file.txt\ntype \"short\"\nrm\n" {
		t.Errorf("heredoc frame replacement = %q", corrected)
	}
}

func TestHPatchCorrectionAppliesHeredocReplacementLineEndings(t *testing.T) {
	for _, test := range []struct {
		name    string
		base    string
		payload string
		want    string
	}{
		{
			name:    "LF",
			base:    "new file.txt\ntype \"old\"\nrm\n",
			payload: "2: type <<BODY\none\nBODY\n",
			want:    "new file.txt\ntype <<BODY\none\nBODY\nrm\n",
		},
		{
			name:    "CRLF",
			base:    "new file.txt\r\ntype \"old\"\r\nrm\r\n",
			payload: "2: type <<BODY\r\none\r\nBODY\r\n",
			want:    "new file.txt\r\ntype <<BODY\r\none\r\nBODY\r\nrm\r\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			corrections, err := parseHPatchCorrections(test.payload)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			corrected, err := applyHPatchCorrections(test.base, corrections)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if corrected != test.want {
				t.Fatalf("corrected script = %q, want %q", corrected, test.want)
			}
		})
	}
}

func TestHPatchCorrectionParsesCompactOperations(t *testing.T) {
	payload := "-2\n+2: copy\n2+: cut\n+2: type <<BODY\nraw\nBODY\n"
	corrections, err := parseHPatchCorrections(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []hpatchCorrection{
		{kind: hpatchDelete, command: 2},
		{kind: hpatchInsertBeforeAnchor, command: 2, replacement: "copy"},
		{kind: hpatchInsertAfterAnchor, command: 2, replacement: "cut"},
		{kind: hpatchInsertBeforeAnchor, command: 2, replacement: "type <<BODY\nraw\nBODY"},
	}
	if !reflect.DeepEqual(corrections, want) {
		t.Fatalf("corrections = %#v, want %#v", corrections, want)
	}
}

func TestHPatchCorrectionPreservesSignificantLeadingSpace(t *testing.T) {
	// Only the single space after the colon separates index from command, so a
	// replacement that intentionally starts with more whitespace keeps it.
	corrections, err := parseHPatchCorrections("3:  type \"x\"\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if corrections[0].replacement != ` type "x"` {
		t.Errorf("replacement = %q, want one retained leading space", corrections[0].replacement)
	}
}

func TestHPatchCorrectionRejectsMalformedPayloads(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		payload string
		want    string
	}{
		{"raw command", "2: sel 2 6:15\ntype \"x\"\n", "is not `INDEX: COMMAND`"},
		{"zero index", "0: sel 2 6:15\n", "is not `INDEX: COMMAND`"},
		{"empty replacement", "2:\n", "has no replacement command"},
		{"blank replacement", "2:   \n", "has no replacement command"},
		{"duplicate index", "2: sel 2 1:2\n2: sel 2 3:4\n", "appears more than once"},
		{"duplicate deletion", "-2\n-2\n", "appears more than once"},
		{"replacement and deletion", "2: rm\n-2\n", "both replaced and deleted"},
		{"empty insertion", "+2:\n", "has no replacement command"},
		{"deletion with command", "-2: rm\n", "is not `INDEX: COMMAND`"},
		{"invalid heredoc delimiter", "2: type <<bad\n", "invalid heredoc delimiter"},
		{"unterminated heredoc", "2: type <<BODY\nraw\n", "unterminated heredoc"},
		{"empty payload", "\n\n", "correction payload is empty"},
	} {
		_, err := parseHPatchCorrections(testCase.payload)
		if err == nil {
			t.Errorf("%s: unexpectedly parsed", testCase.name)
			continue
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s: error = %v, want %q", testCase.name, err, testCase.want)
		}
	}
}

func TestHPatchCorrectionAppliesByCommandIndexNotSourceLine(t *testing.T) {
	// The blank line makes command 3 land on source line 4. A correction keys
	// on the command index, which is what the diagnostic reports.
	base := "in calc.go\nrsel 2:2\n\ntype \"\\tready\\n\"\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 3, replacement: `type "\tset\n"`}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in calc.go\nrsel 2:2\n\ntype \"\\tset\\n\"\n"
	if corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
	}
}

func TestHPatchCorrectionAppliesOrderedInsertionsAroundDeletedAnchor(t *testing.T) {
	corrections, err := parseHPatchCorrections("-2\n+2: copy\n2+: cut\n+2: paste\n2+: del\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base := "in file.txt\nrsel 1:1\ntype \"x\"\n"
	corrected, err := applyHPatchCorrections(base, corrections)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in file.txt\ncopy\ncut\npaste\ndel\ntype \"x\"\n"
	if corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
	}
}

func TestHPatchCorrectionOperationsUseOriginalIndices(t *testing.T) {
	corrections, err := parseHPatchCorrections("-1\n3: rm\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	corrected, err := applyHPatchCorrections("one\ntwo\nthree\n", corrections)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "two\nrm\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionInsertionsPreserveCRLF(t *testing.T) {
	corrections, err := parseHPatchCorrections("+1: copy\r\n1+: paste\r\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	corrected, err := applyHPatchCorrections("rm\r\n", corrections)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "copy\r\nrm\r\npaste\r\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionAppliesSeveralCommands(t *testing.T) {
	base := "in calc.go\nsel 2 9:14\ntype \"a - b\"\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{
		{command: 2, replacement: "sel 2 9:13"},
		{command: 3, replacement: `type "a * b"`},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "in calc.go\nsel 2 9:13\ntype \"a * b\"\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionPreservesCarriageReturns(t *testing.T) {
	base := "in calc.go\r\nsel 2 9:14\r\ntype \"x\"\r\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 2, replacement: "sel 2 9:13"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "in calc.go\r\nsel 2 9:13\r\ntype \"x\"\r\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionRejectsIndexBeyondScript(t *testing.T) {
	_, err := applyHPatchCorrections("in calc.go\nrm\n", []hpatchCorrection{{command: 5, replacement: "rm"}})
	if err == nil || !strings.Contains(err.Error(), "the rejected script has 2 commands") {
		t.Fatalf("error = %v, want a command-count report", err)
	}
}

func TestHPatchCorrectionRejectsEmptyBase(t *testing.T) {
	_, err := applyHPatchCorrections("\n\n", []hpatchCorrection{{command: 1, replacement: "rm"}})
	if err == nil || !strings.Contains(err.Error(), "no commands to correct") {
		t.Fatalf("error = %v", err)
	}
}

func TestHPatchCorrectionBoundsMalformedLinePreview(t *testing.T) {
	long := strings.Repeat("z", 200)
	_, err := parseHPatchCorrections(long + "\n")
	if err == nil {
		t.Fatal("unexpectedly parsed")
	}
	if strings.Contains(err.Error(), strings.Repeat("z", 100)) {
		t.Fatalf("diagnostic echoed an unbounded line: %v", err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("diagnostic lacks truncation marker: %v", err)
	}
}
