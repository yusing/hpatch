package router

import (
	"encoding/json"
	"fmt"
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
	if !codeModeCommentaryParserAvailable {
		if err == nil || changed {
			t.Fatalf("cgo-disabled lowering = %v, error %v", changed, err)
		}
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
	if strings.Count(lowered, commentaryOnceArgument) != 1 || !strings.Contains(lowered, "text('await commentary(ignored)')") || strings.Contains(lowered, "text(result") {
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

func TestCodeModeCommentaryRejectsInvalidProgram(t *testing.T) {
	if _, err := findCodeModeCommentaryCalls("await commentary("); err == nil {
		t.Fatal("invalid Code Mode program was accepted")
	}
}

func TestCodeModeCommentaryReusesOnePublisherCapability(t *testing.T) {
	if !codeModeCommentaryParserAvailable {
		t.Skip("exact JavaScript parser is unavailable")
	}
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	_, changed, delivery, err := transform.lowerCodeModeCommentary(
		"call-code", "await commentary('first');\nawait commentary('second');",
	)
	if err != nil || !changed || delivery != codeModeCommentaryRuntime {
		t.Fatalf("changed = %v, delivery = %v, error %v", changed, delivery, err)
	}
	proxy.commentary.mu.Lock()
	routes := len(proxy.commentary.routes)
	proxy.commentary.mu.Unlock()
	if routes != 1 {
		t.Fatalf("publisher routes = %d", routes)
	}
}

func TestCodeModeCommentaryCapacityFallsBackToInertEvaluation(t *testing.T) {
	if !codeModeCommentaryParserAvailable {
		t.Skip("exact JavaScript parser is unavailable")
	}
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	proxy.setMetrics(newMetricsStore(""))
	for index := range maxCommentaryRoutes {
		if _, err := proxy.commentary.subscribe(
			fmt.Sprintf("history-%d", index), fmt.Sprintf("session-%d", index), fmt.Sprintf("call-%d", index), false, true,
		); err != nil {
			t.Fatal(err)
		}
	}
	lowered, changed, delivery, err := transform.lowerCodeModeCommentary(
		"call-code", "await commentary(sideEffect());",
	)
	if err != nil || !changed || delivery != codeModeCommentarySuppressed || !strings.Contains(lowered, "await tools.exec_command") ||
		!strings.Contains(lowered, "String(sideEffect())") {
		t.Fatalf("lowered = %q, changed = %v, delivery = %v, error %v", lowered, changed, delivery, err)
	}
	item := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON(transform.codeModeToolName),
		"call_id": mustMarshalJSON("call-default"), "id": mustMarshalJSON("item-default"),
		"input": mustMarshalJSON("await commentary(sideEffect());"),
	}
	if changed, err := transform.transformOutputItem(item); err != nil || !changed {
		t.Fatalf("fallback transform changed = %v, error %v", changed, err)
	}
	if message := transform.localStartCommentary(item); message != nil {
		t.Fatalf("capacity commentary = %v", message)
	}
	history := transform.local["call-default"]
	if history.replayCarrier || !history.commentarySuppressed || len(history.commentaryMessageIDs) != 0 ||
		jsonString(history.upstreamItem, "input") != "await commentary(sideEffect());" {
		t.Fatalf("fallback replay history = %+v", history)
	}
	commentary := proxy.metrics.snapshot().Commentary
	if commentary.Suppressed.Count != 0 || commentary.Default.Count != 0 {
		t.Fatalf("capacity commentary metrics = %+v", commentary)
	}
}

func TestCodeModeWithoutExplicitCommentaryGetsRouterDefault(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	item := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON(transform.codeModeToolName),
		"call_id": mustMarshalJSON("call-code-default"), "id": mustMarshalJSON("item-code-default"),
		"input": mustMarshalJSON("text('done');"),
	}
	changed, err := transform.transformOutputItem(item)
	if err != nil || changed {
		t.Fatalf("changed = %v, error %v", changed, err)
	}
	message := transform.localStartCommentary(item)
	if message == nil || jsonString(message, "phase") != "commentary" {
		t.Fatalf("default commentary = %v", message)
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(message["content"], &content) != nil || len(content) != 1 ||
		jsonString(content[0], "text") != "Running the requested operation." {
		t.Fatalf("default content = %s", message["content"])
	}
	if repeated := transform.localStartCommentary(item); repeated != nil {
		t.Fatalf("repeated default = %v", repeated)
	}
}
