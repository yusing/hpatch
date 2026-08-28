package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusing/hpatch/internal/shellruntime"
)

const testToolPluginDeclaration = `export default {
  apiVersion: "hpatch-tool-plugin/v1",
  id: "proxy.test",
  tools: [{
    specification: {type: "custom", name: "plugin_tool", description: "fixture plugin tool"},
    parse(input) {
      if (input === "reject") throw new Error("fixture input rejected");
      return input;
    },
    argv(parsed) { return ["--fixed", parsed]; },
    translate(parsed, api) {
      if (parsed === "function") return api.function("lookup", "{\"value\":\"function\"}");
      if (parsed === "custom") return api.custom("exec", "custom payload");
      if (parsed === "missing") return api.custom("missing_carrier", parsed);
      if (parsed === "wrong-kind") return api.function("exec", "{}");
      if (parsed === "invalid-json") return api.function("lookup", "{");
      if (parsed === "template") return api.exec("before | {.} | after");
      if (parsed === "malformed") return {kind: "exec", payload: "forbidden"};
      return api.exec();
    },
    execute(argv) {
      return {
        stdout: [process.cwd(), process.env.HPATCH_PLUGIN_TEST, ...argv].join("|"),
        stderr: "fixture stderr",
        exitCode: 7,
      };
    }
  }]
};
`

func newToolPluginTestTransform(t *testing.T) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest) {
	t.Helper()
	t.Setenv(shellruntime.RuntimeDirectoryEnvironment, t.TempDir())
	dataDirectory := t.TempDir()
	pluginDirectory := filepath.Join(dataDirectory, "plugins")
	if err := os.Mkdir(pluginDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDirectory, "proxy.mjs"), []byte(testToolPluginDeclaration), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHPatchProxy(testTranslator(t, new(int)), registry, false, false)
	t.Cleanup(func() {
		if err := errors.Join(proxy.Close(), registry.Close()); err != nil {
			t.Error(err)
		}
	})

	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			testCodeModeAdditionalTools(testCodeModeDescription),
			map[string]any{"role": "user", "content": "task"},
		},
		"tools": []any{
			map[string]any{"type": "function", "name": "lookup"},
		},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	metadata := codexTurnMetadata{
		RequestKind: "turn",
		Directories: map[string]json.RawMessage{workspace: nil},
	}
	transform, err := proxy.prepareRequest(t.Context(), &request, "plugin-session", "plugin-thread", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transform.Close)
	return transform, proxy, &request
}

func testToolPluginItem(input string) map[string]any {
	return map[string]any{
		"type":    "custom_tool_call",
		"id":      "item-P",
		"call_id": "call-P",
		"name":    "plugin_tool",
		"input":   input,
		"status":  "completed",
		"future":  map[string]any{"kept": true},
	}
}

func decodeResponseItem(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 {
		t.Fatalf("response output = %#v", response.Output)
	}
	return response.Output[0]
}

func TestToolPluginRequestJSONAndReplay(t *testing.T) {
	transform, proxy, request := newToolPluginTestTransform(t)
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 || jsonString(tools[4], "name") != "plugin_tool" ||
		jsonString(tools[4], "description") != "fixture plugin tool" {
		t.Fatalf("installed tools = %#v", tools)
	}

	original := testToolPluginItem("execute")
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status":      "completed",
		"output":      []any{original},
		"tools":       []any{},
		"tool_choice": "auto",
	}))
	if err != nil {
		t.Fatal(err)
	}
	visible := decodeResponseItem(t, response)
	expectedPayload := registeredWorkerInput(t, proxy, "plugin_tool", []string{"--fixed", "execute"})
	if jsonString(visible, "type") != "custom_tool_call" ||
		jsonString(visible, "name") != "exec" ||
		jsonString(visible, "input") != expectedPayload ||
		string(visible["future"]) != `{"kept":true}` {
		t.Fatalf("visible plugin carrier = %#v", visible)
	}
	history, ok := proxy.history(transform.historySessionID, "call-P")
	if !ok || history.pluginID != "proxy.test" || history.toolName != "plugin_tool" ||
		history.carrierKind != codeModeCarrierCustom || history.carrierPayload != expectedPayload {
		t.Fatalf("plugin history = %+v, available %t", history, ok)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{
			visible,
			map[string]any{"type": "custom_tool_call_output", "call_id": "call-P", "output": "done"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
		t.Fatal(err)
	}
	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 || jsonString(replayed[0], "name") != "plugin_tool" ||
		jsonString(replayed[0], "input") != "execute" ||
		jsonString(replayed[0], "type") != "custom_tool_call" {
		t.Fatalf("restored plugin call = %#v", replayed)
	}

	tampered := maps.Clone(visible)
	tampered["input"] = mustMarshalJSON(expectedPayload + " ")
	replay, err = parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{tampered}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err == nil ||
		!strings.Contains(err.Error(), "changed translated payload") {
		t.Fatalf("tampered replay error = %v", err)
	}
}

func TestToolPluginExecTemplateUsesCanonicalWorkerCommand(t *testing.T) {
	transform, proxy, _ := newToolPluginTestTransform(t)
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status":      "completed",
		"output":      []any{testToolPluginItem("template")},
		"tools":       []any{},
		"tool_choice": "auto",
	}))
	if err != nil {
		t.Fatal(err)
	}

	visible := decodeResponseItem(t, response)
	contribution, ok := proxy.registry.contribution("plugin_tool")
	if !ok {
		t.Fatal("plugin contribution is unavailable")
	}
	payload, err := proxy.registry.execCarrierInput(
		contribution,
		"",
		[]string{"--fixed", "template"},
		"before | {.} | after",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if jsonString(visible, "name") != "exec" || jsonString(visible, "input") != payload {
		t.Fatalf("visible template carrier = %#v", visible)
	}
	history, ok := proxy.history(transform.historySessionID, "call-P")
	if !ok || history.carrierPayload != payload {
		t.Fatalf("template history = %+v, available %t", history, ok)
	}
}

func TestToolPluginGenericCarriers(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantType     string
		wantName     string
		payloadField string
		wantPayload  string
	}{
		{
			name:  "custom",
			input: "custom", wantType: "custom_tool_call", wantName: "exec",
			payloadField: "input", wantPayload: "custom payload",
		},
		{
			name:  "function",
			input: "function", wantType: "function_call", wantName: "lookup",
			payloadField: "arguments", wantPayload: `{"value":"function"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transform, proxy, _ := newToolPluginTestTransform(t)
			response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed",
				"output": []any{testToolPluginItem(test.input)},
			}))
			if err != nil {
				t.Fatal(err)
			}
			visible := decodeResponseItem(t, response)
			if jsonString(visible, "type") != test.wantType ||
				jsonString(visible, "name") != test.wantName ||
				jsonString(visible, test.payloadField) != test.wantPayload {
				t.Fatalf("visible %s carrier = %#v", test.name, visible)
			}
			otherField := "input"
			if test.payloadField == "input" {
				otherField = "arguments"
			}
			if _, exists := visible[otherField]; exists {
				t.Fatalf("visible %s carrier retains %s: %#v", test.name, otherField, visible)
			}
			if test.payloadField == "arguments" {
				replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
					"input": []any{
						visible,
						map[string]any{"type": "function_call_output", "call_id": "call-P", "output": "done"},
					},
				}))
				if err != nil {
					t.Fatal(err)
				}
				if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
					t.Fatal(err)
				}
				var restored []map[string]json.RawMessage
				if err := json.Unmarshal(replay.fields["input"], &restored); err != nil {
					t.Fatal(err)
				}
				if jsonString(restored[0], "type") != "custom_tool_call" ||
					jsonString(restored[0], "name") != "plugin_tool" ||
					jsonString(restored[0], "input") != "function" {
					t.Fatalf("restored function carrier = %#v", restored)
				}
			}
		})
	}
}

func TestToolPluginFunctionCarrierSSE(t *testing.T) {
	transform, _, _ := newToolPluginTestTransform(t)
	item := testToolPluginItem("function")
	added := maps.Clone(item)
	added["status"] = "in_progress"
	added["input"] = ""

	visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.output_item.added",
		"item": added,
	}))
	if err != nil || visible != nil {
		t.Fatalf("buffered plugin item = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.custom_tool_call_input.done", "item_id": "item-P", "input": "function",
	}))
	if err != nil || len(visible) != 2 {
		t.Fatalf("plugin input.done = %q, error %v", visible, err)
	}
	var addedEvent struct {
		Item map[string]json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(visible[0], &addedEvent); err != nil {
		t.Fatal(err)
	}
	if jsonString(addedEvent.Item, "type") != "function_call" ||
		jsonString(addedEvent.Item, "name") != "lookup" ||
		jsonString(addedEvent.Item, "arguments") != "" {
		t.Fatalf("function added event = %s", visible[0])
	}
	var doneEvent map[string]json.RawMessage
	if err := json.Unmarshal(visible[1], &doneEvent); err != nil {
		t.Fatal(err)
	}
	if jsonString(doneEvent, "type") != "response.function_call_arguments.done" ||
		jsonString(doneEvent, "arguments") != `{"value":"function"}` {
		t.Fatalf("function done event = %s", visible[1])
	}

	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": item,
	}))
	if err != nil || len(visible) != 1 || !bytes.Contains(visible[0], []byte(`"type":"function_call"`)) {
		t.Fatalf("function output item done = %q, error %v", visible, err)
	}
}

func TestToolPluginFailuresStayOutsideHPatchRecovery(t *testing.T) {
	t.Run("parser rejection is recoverable", func(t *testing.T) {
		transform, proxy, _ := newToolPluginTestTransform(t)
		response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": []any{testToolPluginItem("reject")},
		}))
		if err != nil {
			t.Fatal(err)
		}
		visible := decodeResponseItem(t, response)
		if jsonString(visible, "name") != "exec" ||
			!strings.Contains(jsonString(visible, "input"), "fixture input rejected") {
			t.Fatalf("rejection carrier = %#v", visible)
		}
		if _, err := proxy.recoverableHistory(transform.historySessionID); err == nil ||
			!strings.Contains(err.Error(), "no rejected hpatch script") {
			t.Fatalf("plugin entered recovery ancestry: %v", err)
		}
	})

	for _, input := range []string{"missing", "wrong-kind", "invalid-json", "malformed"} {
		t.Run(input, func(t *testing.T) {
			transform, _, _ := newToolPluginTestTransform(t)
			_, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed",
				"output": []any{testToolPluginItem(input)},
			}))
			if err == nil {
				t.Fatalf("%s translator failure was accepted", input)
			}
		})
	}
}
