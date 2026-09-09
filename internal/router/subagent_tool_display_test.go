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
		{"exec", `const r = await tools.write_stdin({session_id: 52915, chars: "", yield_time_ms: 30000, max_output_tokens: 3000}); text(r);`, "Wait for command output\n`session 52915`"},
		{"exec", `await tools.write_stdin({session_id: -12, chars: ""})`, "Wait for command output\n`session -12`"},
		{"exec", `await tools.write_stdin({session_id: 9007199254740993, chars: ""})`, "Wait for command output\n`session 9007199254740992`"},
		{"exec", `await tools.write_stdin({session_id: -9007199254740993, chars: ""})`, "Wait for command output\n`session -9007199254740992`"},
		{"exec", `await tools.exec_command({cmd: 'cat a', login: false})`, "Read `a`"},
		{"exec", `await tools.apply_patch("*** Begin Patch\n*** Add File: a\n+x\n*** End Patch\n")`, "Edit\n```diff\n*** Begin Patch\n*** Add File: a\n+x\n*** End Patch\n```"},
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
		`await tools.exec_command({["cmd"]:"cat a"})`,
		`await tools.exec_command({...args})`,
		`await tools.exec_command({get cmd() { return "cat a" }})`,
		`await tools.shell({command: ["cat", , "a"]})`,
		`await tools.write_stdin({session_id: 0x10, chars: ""})`,
		`await tools.write_stdin({session_id: 1_000, chars: ""})`,
		`await tools.write_stdin({session_id: 1n, chars: ""})`,
		`await tools.write_stdin({session_id: -0x10, chars: ""})`,
		`await tools.write_stdin({session_id: -1_000, chars: ""})`,
		`await tools.write_stdin({session_id: -1n, chars: ""})`,
		`await tools.write_stdin({session_id: 1e309, chars: ""})`,
		`await tools.write_stdin({session_id: -1e309, chars: ""})`,
		`await tools.exec_command({cmd: "cat a", \u0063md: "cat b"})`,
	} {
		if _, ok := toolActivityUnwrapExec(source); ok {
			t.Fatalf("unwrapped nontransparent code: %s", source)
		}
	}

	source := `await tools.exec_command({cmd: command})`
	item := map[string]json.RawMessage{"name": mustMarshalJSON("exec"), "input": mustMarshalJSON(source)}
	want := "Run JavaScript\n```javascript\n" + source + "\n```"
	if got := subagentToolActivityText(item, "exec"); got != want {
		t.Fatalf("dynamic display = %q, want %q", got, want)
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

func TestClassifiedToolActivityShowsEveryOperation(t *testing.T) {
	path := strings.Repeat("a", 4200)
	input := "cat " + path + "\nrg needle src\ncat last"
	want := "Read `" + path + "`\n\nSearch `needle src`\n\nRead `last`"
	if got := toolActivityShell(input); got != want {
		t.Fatalf("display: got %q, want %q", got, want)
	}
}

func TestSubagentEditDisplayUsesRetainedTranslation(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: a\n@@\n-old\n+new\n*** End Patch\n"
	want := "Edit\n```diff\n" + patch + "```"
	for _, name := range []string{"hpatch", "hpatch_recover"} {
		item := map[string]json.RawMessage{
			"name":    mustMarshalJSON(name),
			"call_id": mustMarshalJSON("call-edit"),
			"input":   mustMarshalJSON("source edit"),
		}
		history := &hpatchHistory{toolName: name, script: "source edit", patch: patch}
		if got := subagentToolActivityTextWithHistory(item, name, history); got != want {
			t.Fatalf("%s translated display = %q, want %q", name, got, want)
		}
		history.translationError = "rejected"
		if got := subagentToolActivityTextWithHistory(item, name, history); got != "Edit\n`source edit`" {
			t.Fatalf("%s rejected display = %q", name, got)
		}
	}

	for _, item := range []map[string]json.RawMessage{
		{"name": mustMarshalJSON("apply_patch"), "input": mustMarshalJSON(patch)},
		{"name": mustMarshalJSON("apply_patch"), "arguments": mustMarshalJSON(`{"patch":` + string(mustMarshalJSON(patch)) + `}`)},
	} {
		if got := subagentToolActivityText(item, "apply_patch"); got != want {
			t.Fatalf("native apply_patch display = %q, want %q", got, want)
		}
	}
}

func TestToolActivityNestedLanguageFencePreservesBlankLinesAndBackticks(t *testing.T) {
	display := toolActivityDiff("Edit", "+before\n+``` literal\n\n+`after`")
	if !strings.HasPrefix(display, "Edit\n````diff\n") {
		t.Fatalf("diff fence did not avoid literal backticks: %q", display)
	}
	nested := toolActivityNested(display)
	want := "- Edit\n  ````diff\n  +before\n  +``` literal\n  \n  +`after`\n  ````"
	if nested != want {
		t.Fatalf("nested language fence = %q, want %q", nested, want)
	}
}
