package hpatch

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestToolMetricsClassifyPersistAndReportCanonicalShapes(t *testing.T) {
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

	const (
		pluginID      = "example.plugin"
		toolName      = "example_tool"
		definition    = `{"type":"custom","name":"example_tool","description":"Example"}`
		installed     = `[{"type":"custom","name":"example_tool","description":"Example"}]`
		emitted       = `"quotes"; await tools.exec_command({cmd:"not code"})`
		carrier       = `{"path":"a b","literal":"$(still-data)"}`
		currentResult = "verified rows\nwarning\n"
		stockResult   = "plain rows\nwarning\n"
	)
	success, err := ClassifyHostMetrics(HostMetricInput{
		SessionID:           "session",
		InstalledDefinition: installed,
		ToolDefinitions: []HostToolDefinition{{
			PluginID: pluginID, ToolName: toolName, Definition: definition,
		}},
		RemovedDefinition: "native patch definition",
		ToolCall: &HostToolCall{
			PluginID: pluginID, ToolName: toolName,
			EmittedName: toolName, EmittedInput: emitted,
			TranslatedName: "functions.exec", TranslatedPayload: carrier,
		},
		ToolResult: &HostToolResult{
			PluginID: pluginID, ToolName: toolName,
			CurrentOutput: currentResult, StockOutput: stockResult,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(success.ToolMetrics) != 1 {
		t.Fatalf("classified tools = %+v", success.ToolMetrics)
	}
	tool := success.ToolMetrics[0]
	if tool.Calls != 1 || tool.EmittedTokens != count(toolName+"\n"+emitted) ||
		tool.TranslatedTokens != count("functions.exec\n"+carrier) ||
		tool.DefinitionInputTokens != count(definition) || tool.Executions != 1 ||
		tool.CurrentInputTokens != count(currentResult) || tool.StockInputTokens != count(stockResult) {
		t.Fatalf("classified tool = %+v", tool)
	}
	if got := int64(tool.DefinitionInputTokens) + success.SharedDefinitionInputTokens; got != int64(count(installed)) {
		t.Fatalf("definition breakdown = %d + %d, total %d", tool.DefinitionInputTokens, success.SharedDefinitionInputTokens, count(installed))
	}

	rejected, err := ClassifyHostMetrics(HostMetricInput{
		SessionID: "session",
		ToolCall: &HostToolCall{
			PluginID: pluginID, ToolName: toolName,
			EmittedName: toolName, EmittedInput: "bad input",
			FailedTranslation: true,
		},
		Diagnostic: "input rejected",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataDirectory := t.TempDir()
	if err := RecordHostMetrics(t.Context(), dataDirectory, success); err != nil {
		t.Fatal(err)
	}
	if err := RecordHostMetrics(t.Context(), dataDirectory, rejected); err != nil {
		t.Fatal(err)
	}
	gain, err := LoadGainMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(gain.Tools) != 2 || gain.Tools[0].Failed || !gain.Tools[1].Failed {
		t.Fatalf("stable success/failed rows = %+v", gain.Tools)
	}
	if gain.Tools[0].PluginID != pluginID || gain.Tools[0].ToolName != toolName ||
		gain.Tools[1].Reduction != "n/a" || gain.Tools[1].TranslatedTokens != 0 {
		t.Fatalf("gain tool rows = %+v", gain.Tools)
	}
	if gain.AllTools.EmittedTokens != gain.Tools[0].EmittedTokens+gain.Tools[1].EmittedTokens ||
		gain.AllTools.TranslatedTokens != gain.Tools[0].TranslatedTokens {
		t.Fatalf("all-tools = %+v, rows %+v", gain.AllTools, gain.Tools)
	}
	if gain.DefinitionInputTokens != count(installed) || len(gain.ToolDefinitions) != 1 ||
		gain.ToolDefinitions[0].Tokens != count(definition) ||
		int64(gain.ToolDefinitions[0].Tokens)+gain.SharedDefinitionTokens != int64(gain.DefinitionInputTokens) {
		t.Fatalf("gain definitions = %+v", gain)
	}
	if len(gain.ToolInputs) != 1 || gain.ToolInputs[0].PluginID != pluginID ||
		gain.ToolInputs[0].CurrentTokens != count(currentResult) ||
		gain.ToolInputs[0].StockTokens != count(stockResult) ||
		gain.AllToolInputs.CurrentTokens != count(currentResult) {
		t.Fatalf("gain input rows = %+v", gain)
	}
}

func TestMetricToolIdentityMatchesRegistry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pluginID string
		toolName string
		valid    bool
	}{
		{name: "registry forms", pluginID: "A.plugin-1_test", toolName: "_Tool-1", valid: true},
		{name: "maximum lengths", pluginID: "a" + strings.Repeat("x", maxMetricPluginIDBytes-1), toolName: "_" + strings.Repeat("x", maxMetricToolNameBytes-1), valid: true},
		{name: "plugin starts with underscore", pluginID: "_plugin", toolName: "tool"},
		{name: "plugin contains slash", pluginID: "example/plugin", toolName: "tool"},
		{name: "plugin exceeds limit", pluginID: "a" + strings.Repeat("x", maxMetricPluginIDBytes), toolName: "tool"},
		{name: "tool starts with digit", pluginID: "example.plugin", toolName: "1tool"},
		{name: "tool contains dot", pluginID: "example.plugin", toolName: "tool.name"},
		{name: "tool exceeds limit", pluginID: "example.plugin", toolName: "t" + strings.Repeat("x", maxMetricToolNameBytes)},
		{name: "non-ASCII plugin", pluginID: "éxample", toolName: "tool"},
		{name: "non-ASCII tool", pluginID: "example.plugin", toolName: "tøol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMetricToolKey(test.pluginID, test.toolName)
			if (err == nil) != test.valid {
				t.Fatalf("validateMetricToolKey(%q, %q) error = %v, valid %t", test.pluginID, test.toolName, err, test.valid)
			}
		})
	}
}

func TestValidToolMetricsRejectsCombinedCounterOverflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		assign func(*toolMetric)
	}{
		{name: "calls", assign: func(metric *toolMetric) {
			metric.Calls = math.MaxUint64
			metric.FailedTranslations = 1
		}},
		{name: "emitted tokens", assign: func(metric *toolMetric) {
			metric.EmittedTokens = math.MaxUint64
			metric.FailedEmittedTokens = 1
		}},
		{name: "translated tokens", assign: func(metric *toolMetric) {
			metric.TranslatedTokens = math.MaxUint64
			metric.FailedTranslatedTokens = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := metrics{ToolCount: 1}
			value.Tools[0] = toolMetric{PluginID: "example.plugin", ToolName: "tool"}
			test.assign(&value.Tools[0])
			if validToolMetrics(value) {
				t.Fatalf("validToolMetrics() accepted combined %s overflow", test.name)
			}
		})
	}
}

func TestGainIncludesSuccessfulRowForInstalledToolWithoutCalls(t *testing.T) {
	t.Parallel()
	value := metrics{DefinitionRequests: 1, DefinitionInputTokens: 7, ToolCount: 1}
	value.Tools[0] = toolMetric{
		PluginID: "example.plugin", ToolName: "unused_tool", DefinitionInputTokens: 7,
	}
	tools, all, definitions := value.gainToolRows()
	if len(tools) != 1 || tools[0].PluginID != "example.plugin" || tools[0].ToolName != "unused_tool" ||
		tools[0].Failed || tools[0].Calls != 0 || tools[0].EmittedTokens != 0 ||
		tools[0].TranslatedTokens != 0 || tools[0].Reduction != "n/a" {
		t.Fatalf("successful rows = %+v", tools)
	}
	if all.Calls != 0 || all.EmittedTokens != 0 || all.TranslatedTokens != 0 || all.Reduction != "n/a" {
		t.Fatalf("all-tools = %+v", all)
	}
	if len(definitions) != 1 || definitions[0].Tokens != 7 {
		t.Fatalf("definition rows = %+v", definitions)
	}
}

func TestToolMetricSlotRoundTrip(t *testing.T) {
	t.Parallel()
	want := toolPersistenceFixture()
	want.DefinitionRequests = 1
	want.DefinitionInputTokens = 7
	want.SharedDefinitionInputTokens = -2
	want.Tools[0].DefinitionInputTokens = 9

	encoded := encodeMetricsSlot(want, 42)
	got, generation, ok := decodeMetricsSlot(encoded)
	if !ok || generation != 42 || got != want {
		t.Fatalf("decoded slot = (%+v, %d, %t), want (%+v, 42, true)", got, generation, ok, want)
	}
}

func TestToolMetricCollectionOverflowDoesNotChangePersistedAggregate(t *testing.T) {
	dataDirectory := t.TempDir()
	initial := metrics{ToolCount: maxMetricTools}
	for index := range maxMetricTools {
		initial.Tools[index] = toolMetric{
			PluginID: fmt.Sprintf("plugin%03d", index), ToolName: "tool", Calls: 1,
		}
	}
	if err := updateMetrics(dataDirectory, initial); err != nil {
		t.Fatal(err)
	}
	before, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}

	extra := metrics{ToolCount: 1}
	extra.Tools[0] = toolMetric{PluginID: "plugin999", ToolName: "tool", Calls: 1}
	if err := updateMetrics(dataDirectory, extra); err == nil || !strings.Contains(err.Error(), "tool collection exceeds") {
		t.Fatalf("overflow update error = %v", err)
	}
	after, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("aggregate changed after rejected overflow: before %+v, after %+v", before, after)
	}
}
