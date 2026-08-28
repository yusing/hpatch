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
	changed, err := transform.transformOutputItem(item)
	if err != nil && strings.Contains(err.Error(), "requires a build with cgo-enabled") {
		return
	}
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
	if changed, err := transform.transformOutputItem(repeated); err != nil || !changed || jsonString(repeated, "input") != lowered {
		t.Fatalf("repeated lower = changed %v, error %v, input %s", changed, err, repeated["input"])
	}
}

func TestCodeModeCommentaryUsesOneRouteAndFallsBackToEvaluation(t *testing.T) {
	if _, err := findCodeModeCommentaryCalls("await commentary('test');"); err != nil &&
		strings.Contains(err.Error(), "requires a build with cgo-enabled") {
		t.Skip("exact JavaScript parser is unavailable")
	}
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
		if proxy.commentary.subscribe("session", "call") == nil {
			t.Fatalf("route %d was rejected early", index)
		}
	}
	lowered, changed, err = transform.lowerCodeModeCommentary("call-fallback", "await commentary(sideEffect());")
	if err != nil || !changed || !strings.Contains(lowered, "await (void (sideEffect()))") ||
		strings.Contains(lowered, commentaryOnceArgument) {
		t.Fatalf("fallback = %q, changed = %v, error %v", lowered, changed, err)
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
	changed, err := transform.transformOutputItem(item)
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
