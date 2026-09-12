package router

import (
	"fmt"
	"strconv"
	"strings"
)

// tokenCost contains estimates for disjoint billable categories. Reasoning is
// already included in output; cached input is already included in input.
type tokenCost struct {
	uncachedInput float64
	cachedInput   float64
	output        float64
	known         bool
}

type tokenUsageReport struct {
	tokenCounts
	cost tokenCost
}

type tokenPrice struct {
	input, cachedInput, output             float64
	longInput, longCachedInput, longOutput float64
}

// Source: session-usage/scripts/session_usage.py:625:655 select_tier and cost_for_request.
// Rates mirror FALLBACK_USD_PER_MILLION in the same script. These are reference
// list API prices in USD per million tokens, not subscription charges or live billing quotes.
var tokenReferencePrices = map[string]tokenPrice{
	"gpt-6-astra":     {10, 1, 50, 20, 2, 75},
	"gpt-6-astra-pro": {10, 1, 50, 20, 2, 75},
	"gpt-5.5":         {5, 0.5, 30, 10, 1, 45},
	"gpt-5.6-sol":     {4, 0.4, 20, 8, 0.8, 30},
	"gpt-5.6-terra":   {2, 0.2, 12, 4, 0.4, 18},
	"gpt-5.6-luna":    {0.2, 0.02, 1.2, 0.4, 0.04, 1.8},
	"gpt-5.4":         {2.5, 0.25, 15, 5, 0.5, 22.5},
	"gpt-5.4-mini":    {0.75, 0.075, 4.5, 0, 0, 0},
	"gpt-5.4-nano":    {0.2, 0.02, 1.25, 0, 0, 0},
}

func estimateTokenCost(model string, counts tokenCounts) tokenCost {
	model = strings.TrimPrefix(model, "openai/")
	price, ok := tokenReferencePrices[model]
	if !ok || counts.Inconsistent || counts.UncachedInputTokens > counts.InputTokens || counts.ReasoningTokens > counts.OutputTokens {
		return tokenCost{}
	}
	// Select the tier per response, never from cumulative thread input.
	if counts.InputTokens >= 272_000 && price.longInput != 0 {
		price.input, price.cachedInput, price.output = price.longInput, price.longCachedInput, price.longOutput
	}
	return tokenCost{
		uncachedInput: float64(counts.UncachedInputTokens) * price.input / 1_000_000,
		cachedInput:   float64(counts.InputTokens-counts.UncachedInputTokens) * price.cachedInput / 1_000_000,
		output:        float64(counts.OutputTokens) * price.output / 1_000_000,
		known:         true,
	}
}

func (cost *tokenCost) add(next tokenCost) {
	cost.uncachedInput += next.uncachedInput
	cost.cachedInput += next.cachedInput
	cost.output += next.output
	cost.known = cost.known && next.known
}

func formatTokenUsageReport(report tokenUsageReport) string {
	var text strings.Builder
	text.WriteString("Tokens:\n\n| Category | Tokens | API USD |\n| --- | ---: | ---: |\n")
	for _, row := range []struct {
		label  string
		tokens string
		usd    float64
		billed bool
	}{
		{"Input", formatUsageTokens(report.InputTokens), 0, false},
		{"Cached input", formatUsageTokens(report.InputTokens - min(report.InputTokens, report.UncachedInputTokens)), report.cost.cachedInput, true},
		{"Uncached input", formatUsageTokens(report.UncachedInputTokens), report.cost.uncachedInput, true},
		{"Output", formatUsageTokens(report.OutputTokens), report.cost.output, true},
		{"Reasoning", formatUsageTokens(report.ReasoningTokens), 0, false},
		{"Total", "—", report.cost.uncachedInput + report.cost.cachedInput + report.cost.output, true},
	} {
		amount := "—"
		if row.billed {
			amount = "n/a"
			if report.cost.known {
				amount = fmt.Sprintf("$%.4f", row.usd)
			}
		}
		fmt.Fprintf(&text, "| %s | %s | %s |\n", row.label, row.tokens, amount)
	}
	text.WriteString("\nThis thread; cached input and reasoning are included, not added. Reference API prices, not subscription charges.")
	if !report.cost.known {
		text.WriteString(" Cost unavailable: unknown pricing or inconsistent usage.")
	}
	return text.String()
}

func formatUsageTokens(count uint64) string {
	digits := strconv.FormatUint(count, 10)
	var text strings.Builder
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			text.WriteByte(',')
		}
		text.WriteByte(digits[i])
	}
	return text.String()
}
