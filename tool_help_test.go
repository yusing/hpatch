package hpatch

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestHPatch2ToolGrammarQuotedOperands(t *testing.T) {
	target := grammarTerminalRegexp(t, "TARGET_QUOTED")
	for _, value := range []string{
		`"text"`,
		`"quote\"slash\\solidus\/"`,
		`"line\ntext"`,
		`"line\u000Atext"`,
		`"line\u000atext"`,
		`"tab\ttext"`,
		`"tab\u0009text"`,
		"\"literal\ttext\"",
	} {
		if !target.MatchString(value) {
			t.Errorf("TARGET_QUOTED rejects valid value %q", value)
		}
	}
	for control := range 0x20 {
		encoded := fmt.Sprintf(`"before\u%04Xafter"`, control)
		if got, want := target.MatchString(encoded), control == '\t' || control == '\n'; got != want {
			t.Errorf("TARGET_QUOTED matches encoded U+%04X = %v, want %v", control, got, want)
		}
	}
	for _, value := range []string{
		`""`,
		"\"raw\nnewline\"",
		"\"raw\rreturn\"",
		"\"raw\x01control\"",
		`"return\r"`,
		`"return\u000D"`,
		`"vertical\u000btab"`,
	} {
		if target.MatchString(value) {
			t.Errorf("TARGET_QUOTED accepts invalid value %q", value)
		}
	}

	value := grammarTerminalRegexp(t, "QUOTED")
	for _, input := range []string{`""`, `"line\nvalue"`, `"return\rvalue"`, `"tab\tvalue"`} {
		if !value.MatchString(input) {
			t.Errorf("QUOTED rejects valid value %q", input)
		}
	}
}

func TestHPatch2ToolGrammarMatchesPublicCommands(t *testing.T) {
	for _, rule := range []string{
		`path_command: PATH_OP SP PATH`,
		`inline_mutation: "type" SP target SP QUOTED`,
		`| "add" SP add_destination SP QUOTED`,
		`heredoc_mutation: "type" SP target SP "<<PATCH" NL _patch_body "PATCH"`,
		`| "add" SP add_destination SP "<<PATCH" NL _patch_body "PATCH"`,
		`inline_initializer: "type" SP QUOTED`,
		`heredoc_initializer: "type" SP "<<PATCH" NL _patch_body "PATCH"`,
		`ROW: /[1-9][0-9]*:[0-9a-f]{4}/`,
		`| "EOF"`,
		`| TARGET_QUOTED (SP POSINT)?`,
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits %q", rule)
		}
	}
}

func TestHPatch2ToolGrammarLineTerminators(t *testing.T) {
	newline := grammarTerminalRegexp(t, "NL")
	for value, want := range map[string]bool{"\n": true, "\r\n": true, "\r": false} {
		if got := newline.MatchString(value); got != want {
			t.Errorf("NL matches %q = %v, want %v", value, got, want)
		}
	}

	bodyLine := grammarTerminalRegexp(t, "PATCH_BODY_LINE")
	matchesBodyLine := bodyLine.MatchString
	for value, want := range map[string]bool{
		"body\n":                             true,
		"body\r\n":                           true,
		"body\r":                             false,
		"PATCH\n":                            false,
		"new index.html\n":                   true,
		" type <<PATCH\n":                    true,
		"type <<PATCH extra\n":               true,
		"add-not-an-opener <<PATCH\n":        true,
		"type <<PATCH\n":                     false,
		"type 1:a2b3 <<PATCH\n":              false,
		"add 1:a2b3 <<PATCH\r\n":             false,
		"add 1:a2b3 \"literal\" 2 <<PATCH\n": false,
	} {
		if got := matchesBodyLine(value); got != want {
			t.Errorf("patch body matches %q = %v, want %v", value, got, want)
		}
	}
}

func TestToolDescriptionIsNonInstructional(t *testing.T) {
	const want = "Atomic HPATCH/2 edit-script application. Rejection or cancellation leaves the workspace unchanged."
	if got := ToolDescription(); got != want {
		t.Fatalf("ToolDescription() = %q, want %q", got, want)
	}
}

func TestHPatch2ToolDescriptionExamplesExecute(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "parser.go", "package parser\n\nfunc parse() {}\n", 0o644)
	script := "in parser.go\n" +
		"add " + row(3, "func parse() {}") + ` "// parse converts one command.\n"`
	result, err := applyForHostAtTest(t, root, script, "")
	if err != nil {
		t.Fatalf("ApplyForHost() error = %v, diagnostic %q", err, result.Diagnostic)
	}
	want := "package parser\n\n// parse converts one command.\nfunc parse() {}\n"
	if got := readTestFile(t, root, "parser.go"); got != want {
		t.Fatalf("parser.go = %q, want %q", got, want)
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
		pattern = strings.ReplaceAll(pattern, `\/`, "/")
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			t.Fatalf("compile %s terminal: %v", name, err)
		}
		return compiled
	}
	t.Fatalf("%s terminal not found", name)
	return nil
}
