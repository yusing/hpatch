package router

import (
	"encoding/json"
	"testing"
)

func TestOperationCommentaryRequiresAuthoredText(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			arguments string
			text      string
		}{
			{"apply_patch", `{}`, ""},
			{"wait", `{"commentary":" "}`, ""},
			{"lookup", `{"commentary":"Using lookup."}`, "Using lookup."},
			{"apply_patch", `{"commentary":"Applying the requested changes."}`, "Applying the requested changes."},
		} {
			t.Run(tc.name+tc.arguments+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
				transform.commentaryTools = commentaryToolCatalog{
					functionToolKey("external", tc.name): {qualifiedName: "external." + tc.name, explicit: true},
				}
				call := map[string]json.RawMessage{
					"type": mustTestJSON(t, "function_call"), "namespace": mustTestJSON(t, "external"),
					"name": mustTestJSON(t, tc.name), "id": mustTestJSON(t, "item"), "call_id": mustTestJSON(t, "call"),
					"arguments": mustTestJSON(t, tc.arguments),
				}
				var output []map[string]json.RawMessage
				if stream {
					events, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": call}))
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
						if envelope.Item != nil {
							output = append(output, envelope.Item)
						}
					}
				} else {
					response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{call}}))
					if err != nil {
						t.Fatal(err)
					}
					var envelope struct {
						Output []map[string]json.RawMessage `json:"output"`
					}
					if err := json.Unmarshal(response, &envelope); err != nil {
						t.Fatal(err)
					}
					output = envelope.Output
				}
				want := 1
				if tc.text != "" {
					want++
				}
				if len(output) != want {
					t.Fatalf("output = %s", mustTestJSON(t, output))
				}
				if tc.text != "" && commentaryText(t, output[0]) != tc.text {
					t.Fatalf("authored text lost: %s", mustTestJSON(t, output))
				}
				if jsonString(output[len(output)-1], "arguments") != "{}" {
					t.Fatalf("commentary argument not stripped: %s", mustTestJSON(t, output))
				}
				replay := &parsedResponsesRequest{fields: map[string]json.RawMessage{"input": mustTestJSON(t, output)}}
				if err := proxy.reconcileInputPrefix(replay, transform.historySessionID); err != nil {
					t.Fatal(err)
				}
				var restored []map[string]json.RawMessage
				if err := json.Unmarshal(replay.fields["input"], &restored); err != nil {
					t.Fatal(err)
				}
				if len(restored) != 1 || jsonString(restored[0], "arguments") != tc.arguments {
					t.Fatalf("replay changed: %s", replay.fields["input"])
				}
			})
		}
	}
}
