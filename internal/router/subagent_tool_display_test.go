package router

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentToolDisplay(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"shell", "cat 'a b.txt'", "Read `a b.txt`"},
		{"shell", "skills-mgr get golang-best-practices", "Skill Read `golang-best-practices`"},
		{"shell", "skills-mgr get writing-readme/references/cli.md", "Skill Reference Read `writing-readme/references/cli.md`"},
		{"shell", "skills-mgr get writing-readme/references/cli.md 10:30", "Skill Reference Read `writing-readme/references/cli.md 10:30`"},
		{"shell", "skills-mgr get writing-readme 10:30", "Skill Read `writing-readme 10:30`"},
		{"shell", "hread /skills/writing-readme/SKILL.md 1:20", "Skill Read `writing-readme 1:20`"},
		{"shell", "  echo first\n  echo second\n", "Run\n```\n  echo first\n  echo second\n\n```"},
		{"shell", "cat /skills/writing-readme/SKILL.md", "Skill Read `writing-readme`"},
		{"shell", "cat a\ncat b", "Read `a`\n\nRead `b`"},
		{"shell", "hread a.go 1:20", "Read `a.go 1:20`"},
		{"shell", "hgrep -n -F -e 'some text' a.go", "Search `-n -F -e 'some text' a.go`"},
		{"shell", "inspect_file a.go", "Inspect `a.go`"},
		{"shell", "ls src", "List `src`"},
		{"shell", `{"command":[]}`, "Run"},
		{"shell", "cat a > b", "Run\n`cat a > b`"},
		{"shell", "cat a && rm b", "Run\n`cat a && rm b`"},
		{"shell", "cat $(echo a)", "Run\n`cat $(echo a)`"},
		{"shell", "cat *.go", "Run\n`cat *.go`"},
		{"shell", "#!params={\"max_output_tokens\":20000}\ncat a\npwd", "Run\n```\n#!params={\"max_output_tokens\":20000}\ncat a\npwd\n```"},
		{"shell", "echo a\n  echo b", "Run\n```\necho a\n  echo b\n```"},
		{"exec_command", `{"cmd":"shell bash $'cat a\\n'","login":false}`, "Read `a`"},
		{"shell", `{"command":["bash","-lc","cat a"]}`, "Read `a`"},
		{"view_image", `{"path":"/tmp/a.png"}`, "View image\n`/tmp/a.png`"},
		{"exec", `const result = await tools.exec_command({"cmd":"shell bash $'cat a\\n'","login":false}); text(JSON.stringify(Object.assign({}, result, {"retained":false})));`, "Read `a`"},
		{"exec", `await tools.exec_command({"cmd":"echo a\necho b"})`, "Run\n```\necho a\necho b\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.input, func(t *testing.T) {
			item := map[string]json.RawMessage{"name": mustMarshalJSON(tt.name), "input": mustMarshalJSON(tt.input)}
			if got := subagentToolActivityText(item, tt.name); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
func TestSubagentToolDisplayDoesNotUnwrapArbitraryCode(t *testing.T) {
	for _, source := range []string{
		`if (false) await tools.exec_command({"cmd":"cat a"})`,
		`await tools.exec_command({"cmd":"cat a"}); await tools.exec_command({"cmd":"rm b"})`,
		`const r = await tools.exec_command({"cmd":"cat a"}); text(other())`,
		`await tools.exec_command({"cmd":command})`,
		`await tools.exec_command({"cmd":"cat a"}`,
	} {
		if _, ok := toolActivityUnwrapExec(source); ok {
			t.Fatalf("unwrapped nontransparent code: %s", source)
		}
	}
}

func TestToolActivityMultilineFence(t *testing.T) {
	got := toolActivityCode("echo '```'\necho done")
	if !strings.HasPrefix(got, "````\n") || !strings.HasSuffix(got, "\n````") {
		t.Fatalf("unsafe fence: %s", got)
	}
}

func TestSubagentBuiltinToolDisplay(t *testing.T) {
	tests := []struct {
		source, want string
	}{
		{`{"type":"shell_call","action":{"commands":["echo a","echo b"]}}`, "Run\n```\necho a\necho b\n```"},
		{`{"type":"local_shell_call","action":{"command":["bash","-lc","cat a"]}}`, "Read `a`"},
		{`{"type":"web_search_call","action":{"type":"open_page","url":"https://example.com"}}`, "Open page\n`https://example.com`"},
		{`{"type":"file_search_call","queries":["alpha","beta"]}`, "Search files\n```\nalpha\nbeta\n```"},
		{`{"type":"image_generation_call"}`, "Generate image"},
		{`{"type":"code_interpreter_call","code":"print(1)\nprint(2)"}`, "Run code\n```\nprint(1)\nprint(2)\n```"},
	}
	for _, tt := range tests {
		var item map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tt.source), &item); err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(jsonString(item, "type"), "_call")
		if got := subagentToolActivityText(item, name); got != tt.want {
			t.Fatalf("source %s: got %q, want %q", tt.source, got, tt.want)
		}
	}
}

func TestToolActivityPreviewBoundsSourceWindow(t *testing.T) {
	if got := toolActivityPreview(strings.Repeat(" ", 4096) + "outside window"); !strings.HasSuffix(got, "…") || strings.Contains(got, "outside") {
		t.Fatalf("preview escaped source window: %q", got)
	}
}

func TestClassifiedToolActivitySharesPreviewBudget(t *testing.T) {
	input := "cat " + strings.Repeat("a", 200) + "\ncat " + strings.Repeat("b", 100) + "\ncat omitted"
	got := toolActivityShell(input)
	want := "Read `" + strings.Repeat("a", 200) + "`\n\nRead `" + strings.Repeat("b", 40) + "…`"
	if got != want {
		t.Fatalf("aggregate preview: got %q, want %q", got, want)
	}
}
