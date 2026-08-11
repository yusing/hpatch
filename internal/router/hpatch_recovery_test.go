package router

import (
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

func TestIsHPatchRecoveryCandidateUsesFirstNonblankCommand(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "replace", payload: "type 2:abcd \"fixed\"", want: true},
		{name: "insert before", payload: "\n\ntype- 2:abcd \"line\\n\"", want: true},
		{name: "insert after", payload: "type+ 2:abcd \"line\\n\"", want: true},
		{name: "malformed recovery candidate", payload: "type \"initializer\"", want: true},
		{name: "mixed recovery candidate", payload: "type 2:abcd \"fixed\"\nin file.go", want: true},
		{name: "complete script", payload: "in file.go\ntype 2:abcd \"fixed\""},
		{name: "old replacement", payload: "2: type 2:abcd \"fixed\""},
		{name: "old accept", payload: "2: accept"},
		{name: "old value row", payload: "2.1: \"fixed\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isHPatchRecoveryCandidate(test.payload); got != test.want {
				t.Fatalf("isHPatchRecoveryCandidate() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHPatchRecoveryGuidanceUsesOrdinaryScriptRows(t *testing.T) {
	script := "in file.go\n" +
		"type 1:ffff <<PATCH\n" +
		"package main\n" +
		"func main() {\n" +
		")\n" +
		"}\n" +
		"PATCH\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", ValueLine: 3,
	}}
	guidance := hpatchRecoveryGuidance(script, rejections)
	for _, want := range []string{
		"Rejected script `LINE:HASH` rows:",
		hpatch.TextReferences(script, 2),
		hpatch.TextReferences(script, 5),
		hpatch.TextReferences(script, 7),
		"Use hpatch without `in` to patch the rejected script.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance does not contain %q:\n%s", want, guidance)
		}
	}
	if strings.Contains(guidance, "INDEX:") || strings.Contains(guidance, "accept") {
		t.Fatalf("guidance retains obsolete indexed recovery language:\n%s", guidance)
	}
}

func TestHPatchRecoveryRowsExposeMalformedFrameEnd(t *testing.T) {
	script := "in file.go\ntype 1:ffff <<PATCH\nbad\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type",
	}}
	got := hpatch.TextReferences(script, hpatchRecoveryRows(script, rejections)...)
	for _, row := range []int{2, 3} {
		if want := hpatch.TextReferences(script, row); !strings.Contains(got, want) {
			t.Fatalf("references do not contain row %d:\n%s", row, got)
		}
	}
}

func TestHPatchRecoveryRowsMapStandaloneCRToOrdinaryRows(t *testing.T) {
	script := "in file.go\n" +
		"type 1:ffff <<PATCH\n" +
		"package main\rvar broken =\n" +
		"PATCH\n"
	rejections := []hpatch.HostRejection{{
		Command: 2, SourceLine: 2, Operation: "type", ValueLine: 1,
	}}
	got := hpatch.TextReferences(script, hpatchRecoveryRows(script, rejections)...)
	for _, row := range []int{2, 3, 4, 5} {
		if want := hpatch.TextReferences(script, row); !strings.Contains(got, want) {
			t.Fatalf("references do not contain ordinary row %d:\n%s", row, got)
		}
	}
}
