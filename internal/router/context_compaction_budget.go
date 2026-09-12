package router

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	compactionTargetTokens    = 50_000
	compactionOvershootTokens = 30_000
)

// A retention plan changes eligibility, never the evidence-preservation rules.
// Output-only pruning gets the first opportunity to release the warm frontier:
// exact invocations and native reasoning can survive without their old bulk.
type compactionRetentionPlan struct {
	operations int
	outputs    int
}

type compactionBudgetReport struct {
	before int
	after  int
	plan   compactionRetentionPlan
}

// selectCompactionWorkingSet measures the native replay, not the encrypted wire
// envelope. Every candidate is compiled from the same input; a more aggressive
// attempt cannot erode the next attempt's evidence. The reducer owns semantic
// eligibility, reference closure and atomic reasoning/tool groups; this selector
// owns only pressure, candidate ordering and admission.
//
// count is additive across native items (the visible-string token metric). The
// request-local cache avoids retokenizing unchanged authority/frontier items on
// every attempt. It is not a transcript store and never survives the request.
func selectCompactionWorkingSet(
	ctx context.Context,
	input []json.RawMessage,
	target, overshoot int,
	reduce func([]json.RawMessage, compactionRetentionPlan) []json.RawMessage,
	count func(...json.RawMessage) (int, bool),
) ([]json.RawMessage, compactionBudgetReport, error) {
	var report compactionBudgetReport
	if len(input) == 0 {
		return nil, report, fmt.Errorf("local compaction requires nonempty history after removing the trigger")
	}
	cache := make(map[string]int)
	measure := func(items []json.RawMessage) (int, error) {
		total := 0
		for _, raw := range items {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			key := string(raw)
			tokens, cached := cache[key]
			if !cached {
				var ok bool
				tokens, ok = count(raw)
				if !ok {
					return 0, fmt.Errorf("cannot measure native compaction history with the visible-string tokenizer")
				}
				cache[key] = tokens
			}
			total += tokens
		}
		return total, nil
	}
	before, err := measure(input)
	if err != nil {
		return nil, report, err
	}
	report.before, report.after = before, before
	best := input
	// Preserve the established eight-operation continuity buffer until actual
	// token pressure warrants reducing completed output, then whole groups.
	// The newest invocation/result is never made eligible by budget pressure.
	for _, plan := range []compactionRetentionPlan{{8, 8}, {8, 1}, {4, 1}, {2, 1}, {1, 1}} {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		candidate := reduce(input, plan)
		if len(candidate) == 0 {
			return nil, report, fmt.Errorf("context reduction produced empty native history")
		}
		tokens, err := measure(candidate)
		if err != nil {
			return nil, report, err
		}
		if tokens < report.after {
			best, report.after, report.plan = candidate, tokens, plan
		}
		if report.after <= target {
			// No pressure means no escalation, even when the normal pass
			// cannot reduce an already-small window. That is a no-op error,
			// not permission to discard the warm frontier to force success.
			break
		}
	}
	if report.after > target+overshoot {
		return nil, report, fmt.Errorf("native compaction history retains %d visible-string tokens (before %d; target %d + overshoot %d = ceiling %d); protected or unsupported context was not discarded", report.after, before, target, overshoot, target+overshoot)
	}
	if report.after >= before {
		return nil, report, fmt.Errorf("no supported token reduction is available for this history (%d visible-string tokens); protected context was not discarded and no provider compaction was requested", before)
	}
	return best, report, nil
}
