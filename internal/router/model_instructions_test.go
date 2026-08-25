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
	want := "custom prefix\n" + codexinstructions.Instructions() + "custom suffix\n"
	for _, lifecycle := range []string{
		"session start",
		"post compaction",
		"subagent start",
		"subagent post compaction",
	} {
		t.Run(lifecycle, func(t *testing.T) {
			got, err := renderModelInstructions(stock, false)
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

func TestRenderModelInstructionsRefreshesInheritedConversation(t *testing.T) {
	input := "custom prefix\n" + codexinstructions.Instructions() + "custom suffix\n"
	got, err := renderModelInstructions(input, false)
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
			got, err := renderModelInstructions(test.input, true)
			if err != nil {
				t.Fatal(err)
			}
			want := test.input + "\n" + codexinstructions.Instructions()
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
			if _, err := renderModelInstructions(test.input, test.customized); err == nil {
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
			if err := rewriteReceivedModelInstructions(&request, false); err != nil {
				t.Fatal(err)
			}
			if got := string(request.fields["instructions"]); got != before {
				t.Fatalf("instructions = %q, want %q", got, before)
			}
		})
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
