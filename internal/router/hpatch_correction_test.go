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
		{"accept", "5: accept\n", true},
		{"correction", "5: del 1:a793..2:b1e9\n", true},
		{"correction after blank lines", "\n\n5: del 1:a793..2:b1e9\n", true},
		{"multiple corrections", "5: del 1:a793..2:b1e9\n10: del 3:be9d..4:b1e9\n", true},
		{"deletion", "-5\n", true},
		{"insert before", "+5: rm\n", true},
		{"insert after", "5+: del 1:a793\n", true},
		{"script", "in calc.go\ntype 1:a793..2:b1e9 \"x\"\n", false},
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
	corrections, err := parseHPatchCorrections("2: del 1:a793..2:b1e9\n\n10: del 3:be9d..4:55af\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("parsed %d corrections, want 2", len(corrections))
	}
	if corrections[0] != (hpatchCorrection{command: 2, replacement: "del 1:a793..2:b1e9"}) {
		t.Errorf("first correction = %+v", corrections[0])
	}
	if corrections[1].command != 10 || corrections[1].replacement != "del 3:be9d..4:55af" {
		t.Errorf("second correction = %+v", corrections[1])
	}
}

func TestHPatchCorrectionParsesHeredocAsOneEntry(t *testing.T) {
	corrections, err := parseHPatchCorrections("2: type <<PATCH\n5: rm\nPATCH\n3: rm\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("parsed %d corrections, want 2", len(corrections))
	}
	if got := corrections[0]; got.command != 2 || got.replacement != "type <<PATCH\n5: rm\nPATCH" {
		t.Errorf("heredoc correction = %+v", got)
	}
	if got := corrections[1]; got != (hpatchCorrection{command: 3, replacement: "rm"}) {
		t.Errorf("following correction = %+v", got)
	}
}

func TestHPatchCorrectionRejectsSubstitutedHeredocTag(t *testing.T) {
	_, err := parseHPatchCorrections("2: type <<'GO'\nraw\nGO\n3: rm\n")
	if err == nil || !strings.Contains(err.Error(), "requires an unquoted <<PATCH") {
		t.Fatalf("error = %v", err)
	}
}

func TestHPatchCorrectionIndexesAndReplacesCompleteHeredocFrames(t *testing.T) {
	base := "new file.txt\ntype <<PATCH\nraw\nPATCH\nrm\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 3, replacement: `type "tail"`}})
	if err != nil {
		t.Fatalf("replace after heredoc: %v", err)
	}
	if corrected != "new file.txt\ntype <<PATCH\nraw\nPATCH\ntype \"tail\"\n" {
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

func TestHPatchCorrectionReplacesMalformedMultilineQuotedFrame(t *testing.T) {
	base := "in file.txt\n" +
		"type \"start\n" +
		"middle\"\n" +
		"type \"replacement\"\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{
		command:     2,
		replacement: `type "start\nmiddle"`,
	}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in file.txt\n" +
		"type \"start\\nmiddle\"\n" +
		"type \"replacement\"\n"
	if corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
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
			payload: "2: type <<PATCH\none\nPATCH\n",
			want:    "new file.txt\ntype <<PATCH\none\nPATCH\nrm\n",
		},
		{
			name:    "CRLF",
			base:    "new file.txt\r\ntype \"old\"\r\nrm\r\n",
			payload: "2: type <<PATCH\r\none\r\nPATCH\r\n",
			want:    "new file.txt\r\ntype <<PATCH\r\none\r\nPATCH\r\nrm\r\n",
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
	payload := "-2\n+2: rm\n2+: del 1:a793\n+2: type <<PATCH\nraw\nPATCH\n"
	corrections, err := parseHPatchCorrections(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []hpatchCorrection{
		{kind: hpatchDelete, command: 2},
		{kind: hpatchInsertBeforeAnchor, command: 2, replacement: "rm"},
		{kind: hpatchInsertAfterAnchor, command: 2, replacement: "del 1:a793"},
		{kind: hpatchInsertBeforeAnchor, command: 2, replacement: "type <<PATCH\nraw\nPATCH"},
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
		{"raw command", "2: del 1:a793..2:b1e9\ntype \"x\"\n", "is not `INDEX: COMMAND`"},
		{"zero index", "0: del 1:a793..2:b1e9\n", "is not `INDEX: COMMAND`"},
		{"empty replacement", "2:\n", "has no replacement command"},
		{"blank replacement", "2:   \n", "has no replacement command"},
		{"duplicate index", "2: del 1:a793..2:1636\n2: del 2:1636..3:b1e9\n", "appears more than once"},
		{"duplicate deletion", "-2\n-2\n", "appears more than once"},
		{"duplicate acceptance", "2: accept\n2: accept\n", "appears more than once"},
		{"replacement and deletion", "2: rm\n-2\n", "both replaced and deleted"},
		{"acceptance and deletion", "2: accept\n-2\n", "both accepted and deleted"},
		{"acceptance and replacement", "2: accept\n2: rm\n", "both accepted and replaced"},
		{"empty insertion", "+2:\n", "has no replacement command"},
		{"deletion with command", "-2: rm\n", "is not `INDEX: COMMAND`"},
		{"invalid heredoc delimiter", "2: type <<\n", "requires an unquoted <<PATCH"},
		{"unterminated heredoc", "2: type <<PATCH\nraw\n", "unterminated heredoc"},
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
	base := "in calc.go\ntype 2:1636..3:b1e9 \"old\"\n\ntype 4:be9d \"\\tready\\n\"\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 3, replacement: `type "\tset\n"`}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in calc.go\ntype 2:1636..3:b1e9 \"old\"\n\ntype \"\\tset\\n\"\n"
	if corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
	}
}

func TestHPatchCorrectionAppliesOrderedInsertionsAroundDeletedAnchor(t *testing.T) {
	corrections, err := parseHPatchCorrections("-2\n+2: type- 1:a793 \"x\"\n2+: del 1:a793\n+2: type+ 1:a793 \"y\"\n2+: rm\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base := "in file.txt\ntype 1:a793..2:1636 \"x\"\ntype 3:b1e9 \"z\"\n"
	corrected, err := applyHPatchCorrections(base, corrections)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in file.txt\ntype- 1:a793 \"x\"\ndel 1:a793\ntype+ 1:a793 \"y\"\nrm\ntype 3:b1e9 \"z\"\n"
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
	corrections, err := parseHPatchCorrections("+1: type- 1:a793 \"x\"\r\n1+: type+ 1:a793 \"y\"\r\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	corrected, err := applyHPatchCorrections("rm\r\n", corrections)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "type- 1:a793 \"x\"\r\nrm\r\ntype+ 1:a793 \"y\"\r\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionAppliesSeveralCommands(t *testing.T) {
	base := "in calc.go\ntype 1:9645 \"a - b\"\nrm\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{
		{command: 2, replacement: `type 1:9645..2:4b7b "a * b"`},
		{command: 3, replacement: "rm"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "in calc.go\ntype 1:9645..2:4b7b \"a * b\"\nrm\n" {
		t.Fatalf("corrected script = %q", corrected)
	}
}

func TestHPatchCorrectionPreservesCarriageReturns(t *testing.T) {
	base := "in calc.go\r\ntype 1:9645 \"x\"\r\nrm\r\n"
	corrected, err := applyHPatchCorrections(base, []hpatchCorrection{{command: 2, replacement: "type 1:9645..2:4b7b \"x\""}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if corrected != "in calc.go\r\ntype 1:9645..2:4b7b \"x\"\r\nrm\r\n" {
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

func TestHPatchCorrectionParsesAndAppliesDisplayedAcceptance(t *testing.T) {
	corrections, err := parseHPatchCorrections("2: accept\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []hpatchCorrection{{kind: hpatchAccept, command: 2}}
	if !reflect.DeepEqual(corrections, want) {
		t.Fatalf("corrections = %#v, want %#v", corrections, want)
	}

	base := "in script.sh\ntype 1:a793..2:1636 \"exit\\n\"\nrm\n"
	suggestion := "type 1:a793..2:1636 \"\\texit\\n\""
	corrected, err := applyHPatchCorrections(base, corrections, map[int]string{2: suggestion})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := "in script.sh\n" + suggestion + "\nrm\n"; corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
	}
}

func TestHPatchCorrectionRejectsAcceptanceWithoutDisplayedSuggestion(t *testing.T) {
	corrections, err := parseHPatchCorrections("2: accept\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = applyHPatchCorrections("in file.txt\nrm\n", corrections)
	if err == nil || !strings.Contains(err.Error(), "no displayed correction to accept") {
		t.Fatalf("error = %v, want missing-suggestion rejection", err)
	}
}

func TestHPatchCorrectionComposesAcceptancesAndManualOperations(t *testing.T) {
	base := "in file.txt\ntype 1:a793..2:1636 \"one\\n\"\ntype 2:1636..3:b1e9 \"two\\n\"\nrm\n"
	corrections, err := parseHPatchCorrections("2: accept\n3: type 2:be9d..3:b1e9 \"two\\n\"\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	suggestions := map[int]string{
		2: "type 1:a793..2:1636 \"\\tone\\n\"",
	}
	corrected, err := applyHPatchCorrections(base, corrections, suggestions)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "in file.txt\ntype 1:a793..2:1636 \"\\tone\\n\"\ntype 2:be9d..3:b1e9 \"two\\n\"\nrm\n"
	if corrected != want {
		t.Fatalf("corrected script = %q, want %q", corrected, want)
	}
}
