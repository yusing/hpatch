package router

import (
	"encoding/json"
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
			strings.Contains(got, "at the end of both commentary and final") || !strings.Contains(got, "at the end of the final answer, after any permission question") {
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
