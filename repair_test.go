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

func TestRepairContextReportsForwardMatchCountsFromResolvedHash(t *testing.T) {
	content := "target once target once\nnone\ntarget twice target twice\ntarget once\n"
	anchor := hashLine("none")
	repair := repairFor(t, "find.txt", content, "in find.txt\ntsel "+anchor+" \"target once\" 2\ntype \"replacement\"\n")
	for _, want := range []string{
		"found 1 of 2 requested matches at or after line 2",
		"matching lines: 4",
		anchor + ": none",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}

	repair = repairFor(t, "find.txt", content, "in find.txt\ntsel "+anchor+" \"target twice\" 3\ntype \"replacement\"\n")
	for _, want := range []string{
		"found 2 of 3 requested matches at or after line 2",
		"matching lines: 3, 3",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairContextReportsReversedResolvedRange(t *testing.T) {
	repair := repairFor(
		t,
		"calc.go",
		"one\ntwo\n",
		"in calc.go\nrsel "+hashLine("two")+" "+hashLine("one")+"\ntype \"x\\n\"\n",
	)
	for _, want := range []string{
		"resolved hashline range to lines 2:1",
		hashLine("one") + ": one",
		hashLine("two") + ": two",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairContextExplainsEditConflict(t *testing.T) {
	content := "func bar() int {\n\treturn 0\n}\n"
	anchor := hashLine("\treturn 0")
	script := "in amb.go\nrsel " + anchor + " " + anchor + "\ntype \"\\tready\\n\"\nrsel " + anchor + " " + anchor + "\ntype \"\\tagain\\n\"\n"
	repair := repairFor(t, "amb.go", content, script)
	for _, want := range []string{
		"each baseline span accepts one edit",
		"already edited by earlier commands: command 3 (type) line 2",
		"combine the intended change into one command per baseline span",
		anchor + ": \treturn 0",
	} {
		if !strings.Contains(repair, want) {
			t.Fatalf("repair context lacks %q:\n%s", want, repair)
		}
	}
}

func TestRepairContextAbsentForMissingOrAmbiguousHashes(t *testing.T) {
	tests := []struct {
		name, content, hash, want string
	}{
		{name: "missing", content: "present\n", hash: "ffff", want: "does not identify any line"},
		{name: "ambiguous", content: "target\ntarget\n", hash: hashLine("target"), want: "ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "note.txt", test.content, 0o644)
			_, stderr, exitCode := runForTest(root, []string{"translate"}, "in note.txt\ntsel "+test.hash+" \"missing\"\n")
			if exitCode != 1 || !strings.Contains(stderr, test.want) {
				t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
			}
			if strings.Count(stderr, "\n") != 1 {
				t.Fatalf("selector without unique identity emitted repair context: %q", stderr)
			}
		})
	}
}

func TestRepairContextEscapesControlCharactersAndUsesHashOnlyRows(t *testing.T) {
	content := "a\x1b[31mb\n"
	repair := repairFor(
		t,
		"ctl.txt",
		content,
		"in ctl.txt\ntsel "+hashLine("a\x1b[31mb")+" \"missing\"\n",
	)
	if strings.Contains(repair, "\x1b") {
		t.Fatalf("repair context leaked a control byte: %q", repair)
	}
	want := hashLine("a\x1b[31mb") + ": a\\x1b[31mb\n"
	if !strings.Contains(repair, want) {
		t.Fatalf("repair context lacks hash-only row %q:\n%s", want, repair)
	}
}

func TestRepairContextAbsentForFileFailures(t *testing.T) {
	root := t.TempDir()
	_, stderr, exitCode := runForTest(root, []string{"translate"}, "in absent.txt\nrsel 8ed3 8ed3\ntype \"x\"\n")
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
	anchor := hashLine("\treturn a + b")
	stdout, stderr, exitCode := runForTest(
		root,
		[]string{"translate"},
		"in calc.go\ntsel "+anchor+" \"a + b\"\ntype \"a - b\"\n",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("success = exit %d, stderr %q", exitCode, stderr)
	}
	if !strings.Contains(stdout, "return a - b") {
		t.Fatalf("patch lacks corrected edit: %q", stdout)
	}
}
