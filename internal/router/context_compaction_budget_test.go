package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Synthetic additive costs isolate selection from tokenization and reducers.
// Production-tokenizer and transport coverage lives in the integration tests.
func compactionBudgetItem(name string, tokens int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"name":%q,"tokens":%d}`, name, tokens))
}

func compactionBudgetCount(items ...json.RawMessage) (int, bool) {
	total := 0
	for _, raw := range items {
		var item struct {
			Tokens int `json:"tokens"`
		}
		if json.Unmarshal(raw, &item) != nil {
			return 0, false
		}
		total += item.Tokens
	}
	return total, true
}

func TestCompactionBudgetSelection(t *testing.T) {
	tests := []struct {
		name       string
		before     int
		costs      []int
		want       int
		wantPlan   compactionRetentionPlan
		wantError  string
		wantPasses int
	}{
		{"normal target", 120_000, []int{50_000}, 50_000, compactionRetentionPlan{8, 8}, "", 1},
		{"output first", 120_000, []int{95_000, 49_000}, 49_000, compactionRetentionPlan{8, 1}, "", 2},
		{"whole group pressure", 120_000, []int{95_000, 90_000, 70_000, 60_000, 45_000}, 45_000, compactionRetentionPlan{1, 1}, "", 5},
		{"bounded overshoot", 120_000, []int{100_000, 90_000, 80_000, 81_000, 82_000}, 80_000, compactionRetentionPlan{4, 1}, "", 5},
		{"nonmonotonic costs", 120_000, []int{100_000, 65_000, 70_000, 71_000, 66_000}, 65_000, compactionRetentionPlan{8, 1}, "", 5},
		{"ties preserve less aggressive plan", 120_000, []int{70_000, 70_000, 70_000, 70_000, 70_000}, 70_000, compactionRetentionPlan{8, 8}, "", 5},
		{"over ceiling", 120_000, []int{100_000, 95_000, 90_000, 85_000, 80_001}, 80_001, compactionRetentionPlan{1, 1}, "ceiling 80000", 5},
		{"already small no-op", 40_000, []int{40_000}, 40_000, compactionRetentionPlan{}, "no supported token reduction", 1},
		{"already small growth", 40_000, []int{45_000}, 40_000, compactionRetentionPlan{}, "no supported token reduction", 1},
		{"no gain within allowance", 60_000, []int{60_000, 60_000, 60_000, 60_000, 60_000}, 60_000, compactionRetentionPlan{}, "no supported token reduction", 5},
		{"reject growth", 70_000, []int{71_000, 72_000, 73_000, 74_000, 75_000}, 70_000, compactionRetentionPlan{}, "no supported token reduction", 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []json.RawMessage{compactionBudgetItem("original", test.before)}
			original := bytes.Clone(input[0])
			passes := 0
			plans := []compactionRetentionPlan{{8, 8}, {8, 1}, {4, 1}, {2, 1}, {1, 1}}
			reduce := func(history []json.RawMessage, plan compactionRetentionPlan) []json.RawMessage {
				if len(history) != 1 || !bytes.Equal(history[0], original) {
					t.Fatal("candidate compiled from a previously reduced history")
				}
				if passes >= len(test.costs) || plan != plans[passes] {
					t.Fatalf("unexpected escalation: pass=%d plan=%+v", passes, plan)
				}
				item := compactionBudgetItem(fmt.Sprintf("candidate-%d", passes), test.costs[passes])
				passes++
				return []json.RawMessage{item}
			}
			got, report, err := selectCompactionWorkingSet(context.Background(), input,
				compactionTargetTokens, compactionOvershootTokens, reduce, compactionBudgetCount)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				tokens, ok := compactionBudgetCount(got...)
				if !ok || tokens != test.want {
					t.Fatalf("selected tokens=%d, want=%d", tokens, test.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) || got != nil {
				t.Fatalf("unsafe/no-op candidate returned: items=%d err=%v", len(got), err)
			}
			if report.before != test.before || report.after != test.want || report.plan != test.wantPlan || passes != test.wantPasses {
				t.Fatalf("report=%+v passes=%d", report, passes)
			}
			if !bytes.Equal(input[0], original) {
				t.Fatal("selection mutated original history")
			}
		})
	}
}

func TestCompactionBudgetCacheCountsOccurrences(t *testing.T) {
	authority := compactionBudgetItem("authority", 10)
	bulk := compactionBudgetItem("bulk", 200)
	input := []json.RawMessage{authority, authority, bulk}
	counts := make(map[string]int)
	count := func(items ...json.RawMessage) (int, bool) {
		for _, item := range items {
			counts[string(item)]++
		}
		return compactionBudgetCount(items...)
	}
	pass := 0
	reduce := func(history []json.RawMessage, _ compactionRetentionPlan) []json.RawMessage {
		cost := []int{120, 90, 70, 60, 50}[pass]
		pass++
		return []json.RawMessage{history[0], history[1], compactionBudgetItem("smaller", cost)}
	}
	_, report, err := selectCompactionWorkingSet(context.Background(), input, 50, 30, reduce, count)
	if err != nil || report.before != 220 || report.after != 70 || pass != 5 {
		t.Fatalf("duplicate occurrences miscounted: report=%+v passes=%d err=%v", report, pass, err)
	}
	for key, calls := range counts {
		if calls != 1 {
			t.Fatalf("retokenized an unchanged item %q %d times", key, calls)
		}
	}
	// No cached transcript or costs may leak across requests.
	_, _, err = selectCompactionWorkingSet(context.Background(), input, 500, 0,
		func(items []json.RawMessage, _ compactionRetentionPlan) []json.RawMessage { return items }, count)
	if err == nil || counts[string(authority)] != 2 {
		t.Fatalf("request-local cache escaped its lifetime: counts=%v err=%v", counts, err)
	}
}

func TestCompactionBudgetErrorsAndCancellation(t *testing.T) {
	input := []json.RawMessage{compactionBudgetItem("original", 100)}
	for _, name := range []string{"empty input", "empty candidate", "input counting", "candidate counting", "canceled before", "canceled during reduction", "canceled during counting"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			history := slices.Clone(input)
			passes, counts := 0, 0
			reduce := func([]json.RawMessage, compactionRetentionPlan) []json.RawMessage {
				passes++
				if name == "empty candidate" {
					return nil
				}
				if name == "canceled during reduction" {
					cancel()
				}
				return []json.RawMessage{compactionBudgetItem("reduced", 30)}
			}
			count := func(items ...json.RawMessage) (int, bool) {
				counts++
				if name == "input counting" || name == "candidate counting" && counts == 2 {
					return 0, false
				}
				if name == "canceled during counting" {
					cancel()
				}
				return compactionBudgetCount(items...)
			}
			switch name {
			case "empty input":
				history = nil
			case "canceled before":
				cancel()
			}
			got, _, err := selectCompactionWorkingSet(ctx, history, 50, 30, reduce, count)
			if err == nil || got != nil {
				t.Fatalf("error produced a replacement history: %v", err)
			}
			if strings.HasPrefix(name, "canceled") && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was hidden: %v", err)
			}
			if (name == "empty input" || name == "input counting" || name == "canceled before" || name == "canceled during counting") && passes != 0 {
				t.Fatal("reduction ran after an input failure")
			}
		})
	}
}

func FuzzCompactionBudgetAdmission(f *testing.F) {
	f.Add([]byte{200, 180, 140, 110, 90, 50})
	f.Add([]byte{90, 90, 90, 90, 90, 90})
	f.Add([]byte{20, 25, 10, 5, 4, 3})
	f.Fuzz(func(t *testing.T, costs []byte) {
		if len(costs) < 6 {
			return
		}
		before := int(costs[0])
		input := []json.RawMessage{compactionBudgetItem("original", before)}
		pass, best := 0, before
		reduce := func([]json.RawMessage, compactionRetentionPlan) []json.RawMessage {
			pass++
			cost := int(costs[pass])
			best = min(best, cost)
			return []json.RawMessage{compactionBudgetItem(fmt.Sprint(pass), cost)}
		}
		got, report, err := selectCompactionWorkingSet(context.Background(), input, 50, 30, reduce, compactionBudgetCount)
		if report.after != best || report.before != before || pass < 1 || pass > 5 {
			t.Fatalf("invalid selection: report=%+v best=%d passes=%d", report, best, pass)
		}
		admissible := best < before && best <= 80
		if (err == nil) != admissible || err != nil && got != nil {
			t.Fatalf("admission mismatch: report=%+v err=%v", report, err)
		}
		if err == nil {
			tokens, ok := compactionBudgetCount(got...)
			if !ok || tokens != best {
				t.Fatalf("wrong candidate returned: tokens=%d best=%d", tokens, best)
			}
		}
	})
}
