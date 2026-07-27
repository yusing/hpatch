package hpatch

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestChargedScriptAccountsForTheDeclaredPayload(t *testing.T) {
	// A caller that rebuilt this script from a short correction declares the
	// correction as the charged payload. Evaluation must still use stdin, while
	// output accounting must use the correction, or a repair would be measured
	// as costing a full retry.
	root := t.TempDir()
	writeTestFile(t, root, "calc.go", "func total(a, b int) int {\n\treturn a + b\n}\n", 0o644)
	dataDirectory := t.TempDir()
	script := "in calc.go\nsel 2 9:13\ntype \"a - b\"\n"
	correction := "2: sel 2 9:13\n"
	t.Setenv(chargedScriptVariable, correction)

	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("translate = exit %d, stderr %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "return a - b") {
		t.Fatalf("patch lacks the evaluated edit: %q", stdout.String())
	}

	stored, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantCharged, err := countMetrics(correction, stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	if stored.HPatchTokens != wantCharged.HPatchTokens {
		t.Fatalf("charged tokens = %d, want the correction's %d", stored.HPatchTokens, wantCharged.HPatchTokens)
	}
	wantScript, err := countMetrics(script, stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	if stored.HPatchTokens >= wantScript.HPatchTokens {
		t.Fatalf("charged tokens = %d, want fewer than the rebuilt script's %d", stored.HPatchTokens, wantScript.HPatchTokens)
	}
	// The apply_patch baseline still reflects the edit that was really applied,
	// because the comparison is against writing that patch by hand.
	if stored.ApplyPatchTokens != wantCharged.ApplyPatchTokens {
		t.Fatalf("apply_patch tokens = %d, want %d", stored.ApplyPatchTokens, wantCharged.ApplyPatchTokens)
	}
}

func TestChargedScriptAppliesToRejectedScripts(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "calc.go", "func total(a, b int) int {\n\treturn a + b\n}\n", 0o644)
	dataDirectory := t.TempDir()
	correction := "2: sel 2 9:99\n"
	t.Setenv(chargedScriptVariable, correction)

	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"translate"}, strings.NewReader("in calc.go\nsel 2 9:99\ntype \"a - b\"\n"), &stdout, &stderr, root, dataDirectory); exitCode == 0 {
		t.Fatal("out-of-range selector unexpectedly succeeded")
	}
	stored, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want, err := countIneffectiveMetrics(correction, invocationMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	if stored.IneffectiveHPatchTokens != want.IneffectiveHPatchTokens {
		t.Fatalf("ineffective tokens = %d, want the correction's %d", stored.IneffectiveHPatchTokens, want.IneffectiveHPatchTokens)
	}
}

func TestChargedScriptDefaultsToTheEvaluatedScript(t *testing.T) {
	// An absent variable is the ordinary case; an empty one is a caller that
	// exported it without a value, which must not charge zero tokens.
	if err := os.Unsetenv(chargedScriptVariable); err != nil {
		t.Fatal(err)
	}
	if got := chargedScript("in calc.go\n"); got != "in calc.go\n" {
		t.Fatalf("charged script with no variable = %q", got)
	}
	t.Setenv(chargedScriptVariable, "")
	if got := chargedScript("in calc.go\n"); got != "in calc.go\n" {
		t.Fatalf("charged script with an empty variable = %q", got)
	}
}
