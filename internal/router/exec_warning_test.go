package router

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeExecWarningSyntax(t *testing.T) {
	for _, test := range []struct {
		name, source string
		warn         bool
	}{
		{"different binding", `const r = await tools.exec_command({cmd:"one"}); text(r);`, true},
		{"settled batch", `const rs = await Promise.allSettled([tools.exec_command({cmd:"one"}),tools.exec_command({cmd:"two"})]); text(rs);`, true},
		{"computed", `text(await tools["exec_command"]({cmd:"one"}));`, true},
		{"comments", `tools /* gap */ . exec_command({cmd:"one"}).then(text);`, true},
		{"pragma and directive", "// @exec: {\"max_output_tokens\":100}\n'use strict';\nawait tools.exec_command({cmd:'one'});", true},
		{"quoted canonical source", "const s = 'const result = await tools.exec_command({});\\ntext(result.output);'; text(s);", false},
		{"quoted canonical before real call", "const s = `const result = await tools.exec_command({});\ntext(result.output);`; text(await tools.exec_command({cmd:'one'}));", true},
		{"comment", "// const result = await tools.exec_command({});\n// text(result.output);\ntext('ok');", false},
		{"shadowed tools", `const tools = {exec_command() {}}; tools.exec_command({});`, false},
		{"parameter", `function f(tools) { tools.exec_command({}); }`, false},
		{"other helper", `text(await tools.clock__curr_time({}));`, false},
		{"invalid program", `const r = await tools.exec_command(`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, warning, changed, detected := nativeExecCommandInput(test.source)
			if changed != test.warn || detected != test.warn {
				t.Fatalf("changed=%v detected=%v, want %v", changed, detected, test.warn)
			}
			if !test.warn {
				if got != test.source {
					t.Fatal("changed unrelated JavaScript")
				}
				return
			}
			if strings.Replace(got, warning, "", 1) != test.source || strings.Count(got, warning) != 1 {
				t.Fatal("warning changed the submitted program")
			}
			if test.name == "pragma and directive" && !strings.HasPrefix(got, "// @exec: {\"max_output_tokens\":100}\n'use strict';\n") {
				t.Fatal("warning displaced the pragma or directive")
			}
			again, _, changed, detected := nativeExecCommandInput(got)
			if changed || !detected || again != got {
				t.Fatal("warning duplicated on replay")
			}
		})
	}
}

func TestBatchedExecWarningDelivery(t *testing.T) {
	const source = `const rs = await Promise.allSettled([tools.exec_command({cmd:"one"}), tools.exec_command({cmd:"two"})]); for (const r of rs) text(r.value.output);`
	for _, name := range []string{"exec", "shell"} {
		for _, streaming := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/json", true: "/sse"}[streaming], func(t *testing.T) {
				transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
				item := map[string]any{"type": "custom_tool_call", "name": name, "call_id": "batch", "id": "batch-item", "input": source, "status": "completed"}
				var carrier map[string]json.RawMessage
				if streaming {
					events, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						var envelope struct {
							Item map[string]json.RawMessage `json:"item"`
						}
						if err := json.Unmarshal(event, &envelope); err != nil {
							t.Fatal(err)
						}
						if jsonString(envelope.Item, "call_id") == "batch" {
							carrier = envelope.Item
						}
					}
				} else {
					body, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{item}}))
					if err != nil {
						t.Fatal(err)
					}
					var response struct {
						Output []map[string]json.RawMessage `json:"output"`
					}
					if err := json.Unmarshal(body, &response); err != nil {
						t.Fatal(err)
					}
					carrier = response.Output[0]
				}
				program := jsonString(carrier, "input")
				// Execute the delivered carrier against a deterministic Code Mode host.
				// The two commands must run once each; warnings must not replace results.
				host := `const calls=[], output=[]; globalThis.text = x => output.push(String(x)); globalThis.tools = {exec_command: async x => { calls.push(x.cmd); return {output:x.cmd}; }};` + "\n"
				result, err := exec.CommandContext(t.Context(), "bun", "--eval", host+program+"\nconsole.log(JSON.stringify({calls,output}));").CombinedOutput()
				if err != nil {
					t.Fatalf("carrier failed: %v: %s", err, result)
				}
				var got struct{ Calls, Output []string }
				if err := json.Unmarshal(result, &got); err != nil {
					t.Fatal(err)
				}
				if strings.Join(got.Calls, ",") != "one,two" || strings.Join(got.Output[len(got.Output)-2:], ",") != "one,two" {
					t.Fatalf("changed execution/results: %+v", got)
				}
				if strings.Count(strings.Join(got.Output, ""), nativeExecCommandWarning) != 1 {
					t.Fatal("native warning missing or duplicated")
				}
				if name == "shell" && (!strings.Contains(got.Output[0], "Submit shell commands directly") || strings.Contains(got.Output[0], "use functions.exec directly next time")) {
					t.Fatal("recovery sends shell commands back to Code Mode")
				}
			})
		}
	}
}

func TestExecWarningsPreserveLocalOutputBinding(t *testing.T) {
	const source = `const text = "local data"; const r = await tools.exec_command({cmd:"one"}); console.log(r.output, text);`
	for _, name := range []string{"exec", "shell"} {
		t.Run(name, func(t *testing.T) {
			transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
			body, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed", "output": []any{map[string]any{
					"type": "custom_tool_call", "name": name, "call_id": "local-text", "input": source,
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Output []map[string]json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			program := jsonString(response.Output[0], "input")
			if program != source {
				t.Fatal("warning rewrote a program owning the output helper")
			}
			host := `globalThis.tools = {exec_command: async x => ({output:x.cmd})};` + "\n"
			output, err := exec.CommandContext(t.Context(), "bun", "--eval", host+program).CombinedOutput()
			if err != nil || string(output) != "one local data\n" {
				t.Fatalf("carrier changed execution: %v: %s", err, output)
			}
		})
	}
}
