package hpatch

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestToolGrammarTSelQuotesExcludeC0ControlsExceptTab(t *testing.T) {
	quoted := grammarTerminalRegexp(t, "TSEL_QUOTED")
	for _, value := range []string{
		`"text"`,
		`"quote\"slash\\solidus\/"`,
		`"tab\ttext"`,
		`"tab\u0009text"`,
		`"space\u0020text"`,
		"\"literal\ttext\"",
	} {
		if !quoted.MatchString(value) {
			t.Errorf("TSEL_QUOTED rejects valid value %q", value)
		}
	}
	for control := range 0x20 {
		encoded := fmt.Sprintf(`"before\u%04Xafter"`, control)
		if got, want := quoted.MatchString(encoded), control == '\t'; got != want {
			t.Errorf("TSEL_QUOTED matches encoded U+%04X = %v, want %v", control, got, want)
		}
		literal := fmt.Sprintf("\"before%cafter\"", control)
		if got, want := quoted.MatchString(literal), control == '\t'; got != want {
			t.Errorf("TSEL_QUOTED matches literal U+%04X = %v, want %v", control, got, want)
		}
	}
	for _, value := range []string{
		`""`,
		`"backspace\b"`,
		`"formfeed\f"`,
		`"newline\n"`,
		`"return\r"`,
		`"vertical\u000btab"`,
	} {
		if quoted.MatchString(value) {
			t.Errorf("TSEL_QUOTED accepts invalid value %q", value)
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
		"tsel_command: \"tsel\" SP HASH SP TSEL_QUOTED (SP POSINT)?",
		"rsel_command: \"rsel\" SP HASH SP HASH",
		"type_command: \"type\" SP QUOTED",
		"heredoc_command: \"type\" SP \"<<PATCH\" NL _patch_body \"PATCH\"",
		"acceptance: POSINT \":\" SP \"accept\"",
	} {
		if !strings.Contains(toolGrammar, rule) {
			t.Errorf("tool grammar omits exact-space rule %q", rule)
		}
	}
}

func TestToolGrammarHasNoDuplicateCommandAlternatives(t *testing.T) {
	if got := strings.Count(toolGrammar, "\n        | heredoc_command\n"); got != 1 {
		t.Fatalf("heredoc command alternatives = %d, want 1", got)
	}
}

func TestRemovedSelectorsAreNotAdvertised(t *testing.T) {
	for _, removed := range []string{"\nsel_command:", "LINE_REF", "\"sel\""} {
		if strings.Contains(toolGrammar, removed) {
			t.Errorf("grammar still advertises removed selector form %q", removed)
		}
	}
	for _, removed := range []string{"bsel", "LINE:HASH", "\n- sel ", "\nsel "} {
		if strings.Contains(toolDescription, removed) {
			t.Errorf("description still advertises removed selector form %q", removed)
		}
	}
}

func TestToolDescriptionRejectsParallelCalls(t *testing.T) {
	if !strings.Contains(toolDescription, "Do not call this tool in parallel with other tools.") {
		t.Error("tool description permits parallel calls")
	}
}

func TestToolDescriptionExplainsAutomaticGoFormatting(t *testing.T) {
	for _, guidance := range []string{"Go's standard library", "other languages receive no language validation"} {
		if !strings.Contains(toolDescription, guidance) {
			t.Errorf("tool description omits validation guidance %q", guidance)
		}
	}
}

func TestToolDescriptionMinimizesModelRoundTrips(t *testing.T) {
	normalized := strings.Join(strings.Fields(toolDescription), " ")
	for _, guidance := range []string{
		"Minimize model round trips",
		"batch all known independent edits across files into one atomic script",
		"When a later edit depends on content or paths introduced earlier in the same script, use `commit`",
		"Split calls only when the next script depends on a diagnostic or another result unavailable until this call finishes",

		"after inspecting every file required by the task with `hread`",
		"Copy each selector's `HASH`",
		"never reconstruct or guess a hash",
	} {
		if !strings.Contains(normalized, guidance) {
			t.Errorf("tool description omits round-trip guidance %q", guidance)
		}
	}
	if strings.Contains(toolDescription, "`nl -ba -w1 -s'|'`") {
		t.Error("tool description still directs agents to number unhashed lines")
	}
}

func TestToolDescriptionGuidesSparseCommandChoice(t *testing.T) {
	for _, guidance := range []string{
		"tsel cannot target only a later same-line match",
		"from column 1 of that line through EOF",
		"Matches may land on different lines",
		"TEXT must stay on one line",
		"never searches before it",
		"Missing hashes, duplicate-content hashes, and truncated-hash collisions reject without guessing or repair context",
		"it has no syntax or section awareness",
		"Selecting a heading inserts before that heading's existing body, not after the section",
		"trust the reported edited ranges and hash-only preview rows; do not reread the file solely to verify placement",
		"need not fill it, and matching is not syntax-aware",
		"prose, links, examples, or repeated code",
		"No report is available until the whole call finishes",
		"post-commit selectors address that new content",
		"include separator blank lines deliberately",
		"cut combines copy and deletion in one command",
		"the script does not re-emit the selected text",
		"later commands must select text introduced or changed earlier in the same call",
		"otherwise omit it",
		"commit makes the pasted text selectable",
	} {
		if !strings.Contains(toolDescription, guidance) {
			t.Errorf("tool description omits sparse-command guidance %q", guidance)
		}
	}
	for _, excluded := range []string{
		"confined to one line",
		"TEXT need not fill the line and matching is not syntax-aware",
		"formatter or parser/compiler",
		"git diff --check",
		"repaired hashline",
		"repaired line",
	} {
		if strings.Contains(toolDescription, excluded) {
			t.Errorf("tool description retains inaccurate or removed guidance %q", excluded)
		}
	}
}

func TestToolDescriptionSuggestedUseCasesExecute(t *testing.T) {
	t.Run("tsel replaces an exact complete line", func(t *testing.T) {
		const script = `in predicate.go
tsel 9645 "return ready || ready"
type "return ready || cached"`
		if !strings.Contains(toolDescription, fencedScript(script)) {
			t.Fatal("tool description omits executable exact-line tsel example")
		}

		root := t.TempDir()
		prefix := "package sample\nfunc predicate() bool {\n" + strings.Repeat("// padding\n", 21)
		formattedPrefix := "package sample\n\nfunc predicate() bool {\n" + strings.Repeat("\t// padding\n", 21)
		suffix := "}\n"
		writeTestFile(t, root, "predicate.go", prefix+"return ready || ready\n"+suffix, 0o644)

		stdout, stderr, exitCode := runForTest(root, nil, script)
		if exitCode != 0 || stdout != "" || stderr != "" {
			t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if got, want := readTestFile(t, root, "predicate.go"), formattedPrefix+"\treturn ready || cached\n"+suffix; got != want {
			t.Fatalf("predicate.go = %q, want %q", got, want)
		}
	})

	t.Run("cut paste commit then edit introduced text", func(t *testing.T) {
		const script = `in source.go
rsel ffb0 4b7b
cut
in destination.go
rsel 12d9 12d9
paste
commit
tsel ffb0 "sourceRegistry"
type "destinationRegistry"`
		if !strings.Contains(toolDescription, fencedScript(script)) {
			t.Fatal("tool description omits executable cut and commit example")
		}

		root := t.TempDir()
		sourcePrefix := "package sample\n" + strings.Repeat("// source padding\n", 10)
		formattedSourcePrefix := "package sample\n\n" + strings.Repeat("// source padding\n", 10)
		moved := "// sourceRegistry.Register(alpha)\n" + strings.Repeat("// move padding\n", 15) + "// move end\n"
		destinationPrefix := "package sample\n" + strings.Repeat("// destination padding\n", 38)
		formattedDestinationPrefix := "package sample\n\n" + strings.Repeat("// destination padding\n", 38)
		writeTestFile(t, root, "source.go", sourcePrefix+moved+"var sourceTail = true\n", 0o644)
		writeTestFile(t, root, "destination.go", destinationPrefix+"// handlers\nvar destinationTail = true\n", 0o644)

		withoutCommit := strings.Replace(script, "\ncommit\n", "\n", 1)
		stdout, stderr, exitCode := runForTest(root, []string{"translate"}, withoutCommit)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, `hashline ffb0 does not identify any line`) {
			t.Fatalf("Run() without commit = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}

		stdout, stderr, exitCode = runForTest(root, nil, script)
		if exitCode != 0 || stdout != "" || stderr != "" {
			t.Fatalf("Run() = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if got, want := readTestFile(t, root, "source.go"), formattedSourcePrefix+"var sourceTail = true\n"; got != want {
			t.Fatalf("source.go = %q, want %q", got, want)
		}
		adjusted := strings.Replace(moved, "sourceRegistry", "destinationRegistry", 1)
		if got, want := readTestFile(t, root, "destination.go"), formattedDestinationPrefix+"// handlers\n"+adjusted+"var destinationTail = true\n"; got != want {
			t.Fatalf("destination.go = %q, want %q", got, want)
		}

		codec, err := tokenizer.ForModel(tokenizer.GPT5)
		if err != nil {
			t.Fatal(err)
		}
		countTokens := func(input string) int {
			t.Helper()
			count, err := codec.Count(input)
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
		cutTokens := countTokens(script)
		copyDeleteTokens := countTokens(strings.Replace(script, "\ncut\n", "\ncopy\ndel\n", 1))
		if cutTokens >= copyDeleteTokens {
			t.Fatalf("cut script tokens = %d, copy plus del tokens = %d", cutTokens, copyDeleteTokens)
		}
		reemit := "in source.go\nrsel ffb0 4b7b\ndel\nin destination.go\nrsel 12d9 12d9\ntype <<PATCH\n// handlers\n" + adjusted + "PATCH"
		if reemitTokens := countTokens(reemit); cutTokens >= reemitTokens {
			t.Fatalf("cut script tokens = %d, re-emitted body tokens = %d", cutTokens, reemitTokens)
		}
	})
}

func fencedScript(script string) string {
	return "```\n" + script + "\n```"
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
