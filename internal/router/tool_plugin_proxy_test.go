package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
	"github.com/yusing/hpatch"
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
      if (parsed === "stock") return api.exec("before | {.} | after", undefined, "python3 -c 'print(1)'");
      if (parsed === "malformed") return {kind: "exec", payload: "forbidden"};
      return api.exec();
    },
    execute(argv) {
      return {
        stdout: [process.cwd(), process.env.HPATCH_PLUGIN_TEST, ...argv].join("|"),
        stderr: "fixture stderr",
        stock: {stdout: "stock result", exitCode: 0},
        exitCode: 7,
      };
    }
  }]
};
`

func newToolPluginTestTransform(t *testing.T) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest) {
	t.Helper()
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
	proxy := newHPatchProxy(testTranslator(t, new(int)), registry)
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
	transform, err := proxy.prepareRequest(t.Context(), &request, "plugin-session", metadata, true)
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
	if len(tools) != 4 || jsonString(tools[3], "name") != "plugin_tool" ||
		jsonString(tools[3], "description") != "fixture plugin tool" {
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
	if err := proxy.restoreInputPrefix(&replay, transform.historySessionID); err != nil {
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
	if err := proxy.restoreInputPrefix(&replay, transform.historySessionID); err == nil ||
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
	payload, err := workerTemplateExecInputWithParams(
		"plugin_tool",
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

func TestToolPluginMetricsUseOriginalAndStockCarrierShapes(t *testing.T) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	count := func(value string) uint64 {
		t.Helper()
		got, countErr := codec.Count(value)
		if countErr != nil {
			t.Fatal(countErr)
		}
		return uint64(got)
	}
	run := func(input string) (hpatchMetricRecord, string) {
		t.Helper()
		transform, proxy, _ := newToolPluginTestTransform(t)
		var records []hpatchMetricRecord
		proxy.translator = metricsObservingTranslator{
			translate: func(context.Context, string, string) ([]byte, error) {
				t.Fatal("plugin call reached hpatch translation")
				return nil, nil
			},
			record: func(_ context.Context, record hpatchMetricRecord) error {
				records = append(records, record)
				return nil
			},
		}
		response, transformErr := transform.TransformJSON(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": []any{testToolPluginItem(input)},
		}))
		if transformErr != nil {
			t.Fatal(transformErr)
		}
		visible := decodeResponseItem(t, response)
		if len(records) != 1 {
			t.Fatalf("metric records = %d, want 1", len(records))
		}
		return records[0], jsonString(visible, "input")
	}

	success, payload := run("stock")
	var successTool *hpatch.ToolMetricRecord
	for index := range success.ToolMetrics {
		if success.ToolMetrics[index].PluginID == "proxy.test" && success.ToolMetrics[index].ToolName == "plugin_tool" {
			successTool = &success.ToolMetrics[index]
			break
		}
	}
	stockPayload, err := workerCommandExecInputWithParams(
		"before | python3 -c 'print(1)' | after",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if successTool == nil || successTool.Calls != 1 ||
		successTool.EmittedTokens != count("plugin_tool\nstock") ||
		successTool.TranslatedTokens != count("functions.exec\n"+stockPayload) ||
		successTool.FailedTranslations != 0 ||
		payload == stockPayload {
		t.Fatalf("successful plugin metric = %+v, payload %q, stock %q", successTool, payload, stockPayload)
	}
	if success.DefinitionRequests != 1 || len(success.ToolMetrics) != 3 {
		t.Fatalf("installed definition breakdown = %+v", success)
	}

	rejected, _ := run("reject")
	var rejectedTool *hpatch.ToolMetricRecord
	for index := range rejected.ToolMetrics {
		if rejected.ToolMetrics[index].PluginID == "proxy.test" && rejected.ToolMetrics[index].ToolName == "plugin_tool" {
			rejectedTool = &rejected.ToolMetrics[index]
			break
		}
	}
	if rejectedTool == nil || rejectedTool.FailedTranslations != 1 ||
		rejectedTool.FailedEmittedTokens != count("plugin_tool\nreject") ||
		rejectedTool.FailedTranslatedTokens != 0 || rejectedTool.Calls != 0 ||
		rejected.DiagnosticInputTokens == 0 {
		t.Fatalf("rejected plugin metric = %+v, record %+v", rejectedTool, rejected)
	}
}

func TestToolPluginMetricPersistenceFailuresDoNotChangeCarriers(t *testing.T) {
	for _, test := range []struct {
		name         string
		input        string
		wantFragment string
	}{
		{name: "successful translation", input: "execute", wantFragment: "tools.exec_command"},
		{name: "input rejection", input: "reject", wantFragment: "fixture input rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transform, proxy, _ := newToolPluginTestTransform(t)
			proxy.translator = metricsObservingTranslator{
				translate: func(context.Context, string, string) ([]byte, error) {
					t.Fatal("plugin call reached hpatch translation")
					return nil, nil
				},
				record: func(context.Context, hpatchMetricRecord) error {
					return errors.New("metrics unavailable")
				},
			}
			response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed",
				"output": []any{testToolPluginItem(test.input)},
			}))
			if err != nil {
				t.Fatal(err)
			}
			visible := decodeResponseItem(t, response)
			if jsonString(visible, "name") != "exec" || !strings.Contains(jsonString(visible, "input"), test.wantFragment) {
				t.Fatalf("carrier after metrics failure = %#v", visible)
			}
		})
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
				if err := proxy.restoreInputPrefix(&replay, transform.historySessionID); err != nil {
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
