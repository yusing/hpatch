package mekugi

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestMekugi2ToolGrammarQuotedOperands(t *testing.T) {
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

func TestMekugi2ToolGrammarMatchesPublicCommands(t *testing.T) {
	for _, rule := range []string{
		`path_command: PATH_OP SP PATH`,
		`inline_mutation: "type" SP target SP QUOTED`,
		`| "add" SP add_destination SP QUOTED`,
		`heredoc_mutation: "type" SP target SP HEREDOC_MARKER NL _patch_body "PATCH"`,
		`| "add" SP add_destination SP HEREDOC_MARKER NL _patch_body "PATCH"`,
		`inline_initializer: "type" SP QUOTED`,
		`heredoc_initializer: "type" SP HEREDOC_MARKER NL _patch_body "PATCH"`,
		`HEREDOC_MARKER: "<<PATCH" | "<<PATCH-"`,
		`ROW: /[1-9][0-9]*:[0-9a-f]{4}/`,
		`| "EOF"`,
		`| TARGET_QUOTED (SP POSINT)?`,
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits %q", rule)
		}
	}
}

func TestMekugi2ToolGrammarLineTerminators(t *testing.T) {
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
		"type <<PATCH-\n":                    false,
		"type 1:a2b3 <<PATCH-\n":             false,
		"add EOF <<PATCH-\r\n":               false,
		"type <<PATCH- extra\n":              true,
		"type <<PATCH--\n":                   true,
		"type x<<PATCH-\n":                   true,
		"type <<PATC-\n":                     true,
		" type <<PATCH-\n":                   true,
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

func TestMekugi2ToolGrammarHeredocSuffixes(t *testing.T) {
	bodyLine := grammarTerminalRegexp(t, "PATCH_BODY_LINE")
	// Exercise every suffix boundary and one-character near miss. The body
	// grammar reserves only complete unindented heredoc opener-shaped lines.
	for _, operation := range []string{"type", "add", " type", "add-not-an-opener"} {
		for _, prefix := range []string{"", "1:abcd ", `1:abcd "literal" 2 `, "x", "x "} {
			for _, marker := range []string{"<<PATCH", "<<PATCH-"} {
				candidates := []string{marker, marker + "-", marker + " ", marker + " extra"}
				for i := range len(marker) {
					candidates = append(candidates, marker[:i], marker[:i]+"x"+marker[i+1:])
				}
				for _, suffix := range candidates {
					line := operation + " " + prefix + suffix
					operands := strings.TrimPrefix(line, operation+" ")
					reserved := false
					if operation == "type" || operation == "add" {
						for _, opener := range []string{"<<PATCH", "<<PATCH-"} {
							reserved = reserved || operands == opener || strings.HasSuffix(operands, " "+opener)
						}
					}
					for _, ending := range []string{"\n", "\r\n"} {
						if got := bodyLine.MatchString(line + ending); got == reserved {
							t.Fatalf("body accepts %q = %v, reserved = %v", line+ending, got, reserved)
						}
					}
				}
			}
		}
	}
}

func TestTextGrammarPayloadLines(t *testing.T) {
	line := grammarTerminalRegexp(t, "TEXT_BODY_LINE")
	for _, payload := range []string{"PATCH", "TEXT", "type <<PATCH", "type <<TEXT-", "|", "", `"\`, "世界"} {
		for _, ending := range []string{"\n", "\r\n"} {
			if !line.MatchString("|" + payload + ending) {
				t.Errorf("text body rejects %q", payload+ending)
			}
		}
	}
	for _, invalid := range []string{"TEXT\n", "rm\n", "\n", "|no terminator", "|bare\r"} {
		if line.MatchString(invalid) {
			t.Errorf("text body accepts %q", invalid)
		}
	}
	for _, rule := range []string{
		`text_mutation: "type" SP target SP TEXT_MARKER NL TEXT_BODY_LINE* "TEXT"`,
		`| "add" SP add_destination SP TEXT_MARKER NL TEXT_BODY_LINE* "TEXT"`,
		`text_initializer: "type" SP TEXT_MARKER NL TEXT_BODY_LINE* "TEXT"`,
		`TEXT_MARKER: "<<TEXT" | "<<TEXT-"`,
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits %q", rule)
		}
	}
}

func TestShellGrammarPayloadLines(t *testing.T) {
	line := grammarTerminalRegexp(t, "SHELL_BODY_LINE")
	suffix := grammarTerminalRegexp(t, "SHELL_SUFFIX")
	matchesBodyLine := func(value string) bool {
		if line.MatchString(value) {
			return true
		}
		tail, ok := strings.CutPrefix(value, "SHELL")
		return ok && strings.HasSuffix(tail, "\n") && suffix.MatchString(strings.TrimSuffix(tail, "\n"))
	}
	for _, payload := range []string{"", "S", "SH", "SHE", "SHEL", "SHELL ", " SHELL", "SHELLx", "Sx", "SHx", "SHEx", "SHELx", "a\rb", "SHELL\rx", "\r", "#!python3", "type <<PATCH", "PATCH", "TEXT", `echo "$(date)"`, "世界"} {
		for _, ending := range []string{"\n", "\r\n"} {
			if !matchesBodyLine(payload + ending) {
				t.Errorf("shell body rejects %q", payload+ending)
			}
		}
	}
	for _, invalid := range []string{"SHELL\n", "SHELL\r\n", "no terminator", "bare\r"} {
		if matchesBodyLine(invalid) {
			t.Errorf("shell body accepts %q", invalid)
		}
	}
}

func TestShellGrammarClosingLineLexing(t *testing.T) {
	// A body token must not consume the CR after SHELL before discovering LF.
	// Factor the marker from its suffix so NL can win at that boundary.
	body := grammarTerminalRegexp(t, "SHELL_BODY_LINE")
	for _, ending := range []string{"\n", "\r\n"} {
		if body.MatchString("SHELL" + ending) {
			t.Fatalf("closing line matched as body: %q", ending)
		}
	}
	suffix := grammarTerminalRegexp(t, "SHELL_SUFFIX")
	for _, value := range []string{"", "\r", "\r\n", "\n"} {
		if suffix.MatchString(value) {
			t.Errorf("closing suffix matched as body: %q", value)
		}
	}
	for _, value := range []string{"x", " ", "\rx", "\r\r"} {
		if !suffix.MatchString(value) {
			t.Errorf("body suffix rejected: %q", value)
		}
	}
	if !strings.Contains(toolGrammar, `shell_block: "<<SHELL" NL (SHELL_BODY_LINE | "SHELL" SHELL_SUFFIX NL)* "SHELL"`) {
		t.Fatal("shell marker must be factored from body suffix for longest-match lexing")
	}
}

func TestShellInlineGrammar(t *testing.T) {
	// Match the complete line, including ordinary single-< redirections.
	inline := grammarTerminalRegexp(t, "SHELL_INLINE")
	token := regexp.MustCompile(strings.TrimSuffix(inline.String(), "$"))
	for _, command := range []string{"go test ./...", `rg -n 'TODO' src | head`, `echo "$(printf x)"`, "  echo x  ", "# comment", "世界", "printf '%s' 'a\rb'", "<", "cat < input", "echo '<'", "echo a<b", "echo > output"} {
		if !inline.MatchString(command) || token.FindString(command) != command {
			t.Errorf("inline grammar rejects or truncates %q", command)
		}
	}
	for _, invalid := range []string{"", "<<", "<<SHELL", "<<SHELLx", "<<SHELL ", "echo '<<'", "cat <<<text", "echo $((1<<2))", "echo x\nnew a", "echo x\r\n", "<<SHELL\r\n", "<<SHELL\n"} {
		if inline.MatchString(invalid) {
			t.Errorf("inline grammar accepts %q", invalid)
		}
	}
}

func TestValidateScriptSyntaxDoesNotEvaluate(t *testing.T) {
	if err := ValidateScriptSyntax("in missing\ntype 1:abcd \"value\""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScriptSyntax("shell <<SHELL\ntrue\nSHELL"); err == nil {
		t.Fatal("engine syntax accepted routed execution")
	}
}

func TestToolDescriptionIsNonInstructional(t *testing.T) {
	const want = "HPATCH/2 edits with optional shell COMMAND lines or shell <<SHELL blocks closed by SHELL (Code Mode required). Edit validation is atomic; failed host application may have partial effects. Mixed scripts apply each edit segment separately, stop on failure, and retain pending work for resume HANDLE without replaying completed effects."
	if got := ToolDescription(); got != want {
		t.Fatalf("ToolDescription() = %q, want %q", got, want)
	}
}

func TestMekugi2ToolDescriptionExamplesExecute(t *testing.T) {
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
