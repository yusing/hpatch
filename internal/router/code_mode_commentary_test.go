package router

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

func TestCodeModeCommentaryLowersRuntimeExpressionAndPreservesOriginal(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	source := "for (let i = 1; i <= 2; i++) {\n" +
		"  await commentary(`Running ${i}/2`);\n" +
		"}\n" +
		"text('await commentary(ignored)');"
	item := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON(transform.codeModeToolName),
		"call_id": mustMarshalJSON("call-code"), "id": mustMarshalJSON("item-code"), "input": mustMarshalJSON(source),
	}
	view := newResponsesItem(item)
	changed, err := transform.transformOutputItem(&view)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error %v", changed, err)
	}
	lowered := jsonString(item, "input")
	for _, required := range []string{"await tools.exec_command", "encodeURIComponent(String(`Running ${i}/2`))", commentaryOnceArgument} {
		if !strings.Contains(lowered, required) {
			t.Fatalf("lowered input missing %q: %s", required, lowered)
		}
	}
	if strings.Count(lowered, commentaryOnceArgument) != 1 || !strings.Contains(lowered, "text('await commentary(ignored)')") {
		t.Fatalf("lowered input = %s", lowered)
	}
	history := transform.local["call-code"]
	if history.script != source || jsonString(history.upstreamItem, "input") != source || history.carrierPayload != lowered {
		t.Fatalf("history = %+v", history)
	}
	repeated := maps.Clone(item)
	repeated["input"] = mustMarshalJSON(source)
	repeatedView := newResponsesItem(repeated)
	if changed, err := transform.transformOutputItem(&repeatedView); err != nil || !changed || jsonString(repeated, "input") != lowered {
		t.Fatalf("repeated lower = changed %v, error %v, input %s", changed, err, repeated["input"])
	}
}

func TestCodeModeCommentaryUsesOneRouteAndFallsBackToEvaluation(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	lowered, changed, err := transform.lowerCodeModeCommentary(
		"call-code", "await commentary('first');\nawait commentary('second');",
	)
	if err != nil || !changed || strings.Count(lowered, commentaryOnceArgument) != 2 {
		t.Fatalf("lowered = %q, changed = %v, error %v", lowered, changed, err)
	}
	proxy.commentary.mu.Lock()
	routes := len(proxy.commentary.routes)
	proxy.commentary.mu.Unlock()
	if routes != 1 {
		t.Fatalf("publisher routes = %d", routes)
	}

	for index := routes; index < maxCommentaryRoutes; index++ {
		if proxy.commentary.subscribe("session", "call") == "" {
			t.Fatalf("route %d was rejected early", index)
		}
	}
	lowered, changed, err = transform.lowerCodeModeCommentary("call-fallback", "await commentary(sideEffect());")
	if err != nil || !changed || !strings.Contains(lowered, "await (void (sideEffect()))") ||
		strings.Contains(lowered, commentaryOnceArgument) {
		t.Fatalf("fallback = %q, changed = %v, error %v", lowered, changed, err)
	}
}

func TestCodeModeCommentarySupportsNestingAndAlwaysReturnsUndefined(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	lowered, changed, err := transform.lowerCodeModeCommentary(
		"call-nested", `await commentary(await commentary("inner"));`,
	)
	if err != nil || !changed || strings.Count(lowered, commentaryOnceArgument) != 2 ||
		strings.Count(lowered, "await (void (await tools.exec_command") != 2 {
		t.Fatalf("lowered = %q, changed = %v, error = %v", lowered, changed, err)
	}
}

func TestCodeModeCommentaryLowersAuthoritativeStreamingInput(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	source := `await commentary("Working");`
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added", "item": map[string]any{
			"type": "custom_tool_call", "id": "item-code", "call_id": "call-code",
			"name": transform.codeModeToolName, "input": "",
		},
	})
	if events, err := transform.TransformSSE(added); err != nil || len(events) != 1 {
		t.Fatalf("added events = %q, error = %v", events, err)
	}
	inputDone := mustTestJSON(t, map[string]any{
		"type": "response.custom_tool_call_input.done", "item_id": "item-code",
		"call_id": "call-code", "input": source,
	})
	events, err := transform.TransformSSE(inputDone)
	if err != nil || len(events) != 1 {
		t.Fatalf("input events = %q, error = %v", events, err)
	}
	var completedInput struct {
		Input string `json:"input"`
	}
	if json.Unmarshal(events[0], &completedInput) != nil || completedInput.Input == source ||
		!strings.Contains(completedInput.Input, commentaryOnceArgument) {
		t.Fatalf("completed input = %q", events[0])
	}
	itemDone := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done", "item": map[string]any{
			"type": "custom_tool_call", "id": "item-code", "call_id": "call-code",
			"name": transform.codeModeToolName, "input": source, "status": "completed",
		},
	})
	events, err = transform.TransformSSE(itemDone)
	if err != nil || len(events) != 1 {
		t.Fatalf("item events = %q, error = %v", events, err)
	}
	var completedItem struct {
		Item map[string]json.RawMessage `json:"item"`
	}
	if json.Unmarshal(events[0], &completedItem) != nil ||
		jsonString(completedItem.Item, "input") != completedInput.Input {
		t.Fatalf("completed item = %q, input = %q", events[0], completedInput.Input)
	}
	if jsonString(transform.local["call-code"].upstreamItem, "status") != "completed" {
		t.Fatalf("retained item = %s", mustMarshalJSON(transform.local["call-code"].upstreamItem))
	}
}

func TestCodeModeWithoutExplicitCommentaryGetsDefault(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	item := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON(transform.codeModeToolName),
		"call_id": mustMarshalJSON("call-default"), "id": mustMarshalJSON("item-default"),
		"input": mustMarshalJSON("text('done');"),
	}
	view := newResponsesItem(item)
	changed, err := transform.transformOutputItem(&view)
	if err != nil || changed {
		t.Fatalf("changed = %v, error %v", changed, err)
	}
	message := transform.localStartCommentary(item)
	if message == nil || !strings.Contains(string(message["content"]), "Running the requested operation.") {
		t.Fatalf("default commentary = %v", message)
	}
	if repeated := transform.localStartCommentary(item); repeated != nil {
		t.Fatalf("repeated default = %v", repeated)
	}
}
