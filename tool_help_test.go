package hpatch

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestToolGrammarTSelQuotesExcludeDecodedLineBreaks(t *testing.T) {
	quoted := grammarTerminalRegexp(t, "TSEL_QUOTED")
	tests := []struct {
		value string
		want  bool
	}{
		{value: `"text"`, want: true},
		{value: `"tab\ttext"`, want: true},
		{value: `"unicode\u000B"`, want: true},
		{value: `"unicode\u000b"`, want: true},
		{value: "\"literal\ttext\"", want: true},
		{value: `""`, want: false},
		{value: `"first\nsecond"`, want: false},
		{value: `"first\rsecond"`, want: false},
		{value: `"first\u000Asecond"`, want: false},
		{value: `"first\u000asecond"`, want: false},
		{value: `"first\u000Dsecond"`, want: false},
		{value: `"first\u000dsecond"`, want: false},
	}
	for _, test := range tests {
		if got := quoted.MatchString(test.value); got != test.want {
			t.Errorf("TSEL_QUOTED matches %q = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestToolGrammarLineTerminatorsMatchPhysicalLineParser(t *testing.T) {
	newline := grammarTerminalRegexp(t, "NL")
	for value, want := range map[string]bool{
		"\n":   true,
		"\r\n": true,
		"\r":   false,
	} {
		if got := newline.MatchString(value); got != want {
			t.Errorf("NL matches %q = %v, want %v", value, got, want)
		}
	}

	bodyLine := grammarTerminalRegexp(t, "PATCH_BODY_LINE")
	for value, want := range map[string]bool{
		"body\n":   true,
		"body\r\n": true,
		"body\r":   false,
	} {
		if got := bodyLine.MatchString(value); got != want {
			t.Errorf("PATCH_BODY_LINE matches %q = %v, want %v", value, got, want)
		}
	}
}

func TestToolGrammarCommandOperandsUseExactSpaces(t *testing.T) {
	for _, rule := range []string{
		"path_command: PATH_OP SP PATH",
		"sel_command: \"sel\" SP POSINT SP POSINT \":\" POSINT",
		"tsel_command: \"tsel\" SP POSINT SP TSEL_QUOTED (SP POSINT)?",
		"bsel_command: \"bsel\" SP NONEMPTY_QUOTED SP NONEMPTY_QUOTED",
		"rsel_command: \"rsel\" SP POSINT \":\" POSINT",
		"type_command: \"type\" SP QUOTED",
		"heredoc_command: \"type\" SP \"<<PATCH\" NL _patch_body \"PATCH\"",
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits exact-space rule %q", rule)
		}
	}
}

func TestToolDescriptionSelectorTokenComparisons(t *testing.T) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		script string
		want   int
	}{
		{
			name: "long body rsel",
			script: "functions.hpatch\nin invoice.go\nrsel 20:27\ntype <<PATCH\n" +
				"func calculateInvoiceTotal(\n    invoice Invoice,\n    discounts []Discount,\n) (Money, error) {\n" +
				"    subtotal := sumLines(invoice.Lines)\n    adjusted, err := applyDiscounts(subtotal, discounts)\n" +
				"    if err != nil {\n        return Money{}, err\n    }\n    return adjusted, nil\n}\nPATCH",
			want: 84,
		},
		{
			name: "long body bsel",
			script: "functions.hpatch\nin invoice.go\n" +
				"bsel \"subtotal := sumLines(invoice.Lines)\" \"return adjusted, nil\"\ntype <<PATCH\n" +
				"subtotal := sumLines(invoice.Lines)\n    adjusted, err := applyDiscounts(subtotal, discounts)\n" +
				"    if err != nil {\n        return Money{}, err\n    }\n    return adjusted, nil\nPATCH",
			want: 71,
		},
		{
			name:   "short block rsel",
			script: "functions.hpatch\nin worker.go\nrsel 40:42\ntype <<PATCH\nif ready {\n    return run(ctx)\n} // ready\nPATCH",
			want:   32,
		},
		{
			name:   "short block bsel",
			script: "functions.hpatch\nin worker.go\nbsel \"if ready {\" \"} // ready\"\ntype <<PATCH\nif ready {\n    return run(ctx)\n} // ready\nPATCH",
			want:   35,
		},
		{
			name:   "expression rsel",
			script: "functions.hpatch\nin worker.go\nrsel 40:40\ntype \"result := calculateFreshValue(request, cache)\"",
			want:   26,
		},
		{
			name:   "expression tsel",
			script: "functions.hpatch\nin worker.go\ntsel 40 \"calculateValue(request)\"\ntype \"calculateFreshValue(request, cache)\"",
			want:   25,
		},
		{
			name:   "expression sel",
			script: "functions.hpatch\nin worker.go\nsel 40 11:33\ntype \"calculateFreshValue(request, cache)\"",
			want:   25,
		},
		{
			name:   "two sites rsel",
			script: "functions.hpatch\nin config.go\nrsel 10:11\ntype <<PATCH\ncache.Enabled = true\nretry.Enabled = true\nPATCH",
			want:   30,
		},
		{
			name:   "two sites tsel",
			script: "functions.hpatch\nin config.go\ntsel 10 \"Enabled = false\" 2\ntype \"Enabled = true\"",
			want:   25,
		},
	}
	counts := make(map[string]int, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := codec.Count(test.script)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("tokens = %d, want %d", got, test.want)
			}
			counts[test.name] = got
		})
	}
	if counts["expression tsel"] != counts["expression sel"] {
		t.Fatalf("expression selectors differ: tsel %d, sel %d", counts["expression tsel"], counts["expression sel"])
	}
	for _, comparison := range []string{
		fmt.Sprintf("bsel %d versus rsel %d", counts["long body bsel"], counts["long body rsel"]),
		fmt.Sprintf("rsel %d versus bsel %d", counts["short block rsel"], counts["short block bsel"]),
		fmt.Sprintf("tsel or sel %d versus rsel %d", counts["expression tsel"], counts["expression rsel"]),
		fmt.Sprintf("tsel %d versus rsel %d", counts["two sites tsel"], counts["two sites rsel"]),
	} {
		if !strings.Contains(toolDescription, comparison) {
			t.Errorf("tool description omits measured comparison %q", comparison)
		}
	}
}

func grammarTerminalRegexp(t *testing.T, name string) *regexp.Regexp {
	t.Helper()
	prefix := name + ": /"
	for line := range strings.SplitSeq(toolGrammar, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		pattern, ok := strings.CutSuffix(strings.TrimPrefix(line, prefix), "/")
		if !ok {
			t.Fatalf("%s terminal does not end with /: %q", name, line)
		}
		pattern = strings.ReplaceAll(pattern, `\/`, `/`)
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			t.Fatalf("compile %s terminal: %v", name, err)
		}
		return compiled
	}
	t.Fatalf("%s terminal not found", name)
	return nil
}
