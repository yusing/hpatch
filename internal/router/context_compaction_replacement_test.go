package router

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func compactionReplacementReasoning(id string) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": "reasoning", "id": id, "summary": []any{},
		"encrypted_content": strings.Repeat("opaque", 512),
	})
}

func compactionReplacementRecent(prefix string) []json.RawMessage {
	items := []json.RawMessage{mustMarshalJSON(map[string]any{
		"type": "message", "role": "user", "content": "Continue the current task.",
	})}
	for index := range compactionRecentOperations {
		id := fmt.Sprintf("%s_%d", prefix, index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}
	return items
}

func compactionReplacementAssertNative(t *testing.T, items []json.RawMessage, want json.RawMessage) {
	t.Helper()
	if !slices.ContainsFunc(items, func(raw json.RawMessage) bool { return string(raw) == string(want) }) {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(want, &fields)
		t.Fatalf("protected native item changed or retired: type=%q id=%q call=%q", jsonString(fields, "type"), jsonString(fields, "id"), jsonString(fields, "call_id"))
	}
}

func TestCompactionReplacementEvidenceSurvivesEveryPlan(t *testing.T) {
	for _, fixture := range []struct {
		name, command, read string
		rows, native        bool
	}{
		{"search-cat", "rg needle .", "cat listing.txt", false, false},
		{"search-hcat", "hgrep needle .", "hcat listing.txt", false, false},
		{"search-native-hcat", "rg needle .", "hcat listing.txt", false, true},
		{"legacy-search-hread", "hgrep needle .", "hread listing.txt", false, false},
		{"search-source-rows", "rg needle .", "hcat listing.txt", true, false},
		{"repeated-source-rows", "hcat earlier.txt", "hcat listing.txt", true, false},
	} {
		for _, plan := range []compactionRetentionPlan{{8, 8}, {8, 1}, {4, 1}, {2, 1}, {1, 1}} {
			t.Run(fmt.Sprintf("%s/%d/%d", fixture.name, plan.operations, plan.outputs), func(t *testing.T) {
				listing := strings.Repeat("matched file with exact historical search evidence\n", 512)
				if fixture.rows {
					listing = strings.Join(compactionSourceTestRows("", 64), "")
				}
				output := func(id, text string) json.RawMessage {
					if !fixture.native {
						return compactTestOutput(id, text, 0)
					}
					return mustMarshalJSON(map[string]any{
						"type": "function_call_output", "call_id": id,
						"output": "Wall time: 1 seconds\nProcess exited with code 0\nOutput:\n" + text,
					})
				}
				// Both copies are old and share one otherwise-profitable group.
				// Ordinary intra-group reference scanning must not authorize
				// dropping evidence promised by a replacement note.
				items := []json.RawMessage{
					compactionReplacementReasoning("rs_replacement"),
					compactTestCall("replacement-search", fixture.command),
					output("replacement-search", listing),
					compactTestCall("replacement-read", fixture.read),
					output("replacement-read", listing),
					compactionReplacementReasoning("rs_unrelated"),
					compactTestCall("unrelated-finished", "make inspect"),
					compactTestOutput("unrelated-finished", strings.Repeat("UNREFERENCED_BULK\n", 512), 0),
				}
				items = append(items, compactionReplacementRecent("recent")...)
				before := string(mustMarshalJSON(items))
				got := reduceContextCompactionWithPlan(items, plan)
				keptListing := false
				for _, raw := range got {
					compactionVisitReferenceStrings(raw, func(text string) {
						keptListing = keptListing || strings.Contains(text, listing)
					})
				}
				if !keptListing {
					t.Fatal("both verbatim copies of the replacement listing were lost")
				}
				for _, index := range []int{4, 3, 1, 0} {
					compactionReplacementAssertNative(t, got, items[index])
				}
				if !contextCompactionReferencedResults(got)["replacement-read"] {
					t.Fatal("replacement note was lost during output-only reduction")
				}
				if slices.ContainsFunc(got, func(raw json.RawMessage) bool { return string(raw) == string(items[2]) }) {
					t.Fatal("fixture did not replace the original duplicate output")
				}
				if strings.Contains(string(mustMarshalJSON(got)), "UNREFERENCED_BULK") {
					t.Fatal("replacement protection blocked unrelated retirement")
				}
				if string(mustMarshalJSON(items)) != before {
					t.Fatal("compaction mutated its input")
				}
				// A newer duplicate must not redirect a still-referenced result
				// or make its only verbatim copy eligible on the next pass.
				again := append(slices.Clone(got), compactTestCall("newer-copy", fixture.read), output("newer-copy", listing))
				again = append(again, compactionReplacementRecent("next")...)
				again = reduceContextCompactionWithPlan(again, plan)
				compactionReplacementAssertNative(t, again, items[4])
				if !contextCompactionReferencedResults(again)["replacement-read"] {
					t.Fatal("replacement dependency disappeared on repeated compaction")
				}
			})
		}
	}
}

func TestCompactionReplacementNotesSurviveLedgerRetirement(t *testing.T) {
	for _, note := range []string{
		"[mekugi compaction: matching search listing retained verbatim in tool result \"replacement-read\"; original command and successful exit status retained]\n",
		"[mekugi compaction: 64 source rows (1:0001 through 64:0040) retained verbatim in later tool result \"replacement-read\"]\n",
	} {
		listing := strings.Join(compactionSourceTestRows("", 64), "")
		items := []json.RawMessage{
			compactionReplacementReasoning("rs_consumer"),
			compactTestCall("ledger-consumer", "make inspect"),
			compactTestOutput("ledger-consumer", note+strings.Repeat("CONSUMER_BULK\n", 512), 0),
			compactionReplacementReasoning("rs_replacement"),
			compactTestCall("replacement-read", "cat listing.txt"),
			compactTestOutput("replacement-read", listing, 0),
		}
		items = append(items, compactionReplacementRecent("ledger_recent")...)
		retained := retireCompactionOperationsWithFrontier(items, compactionRecentOperations)
		var record map[string]json.RawMessage
		_ = json.Unmarshal(retained[2], &record)
		if jsonString(record, "type") != "message" || strings.Contains(string(retained[2]), "CONSUMER_BULK") {
			t.Fatal("eligible consumer did not retire independently of its protected producer")
		}
		for _, history := range [][]json.RawMessage{retained, consolidateContextCompactionRecords(items, retained)} {
			if !contextCompactionReferencedResults(history)["replacement-read"] {
				t.Fatal("native replacement reference was lost when its consumer became an assistant ledger record")
			}
			compactionReplacementAssertNative(t, history, items[5])
			history = append(slices.Clone(history), compactTestCall("ledger-newer-copy", "cat listing.txt"), compactTestOutput("ledger-newer-copy", listing, 0))
			history = append(history, compactionReplacementRecent("ledger_next")...)
			again := reduceContextCompactionWithPlan(history, compactionRetentionPlan{1, 1})
			compactionReplacementAssertNative(t, again, items[5])
			if !contextCompactionReferencedResults(again)["replacement-read"] {
				t.Fatal("ledger replacement dependency disappeared on repeated compaction")
			}
		}
	}
}

func TestCompactionReplacementPinFollowsOriginalDependencies(t *testing.T) {
	items := []json.RawMessage{
		compactionReplacementReasoning("rs_upstream"),
		compactTestCall("upstream-evidence", "cat upstream.txt"),
		compactTestOutput("upstream-evidence", strings.Repeat("exact upstream evidence\n", 512), 0),
		compactionReplacementReasoning("rs_replacement"),
		compactTestCall("replacement-search", "rg needle ."),
		compactTestOutput("replacement-search", "[mekugi compaction: matching search listing retained verbatim in tool result \"replacement-read\"; original command and successful exit status retained]\n", 0),
		compactTestCall("replacement-read", "cat listing.txt"),
		compactTestOutput("replacement-read", "Depends on upstream-evidence.\n"+strings.Repeat("exact replacement evidence\n", 512), 0),
	}
	items = append(items, compactionReplacementRecent("dependency_recent")...)
	got := retireCompactionOperationsWithFrontier(items, 1)
	for index := range 8 {
		compactionReplacementAssertNative(t, got, items[index])
	}
}
