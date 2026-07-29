package hpatch

import (
	"regexp"
	"strings"
	"testing"
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
