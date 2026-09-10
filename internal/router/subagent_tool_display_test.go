package router

import (
	"encoding/json"
	"strings"
	"testing"
)

// Most display tests compare one textual preview; delivery tests check message boundaries.
func subagentToolActivityText(item map[string]json.RawMessage, name string) string {
	return subagentToolActivityTextWithHistory(item, name, nil)
}

func subagentToolActivityTextWithHistory(item map[string]json.RawMessage, name string, history *hpatchHistory) string {
	return strings.Join(subagentToolActivityTexts(item, name, history), "\n\n")
}

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
		{"shell", "  echo first\n  echo second\n", "Run\n```bash\n  echo first\n  echo second\n```"},
		{"shell", "cat /skills/writing-readme/SKILL.md", "Skill Read `writing-readme`"},
		{"shell", "cat a\ncat b", "Read `a`\n\nRead `b`"},
		{"shell", "hread a.go 1:20", "Read `a.go 1:20`"},
		{"shell", "hgrep -n -F -e 'some text' a.go", "Search `-n -F -e 'some text' a.go`"},
		{"shell", "inspect_file a.go", "Inspect `a.go`"},
		{"shell", "ls src", "List `src`"},
		{"shell", `{"command":[]}`, "Run"},
		{"shell", `{"command":["sh","-c","echo done"]}`, "Run\n```sh\necho done\n```"},
		{"shell", "shell sh $'echo done'", "Run\n```sh\necho done\n```"},
		{"shell", "#!python\nprint(1)", "Run\n```python\n#!python\nprint(1)\n```"},
		{"shell", "#!python3\nprint(1)", "Run\n```python\n#!python3\nprint(1)\n```"},
		{"shell", "#!/usr/bin/env -S python3 -u\nprint(1)", "Run\n```python\n#!/usr/bin/env -S python3 -u\nprint(1)\n```"},
		{"shell", "#!node\nprint(1)", "Run\n```javascript\n#!node\nprint(1)\n```"},
		{"shell", "#!ruby\nprint(1)", "Run\n```ruby\n#!ruby\nprint(1)\n```"},
		{"shell", "#!sh\nprint(1)", "Run\n```sh\n#!sh\nprint(1)\n```"},
		{"shell", "#!pwsh\nprint(1)", "Run\n```powershell\n#!pwsh\nprint(1)\n```"},
		{"shell", "cat a > b", "Run\n```bash\ncat a > b\n```"},
		{"shell", "cat a && rm b", "Run\n```bash\ncat a && rm b\n```"},
		{"shell", "cat $(echo a)", "Run\n```bash\ncat $(echo a)\n```"},
		{"shell", "cat *.go", "Run\n```bash\ncat *.go\n```"},
		{"shell", "#!params={\"max_output_tokens\":20000}\ncat a\npwd", "Run\n```bash\n#!params={\"max_output_tokens\":20000}\ncat a\npwd\n```"},
		{"shell", "echo a\n  echo b", "Run\n```bash\necho a\n  echo b\n```"},
		{"exec_command", `{"cmd":"shell bash $'cat a\\n'","login":false}`, "Read `a`"},
		{"shell", `{"command":["bash","-lc","cat a"]}`, "Read `a`"},
		{"view_image", `{"path":"/tmp/a.png"}`, "View image\n`/tmp/a.png`"},
		{"exec", `const result = await tools.exec_command({"cmd":"shell bash $'cat a\\n'","login":false}); text(JSON.stringify(Object.assign({}, result, {"retained":false})));`, "Read `a`"},
		{"exec", `await tools.exec_command({"cmd":"echo a\necho b"})`, "Run\n```bash\necho a\necho b\n```"},
		{"exec", `const r = await tools.write_stdin({session_id: 52915, chars: "", yield_time_ms: 30000, max_output_tokens: 3000}); text(r);`, "Wait\n`session 52915`"},
		{"exec", `await tools.write_stdin({session_id: -12, chars: ""})`, "Wait\n`session -12`"},
		{"exec", `await tools.write_stdin({session_id: 9007199254740993, chars: ""})`, "Wait\n`session 9007199254740992`"},
		{"exec", `await tools.write_stdin({session_id: -9007199254740993, chars: ""})`, "Wait\n`session -9007199254740992`"},
		{"exec", `await tools.exec_command({cmd: 'cat a', login: false})`, "Read `a`"},
		{"exec", `await tools.apply_patch("*** Begin Patch\n*** Add File: a\n+x\n*** End Patch\n")`, "Write `a`\n```diff\n+x\n```"},
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
func TestSubagentCodeModeOutputProjection(t *testing.T) {
	command := "python3 - <<'PY'\nimport pathlib, json\nprint('done')\nPY"
	for _, projection := range []string{
		"text(r.output)", "text ( r . output )", "text(/* output */ r.output)",
		"text(r)", "text ( JSON . stringify ( r ) )",
		`text(JSON.stringify(Object.assign( { }, r, { retained : false } )))`,
	} {
		source := "const r = await tools.exec_command({cmd:" +
			string(mustMarshalJSON(command)) +
			`,workdir:"/tmp",yield_time_ms:30000,max_output_tokens:12000});` +
			"\n" + projection + ";"
		item := map[string]json.RawMessage{"name": mustMarshalJSON("exec"), "input": mustMarshalJSON(source)}
		want := "Run\n```bash\n" + command + "\n```"
		if got := subagentToolActivityText(item, "exec"); got != want {
			t.Fatalf("display = %q, want %q", got, want)
		}
	}
}

func TestSubagentWriteStdinDisplay(t *testing.T) {
	for _, tt := range []struct {
		arguments string
		want      string
	}{
		{`{"session_id":52915}`, "Wait\n`session 52915`"},
		{`{"session_id":52915,"chars":""}`, "Wait\n`session 52915`"},
		{`{"session_id":52915,"chars":"yes"}`, "Send input\n`yes`"},
	} {
		for _, projection := range []string{"text(r)", "text (r . output)", "text(JSON.stringify(r))"} {
			source := "const r = await tools . write_stdin(" + tt.arguments + ");\n" + projection + ";"
			item := map[string]json.RawMessage{"input": mustMarshalJSON(source)}
			native := map[string]json.RawMessage{"arguments": mustMarshalJSON(tt.arguments)}
			want := subagentToolActivityText(native, "write_stdin")
			if want != tt.want {
				t.Fatalf("native display = %q, want %q", want, tt.want)
			}
			if got := subagentToolActivityText(item, "exec"); got != want {
				t.Fatalf("Code Mode display = %q, want native display %q", got, want)
			}
		}
	}
}

func TestSubagentInlineAwaitDisplay(t *testing.T) {
	for _, tt := range []struct {
		source, want string
	}{
		{
			`text(await tools.exec_command({cmd:"cat /home/ubuntu/.codex/IMPLEMENTATION.md; git diff --stat; git diff -- internal/router/subagent_tool_display.go doc/spec/commentary.md",max_output_tokens:11000}));`,
			"Run\n```bash\ncat /home/ubuntu/.codex/IMPLEMENTATION.md; git diff --stat; git diff -- internal/router/subagent_tool_display.go doc/spec/commentary.md\n```",
		},
		{
			`text(await tools.exec_command({cmd:"skills-mgr get golang-best-practices; sed -n '1,245p' internal/router/subagent_tool_display.go; sed -n '320,475p' internal/router/subagent_tool_display.go; git diff -- internal/router/subagent_tool_display_test.go",max_output_tokens:10100}));`,
			"Run\n```bash\nskills-mgr get golang-best-practices; sed -n '1,245p' internal/router/subagent_tool_display.go; sed -n '320,475p' internal/router/subagent_tool_display.go; git diff -- internal/router/subagent_tool_display_test.go\n```",
		},
		{
			`text(await tools.exec_command({cmd:"gopls references internal/router/subagent_tool_display.go:17:6; sed -n '60,135p' doc/spec/commentary.md; sed -n '1,65p' internal/router/subagent_tool_display_test.go; sed -n '540,650p' internal/router/subagent_tool_display.go",max_output_tokens:5000}));`,
			"Run\n```bash\ngopls references internal/router/subagent_tool_display.go:17:6; sed -n '60,135p' doc/spec/commentary.md; sed -n '1,65p' internal/router/subagent_tool_display_test.go; sed -n '540,650p' internal/router/subagent_tool_display.go\n```",
		},
		{
			`text(await tools.write_stdin({session_id:23221,chars:"",yield_time_ms:1000,max_output_tokens:5000}));`,
			"Wait\n`session 23221`",
		},
	} {
		t.Run(tt.source, func(t *testing.T) {
			item := map[string]json.RawMessage{"input": mustMarshalJSON(tt.source)}
			if got := subagentToolActivityText(item, "exec"); got != tt.want {
				t.Fatalf("display = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubagentRunInterpreterLanguages(t *testing.T) {
	for _, tt := range []struct {
		interpreters []string
		language     string
	}{
		{[]string{"python2.7", "python3.12", "pythonw3", "pypy", "pypy3", "/usr/bin/python3.13", `C:\Python\python3.exe`, "/usr/bin/env -S python3.12 -u"}, "python"},
		{[]string{"nodejs", "bun", "deno", "qjs", "quickjs"}, "javascript"},
		{[]string{"ts-node", "ts-node-esm", "tsx"}, "typescript"},
		{[]string{"ruby3.3", "jruby", "truffleruby"}, "ruby"},
		{[]string{"perl5.40"}, "perl"},
		{[]string{"php8.3"}, "php"},
		{[]string{"lua5.4", "luajit2.1"}, "lua"},
		{[]string{"tclsh8.6", "wish"}, "tcl"},
		{[]string{"Rscript"}, "r"},
		{[]string{"runghc", "runghc9.8", "runhaskell"}, "haskell"},
		{[]string{"powershell.exe", "pwsh"}, "powershell"},
		{[]string{"ash", "dash", "ksh93"}, "bash"},
		{[]string{"gawk", "mawk", "nawk"}, "awk"},
		{[]string{"julia"}, "julia"},
		{[]string{"fish"}, "fish"},
		{[]string{"custom3.2"}, "custom3.2"},
		{[]string{"python-helper"}, "python-helper"},
	} {
		for _, interpreter := range tt.interpreters {
			t.Run(interpreter, func(t *testing.T) {
				source := "#!" + interpreter + "\nsource `with` backticks\n"
				item := map[string]json.RawMessage{"name": mustMarshalJSON("shell"), "input": mustMarshalJSON(source)}
				want := "Run\n```" + tt.language + "\n" + source + "```"
				if got := subagentToolActivityText(item, "shell"); got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSubagentToolDisplayDoesNotUnwrapArbitraryCode(t *testing.T) {
	for _, source := range []string{
		`if (false) await tools.exec_command({"cmd":"cat a"})`,
		`await tools.exec_command({"cmd":"cat a"}); await tools.exec_command({"cmd":"rm b"})`,
		`text(await tools.exec_command({cmd:"cat a"}), other())`,
		`other(await tools.exec_command({cmd:"cat a"}))`,
		`text(await tools.exec_command({cmd:command}))`,
		`text(await tools.exec_command({cmd:"cat a"})); other()`,
		`text?.(await tools.exec_command({cmd:"cat a"}))`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(r["output"])`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(r.output, other())`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(JSON.stringify(other))`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(JSON.stringify(Object.assign({}, other, {retained:false})))`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(JSON.stringify(Object.assign({}, r, {retained:other()})))`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(other.output)`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(r.output())`,
		`const r = await tools.exec_command({cmd:"cat a"}); text(r.output); other()`,
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
		{`{"type":"shell_call","action":{"commands":["echo a","echo b"]}}`, "Run\n```bash\necho a\necho b\n```"},
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
	want := "Edit `a`\n```diff\n@@\n-old\n+new\n```"
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

func TestSubagentEditDisplayFileSections(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a b.txt\n+*** Begin Patch\n+```\n+\n" +
		"*** Update File: old.txt\n*** Move to: new.txt\n@@\n-old\n+new\n*** End of File\n" +
		"*** Delete File: obsolete.txt\n*** End Patch\n"
	want := "Write `a b.txt`\n````diff\n+*** Begin Patch\n+```\n+\n````\n\n" +
		"Move `old.txt` → `new.txt`\n```diff\n@@\n-old\n+new\n```\n\nDelete `obsolete.txt`"
	for _, source := range []string{patch, strings.ReplaceAll(patch, "\n", "\r\n")} {
		item := map[string]json.RawMessage{"name": mustMarshalJSON("apply_patch"), "input": mustMarshalJSON(source)}
		if got := subagentToolActivityText(item, "apply_patch"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestSubagentEditDisplayUnrecognizedPatch(t *testing.T) {
	for _, patch := range []string{
		"*** Begin Patch\n*** Update File: a\n+incomplete",
		"*** Begin Patch\n*** Update File: \n+missing path\n*** End Patch",
		"*** Begin Patch\nno file header\n*** End Patch",
	} {
		item := map[string]json.RawMessage{"name": mustMarshalJSON("apply_patch"), "input": mustMarshalJSON(patch)}
		want := "Edit\n```diff\n" + patch + "\n```"
		if got := subagentToolActivityText(item, "apply_patch"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
