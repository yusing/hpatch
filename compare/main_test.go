package main

import (
	"hpatch/internal/patchtest"
	"reflect"
	"strings"
	"testing"
)

func TestRunHPatchAcceptsFinalStateReportAndPreservesUnrelatedFiles(t *testing.T) {
	scenario := scenario{
		initial: map[string]string{
			"target.txt":    "old\n",
			"unrelated.txt": "keep\n",
		},
		script: "in target.txt\ntsel 1 \"old\"\ntype \"new\"\n",
	}
	got, err := runHPatch(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if got["target.txt"] != "new\n" || got["unrelated.txt"] != "keep\n" || len(got) != 2 {
		t.Fatalf("tree = %#v", got)
	}
}

func TestRunHPatchRejectsMalformedAndFutureCommands(t *testing.T) {
	for _, script := range []string{
		"in target.txt\ntsel 1\n",
		"future-command\n",
	} {
		t.Run(strings.TrimSpace(script), func(t *testing.T) {
			_, err := runHPatch(scenario{initial: map[string]string{"target.txt": "old\n"}, script: script})
			if err == nil || !strings.Contains(err.Error(), "hpatch exited 1") {
				t.Fatalf("runHPatch() error = %v", err)
			}
		})
	}
}

func TestScenariosProduceEquivalentChanges(t *testing.T) {
	for _, scenario := range scenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			got, err := runHPatch(scenario)
			if err != nil {
				t.Fatal(err)
			}
			want, err := patchtest.Apply(scenario.initial, scenario.patch)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("hpatch tree = %#v, apply_patch tree = %#v", got, want)
			}
		})
	}
}
