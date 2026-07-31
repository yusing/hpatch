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

func TestRepairContextReportsForwardMatchCounts(t *testing.T) {
	content := "target once target once\nnone\ntarget twice target twice\ntarget once\n"
	repair := repairFor(t, "find.txt", content, "in find.txt\ntsel 2 \"target once\" 2\ntype \"replacement\"\n")
	for _, want := range []string{
		"found 1 of 2 requested matches at or after line 2",
		"matching lines: 4",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}

	repair = repairFor(t, "find.txt", content, "in find.txt\ntsel 2 \"target twice\" 3\ntype \"replacement\"\n")
	for _, want := range []string{
		"found 2 of 3 requested matches at or after line 2",
		"matching lines: 3, 3",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairContextReportsMissingLine(t *testing.T) {
	repair := repairFor(t, "calc.go", "one\ntwo\nthree\n", "in calc.go\ntsel 99 \"x\"\ntype \"x\"\n")
	if !strings.Contains(repair, "calc.go has 3 lines; line 99 does not exist") {
		t.Fatalf("repair context lacks line count:\n%s", repair)
	}
	// The window still orients the retry, clamped to the real file.
	if !strings.Contains(repair, "3|three") {
		t.Fatalf("repair context lacks clamped window:\n%s", repair)
	}
}

func TestRepairContextReportsRangeBounds(t *testing.T) {
	repair := repairFor(t, "calc.go", "one\ntwo\n", "in calc.go\nrsel 4:9\ntype \"x\\n\"\n")
	if !strings.Contains(repair, "calc.go has 2 lines; requested lines 4:9") {
		t.Fatalf("repair context lacks range bounds:\n%s", repair)
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
	if !strings.Contains(repair, "2|\treturn 0") {
		t.Fatalf("repair window not centered on addressed line:\n%s", repair)
	}
}

func TestRepairContextEscapesControlCharacters(t *testing.T) {
	repair := repairFor(t, "ctl.txt", "a\x1b[31mb\n", "in ctl.txt\ntsel 1 \"missing\"\ntype \"x\"\n")
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
	stdout, stderr, exitCode := runForTest(root, []string{"translate"}, "in calc.go\ntsel 2 \"a + b\"\ntype \"a - b\"\n")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("success = exit %d, stderr %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "return a - b") {
		t.Fatalf("patch lacks corrected edit: %q", stdout)
	}
}
