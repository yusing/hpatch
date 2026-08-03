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
		if got, want := target.MatchString(encoded), control == '\t'; got != want {
			t.Errorf("TARGET_QUOTED matches encoded U+%04X = %v, want %v", control, got, want)
		}
	}
	for _, value := range []string{`""`, `"newline\n"`, `"return\r"`, `"vertical\u000btab"`} {
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
		`inline_mutation: TYPE_OP SP target SP QUOTED`,
		`heredoc_mutation: TYPE_OP SP target SP "<<PATCH" NL _patch_body "PATCH"`,
		`inline_initializer: "type" SP QUOTED`,
		`heredoc_initializer: "type" SP "<<PATCH" NL _patch_body "PATCH"`,
		`delete_command: "del" SP target`,
		`ROW: /[1-9][0-9]*:[0-9a-f]{4}/`,
		`TYPE_OP: "type" | "type-" | "type+"`,
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits %q", rule)
		}
	}
	for _, removed := range []string{"tsel_command", "rsel_command", `"copy"`, `"cut"`, `"paste"`, `"commit"`, "<<TAG"} {
		if strings.Contains(toolGrammar, removed) {
			t.Errorf("tool grammar retains HPATCH/1 form %q", removed)
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
	for value, want := range map[string]bool{"body\n": true, "body\r\n": true, "body\r": false} {
		if got := bodyLine.MatchString(value); got != want {
			t.Errorf("PATCH_BODY_LINE matches %q = %v, want %v", value, got, want)
		}
	}
}

func TestHPatch2ToolDescriptionCoversSafeCommandChoice(t *testing.T) {
	normalized := strings.Join(strings.Fields(toolDescription), " ")
	for _, guidance := range []string{
		"HPATCH/2",
		"Do not call this tool in parallel with other tools.",
		"LINE:HASH TEXT",
		"copy the complete `LINE:HASH` reference",
		"Line and range replacement preserve",
		"`type` replaces",
		"`type-` inserts before",
		"`type+` inserts after",
		"`del` deletes",
		"fixed `<<PATCH`",
		"immutable baseline",
		"not targetable in the same call",
		"apply, reread, and use a later invocation",
		"Multiple insertions at the same boundary render in script order.",
		"Changed Go files are parsed and formatted before success",
		"parents for `new` or `mv` must exist",
		"reread stale rows instead of guessing",
	} {
		if !strings.Contains(normalized, guidance) {
			t.Errorf("tool description omits %q", guidance)
		}
	}
	for _, excluded := range []string{"HPATCH/1", "\ntsel ", "\nrsel ", "\ncopy", "\ncut", "\npaste", "\ncommit", "<<TAG", "Usage:", "--root", "hpatch gain"} {
		if strings.Contains(toolDescription, excluded) {
			t.Errorf("tool description retains excluded material %q", excluded)
		}
	}
}

func TestHPatch2ToolDescriptionExamplesExecute(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "parser.go", "package parser\n\nfunc parse() {}\n", 0o644)
	script := "in parser.go\n" +
		"type- " + row(3, "func parse() {}") + ` "// parse converts one command.\n"`
	_, stderr, exitCode := runForTest(root, nil, script)
	if exitCode != 0 {
		t.Fatalf("Run() = exit %d, stderr %q", exitCode, stderr)
	}
	want := "package parser\n\n// parse converts one command.\nfunc parse() {}\n"
	if got := readTestFile(t, root, "parser.go"); got != want {
		t.Fatalf("parser.go = %q, want %q", got, want)
	}
}

func TestHPatch2ToolDescriptionUsesInlineForSingleLineInsertion(t *testing.T) {
	if !strings.Contains(toolDescription, `type- 37:8c2f "// parseCommand parses one physical script line.\n"`) {
		t.Fatal("tool description lacks the approved non-heredoc single-line insertion")
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
