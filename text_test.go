package hpatch

import (
	"strings"
	"testing"
)

func TestEditTextAppliesOrdinaryMutationsToImmutableBaseline(t *testing.T) {
	baseline := "one\ntwo\n"
	script := strings.Join([]string{
		`add ` + row(1, "one") + ` "zero\n"`,
		`type ` + row(2, "two") + ` "TWO"`,
		`add EOF "tail\n"`,
	}, "\n")
	got, err := EditText(t.Context(), baseline, script)
	if err != nil {
		t.Fatal(err)
	}
	if want := "zero\none\nTWO\ntail\n"; got != want {
		t.Fatalf("EditText() = %q, want %q", got, want)
	}
}

func TestEditTextMutatesHeredocBodyByScriptRow(t *testing.T) {
	baseline := "in file.go\ntype 1:ffff <<PATCH\nbad\nPATCH\n"
	got, err := EditText(t.Context(), baseline, `type `+row(3, "bad")+` "good"`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "in file.go\ntype 1:ffff <<PATCH\ngood\nPATCH\n"; got != want {
		t.Fatalf("EditText() = %q, want %q", got, want)
	}
}

func TestEditTextRejectsCommandsOutsideMutationSubset(t *testing.T) {
	baseline := "in file.txt\ntype 1:ffff \"value\"\n"
	for _, script := range []string{"in other.txt", `type "initializer"`, "rm"} {
		if got, err := EditText(t.Context(), baseline, script); err == nil || got != "" {
			t.Fatalf("EditText(%q) = %q, %v; want atomic rejection", script, got, err)
		}
	}
}

func TestTextReferencesUseTargetableRows(t *testing.T) {
	text := "first\r\nsecond\rthird\n"
	got := TextReferences(text, 2, 2, 4, 1, 3)
	want := "2:" + hashLine("second") + " second\n" +
		"1:" + hashLine("first") + " first\n" +
		"3:" + hashLine("third") + " third\n"
	if got != want {
		t.Fatalf("TextReferences() = %q, want %q", got, want)
	}
	if got := TextLineCount(text); got != 3 {
		t.Fatalf("TextLineCount() = %d, want 3", got)
	}
}

func TestGoSyntaxDiagnosticUsesConciseCommandShape(t *testing.T) {
	script := "new hpatch_invalid_recovery_probe.go\n" +
		"type <<PATCH\n" +
		"package main\n\n" +
		"func main() {\n" +
		"\tprintln(\"ok\")\n" +
		")\n" +
		"}\n" +
		"PATCH\n"
	result, err := TranslateForHostAt(t.Context(), t.TempDir(), script, t.TempDir())
	if err == nil {
		t.Fatal("invalid Go source unexpectedly succeeded")
	}
	want := "type: command 2, path \"hpatch_invalid_recovery_probe.go\", reason language-syntax: expected statement, found ')' (and 1 more errors)\n"
	if !strings.HasPrefix(result.Diagnostic, want) {
		t.Fatalf("diagnostic = %q, want prefix %q", result.Diagnostic, want)
	}
}
