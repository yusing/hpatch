package router

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func bridgeTestRequest(t *testing.T, additional bool) parsedResponsesRequest {
	t.Helper()
	ns := map[string]any{"type": "namespace", "name": "collaboration", "tools": []any{map[string]any{"type": "function", "name": "spawn_agent", "parameters": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string", "encrypted": true}, "model": map[string]string{"type": "string"}}}}}}
	request := map[string]any{"model": "gpt-test", "instructions": "keep", "tools": []any{ns}, "input": []any{map[string]string{"type": "function_call", "namespace": "collaboration", "name": "spawn_agent", "arguments": "{}", "call_id": "c1"}}}
	if additional {
		request["tools"] = []any{}
		request["input"] = append(request["input"].([]any), map[string]any{"type": "additional_tools", "tools": []any{ns}})
	}
	parsed, err := parseResponsesRequest(mustTestJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func TestSubagentBridgeProjectsAndRestoresPlaintext(t *testing.T) {
	for _, additional := range []bool{false, true} {
		request := bridgeTestRequest(t, additional)
		bridge, err := prepareSubagentBridge(&request)
		if err != nil {
			t.Fatal(err)
		}
		data := mustMarshalJSON(request.fields)
		if bytes.Contains(data, []byte(`"encrypted":true`)) || !bytes.Contains(data, []byte(`"namespace":"mekugi_collaboration"`)) {
			t.Fatalf("projection=%s", data)
		}
		if !strings.Contains(jsonString(request.fields, "instructions"), "plaintext") {
			t.Fatal("missing bridge guidance")
		}
		item := map[string]any{"type": "function_call", "namespace": subagentBridgeNamespace, "name": "spawn_agent", "call_id": "c1", "arguments": `{"message":"plain"}`}
		response, err := bridge.TransformJSON(mustTestJSON(t, map[string]any{"output": []any{item}}))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(response, []byte(`"namespace":"collaboration"`)) || !bytes.Contains(response, []byte(`"encrypted_function_args":[]`)) {
			t.Fatalf("restoration=%s", response)
		}
		events, err := bridge.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(events[0], []byte(`"encrypted_function_args":[]`)) {
			t.Fatal("stream marker missing")
		}
		terminal, err := bridge.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{item}}}))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(terminal[0], []byte(`"namespace":"collaboration"`)) {
			t.Fatal("terminal call not restored")
		}
	}
}
func TestGrokCatalogPreservesNativeMetadata(t *testing.T) {
	catalog := []byte(`{"models":[{"slug":"gpt-5.6-sol","multi_agent_version":"v2","use_responses_lite":true,"model_messages":{"instructions_template":"native instructions"},"unknown_future_field":42}],"extra":"keep"}`)
	result, err := appendGrokModel(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Models []map[string]json.RawMessage `json:"models"`
		Extra  string                       `json:"extra"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Models) != 2 || parsed.Extra != "keep" || jsonString(parsed.Models[0], "slug") != "gpt-5.6-sol" || jsonString(parsed.Models[1], "slug") != grokModel || string(parsed.Models[1]["unknown_future_field"]) != "42" {
		t.Fatalf("catalog=%s", result)
	}
	if string(parsed.Models[1]["use_responses_lite"]) != "false" {
		t.Fatal("inherited OpenAI lite transport")
	}
	if _, err := appendGrokModel(result); err == nil {
		t.Fatal("accepted conflicting alias")
	}
}

func TestSubagentBridgePreservesScalarOpenAIInput(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		request := bridgeTestRequest(t, false)
		request.fields["input"] = mustMarshalJSON("hello")
		if !withTools {
			delete(request.fields, "tools")
		}
		_, err := prepareSubagentBridge(&request)
		if err != nil {
			t.Fatal(err)
		}
		if string(request.fields["input"]) != `"hello"` {
			t.Fatal("scalar input was rewritten")
		}
	}
}
func TestGrokCatalogRequiresAnActualV2Template(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		models := []any{map[string]any{"slug": "gpt-5.6-sol", "multi_agent_version": "v1", "marker": "wrong"}}
		if fallback {
			models = append(models, map[string]any{"slug": "other", "multi_agent_version": "v2", "marker": "correct"})
		}
		result, err := appendGrokModel(mustTestJSON(t, map[string]any{"models": models}))
		if !fallback {
			if err == nil {
				t.Fatal("accepted non-v2 catalog")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Models []map[string]json.RawMessage `json:"models"`
		}
		json.Unmarshal(result, &catalog)
		if jsonString(catalog.Models[len(catalog.Models)-1], "marker") != "correct" {
			t.Fatal("selected non-v2 template")
		}
	}
}
