package hpatch

import (
	"strings"
	"testing"
)

// repairFor runs script against a one-file root and returns the stderr that
// followed the single-line diagnostic.
func repairFor(t *testing.T, name, content, script string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, name, content, 0o644)
	_, stderr, exitCode := runForTest(root, []string{"translate"}, script)
	if exitCode == 0 {
		t.Fatalf("script unexpectedly succeeded: %q", script)
	}
	_, repair, _ := strings.Cut(stderr, "\n")
	return repair
}

func TestRepairContextReportsColumnCountAndSpans(t *testing.T) {
	// A tab is one column, so a line that looks wider than it measures is the
	// case a column selector gets wrong most often.
	repair := repairFor(t, "calc.go", "func total(a, b int) int {\n\treturn a + b\n}\n", "in calc.go\nsel 2 9:14\ntype \"a - b\"\n")
	for _, want := range []string{
		"line 2 has 13 columns; requested 9:14",
		"one tab is one column",
		">2 \\treturn a + b",
		"column guide for line 2: return=2:7 a=9:9 +=11:11 b=13:13",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairColumnGuideIsUnambiguousForRepeatedRunes(t *testing.T) {
	// Sampling single columns is ambiguous when a character recurs: on this
	// line "r" appears at columns 6 and 10, so a sampled "10=r" invites a
	// selector starting mid-token. Token spans cannot be misread that way.
	guide := columnGuide("\t\t\t\t\treturn nil")
	if guide != "return=6:11 nil=13:15" {
		t.Fatalf("column guide = %q, want token spans", guide)
	}
	if strings.Contains(guide, "10=") {
		t.Fatalf("column guide samples ambiguous single columns: %q", guide)
	}
}

func TestRepairColumnGuideHandlesWhitespaceOnlyLine(t *testing.T) {
	if got := columnGuide("\t\t  "); got != "line contains only whitespace" {
		t.Fatalf("whitespace-only guide = %q", got)
	}
}

func TestRepairContextReportsMissingLine(t *testing.T) {
	repair := repairFor(t, "calc.go", "one\ntwo\nthree\n", "in calc.go\nsel 99 1:3\ntype \"x\"\n")
	if !strings.Contains(repair, "calc.go has 3 lines; line 99 does not exist") {
		t.Fatalf("repair context lacks line count:\n%s", repair)
	}
	// The window still orients the retry, clamped to the real file.
	if !strings.Contains(repair, ">3 three") {
		t.Fatalf("repair context lacks clamped window:\n%s", repair)
	}
}

func TestRepairContextReportsRangeBounds(t *testing.T) {
	repair := repairFor(t, "calc.go", "one\ntwo\n", "in calc.go\nrsel 4:9\ntype \"x\\n\"\n")
	if !strings.Contains(repair, "calc.go has 2 lines; requested lines 4:9") {
		t.Fatalf("repair context lacks range bounds:\n%s", repair)
	}
}

func TestRepairContextLocatesAmbiguousAnchors(t *testing.T) {
	content := "func bar() int {\n\treturn 0\n}\n\nfunc baz() int {\n\treturn 0\n}\n"
	repair := repairFor(t, "amb.go", content, "in amb.go\nbsel \"return 0\" \"}\"\ntype \"x\"\n")
	for _, want := range []string{
		`START anchor "return 0" is ambiguous, occurring at lines 2, 6`,
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairContextReportsMissingAnchor(t *testing.T) {
	content := "func bar() int {\n\treturn 0\n}\n"
	repair := repairFor(t, "amb.go", content, "in amb.go\nbsel \"func qux() {\" \"}\"\ntype \"x\"\n")
	if !strings.Contains(repair, `START anchor "func qux() {" has no occurrence after normalizing horizontal whitespace in the searched scope`) {
		t.Fatalf("repair context lacks missing-anchor report:\n%s", repair)
	}
}

func TestRepairContextExplainsEditConflict(t *testing.T) {
	content := "func bar() int {\n\treturn 0\n}\n"
	script := "in amb.go\nrsel 2:2\ntype \"\\tready\\n\"\nrsel 2:2\ntype \"\\tagain\\n\"\n"
	repair := repairFor(t, "amb.go", content, script)
	for _, want := range []string{
		"each baseline span accepts one edit",
		"already edited by earlier commands: command 3 (type) line 2",
		"combine the intended change into one command per baseline span",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
	// The window centers on the line the rejected selector addressed, not on
	// the stale cursor left by the earlier edit.
	if !strings.Contains(repair, ">2 \\treturn 0") {
		t.Fatalf("repair window not centered on addressed line:\n%s", repair)
	}
}

func TestRepairContextEscapesControlCharacters(t *testing.T) {
	repair := repairFor(t, "ctl.txt", "a\x1b[31mb\n", "in ctl.txt\nsel 1 40:50\ntype \"x\"\n")
	if strings.Contains(repair, "\x1b") {
		t.Fatalf("repair context leaked a control byte: %q", repair)
	}
	if strings.Count(repair, "\n") == 0 {
		t.Fatalf("repair context is empty: %q", repair)
	}
}

func TestRepairContextAbsentForFileFailures(t *testing.T) {
	// A missing file has no baseline to show, so the diagnostic stands alone
	// rather than emitting an empty context block.
	root := t.TempDir()
	_, stderr, exitCode := runForTest(root, []string{"translate"}, "in absent.txt\nrsel 1:1\ntype \"x\"\n")
	if exitCode == 0 {
		t.Fatal("missing file unexpectedly succeeded")
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("expected diagnostic only, got %q", stderr)
	}
}

func TestRepairContextDoesNotAffectSuccess(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "calc.go", "func total(a, b int) int {\n\treturn a + b\n}\n", 0o644)
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in calc.go\nsel 2 9:13\ntype \"a - b\"\n")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("success = exit %d, stderr %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "return a - b") {
		t.Fatalf("patch lacks corrected edit: %q", stdout)
	}
}
