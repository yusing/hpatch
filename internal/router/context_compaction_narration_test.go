package router

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactionNarrationReducesOnlyExactRepeatedProse(t *testing.T) {
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Only change the router."}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "routine_id", "content": "I'll inspect " + strings.Repeat("the implementation carefully and methodically ", 12) + "now."}),
		compactTestCall("old_step", "pwd"), compactTestOutput("old_step", "/workspace\n", 0),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "repeat_first", "content": strings.Repeat("The accepted decision remains qualified. ", 8)}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "repeat_last", "content": strings.Repeat("The accepted decision remains qualified. ", 8)}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "qualified", "content": "I'll inspect the implementation, but the unresolved failure must remain visible."}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "id": "assistant_keep", "content": strings.Repeat("I'll inspect operation_00 before continuing. ", 8)}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Preserve assistant_keep exactly."}),
	}
	for index := range 12 {
		id := fmt.Sprintf("operation_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}

	got := reduceContextCompactionNarration(items)
	wire := string(mustMarshalJSON(got))
	for _, id := range []string{"assistant_keep"} {
		if !strings.Contains(wire, id) {
			t.Fatalf("narration reduction lost stable item ID %q", id)
		}
	}
	if !strings.Contains(string(got[1]), "implementation carefully and methodically") {
		t.Fatal("non-repeated narration was guessed to be routine")
	}
	if strings.Contains(wire, "routine_id") {
		t.Fatal("unreferenced old assistant transport ID was retained")
	}
	if !strings.Contains(string(got[4]), "exact repeated historical narration omitted") ||
		!strings.Contains(string(got[5]), "The accepted decision remains qualified.") {
		t.Fatal("exact repetition was not consolidated into its later complete occurrence")
	}
	if !strings.Contains(string(got[6]), "unresolved failure must remain visible") ||
		string(got[7]) != string(items[7]) || string(got[8]) != string(items[8]) || string(got[0]) != string(items[0]) {
		t.Fatal("qualified, referenced, or authority-bearing narration changed")
	}
}

func TestCompactionNarrationPreservesRecentAndAmbiguousProse(t *testing.T) {
	var items []json.RawMessage
	for index := range 12 {
		id := fmt.Sprintf("step_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}
	recent := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "id": "recent_narration",
		"content": "I'll inspect " + strings.Repeat("the implementation carefully and methodically ", 12) + "now.",
	})
	ambiguous := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "id": "ambiguous_narration",
		"content": "The implementation may require another inspection depending on the retained state.",
	})
	items = append(items, recent, ambiguous)
	got := reduceContextCompactionNarration(items)
	if string(got[len(got)-2]) != string(recent) || string(got[len(got)-1]) != string(ambiguous) {
		t.Fatal("recent or ambiguous narration changed")
	}
}

func TestCompactionNarrationPreservesConditionalAuthorization(t *testing.T) {
	conditional := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "id": "conditional_authorization",
		"content": "I'll run " + strings.Repeat("the verification carefully and methodically ", 8) + "after you approve the external operation.",
	})
	items := []json.RawMessage{conditional, compactTestCall("authorized_step", "pwd"), compactTestOutput("authorized_step", "/workspace\n", 0)}
	for index := range 9 {
		id := fmt.Sprintf("later_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}
	got := reduceContextCompactionNarration(items)
	var fields map[string]json.RawMessage
	if json.Unmarshal(got[0], &fields) != nil || jsonString(fields, "content") != jsonString(mustUnmarshalObject(t, conditional), "content") {
		t.Fatal("conditional authorization narration changed")
	}
}

func mustUnmarshalObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("invalid test object")
	}
	return fields
}

func TestCompactionNarrationKeepsV2AgentIdentityWhenAssistantIDIsOmitted(t *testing.T) {
	assistant := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "id": "assistant_transport_old",
		"content": "An older assistant observation.",
	})
	agent := mustMarshalJSON(map[string]any{
		"type": "agent_message", "author": "/root/explorer", "recipient": "/root",
		"content": []any{map[string]any{"type": "input_text", "text": "Message Type: NEW_TASK\nTask name: /root/explorer\nSender: /root\nPayload:\nInspect the source."}},
	})
	items := []json.RawMessage{assistant, agent, agent}
	for index := range 12 {
		id := fmt.Sprintf("identity_%02d", index)
		items = append(items, compactTestCall(id, "pwd"), compactTestOutput(id, "/workspace\n", 0))
	}

	reduced := reduceContextCompaction(items)
	var retainedAgents []json.RawMessage
	for _, raw := range reduced {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if jsonString(fields, "id") == "assistant_transport_old" {
			t.Fatal("unreferenced old ordinary-assistant transport ID survived")
		}
		if jsonString(fields, "type") == "agent_message" {
			retainedAgents = append(retainedAgents, raw)
		}
	}
	if len(retainedAgents) != 2 || string(retainedAgents[0]) != string(agent) || string(retainedAgents[1]) != string(agent) {
		t.Fatal("repeated no-ID V2-eligible agent message changed")
	}

	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	capsule, err := compactor.seal(t.Context(), reduced)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := compactor.restore(t.Context(), []json.RawMessage{retainedAgents[1], capsule})
	if err != nil || contextCompactionCanonicalJSON(mustMarshalJSON(restored)) != contextCompactionCanonicalJSON(mustMarshalJSON(reduced)) {
		t.Fatalf("V2-carried agent message did not reconcile after assistant ID omission: %v", err)
	}
}
