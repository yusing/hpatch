package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

func TestRenderModelInstructionsAtInstructionLifecycles(t *testing.T) {
	stock := stockModelInstructionsForTest("custom prefix\n", "custom suffix\n")
	want := "custom prefix\n" + codexinstructions.NativeInstructions() + "custom suffix\n"
	for _, lifecycle := range []string{
		"session start",
		"post compaction",
		"subagent start",
		"subagent post compaction",
	} {
		t.Run(lifecycle, func(t *testing.T) {
			got, err := renderModelInstructions(stock, false, codexinstructions.NativeInstructions())
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("renderModelInstructions() = %q, want %q", got, want)
			}
		})
	}
}

func TestCentralModelInstructionsHaveOneMarkerPair(t *testing.T) {
	instructions := codexinstructions.Instructions()
	if strings.Count(instructions, hpatchInstructionsStartMarker) != 1 ||
		strings.Count(instructions, hpatchInstructionsEndMarker) != 1 {
		t.Fatal("central model instructions do not contain one marker pair")
	}
	if strings.Index(instructions, hpatchInstructionsStartMarker) >= strings.Index(instructions, hpatchInstructionsEndMarker) {
		t.Fatal("central model instruction markers are reversed")
	}
}

func TestRewriteGPT5RecordedEditingFragments(t *testing.T) {
	// Fragment fixture from contrib/codex/install_test.go at
	// 9918aca0d080ef90bfe51baf90a9662adcf0b400, including its before/after sentinels.
	// This is not a full upstream prompt. Keep input and expected output
	// independent of the production matcher's stock constants.
	data, err := os.ReadFile("testdata/gpt-5-stock-editing-fragments.txt")
	if err != nil {
		t.Fatal(err)
	}
	stock := string(data)
	for _, carrier := range []string{"instructions", "developer"} {
		for _, protocol := range []string{"native", "ctp2"} {
			t.Run(carrier+"/"+protocol, func(t *testing.T) {
				guidance := codexinstructions.NativeInstructions()
				if protocol == "ctp2" {
					guidance = codexinstructions.Instructions()
				}
				request := parsedResponsesRequest{fields: make(map[string]json.RawMessage)}
				if carrier == "instructions" {
					request.fields[carrier] = mustTestJSON(t, stock)
				} else {
					request.fields["input"] = mustTestJSON(t, []any{
						map[string]any{"type": "message", "role": "developer", "content": stock},
					})
				}
				if err := rewriteReceivedModelInstructions(&request, false, guidance); err != nil {
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
				if got != "before\n"+guidance+"after\n" {
					t.Fatal("recorded stock rewrite changed unrelated content or retained displaced guidance")
				}
				refreshed, err := renderModelInstructions(got, false, guidance)
				if err != nil || refreshed != got {
					t.Fatalf("recorded stock guidance refresh is not idempotent: %v", err)
				}
			})
		}
	}
}

func TestRewriteAstraStockModelInstructions(t *testing.T) {
	// Stock Astra template from Codex's 2026-09-05 model catalog, before
	// personality variables are expanded. Keep this independent of the matcher.
	data, err := os.ReadFile("testdata/gpt-6-astra-instructions.txt")
	if err != nil {
		t.Fatal(err)
	}
	stock := string(data)
	for _, carrier := range []string{"instructions", "developer"} {
		for _, protocol := range []string{"native", "ctp2"} {
			t.Run(carrier+"/"+protocol, func(t *testing.T) {
				guidance := codexinstructions.NativeInstructions()
				if protocol == "ctp2" {
					guidance = codexinstructions.Instructions()
				}
				request := parsedResponsesRequest{fields: map[string]json.RawMessage{
					"model": mustTestJSON(t, "gpt-6-astra"),
				}}
				if carrier == "instructions" {
					request.fields["instructions"] = mustTestJSON(t, stock)
				} else {
					request.fields["input"] = mustTestJSON(t, []any{
						map[string]any{"type": "message", "role": "developer", "content": stock},
					})
				}
				if err := rewriteReceivedModelInstructions(&request, false, guidance); err != nil {
					t.Fatal(err)
				}
				var got string
				if carrier == "instructions" {
					if err := json.Unmarshal(request.fields["instructions"], &got); err != nil {
						t.Fatal(err)
					}
				} else {
					var input []struct{ Content string }
					if err := json.Unmarshal(request.fields["input"], &input); err != nil {
						t.Fatal(err)
					}
					got = input[0].Content
				}
				want := strings.Replace(stock, stockRGInstruction+"\n", guidance, 1)
				want = strings.Replace(want, stockExecInstruction+"\n", "", 1)
				if got != want {
					t.Fatal("Astra rewrite did not preserve unrelated stock instructions")
				}
				refreshed, err := renderModelInstructions(got, false, guidance)
				if err != nil || refreshed != got {
					t.Fatalf("Astra guidance refresh is not idempotent: %v", err)
				}
			})
		}
	}
	for _, test := range []struct{ name, input string }{
		{"changed identity", strings.Replace(stock, "an agent based on GPT-6", "an agent based on GPT-7", 1)},
		{"missing search instruction", strings.Replace(stock, stockRGInstruction, "search differently", 1)},
		{"missing exec instruction", strings.Replace(stock, stockExecInstruction, "execute differently", 1)},
		{"duplicate search instruction", stock + "\n" + stockRGInstruction},
		{"changed rules heading", strings.Replace(stock, "# Rules for getting work done", "# Changed rules", 1)},
		{"nonblank separator", strings.Replace(stock, "# Rules for getting work done\n\n", "# Rules for getting work done\nnew guidance\n", 1)},
		{"partial old editing section", stock + "\n" + stockEditHeading},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := renderModelInstructions(test.input, false, codexinstructions.NativeInstructions()); err == nil {
				t.Fatal("changed stock instructions were accepted")
			}
		})
	}
}

func TestRenderModelInstructionsRefreshesInheritedConversation(t *testing.T) {
	input := "custom prefix\n" + codexinstructions.NativeInstructions() + "custom suffix\n"
	got, err := renderModelInstructions(input, false, codexinstructions.NativeInstructions())
	if err != nil {
		t.Fatal(err)
	}
	if got != input {
		t.Fatalf("inherited instructions changed\n got: %q\nwant: %q", got, input)
	}
}

func TestRenderModelInstructionsAppendsForCustomizedModelInstructions(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "unrecognized", input: "custom instructions\n"},
		{name: "partial stock", input: stockEditHeading + "\n"},
		{name: "malformed stock", input: strings.Replace(stockModelInstructionsForTest("", ""), stockEditHeading+"\n\n", stockEditHeading+"\ncustom guidance\n", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := renderModelInstructions(test.input, true, codexinstructions.NativeInstructions())
			if err != nil {
				t.Fatal(err)
			}
			want := test.input + "\n" + codexinstructions.NativeInstructions()
			if got != want {
				t.Fatalf("renderModelInstructions() = %q, want %q", got, want)
			}
		})
	}
}

func TestRenderModelInstructionsFailsClosedForChangedUpstreamInstructions(t *testing.T) {
	for _, test := range []struct {
		name       string
		input      string
		customized bool
	}{
		{name: "missing stock section", input: "changed upstream instructions\n"},
		{name: "nonblank stock separator", input: strings.Replace(stockModelInstructionsForTest("", ""), stockEditHeading+"\n\n", stockEditHeading+"\nnew upstream guidance\n", 1)},
		{name: "incomplete marker", input: hpatchInstructionsStartMarker + "\n", customized: true},
		{name: "reversed markers", input: hpatchInstructionsEndMarker + "\n" + hpatchInstructionsStartMarker + "\n", customized: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := renderModelInstructions(test.input, test.customized, codexinstructions.NativeInstructions()); err == nil {
				t.Fatal("renderModelInstructions() succeeded")
			}
		})
	}
}

func TestRewriteReceivedModelInstructionsLeavesMissingAndNullValues(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]json.RawMessage
	}{
		{name: "missing", fields: map[string]json.RawMessage{}},
		{name: "null", fields: map[string]json.RawMessage{"instructions": json.RawMessage("null")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := string(test.fields["instructions"])
			request := parsedResponsesRequest{fields: test.fields}
			if err := rewriteReceivedModelInstructions(&request, false, codexinstructions.NativeInstructions()); err != nil {
				t.Fatal(err)
			}
			if got := string(request.fields["instructions"]); got != before {
				t.Fatalf("instructions = %q, want %q", got, before)
			}
		})
	}
}

func TestRewriteReceivedModelInstructionsUsesDeveloperCarrierWhenTopLevelIsEmpty(t *testing.T) {
	developerInstructions := stockModelInstructionsForTest("developer prefix\n", "developer suffix\n")
	request := parsedResponsesRequest{fields: map[string]json.RawMessage{
		"instructions": json.RawMessage(`""`),
		"input": mustTestJSON(t, []any{
			map[string]any{
				"type": "message", "role": "developer", "content": developerInstructions,
				"provider_item": map[string]any{"kept": true},
			},
			map[string]any{"type": "message", "role": "user", "content": "keep this unchanged"},
		}),
	}}

	if err := rewriteReceivedModelInstructions(&request, false, codexinstructions.NativeInstructions()); err != nil {
		t.Fatal(err)
	}
	if got := string(request.fields["instructions"]); got != `""` {
		t.Fatalf("top-level instructions = %s, want empty string", got)
	}
	var input []map[string]any
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	want := "developer prefix\n" + codexinstructions.NativeInstructions() + "developer suffix\n"
	if got := input[0]["content"]; got != want {
		t.Fatalf("developer instructions = %q, want %q", got, want)
	}
	if got := input[1]["content"]; got != "keep this unchanged" {
		t.Fatalf("user content = %q", got)
	}
	providerItem, ok := input[0]["provider_item"].(map[string]any)
	if !ok || providerItem["kept"] != true {
		t.Fatalf("provider-owned item = %#v", input[0]["provider_item"])
	}
}

func TestModelInstructionFileConfiguredAt(t *testing.T) {
	directory := t.TempDir()
	for _, test := range []struct {
		name    string
		config  string
		missing bool
		want    bool
		wantErr bool
	}{
		{name: "missing config", missing: true},
		{name: "unset", config: "model = 'gpt-test'\n"},
		{name: "set", config: "model_instructions_file = '/tmp/custom.md'\n", want: true},
		{name: "quoted key", config: "\"model_instructions_file\" = '/tmp/custom.md'\n", want: true},
		{name: "wrong type", config: "model_instructions_file = true\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "_")+".toml")
			if !test.missing {
				if err := os.WriteFile(path, []byte(test.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := modelInstructionFileConfiguredAt(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("configured = %v, want %v", got, test.want)
			}
		})
	}
}

func stockModelInstructionsForTest(prefix, suffix string) string {
	return prefix + stockRGInstruction + "\n" + stockExecInstruction + "\n" +
		stockEditHeading + "\n\n" + stockEditInstruction + "\n" + suffix
}
