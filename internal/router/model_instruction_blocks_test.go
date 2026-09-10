package router

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

func TestRewriteDeveloperModeBlocks(t *testing.T) {
	// Recorded developer content part from the supplied session export.
	recorded, err := os.ReadFile("testdata/collaboration-mode-instructions.txt")
	if err != nil {
		t.Fatal(err)
	}
	mode := string(recorded)
	requestInput := "<request_user_input>Ask a question.</request_user_input>"
	message := func(role string, content any) map[string]any {
		return map[string]any{"type": "message", "role": role, "content": content, "id": "preserved"}
	}
	part := func(text string) map[string]any {
		return map[string]any{"type": "input_text", "text": text, "extra": true}
	}
	permissions := "<permissions>Keep permissions.</permissions>"
	for _, tc := range []struct {
		name        string
		input, want []any
	}{
		{"standalone", []any{message("developer", mode), message("developer", requestInput)}, []any{}},
		{"adjacent blocks", []any{message("developer", mode+mode+permissions+requestInput)}, []any{message("developer", permissions)}},
		{"surrounding text", []any{message("developer", "before"+mode+"after")}, []any{message("developer", "beforeafter")}},
		{"quoted tags", []any{message("developer", "Keep `<collaboration_mode>example</collaboration_mode>`.")}, []any{message("developer", "Keep `<collaboration_mode>example</collaboration_mode>`.")}},
		{"mixed string", []any{message("developer", permissions+"\n"+mode)}, []any{message("developer", permissions+"\n")}},
		{"mixed parts", []any{message("developer", []any{part(permissions), part(mode), part(requestInput)})}, []any{message("developer", []any{part(permissions)})}},
		{"standalone parts", []any{message("developer", []any{part(mode), part(requestInput)})}, []any{}},
		{"other roles", []any{message("user", mode), message("assistant", requestInput)}, []any{message("user", mode), message("assistant", requestInput)}},
		{"incomplete", []any{message("developer", "<collaboration_mode>unfinished")}, []any{message("developer", "<collaboration_mode>unfinished")}},
		{"unrelated empty part", []any{message("developer", []any{part(""), part(mode), part(permissions)})}, []any{message("developer", []any{part(""), part(permissions)})}},
		{"unrelated mention", []any{message("developer", "Use request_user_input_async when available.")}, []any{message("developer", "Use request_user_input_async when available.")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, carrier := range []string{"instructions", "developer"} {
				t.Run(carrier, func(t *testing.T) {
					guidance := codexinstructions.InstructionsForModel("", false)
					input := append([]any{}, tc.input...)
					want := append([]any{}, tc.want...)
					request := parsedResponsesRequest{fields: make(map[string]json.RawMessage)}
					if carrier == "instructions" {
						request.fields["instructions"] = mustTestJSON(t, guidance)
					} else {
						input = append([]any{message("developer", guidance)}, input...)
						want = append([]any{message("developer", guidance)}, want...)
					}
					request.fields["input"] = mustTestJSON(t, input)
					for range 2 {
						if err := rewriteReceivedModelInstructions(t.Context(), &request, false, guidance); err != nil {
							t.Fatal(err)
						}
						var got, expected any
						if err := json.Unmarshal(request.fields["input"], &got); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(mustTestJSON(t, want), &expected); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, expected) {
							t.Fatalf("input = %s; want %s", request.fields["input"], mustTestJSON(t, want))
						}
					}
				})
			}
		})
	}
}
