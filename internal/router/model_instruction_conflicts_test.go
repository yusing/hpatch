package router

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	codexinstructions "github.com/yusing/mekugi/contrib/codex"
)

func TestRewriteModelFamilyToolConflicts(t *testing.T) {
	// GPT-5.6 Sol, Terra, and Luna share the exact instructions_template in
	// ~/.codex/models_cache.json inspected on 2026-09-10. Astra's template
	// matches the existing recorded fixture (apart from a trailing blank line).
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		fixture := "testdata/gpt-5.6-instructions.txt"
		if model == "gpt-6-astra" {
			fixture = "testdata/gpt-6-astra-instructions.txt"
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		for _, carrier := range []string{"instructions", "developer"} {
			for _, compact := range []bool{false, true} {
				t.Run(model+"/"+carrier+"/"+map[bool]string{false: "native", true: "ctp2"}[compact], func(t *testing.T) {
					guidance := codexinstructions.InstructionsForModel(model, compact)
					request := parsedResponsesRequest{fields: map[string]json.RawMessage{"model": mustTestJSON(t, model)}}
					stock := string(data)
					if carrier == "instructions" {
						request.fields[carrier] = mustTestJSON(t, stock)
					} else {
						request.fields["input"] = mustTestJSON(t, []any{map[string]any{"type": "message", "role": "developer", "content": stock}})
					}
					if err := rewriteReceivedModelInstructions(t.Context(), &request, false, guidance); err != nil {
						t.Fatal(err)
					}
					var got string
					if carrier == "instructions" {
						if err := json.Unmarshal(request.fields[carrier], &got); err != nil {
							t.Fatal(err)
						}
					} else {
						var input []struct{ Content string }
						if err := json.Unmarshal(request.fields["input"], &input); err != nil {
							t.Fatal(err)
						}
						got = input[0].Content
					}
					if strings.Count(got, guidance) != 1 {
						t.Fatal("selected guidance must occur once")
					}
					outside := strings.Replace(got, guidance, "", 1)
					for _, conflict := range []string{
						"`apply_patch`", "`rg`", "exec_command", "functions.exec",
						"Promise.allSettled", "prefer parallelization over sequential",
						"in the `commentary` channel", "to the `commentary` channel", "in the commentary channel",
						"without a commentary update for more than 60 seconds", "wait calls longer than 60 seconds",
						"at the end of both commentary and final",
					} {
						if strings.Contains(outside, conflict) {
							t.Errorf("forwarded prompt retains conflict %q", conflict)
						}
					}
					for _, preserved := range []string{"# Personality", "## Final answer", "# Using skills", "Never repurpose `$HOME`, `$home`, or `$CODEX_HOME`"} {
						if !strings.Contains(got, preserved) {
							t.Errorf("lost unrelated guidance %q", preserved)
						}
					}
					refreshed, strategy, err := renderModelInstructions(got, false, guidance)
					if err != nil || strategy != "marked" || refreshed != got {
						t.Fatalf("refresh is not idempotent: strategy=%s error=%v", strategy, err)
					}
				})
			}
		}
	}
}

func TestRefreshAndCustomAppendRemoveInheritedConflicts(t *testing.T) {
	guidance := codexinstructions.InstructionsForModel("gpt-6-astra", false)
	const before = "custom prefix\n- You share updates in the `commentary` channel.\n"
	const after = "\nThe first time in a conversation that you decide to apply a skill, inform the user in the commentary channel.\nPut this explanation in a short, separate paragraph at the end of both commentary and final, after any permission question.\ncustom suffix"
	for _, marked := range []bool{false, true} {
		input := before + after
		if marked {
			input = before + codexinstructions.InstructionsForModel("gpt-5.6-sol", true) + after
		}
		got, _, err := renderModelInstructions(input, !marked, guidance)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "custom prefix\n") || !strings.Contains(got, "custom suffix") || strings.Count(got, guidance) != 1 ||
			strings.Contains(got, "in the `commentary` channel") || strings.Contains(got, "in the commentary channel") ||
			strings.Contains(got, "at the end of both commentary and final") || !strings.Contains(got, "Record this explanation as a short journal item after any permission question") {
			t.Fatalf("inherited/custom rewrite failed: marked=%v", marked)
		}
	}
}

func TestActiveAstraPromptWithoutLegacyExecWarning(t *testing.T) {
	// Active prompt fragments differ from the cache: the legacy warning is gone
	// and batching includes all tool calls, not just searches and reads.
	const batch = "- Batch independent searches, reads, and other tool calls in one functions.exec using await Promise.allSettled([...]); keep each batch bounded to decision-relevant output by selecting needed ranges or fields first, and inspect every returned result. If output truncates, retrieve only the missing evidence rather than repeating an unchanged whole scan. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential. Avoid unnecessary output."
	const safety = "- Treat shell command text as code. `JSON.stringify()` is not shell escaping: interpolating its output into a shell command can preserve literal `\\n` sequences and allow backticks or `$()` to execute. Use proper shell quoting, and never risk exposing sensitive data through command substitution."
	stock := stockAstraIntroduction + "\n\n" + stockWorkHeading + "\n\n" + stockRGInstruction + "\n" + batch + "\n" + safety + "\n"
	guidance := codexinstructions.InstructionsForModel("gpt-6-astra", false)
	got, strategy, err := renderModelInstructions(stock, false, guidance)
	if err != nil || strategy != "stock-astra" {
		t.Fatalf("active Astra prompt rejected: strategy=%s error=%v", strategy, err)
	}
	if strings.Contains(got, "functions.exec") || !strings.Contains(got, safety) || !strings.Contains(got, "functions.shell script") {
		t.Fatal("active prompt must replace batching but preserve shell safety")
	}
	for _, invalid := range []string{strings.Replace(stock, safety, "changed safety rule", 1), stock + safety} {
		if _, _, err := renderModelInstructions(invalid, false, guidance); err == nil {
			t.Fatal("unknown or duplicated active safety anchor accepted")
		}
	}
}

func TestRecordedSolPromptRewritesBatchAndWrappedCommentary(t *testing.T) {
	// Fragments observed in the 2026-09-11 Sol instruction dump. The prefix and
	// line break differ from the cached model templates.
	const recorded = `Keep unrelated authorization intact.
If the user asks a question or requests status during active work, answer briefly in commentary,
then resume the active task or wait unless the user clearly asks you to stop.
- To reduce round trips, batch independent searches, reads, and other tool calls in one functions.exec using await Promise.allSettled([...]); keep each batch bounded to decision-relevant output by selecting needed ranges or fields first, and inspect every returned result. If output truncates, retrieve only the missing evidence rather than repeating an unchanged whole scan. Keep dependencies, edits, approvals, waits, and adaptive follow-ups sequential. Avoid unnecessary output.
Preserve unrelated validation policy.`
	for _, model := range []string{"gpt-5.6-sol", "gpt-6-astra"} {
		for _, compact := range []bool{false, true} {
			for _, carrier := range []string{"instructions", "developer"} {
				t.Run(fmt.Sprintf("%s/%t/%s", model, compact, carrier), func(t *testing.T) {
					guidance := codexinstructions.InstructionsForModel(model, compact)
					input := recorded + "\n" + codexinstructions.InstructionsForModel("gpt-5.6-sol", false)
					request := parsedResponsesRequest{fields: map[string]json.RawMessage{"model": mustTestJSON(t, model)}}
					if carrier == "instructions" {
						request.fields[carrier] = mustTestJSON(t, input)
					} else {
						request.fields["input"] = mustTestJSON(t, []any{map[string]any{"type": "message", "role": "developer", "content": input}})
					}
					if err := rewriteReceivedModelInstructions(t.Context(), &request, false, guidance); err != nil {
						t.Fatal(err)
					}
					var got string
					if carrier == "instructions" {
						if err := json.Unmarshal(request.fields[carrier], &got); err != nil {
							t.Fatal(err)
						}
					} else {
						var messages []struct{ Content string }
						if err := json.Unmarshal(request.fields["input"], &messages); err != nil {
							t.Fatal(err)
						}
						got = messages[0].Content
					}
					if strings.Contains(got, "Promise.allSettled") || strings.Contains(got, "answer briefly in commentary") {
						t.Fatal("recorded conflicting guidance survived request rewriting")
					}
					for _, want := range []string{
						"Keep unrelated authorization intact.",
						"Preserve unrelated validation policy.",
						"functions.shell script",
						"unless the user clearly asks you to stop",
						"report_now journal item",
					} {
						if !strings.Contains(got, want) {
							t.Errorf("missing %q", want)
						}
					}
					if strings.Count(got, guidance) != 1 {
						t.Fatal("selected model guidance must occur once")
					}
					refreshed, _, err := renderModelInstructions(got, false, guidance)
					if err != nil || refreshed != got {
						t.Fatalf("recorded prompt refresh is not idempotent: %v", err)
					}
				})
			}
		}
	}
}

func TestChecklistInstructionsRewrittenAcrossModelLifecycles(t *testing.T) {
	const checklist = "\n## Planning\nYou have access to an `update_plan` tool which tracks steps.\n\n### Examples\nKeep steps current.\n\n## `update_plan`\nA tool named `update_plan` is available to you. Update the checklist.\n\n## Plan tool\nWhen using the planning tool:\n- Skip using the planning tool for straightforward tasks (roughly the easiest 25%).\n- Do not make single-step plans.\n- When you made a plan, update it after having performed one of the sub-tasks that you shared on the plan.\n\n## Plan Mode vs update_plan tool\nSeparately, `update_plan` is a checklist/progress/TODOs tool; it does not enter or exit Plan Mode.\n\n# Tasks\nWhen `update_plan` is available, follow this section.\nKeep a checklist.\n\n## Work\nKeep working.\n- Use the plan tool to explain the work\n    - Keep steps current.\n- If you create a checklist or task list, update its statuses.\n\nProgress visibility:\nIf update_plan is available, use it for complex work.\n\n## Planning\nDiscuss architecture and inspect update_plan before editing.\n"
	const preserved = "\n## Work\nKeep working.\n\n## Planning\nDiscuss architecture and inspect update_plan before editing.\n"
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		fixture := "testdata/gpt-5.6-instructions.txt"
		if model == "gpt-6-astra" {
			fixture = "testdata/gpt-6-astra-instructions.txt"
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		for _, compact := range []bool{false, true} {
			guidance := codexinstructions.InstructionsForModel(model, compact)
			for _, lifecycle := range []string{"stock", "marked", "custom"} {
				base := string(data)
				if lifecycle == "marked" {
					base = codexinstructions.InstructionsForModel("other", !compact)
				} else if lifecycle == "custom" {
					base = "Custom authorization.\n"
				}
				for _, carrier := range []string{"instructions", "developer"} {
					t.Run(fmt.Sprintf("%s/%t/%s/%s", model, compact, lifecycle, carrier), func(t *testing.T) {
						request := parsedResponsesRequest{fields: map[string]json.RawMessage{"model": mustTestJSON(t, model)}}
						if carrier == "instructions" {
							request.fields[carrier] = mustTestJSON(t, base+checklist)
						} else {
							request.fields["input"] = mustTestJSON(t, []any{map[string]any{"type": "message", "role": "developer", "content": base + checklist}})
						}
						if err := rewriteReceivedModelInstructions(t.Context(), &request, lifecycle == "custom", guidance); err != nil {
							t.Fatal(err)
						}
						var got string
						if carrier == "instructions" {
							if err := json.Unmarshal(request.fields[carrier], &got); err != nil {
								t.Fatal(err)
							}
						} else {
							var messages []struct{ Content string }
							if err := json.Unmarshal(request.fields["input"], &messages); err != nil {
								t.Fatal(err)
							}
							got = messages[0].Content
						}
						if !strings.Contains(got, preserved) || strings.Count(got, guidance) != 1 {
							t.Fatal("lost unrelated planning or selected journal guidance")
						}
						for _, unwanted := range []string{"tracks steps", "Keep steps current", "Update the checklist", "## Plan tool", "# Tasks", "Progress visibility:"} {
							if strings.Contains(got, unwanted) {
								t.Errorf("retained checklist instruction %q", unwanted)
							}
						}
						refreshed, _, err := renderModelInstructions(got, false, guidance)
						if err != nil || refreshed != got {
							t.Fatalf("refresh changed instructions: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestChecklistRewritePreservesCustomPolicyAndMarkers(t *testing.T) {
	guidance := codexinstructions.InstructionsForModel("gpt-6-astra", false)
	for _, prefix := range []string{
		"## Plan tool\nNever deploy without explicit approval.\n",
		"## Planning\nYou have access to an `update_plan` tool which tracks steps.\n\n",
	} {
		for _, marked := range []bool{false, true} {
			input := prefix
			if marked {
				input += guidance
			}
			got, _, err := renderModelInstructions(input, true, guidance)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(got, guidance) != 1 {
				t.Fatal("lost owned guidance")
			}
			if strings.Contains(prefix, "Never deploy") && !strings.Contains(got, prefix) {
				t.Fatal("lost custom approval policy")
			}
			refreshed, _, err := renderModelInstructions(got, false, guidance)
			if err != nil || refreshed != got {
				t.Fatalf("refresh damaged marker boundary: %v", err)
			}
		}
	}
}
