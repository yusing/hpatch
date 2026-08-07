package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

const (
	testTranslatedPatch = "*** Begin Patch\n*** Add File: created.txt\n+payload\n*** End Patch\n"
	testHPatchScript    = "new created.txt\ntype \"payload\"\n"
	testHPatchReport    = "in created.txt 1:8\n239f: payload\n"
)

const testHPatchToolDescription = "fixture hpatch description\nwith exact trailing newline\n"

const testCodeModeDescription = "Run JavaScript.\n- All nested tools are available on the global `tools` object, for example `await tools.exec_command(...)`. Tool names are exposed as normalized JavaScript identifiers.\n\n### `exec_command`\nRun a shell command.\n\nexec tool declaration:\n```ts\ndeclare const tools: { exec_command(args: { cmd: string; workdir?: string }): Promise<unknown>; };\n```\n\n### `apply_patch`\nThe default editor.\n\nexec tool declaration:\n```ts\ndeclare const tools: { apply_patch(input: string): Promise<unknown>; };\n```\n\n### `create_goal`\nCreate a goal."

func testCodeModeAdditionalTools(description string) map[string]any {
	return map[string]any{
		"type":   "additional_tools",
		"role":   "developer",
		"future": map[string]any{"kept": true},
		"tools": []any{
			map[string]any{
				"type":        "namespace",
				"name":        "functions",
				"description": "",
				"future":      true,
				"tools": []any{
					map[string]any{
						"type":        "custom",
						"name":        "exec",
						"description": description,
						"format":      map[string]any{"type": "text"},
						"future":      true,
					},
					map[string]any{"type": "function", "name": "wait", "future": true},
				},
			},
			map[string]any{
				"type":   "namespace",
				"name":   "collaboration",
				"future": true,
				"tools":  []any{map[string]any{"type": "function", "name": "send_message", "future": true}},
			},
		},
	}
}

func testFlatCodeModeAdditionalTools(description string) map[string]any {
	return map[string]any{
		"type":   "additional_tools",
		"role":   "developer",
		"future": map[string]any{"kept": true},
		"tools": []any{
			map[string]any{
				"type":        "custom",
				"name":        "exec",
				"description": description,
				"format":      map[string]any{"type": "text"},
				"future":      true,
			},
			map[string]any{"type": "function", "name": "wait", "future": true},
			map[string]any{
				"type":   "namespace",
				"name":   "collaboration",
				"future": true,
				"tools":  []any{map[string]any{"type": "function", "name": "send_message", "future": true}},
			},
		},
	}
}

func testFunctionsNamespaceTools(t *testing.T, fields map[string]json.RawMessage) ([]map[string]json.RawMessage, []map[string]json.RawMessage) {
	t.Helper()
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || jsonString(items[0], "type") != "additional_tools" {
		t.Fatalf("input has no leading additional_tools item: %#v", items)
	}
	var namespaces []map[string]json.RawMessage
	if err := json.Unmarshal(items[0]["tools"], &namespaces); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(namespaces, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "type") == "namespace" && jsonString(tool, "name") == "functions"
	})
	if index < 0 {
		t.Fatalf("additional_tools has no functions namespace: %#v", namespaces)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(namespaces[index]["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	return tools, namespaces
}

func testInstalledTools() []map[string]json.RawMessage {
	return []map[string]json.RawMessage{
		customGrammarTool(hpatchToolName, testHPatchToolDescription, hpatch.ToolGrammar()),
		customGrammarTool("hread", "fixture hread description", "start: TEST"),
		customGrammarTool("hgrep", "fixture hgrep description", "start: TEST"),
	}
}

type hpatchTranslatorFunc func(context.Context, routingWorkspace, string) ([]byte, error)

func (f hpatchTranslatorFunc) Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error) {
	patch, err := f(ctx, workspace, script)
	return hpatchTranslationResult{patch: patch, report: testHPatchReport}, err
}

func (hpatchTranslatorFunc) ToolDescription() string {
	return testHPatchToolDescription
}

func (hpatchTranslatorFunc) RecordMetrics(context.Context, hpatchMetricRecord) error {
	return nil
}

type hpatchResultTranslatorFunc func(context.Context, routingWorkspace, string) (hpatchTranslationResult, error)

func (f hpatchResultTranslatorFunc) Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error) {
	return f(ctx, workspace, script)
}

func (hpatchResultTranslatorFunc) ToolDescription() string {
	return testHPatchToolDescription
}

func (hpatchResultTranslatorFunc) RecordMetrics(context.Context, hpatchMetricRecord) error {
	return nil
}

func newManagedHPatchProxy(t *testing.T, translator hpatchTranslator) *hpatchProxy {
	t.Helper()
	if translator == nil {
		return nil
	}
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), translator.ToolDescription())
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHPatchProxy(translator, registry)
	t.Cleanup(func() {
		if err := errors.Join(proxy.Close(), registry.Close()); err != nil {
			t.Error(err)
		}
	})
	return proxy
}

func registeredWorkerInput(t *testing.T, proxy *hpatchProxy, name string, arguments []string) string {
	t.Helper()
	_, ok := proxy.registry.wrapper(name)
	if !ok {
		t.Fatalf("registered worker %q is unavailable", name)
	}
	input, err := workerExecInputWithParams(name, arguments, nil)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func newHPatchTestTransform(t *testing.T, translator hpatchTranslator) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest, string) {
	t.Helper()
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			testCodeModeAdditionalTools(testCodeModeDescription),
			map[string]any{"role": "user", "content": "task", "future": true},
		},
		"tools":               []any{map[string]any{"type": "function", "name": "lookup", "future": true}},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"future_request":      map[string]any{"kept": true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newManagedHPatchProxy(t, translator)
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "session-1", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	if transform == nil {
		t.Fatal("prepareRequest returned no transform")
	}
	t.Cleanup(func() {
		transform.Close()
	})
	return transform, proxy, &request, workspace
}

func testTranslator(t *testing.T, calls *int) hpatchTranslator {
	t.Helper()
	return hpatchTranslatorFunc(func(_ context.Context, workspace routingWorkspace, script string) ([]byte, error) {
		*calls++
		if workspace.canonical == "" || workspace.root == nil {
			t.Fatal("translator received no trusted workspace")
		}
		if script != testHPatchScript {
			t.Fatalf("script = %q", script)
		}
		return []byte(testTranslatedPatch), nil
	})
}

func testHPatchItem() map[string]any {
	return map[string]any{
		"type":    "custom_tool_call",
		"id":      "item-H",
		"call_id": "call-H",
		"name":    hpatchToolName,
		"input":   testHPatchScript,
		"status":  "completed",
		"future":  map[string]any{"kept": true},
	}
}

func TestBuildCodeModeCarrierCatalogReadsNamespacedTools(t *testing.T) {
	additional := testCodeModeAdditionalTools(testCodeModeDescription)
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{additional}),
	}
	registry := &toolRegistry{byName: map[string]toolContribution{}}
	catalog, err := buildCodeModeCarrierCatalog(fields, registry)
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog["exec"]; got != codeModeCarrierCustom {
		t.Fatalf("exec carrier kind = %q", got)
	}

	namespaces := additional["tools"].([]any)
	functionsNamespace := namespaces[0].(map[string]any)
	nestedTools := functionsNamespace["tools"].([]any)
	nestedTools[0].(map[string]any)["type"] = "function"
	fields["input"] = mustTestJSON(t, []any{additional})
	catalog, err = buildCodeModeCarrierCatalog(fields, registry)
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog["exec"]; got != codeModeCarrierFunction {
		t.Fatalf("exec carrier kind = %q", got)
	}
}

func TestBuildCodeModeCarrierCatalogReadsFlatTools(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testFlatCodeModeAdditionalTools(testCodeModeDescription)}),
	}
	registry := &toolRegistry{byName: map[string]toolContribution{}}
	catalog, err := buildCodeModeCarrierCatalog(fields, registry)
	if err != nil {
		t.Fatal(err)
	}
	for name, kind := range map[string]codeModeCarrierKind{
		"exec":         codeModeCarrierCustom,
		"wait":         codeModeCarrierFunction,
		"send_message": codeModeCarrierFunction,
	} {
		if got := catalog[name]; got != kind {
			t.Fatalf("%s carrier kind = %q, want %q", name, got, kind)
		}
	}
}

func TestBuildCodeModeCarrierCatalogRejectsDuplicateNames(t *testing.T) {
	additional := testCodeModeAdditionalTools(testCodeModeDescription)
	namespaces := additional["tools"].([]any)
	functionsNamespace := namespaces[0].(map[string]any)
	functionsNamespace["tools"] = append(functionsNamespace["tools"].([]any), map[string]any{
		"type": "custom", "name": "exec", "description": "duplicate",
	})
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{additional}),
	}
	registry := &toolRegistry{byName: map[string]toolContribution{}}
	if _, err := buildCodeModeCarrierCatalog(fields, registry); err == nil || !strings.Contains(err.Error(), "defined more than once") {
		t.Fatalf("duplicate carrier error = %v", err)
	}
}

func TestHPatchPrepareRequestRewritesNamespacedExecWithShell(t *testing.T) {
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input":       []any{testCodeModeAdditionalTools(testCodeModeDescription)},
		"tools":       []any{map[string]any{"type": "function", "name": "lookup", "future": true}},
		"tool_choice": "auto",
	}))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	shell := toolContribution{
		PluginID: "test.shell",
		Name:     "shell",
		Specification: mustMarshalJSON(map[string]any{
			"type": "custom", "name": "shell", "description": "shell base description",
		}),
		Executor: true,
	}
	proxy.registry.ordered = append(proxy.registry.ordered, shell)
	proxy.registry.byName[shell.Name] = shell

	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "session-functions-exec", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	if transform == nil {
		t.Fatal("prepareRequest returned no transform")
	}
	defer transform.Close()

	functionsTools, namespaces := testFunctionsNamespaceTools(t, request.fields)
	execIndex := slices.IndexFunc(functionsTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "exec"
	})
	if execIndex < 0 {
		t.Fatalf("functions namespace lost exec: %#v", functionsTools)
	}
	description := jsonString(functionsTools[execIndex], "description")
	for _, forbidden := range []string{codeModeApplyPatchHeading, codeModeExecCommandHeading, "tools.apply_patch", "tools.exec_command", "exec_command(args:"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("namespaced exec description contains %q: %q", forbidden, description)
		}
	}
	if !strings.Contains(description, "### `create_goal`") {
		t.Fatalf("namespaced exec lost unrelated nested tool: %q", description)
	}
	if !slices.ContainsFunc(functionsTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "wait" && string(tool["future"]) == "true"
	}) {
		t.Fatalf("functions namespace lost sibling tools: %#v", functionsTools)
	}
	if !slices.ContainsFunc(namespaces, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "collaboration" && string(tool["future"]) == "true"
	}) {
		t.Fatalf("additional_tools lost sibling namespace: %#v", namespaces)
	}
	if len(transform.execCommandDefinitions) != 2 {
		t.Fatalf("removed exec_command definitions = %d, want 2", len(transform.execCommandDefinitions))
	}
}

func TestHPatchPrepareRequestExposesOnlyStandaloneHPatch(t *testing.T) {
	transform, _, request, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	if !transform.originalToolsPresent || len(transform.originalTools) == 0 {
		t.Fatal("original top-level tools were not retained")
	}
	var topTools []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["tools"], &topTools); err != nil {
		t.Fatal(err)
	}
	if len(topTools) != 4 || jsonString(topTools[0], "name") != "lookup" || jsonString(topTools[1], "name") != hpatchToolName || jsonString(topTools[2], "name") != "hread" || jsonString(topTools[3], "name") != "hgrep" {
		t.Fatalf("top-level tools = %#v", topTools)
	}
	if jsonString(topTools[1], "type") != "custom" {
		t.Fatalf("standalone hpatch definition = %#v", topTools[1])
	}
	var format struct {
		Type       string `json:"type"`
		Syntax     string `json:"syntax"`
		Definition string `json:"definition"`
	}
	if err := json.Unmarshal(topTools[1]["format"], &format); err != nil {
		t.Fatal(err)
	}
	if format.Type != "grammar" || format.Syntax != "lark" || format.Definition != hpatch.ToolGrammar() {
		t.Fatalf("standalone hpatch format = %#v", topTools[1])
	}
	exposed := jsonString(topTools[1], "description")
	if exposed != testHPatchToolDescription {
		t.Fatalf("standalone hpatch description = %q, want native tool help only", exposed)
	}
	if description := jsonString(topTools[2], "description"); !strings.Contains(description, "Use `hread` as replacement") {
		t.Fatalf("standalone hread description = %q", jsonString(topTools[2], "description"))
	}
	if err := json.Unmarshal(topTools[2]["format"], &format); err != nil {
		t.Fatal(err)
	}
	if format.Type != "grammar" || format.Syntax != "regex" || !strings.HasPrefix(format.Definition, "\\A") || !strings.Contains(format.Definition, "{0,5}") || !strings.HasSuffix(format.Definition, "\\z") {
		t.Fatalf("standalone hread format = %#v", topTools[2])
	}
	if description := jsonString(topTools[3], "description"); !strings.Contains(description, "Use `hgrep` as replacement") {
		t.Fatalf("standalone hgrep description = %q", jsonString(topTools[3], "description"))
	}
	if err := json.Unmarshal(topTools[3]["format"], &format); err != nil {
		t.Fatal(err)
	}
	if format.Type != "grammar" || format.Syntax != "regex" || !strings.HasPrefix(format.Definition, "\\A") || !strings.HasSuffix(format.Definition, "\\z") {
		t.Fatalf("standalone hgrep format = %#v", topTools[3])
	}
	for _, correctionGuidance := range []string{
		"Repairing a rejected script:",
		"INDEX: COMMAND",
		`INDEX.ROW: "VALUE"`,
	} {
		if strings.Contains(exposed, correctionGuidance) {
			t.Fatalf("standalone hpatch description includes rejection-only guidance %q: %q", correctionGuidance, exposed)
		}
	}
	if strings.Contains(exposed, "type <<PATCH replacement or insertion consumes") {
		t.Fatalf("standalone hpatch description retains grammar-enforced correction framing: %q", exposed)
	}
	if strings.Contains(exposed, "workspace_id") {
		t.Fatalf("standalone hpatch description retains workspace selection: %q", exposed)
	}
	if string(topTools[0]["future"]) != "true" {
		t.Fatalf("unrelated top-level tool changed: %#v", topTools[0])
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || jsonString(items[0], "type") != "additional_tools" || string(items[1]["future"]) != "true" {
		t.Fatalf("rewritten input items = %#v", items)
	}
	functionsTools, namespaces := testFunctionsNamespaceTools(t, request.fields)
	if len(namespaces) != 2 || string(namespaces[0]["future"]) != "true" || jsonString(namespaces[1], "name") != "collaboration" {
		t.Fatalf("additional tool namespaces changed unexpectedly: %#v", namespaces)
	}
	execIndex := slices.IndexFunc(functionsTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "exec"
	})
	if execIndex < 0 || string(functionsTools[execIndex]["future"]) != "true" {
		t.Fatalf("functions namespace changed unexpectedly: %#v", functionsTools)
	}
	description := jsonString(functionsTools[execIndex], "description")
	if strings.Contains(description, codeModeApplyPatchHeading) ||
		strings.Contains(description, "tools.apply_patch") ||
		!strings.Contains(description, codeModeExecCommandHeading) ||
		!strings.Contains(description, "tools.exec_command") ||
		!strings.Contains(description, "### `create_goal`") {
		t.Fatalf("native apply_patch was not hidden or native exec_command was not preserved: %q", description)
	}
	if !bytes.Contains(items[0]["future"], []byte(`"kept":true`)) || !bytes.Contains(request.fields["future_request"], []byte(`"kept":true`)) {
		t.Fatalf("future fields were not preserved: %#v", request.fields)
	}
	if string(request.fields["parallel_tool_calls"]) != "true" {
		t.Fatalf("parallel_tool_calls = %s", request.fields["parallel_tool_calls"])
	}
}

func TestHPatchReplacementRemovesExecCommandContractFromNamespacedExec(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}
	installed := append(testInstalledTools(), map[string]json.RawMessage{
		"type":        mustMarshalJSON("custom"),
		"name":        mustMarshalJSON("shell"),
		"description": mustMarshalJSON("shell base description"),
	})
	applyPatchDefinition, execCommandDefinitions, owner, replaced, err := replaceAdditionalToolsApplyPatch(fields, installed)
	if err != nil || !replaced || owner != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", owner, replaced, err)
	}
	if !strings.HasPrefix(applyPatchDefinition, codeModeApplyPatchHeading) || len(execCommandDefinitions) != 2 {
		t.Fatalf("removed definitions = apply %q, exec %q", applyPatchDefinition, execCommandDefinitions)
	}
	if !slices.ContainsFunc(execCommandDefinitions, func(definition string) bool {
		return strings.Contains(definition, codeModeExecCommandHeading)
	}) || !slices.ContainsFunc(execCommandDefinitions, func(definition string) bool {
		return strings.Contains(definition, "tools.exec_command")
	}) {
		t.Fatalf("removed exec definitions = %q", execCommandDefinitions)
	}

	functionsTools, namespaces := testFunctionsNamespaceTools(t, fields)
	if len(namespaces) != 2 || jsonString(namespaces[1], "name") != "collaboration" {
		t.Fatalf("sibling namespace changed: %#v", namespaces)
	}
	execIndex := slices.IndexFunc(functionsTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "exec"
	})
	if execIndex < 0 {
		t.Fatalf("functions namespace lost exec: %#v", functionsTools)
	}
	description := jsonString(functionsTools[execIndex], "description")
	for _, forbidden := range []string{
		codeModeApplyPatchHeading,
		codeModeExecCommandHeading,
		codeModeExecCommandPlainHeading,
		"tools.exec_command",
		"exec_command(args:",
		"declare const tools: { exec_command",
	} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("exec description contains %q: %q", forbidden, description)
		}
	}
	if !strings.Contains(description, "### `create_goal`") {
		t.Fatalf("rewritten exec description lost unrelated tools: %q", description)
	}

	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	shellIndex := slices.IndexFunc(tools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "shell"
	})
	if shellIndex < 0 || jsonString(tools[shellIndex], "description") != "shell base description" {
		t.Fatalf("shell description = %#v", tools)
	}
}

func TestHPatchReplacementRemovesExecCommandContractFromFlatExec(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testFlatCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}
	installed := append(testInstalledTools(), map[string]json.RawMessage{
		"type":        mustMarshalJSON("custom"),
		"name":        mustMarshalJSON("shell"),
		"description": mustMarshalJSON("shell base description"),
	})
	applyPatchDefinition, execCommandDefinitions, owner, replaced, err := replaceAdditionalToolsApplyPatch(fields, installed)
	if err != nil || !replaced || owner != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", owner, replaced, err)
	}
	if !strings.HasPrefix(applyPatchDefinition, codeModeApplyPatchHeading) || len(execCommandDefinitions) != 2 {
		t.Fatalf("removed definitions = apply %q, exec %q", applyPatchDefinition, execCommandDefinitions)
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !bytes.Contains(items[0]["future"], []byte(`"kept":true`)) {
		t.Fatalf("flat additional_tools item changed: %#v", items)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(items[0]["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	execIndex := slices.IndexFunc(tools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "type") == "custom" && jsonString(tool, "name") == "exec"
	})
	if execIndex < 0 {
		t.Fatalf("flat additional_tools lost exec: %#v", tools)
	}
	description := jsonString(tools[execIndex], "description")
	for _, forbidden := range []string{codeModeApplyPatchHeading, codeModeExecCommandHeading, "tools.apply_patch", "tools.exec_command"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("flat exec description contains %q: %q", forbidden, description)
		}
	}
	if !strings.Contains(description, "### `create_goal`") ||
		!slices.ContainsFunc(tools, func(tool map[string]json.RawMessage) bool {
			return jsonString(tool, "name") == "wait" && string(tool["future"]) == "true"
		}) ||
		!slices.ContainsFunc(tools, func(tool map[string]json.RawMessage) bool {
			return jsonString(tool, "name") == "collaboration" && string(tool["future"]) == "true"
		}) {
		t.Fatalf("flat exec rewrite lost sibling content: %#v", tools)
	}
}

func TestHPatchReplacementRejectsUnsupportedAndTopLevelExecCarriers(t *testing.T) {
	flat := func(name string) []any {
		return []any{map[string]any{
			"type": "additional_tools",
			"tools": []any{map[string]any{
				"type": "custom", "name": name, "description": testCodeModeDescription,
			}},
		}}
	}
	tests := []struct {
		name  string
		input []any
		tools []any
	}{
		{name: "flat functions exec", input: flat("functions.exec")},
		{
			name:  "top-level exec",
			input: []any{testCodeModeAdditionalTools(testCodeModeDescription)},
			tools: []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}},
		},
		{
			name:  "top-level functions exec",
			input: []any{testCodeModeAdditionalTools(testCodeModeDescription)},
			tools: []any{map[string]any{"type": "custom", "name": "functions.exec", "description": testCodeModeDescription}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{
				"input": mustTestJSON(t, test.input),
				"tools": mustTestJSON(t, test.tools),
			}
			beforeInput := bytes.Clone(fields["input"])
			beforeTools := bytes.Clone(fields["tools"])
			_, _, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
			if err == nil || replaced {
				t.Fatalf("replacement = %t, error %v", replaced, err)
			}
			if !bytes.Equal(fields["input"], beforeInput) || !bytes.Equal(fields["tools"], beforeTools) {
				t.Fatalf("rejected request mutated: %#v", fields)
			}
		})
	}
}

func TestStripCodeModeExecCommandSectionAcceptsAppAndCLISchemas(t *testing.T) {
	descriptions := map[string]string{
		"app": "Run JavaScript.\n\n### `exec_command` \t\nRun a command.\n\nexec tool declaration:\n```ts\ndeclare const tools: { exec_command(args: { cmd: string; workdir?: string; tty?: boolean }): Promise<{ output: string }>; };\n```\n\n### `create_goal`\nKeep this.",
		"cli": "Run JavaScript.\n\n### exec_command\t \nRun a command in a PTY.\n\nParameters:\n- `cmd`: required command text.\n- `workdir`: optional working directory.\n- `yield_time_ms`: optional initial wait.\n\n### `create_goal`\nKeep this.",
	}
	for name, baseDescription := range descriptions {
		for lineEndingName, lineEnding := range map[string]string{"lf": "\n", "crlf": "\r\n"} {
			t.Run(name+"/"+lineEndingName, func(t *testing.T) {
				description := strings.ReplaceAll(baseDescription, "\n", lineEnding)
				stripped, section, found, err := stripCodeModeExecCommandSection(description)
				if err != nil || !found {
					t.Fatalf("found = %t, error %v", found, err)
				}
				want := strings.ReplaceAll("Run JavaScript.\n\n### `create_goal`\nKeep this.", "\n", lineEnding)
				if !strings.Contains(section, "exec_command") || stripped != want {
					t.Fatalf("section = %q, stripped = %q, want %q", section, stripped, want)
				}
			})
		}
	}
}

func TestStripCodeModeExecCommandContractRejectsUnownedReference(t *testing.T) {
	description := "Run JavaScript with `tools.exec_command(...)`.\n\n### `create_goal`\nKeep this."
	if _, _, _, err := stripCodeModeExecCommandContract(description); err == nil {
		t.Fatal("unowned exec_command reference was accepted")
	}

	description = "Run JavaScript.\n\n### `create_goal`\nKeep this."
	stripped, definitions, found, err := stripCodeModeExecCommandContract(description)
	if err != nil || found || len(definitions) != 0 || stripped != description {
		t.Fatalf("absent contract = stripped %q, definitions %q, found %t, error %v", stripped, definitions, found, err)
	}
}

func TestHPatchDirectAdditionalApplyPatchIsRejectedWithoutExecCarrier(t *testing.T) {
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"role": "developer",
				"tools": []any{
					map[string]any{"type": "custom", "name": "shell", "future": true},
					map[string]any{"type": "custom", "name": applyPatchToolName, "description": "Apply a patch.", "future": map[string]any{"kept": true}},
				},
				"future": map[string]any{"kept": true},
			},
			map[string]any{"role": "user", "content": "task"},
		},
		"tools":       []any{map[string]any{"type": "function", "name": "lookup"}},
		"tool_choice": "auto",
	}))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Error(err)
		}
	})
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "session-direct", metadata, true)
	if err == nil || transform != nil || !strings.Contains(err.Error(), "unsupported flat apply_patch") {
		t.Fatalf("direct rewrite = transform %v, error %v", transform, err)
	}
	if len(proxy.sessions) != 0 {
		t.Fatalf("direct rejection created session resources: %#v", proxy.sessions)
	}
}

func TestHPatchAdditionalToolsReplacementRejectsDuplicateAndConflictingOwners(t *testing.T) {
	execTool := func() map[string]any {
		return map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}
	}
	additional := func(tools ...any) map[string]any {
		return map[string]any{
			"type": "additional_tools",
			"role": "developer",
			"tools": []any{map[string]any{
				"type": "namespace", "name": "functions", "tools": tools,
			}},
		}
	}
	tests := []struct {
		name  string
		input []any
		tools []any
	}{
		{
			name:  "duplicate additional items",
			input: []any{additional(execTool()), additional(execTool())},
		},
		{
			name:  "duplicate exec tools",
			input: []any{additional(execTool(), execTool())},
		},
		{
			name:  "flat and namespaced exec",
			input: []any{testFlatCodeModeAdditionalTools(testCodeModeDescription), additional(execTool())},
		},
		{
			name:  "standalone native collision",
			input: []any{additional(execTool())},
			tools: []any{map[string]any{"type": "custom", "name": applyPatchToolName}},
		},
		{
			name:  "existing hpatch collision",
			input: []any{additional(execTool())},
			tools: []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		},
		{
			name:  "direct namespaced apply_patch collision",
			input: []any{additional(execTool(), map[string]any{"type": "custom", "name": applyPatchToolName})},
		},
		{
			name:  "namespaced hpatch collision",
			input: []any{additional(execTool(), map[string]any{"type": "custom", "name": hpatchToolName})},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{
				"input": mustTestJSON(t, test.input),
				"tools": mustTestJSON(t, test.tools),
			}
			beforeInput := bytes.Clone(fields["input"])
			beforeTools := bytes.Clone(fields["tools"])
			_, _, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
			if err == nil || replaced {
				t.Fatalf("replacement = %v, error %v", replaced, err)
			}
			if !bytes.Equal(fields["input"], beforeInput) || !bytes.Equal(fields["tools"], beforeTools) {
				t.Fatalf("rejected request mutated: %#v", fields)
			}
		})
	}
}

func TestHPatchAdditionalToolsReplacementPreservesExecCommandWithoutShell(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}

	_, removedExecCommandDefinitions, owner, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
	if err != nil || !replaced || owner != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", owner, replaced, err)
	}
	if len(removedExecCommandDefinitions) != 0 {
		t.Fatalf("removed exec_command definitions without shell = %q", removedExecCommandDefinitions)
	}

	functionsTools, _ := testFunctionsNamespaceTools(t, fields)
	execIndex := slices.IndexFunc(functionsTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "exec"
	})
	if execIndex < 0 {
		t.Fatalf("functions namespace lost exec: %#v", functionsTools)
	}
	description := jsonString(functionsTools[execIndex], "description")
	if strings.Contains(description, codeModeApplyPatchHeading) ||
		!strings.Contains(description, codeModeExecCommandHeading) ||
		!strings.Contains(description, "tools.exec_command") ||
		!strings.Contains(description, "### `create_goal`") {
		t.Fatalf("rewritten carrier description = %q", description)
	}
}

func TestHPatchAdditionalToolsReplacementLeavesUnsupportedAndMalformedRequestsUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		input      json.RawMessage
		tools      json.RawMessage
		toolChoice json.RawMessage
	}{
		{
			name:  "malformed additional tools",
			input: json.RawMessage(`[{"type":"additional_tools","tools":{}}]`),
			tools: mustTestJSON(t, []any{map[string]any{"type": "function", "name": "lookup"}}),
		},
		{
			name:  "unrelated heading collision",
			input: mustTestJSON(t, []any{testCodeModeAdditionalTools("### `apply_patch`\ndocumentation only")}),
			tools: mustTestJSON(t, []any{}),
		},
		{
			name:       "restricted exec choice",
			input:      mustTestJSON(t, []any{testCodeModeAdditionalTools(testCodeModeDescription)}),
			tools:      mustTestJSON(t, []any{}),
			toolChoice: mustTestJSON(t, map[string]any{"type": "custom", "name": "exec"}),
		},
		{
			name:  "unknown future input shape",
			input: json.RawMessage(`{"future":true}`),
			tools: mustTestJSON(t, []any{}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]json.RawMessage{"input": bytes.Clone(test.input), "tools": bytes.Clone(test.tools)}
			if test.toolChoice != nil {
				fields["tool_choice"] = bytes.Clone(test.toolChoice)
			}
			beforeInput := bytes.Clone(fields["input"])
			beforeTools := bytes.Clone(fields["tools"])
			beforeChoice := bytes.Clone(fields["tool_choice"])
			_, _, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
			if err != nil || replaced {
				t.Fatalf("replacement = %v, error %v", replaced, err)
			}
			if !bytes.Equal(fields["input"], beforeInput) || !bytes.Equal(fields["tools"], beforeTools) || !bytes.Equal(fields["tool_choice"], beforeChoice) {
				t.Fatalf("unsupported request mutated: %#v", fields)
			}
		})
	}
}

func TestHPatchReplacementRetainsNamespacedExecOwnerName(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}
	_, _, got, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
	if err != nil || !replaced || got != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", got, replaced, err)
	}
}

func TestHPatchPrepareRequestLeavesIneligibleRequestUnchanged(t *testing.T) {
	workspace := t.TempDir()
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{map[string]any{
			"type":  "additional_tools",
			"tools": []any{map[string]any{"type": "custom", "name": "exec", "description": testCodeModeDescription}},
		}},
		"tools": []any{map[string]any{"type": "function", "name": "lookup"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	beforeInput := bytes.Clone(request.fields["input"])
	beforeTools := bytes.Clone(request.fields["tools"])
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	transform, err := proxy.prepareRequest(t.Context(), &request, "", metadata, true)
	if err == nil || transform != nil || !strings.Contains(err.Error(), "valid session ID") || !bytes.Equal(beforeInput, request.fields["input"]) || !bytes.Equal(beforeTools, request.fields["tools"]) {
		t.Fatalf("ineligible request = transform %v, error %v, fields %#v", transform, err, request.fields)
	}
}

func TestHPatchIneligibleContinuationDoesNotRestoreHistory(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": {script: testHPatchScript, patch: testTranslatedPatch}}); err != nil {
		t.Fatal(err)
	}
	request, err := parseResponsesRequest([]byte(`{"input":[{"type":"custom_tool_call","name":"apply_patch","call_id":"call-H","input":` + jsonQuoted(testTranslatedPatch) + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(request.fields["input"])
	transform, err := proxy.prepareRequest(t.Context(), &request, "session", codexTurnMetadata{}, false)
	if err == nil || transform != nil || !strings.Contains(err.Error(), "valid turn metadata") || !bytes.Equal(before, request.fields["input"]) {
		t.Fatalf("ineligible continuation = transform %v, error %v, input %s", transform, err, request.fields["input"])
	}
}

func TestHPatchJSONWrapsPatchAndImmediateReportInCodeModeExec(t *testing.T) {
	calls := 0
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, &calls))
	originalItem := mustTestJSON(t, testHPatchItem())
	payload := mustTestJSON(t, map[string]any{
		"status":      "completed",
		"output":      []any{json.RawMessage(originalItem), map[string]any{"type": "message", "future": true}},
		"tools":       []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		"tool_choice": map[string]any{"type": "custom", "name": hpatchToolName},
		"future":      map[string]any{"kept": true},
	})
	visible, err := transform.TransformJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("translations = %d", calls)
	}
	var response struct {
		Output     []json.RawMessage `json:"output"`
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tools) != 1 || !bytes.Contains(response.Tools[0], []byte(`"name":"lookup"`)) || string(response.ToolChoice) != `"auto"` {
		t.Fatalf("restored response contract = %s", visible)
	}
	var carrier struct {
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Input  string          `json:"input"`
		Future json.RawMessage `json:"future"`
	}
	if err := json.Unmarshal(response.Output[0], &carrier); err != nil {
		t.Fatal(err)
	}
	wantInput := hpatchApplyExecInput(testTranslatedPatch, testHPatchReport)
	if carrier.CallID != "call-H" || carrier.Name != "exec" || carrier.Input != wantInput || string(carrier.Future) != `{"kept":true}` {
		t.Fatalf("translated call = %s", response.Output[0])
	}

	replay, err := parseResponsesRequest([]byte(`{"input":[` + string(response.Output[0]) + `,{"type":"custom_tool_call_output","call_id":"call-H","output":` + jsonQuoted(testHPatchReport) + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.restoreInputPrefix(&replay, transform.historySessionID); err != nil {
		t.Fatal(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &items); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(items[0], originalItem) {
		t.Fatalf("reconstructed prefix item = %s, want %s", items[0], originalItem)
	}
	if !bytes.Contains(items[1], []byte(`"output":`+jsonQuoted(testHPatchReport))) {
		t.Fatalf("immediate hpatch report changed during replay: %s", items[1])
	}
}

func TestHReadJSONReturnsExecCommandAndRestoresReplay(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	readItem := map[string]any{
		"type": "custom_tool_call", "id": "item-R", "call_id": "call-R",
		"name": "hread", "input": `"lines.txt" 2:3`, "status": "completed",
	}
	missingItem := map[string]any{
		"type": "custom_tool_call", "id": "item-M", "call_id": "call-M",
		"name": "hread", "input": `"missing.txt"`, "status": "completed",
	}
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{readItem, missingItem},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 ||
		jsonString(response.Output[0], "name") != "exec" ||
		jsonString(response.Output[0], "input") != registeredWorkerInput(t, proxy, "hread", []string{`"lines.txt" 2:3`}) {
		t.Fatalf("translated hread response = %s", visible)
	}
	if missing := jsonString(response.Output[1], "input"); missing != registeredWorkerInput(t, proxy, "hread", []string{`"missing.txt"`}) {
		t.Fatalf("translated missing-file response = %q", missing)
	}
	carrierInput := jsonString(response.Output[0], "input")
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")
	var arguments struct {
		Command     string          `json:"cmd"`
		Environment json.RawMessage `json:"env"`
		Workdir     json.RawMessage `json:"workdir"`
		Shell       json.RawMessage `json:"shell"`
		Login       *bool           `json:"login"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	wantCommand := `hread '"lines.txt" 2:3'`
	if arguments.Command != wantCommand {
		t.Fatalf("translated exec command = %q, want %q", arguments.Command, wantCommand)
	}
	if len(arguments.Shell) != 0 || arguments.Login == nil || *arguments.Login {
		t.Fatalf("translated exec shell = %s, login = %v", arguments.Shell, arguments.Login)
	}
	if len(arguments.Environment) != 0 || len(arguments.Workdir) != 0 {
		t.Fatalf("translated hread overrides Codex environment or working directory: %s", carrierInput)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{response.Output[0]},
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
	if len(replayed) != 1 || jsonString(replayed[0], "name") != "hread" || jsonString(replayed[0], "input") != `"lines.txt" 2:3` {
		t.Fatalf("replayed hread = %s", replay.fields["input"])
	}
}

func TestHGrepJSONReturnsExecCommandAndRestoresReplay(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	searchItem := map[string]any{
		"type": "custom_tool_call", "id": "item-G", "call_id": "call-G",
		"name": "hgrep", "input": "-F needle internal/router\n", "status": "completed",
	}
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{searchItem},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || jsonString(response.Output[0], "name") != "exec" ||
		jsonString(response.Output[0], "input") != registeredWorkerInput(t, proxy, "hgrep", []string{"-F", "needle", "internal/router"}) {
		t.Fatalf("translated hgrep response = %s", visible)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{response.Output[0]},
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
	if len(replayed) != 1 || jsonString(replayed[0], "name") != "hgrep" ||
		jsonString(replayed[0], "input") != "-F needle internal/router\n" {
		t.Fatalf("replayed hgrep = %s", replay.fields["input"])
	}
}

func TestHGrepExecInputUsesStableBasename(t *testing.T) {
	carrierInput, err := workerExecInputWithParams("hgrep", []string{"-n", "-A", "80", "-B", "20"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")

	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	if arguments.Command != "hgrep -n -A 80 -B 20" {
		t.Fatalf("translated exec command = %q", arguments.Command)
	}
}

func TestWorkerTemplateExecInputQuotesNestedShellCommand(t *testing.T) {
	carrierInput, err := workerTemplateExecInputWithParams(
		"shell",
		[]string{"python3", `print('{"hello":"world"}')`},
		"curl -fsSL URL | {.} | jq",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")

	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	want := `curl -fsSL URL | shell python3 'print('"'"'{"hello":"world"}'"'"')' | jq`
	if arguments.Command != want {
		t.Fatalf("translated template command = %q, want %q", arguments.Command, want)
	}

	for _, template := range []string{"missing", "{.} then {.}"} {
		if _, err := workerTemplateExecInputWithParams("shell", []string{"bash", ""}, template, nil); err == nil {
			t.Fatalf("worker template %q did not reject", template)
		}
	}
}

func TestWorkerExecInputMergesValidatedParams(t *testing.T) {
	carrierInput, err := workerExecInputWithParams("shell", []string{"bash", "printf ok"}, map[string]json.RawMessage{
		"workdir": mustMarshalJSON("/tmp/example"),
		"tty":     mustMarshalJSON(true),
		"login":   mustMarshalJSON(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")
	var arguments struct {
		Command string `json:"cmd"`
		Workdir string `json:"workdir"`
		TTY     bool   `json:"tty"`
		Login   bool   `json:"login"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	if arguments.Command != "shell bash 'printf ok'" || arguments.Workdir != "/tmp/example" ||
		!arguments.TTY || arguments.Login {
		t.Fatalf("translated exec arguments = %+v", arguments)
	}
	if _, err := workerExecInputWithParams("shell", []string{"bash", ""}, map[string]json.RawMessage{
		"cmd": mustMarshalJSON("forbidden"),
	}); err == nil {
		t.Fatal("exec params accepted cmd")
	}
	if _, err := workerExecInputWithParams("shell", []string{"bash", ""}, map[string]json.RawMessage{
		"login": mustMarshalJSON(true),
	}); err == nil {
		t.Fatal("exec params accepted login true")
	}
}

func TestHReadExecInputQuotesOnlyShellSensitiveArguments(t *testing.T) {
	carrierInput, err := workerExecInputWithParams("hread", []string{`"line's $(echo injected).txt" 2:3`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")

	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	want := `hread '"line'"'"'s $(echo injected).txt" 2:3'`
	if arguments.Command != want {
		t.Fatalf("translated exec command = %q, want %q", arguments.Command, want)
	}
}

func TestHReadExecInputCarriesOneNewlineDelimitedBatchArgument(t *testing.T) {
	input := "\"alpha.txt\"\n\"beta.txt\" 2:3"
	carrierInput, err := workerExecInputWithParams("hread", []string{input}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")

	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	want := "hread '\"alpha.txt\"\n\"beta.txt\" 2:3'"
	if arguments.Command != want {
		t.Fatalf("translated batch command = %q, want %q", arguments.Command, want)
	}
}

func TestHReadExecInputDoesNotRepairMissingRangeSeparator(t *testing.T) {
	carrierInput, err := workerExecInputWithParams("hread", []string{`"file.txt"2:3`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodedArguments := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	encodedArguments = strings.TrimSuffix(encodedArguments, ");\ntext(result.output);")

	var arguments struct {
		Command string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(encodedArguments), &arguments); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
	want := `hread '"file.txt"2:3'`
	if arguments.Command != want {
		t.Fatalf("translated malformed exec command = %q, want unchanged grammar input %q", arguments.Command, want)
	}
}

func TestHPatchHistoryDoesNotCrossWorkspacesSharingSessionIdentity(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	requestFor := func(t *testing.T, extraItems ...any) parsedResponsesRequest {
		t.Helper()
		items := []any{testCodeModeAdditionalTools(testCodeModeDescription)}
		items = append(items, extraItems...)
		request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
			"input":               items,
			"tools":               []any{},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return request
	}
	metadataFor := func(workspace string) codexTurnMetadata {
		return codexTurnMetadata{
			RequestKind: "turn",
			Workspaces:  map[string]json.RawMessage{workspace: nil},
		}
	}

	firstWorkspace := t.TempDir()
	firstRequest := requestFor(t)
	first, err := proxy.prepareRequest(t.Context(), &firstRequest, "shared-cache-key", metadataFor(firstWorkspace), true)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := first.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{testHPatchItem()},
	}))
	first.Close()
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 {
		t.Fatalf("translated output = %s", visible)
	}

	secondWorkspace := t.TempDir()
	secondRequest := requestFor(t, response.Output[0])
	second, err := proxy.prepareRequest(t.Context(), &secondRequest, "shared-cache-key", metadataFor(secondWorkspace), true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(secondRequest.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 {
		t.Fatalf("replayed input = %s", secondRequest.fields["input"])
	}
	if name := jsonString(replayed[1], "name"); name != "exec" {
		t.Fatalf("cross-workspace replay restored tool name %q", name)
	}
	if input := jsonString(replayed[1], "input"); input != hpatchApplyExecInput(testTranslatedPatch, testHPatchReport) {
		t.Fatalf("cross-workspace replay restored input %q", input)
	}

	history, err := second.translate("call-correction", "1: accept\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.translationError, "no hpatch call to correct") {
		t.Fatalf("cross-workspace correction history = %+v", history)
	}
}

func TestHPatchReplayPreservesImmediateApplyFailure(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	history := hpatchHistory{script: testHPatchScript, patch: testTranslatedPatch, carrierName: "exec", report: testHPatchReport}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": history}); err != nil {
		t.Fatal(err)
	}
	const applyFailure = "Failed to find expected lines in created.txt:\nmissing\n"
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{
		map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call-H", "input": history.carrierInput()},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call-H", "output": applyFailure},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.restoreInputPrefix(&request, "session"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(request.fields["input"], []byte(jsonQuoted(applyFailure))) || bytes.Contains(request.fields["input"], []byte(jsonQuoted(testHPatchReport))) {
		t.Fatalf("apply failure changed during replay: %s", request.fields["input"])
	}
}

func TestHPatchReplayRejectsChangedExecCarrierAndIgnoresUnrelatedCalls(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	history := hpatchHistory{script: testHPatchScript, patch: testTranslatedPatch, carrierName: "exec", report: testHPatchReport}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-H": history}); err != nil {
		t.Fatal(err)
	}
	changed, _ := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "call-H", "input": "changed",
	}}}))
	if err := proxy.restoreInputPrefix(&changed, "session"); err == nil {
		t.Fatal("changed replay was accepted")
	}
	unrelated, _ := parseResponsesRequest([]byte(`{"input":[{"type":"custom_tool_call","name":"exec","call_id":"native","input":"unchanged"}]}`))
	before := bytes.Clone(unrelated.fields["input"])
	if err := proxy.restoreInputPrefix(&unrelated, "session"); err != nil || !bytes.Equal(before, unrelated.fields["input"]) {
		t.Fatalf("unrelated call changed to %s, error %v", unrelated.fields["input"], err)
	}
}

func TestHPatchReportSeparatesHookWarning(t *testing.T) {
	if got := hpatchReport("in file.txt 1:1", "hpatch: warning: hook failed\n"); got != "in file.txt 1:1\nhpatch: warning: hook failed\n" {
		t.Fatalf("hpatchReport() = %q", got)
	}
}

func TestHPatchExecInputQuotesPatchReportAndDiagnostic(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: quoted.txt\n+` ${value} \\\"\n*** End Patch\n"
	report := "in quoted.txt 1:14\n1 ` ${value} \\\"\n"
	input := hpatchApplyExecInput(patch, report)
	if !strings.HasPrefix(input, hpatchApplyExecMarker) || !strings.Contains(input, strconv.Quote(patch)) || !strings.Contains(input, strconv.Quote(report)) {
		t.Fatalf("unsafe or incomplete apply wrapper: %q", input)
	}
	diagnostic := "selector `x` rejected: ${value} " + string([]byte{'\\'})
	if got := hpatchDiagnosticExecInput(diagnostic); got != "text("+strconv.Quote(diagnostic)+");" {
		t.Fatalf("diagnostic wrapper = %q", got)
	}
}

func TestHPatchStreamingTerminalFinalizesRequestAccounting(t *testing.T) {
	records := 0
	transform, _, _, _ := newHPatchTestTransform(t, metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("terminal response without an hpatch call reached translation")
			return nil, nil
		},
		record: func(context.Context, hpatchMetricRecord) error {
			records++
			return nil
		},
	})
	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": []any{}},
	})
	visible, err := transform.TransformSSE(completed)
	if err != nil || len(visible) != 1 || records != 1 {
		t.Fatalf("completed = %q, metric records %d, error %v", visible, records, err)
	}
	if err := transform.Finish(true); err != nil || records != 1 {
		t.Fatalf("repeated finish = metric records %d, error %v", records, err)
	}
}

func TestWriteSSEEventPreservesTerminalStateWhenHPatchFinishIsCanceled(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, metricsObservingTranslator{
		translate: func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("terminal response without an hpatch call reached translation")
			return nil, nil
		},
		record: func(ctx context.Context, _ hpatchMetricRecord) error {
			return ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transform.ctx = ctx
	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": []any{}},
	})
	var output bytes.Buffer
	state, err := writeSSEEvent(
		&output,
		[]string{"event: response.completed\n", "data: " + string(completed) + "\n"},
		"\n",
		transform,
		nil,
	)
	if state != responseTerminalCompleted || !errors.Is(err, context.Canceled) {
		t.Fatalf("terminal state = %v, error %v", state, err)
	}
	if output.Len() != 0 {
		t.Fatalf("canceled terminal event was written: %q", output.String())
	}
}

func TestHPatchStreamingReplacesLifecycleWithoutChangingCallID(t *testing.T) {
	calls := 0
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, &calls))
	item := testHPatchItem()
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""

	visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
	if err != nil || visible != nil {
		t.Fatalf("buffered added = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.delta", "item_id": "item-H", "delta": "secret"}))
	if err != nil || visible != nil {
		t.Fatalf("delta = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript}))
	if err != nil || len(visible) != 2 || !bytes.Contains(visible[0], []byte(`"name":"exec"`)) || !bytes.Contains(visible[0], []byte(`"call_id":"call-H"`)) || !bytes.Contains(visible[1], []byte(jsonQuoted(hpatchApplyExecInput(testTranslatedPatch, testHPatchReport)))) {
		t.Fatalf("input.done = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
	if err != nil || len(visible) != 1 || !bytes.Contains(visible[0], []byte(`"call_id":"call-H"`)) {
		t.Fatalf("item.done = %q, error %v", visible, err)
	}
	completed := map[string]any{"status": "completed", "output": []any{item}}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.completed", "response": completed}))
	if err != nil || len(visible) != 1 || calls != 1 {
		t.Fatalf("completed = %q, translations %d, error %v", visible, calls, err)
	}
}

func TestHReadStreamingUsesTextLifecycle(t *testing.T) {
	transform, _, _, workspace := newHPatchTestTransform(t, testTranslator(t, new(int)))
	if err := os.WriteFile(filepath.Join(workspace, "line.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := map[string]any{
		"type": "custom_tool_call", "id": "item-R", "call_id": "call-R",
		"name": "hread", "input": `"line.txt"`, "status": "completed",
	}
	added := maps.Clone(item)
	added["status"] = "in_progress"
	added["input"] = ""

	visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
	if err != nil || visible != nil {
		t.Fatalf("buffered hread added = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{
		"type": "response.custom_tool_call_input.done", "item_id": "item-R", "input": `"line.txt"`,
	}))
	if err != nil || len(visible) != 2 ||
		!bytes.Contains(visible[0], []byte(`"name":"exec"`)) ||
		!bytes.Contains(visible[1], []byte(jsonQuoted(registeredWorkerInput(t, transform.proxy, "hread", []string{`"line.txt"`})))) {
		t.Fatalf("hread input.done = %q, error %v", visible, err)
	}
	visible, err = transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item}))
	if err != nil || len(visible) != 1 || !bytes.Contains(visible[0], []byte(`"call_id":"call-R"`)) {
		t.Fatalf("hread item.done = %q, error %v", visible, err)
	}
}

func TestReadOnlyHistoryIsExcludedFromCorrections(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-H": {
			toolName: hpatchToolName, script: testHPatchScript,
			translationError: "rejected", sequence: 1,
		},
		"call-R": {
			toolName: "hread", script: `"file.txt"`,
			report: "8ed3: alpha\n", sequence: 2,
		},
		"call-G": {
			toolName: "hgrep", script: `alpha .`,
			sequence: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := proxy.correctableHistory("session")
	if err != nil {
		t.Fatal(err)
	}
	if history.toolName != hpatchToolName || history.script != testHPatchScript {
		t.Fatalf("correctable history = %+v", history)
	}

	transform := &hpatchResponseTransform{
		proxy:            proxy,
		sessionID:        "session",
		historySessionID: "session",

		local: map[string]hpatchHistory{
			"call-local-read": {
				toolName: "hgrep",
				script:   `alpha .`,
				sequence: 1,
			},
		},
	}
	history, err = transform.correctionHistory()
	if err != nil {
		t.Fatal(err)
	}
	if history.toolName != hpatchToolName || history.script != testHPatchScript {
		t.Fatalf("correction after local read-only call = %+v", history)
	}
}

func TestHPatchCorrectionRetainsCorrelationAndIncrementsAttempt(t *testing.T) {
	calls := 0
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		return []byte(testTranslatedPatch), nil
	}))
	first, err := transform.translate("call-1", testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.correlationID != "call-1" || first.attempt != 1 {
		t.Fatalf("first attempt metadata = %+v", first)
	}
	second, err := transform.translate("call-2", "2: type \"changed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.translationError != "" || second.correlationID != "call-1" || second.attempt != 2 {
		t.Fatalf("correction metadata = %+v", second)
	}
	if _, ok := proxy.history(transform.historySessionID, "call-1"); ok {
		t.Fatal("local history committed before response completion")
	}
}

func TestHPatchCorrectionInstructionsAppearOnlyOnInitialRejection(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("rejected")
	}))

	first, err := transform.translate("call-1", testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(first.translationError, "Repairing a rejected script:"); got != 1 {
		t.Fatalf("initial rejection correction instruction count = %d, want 1:\n%s", got, first.translationError)
	}
	if !strings.Contains(first.translationError, `INDEX.ROW: "VALUE"`) {
		t.Fatalf("initial rejection lacks multiline correction instructions:\n%s", first.translationError)
	}

	second, err := transform.translate("call-2", "2: type \"changed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.correlationID != "call-1" || second.attempt != 2 {
		t.Fatalf("correction rejection metadata = %+v", second)
	}
	if strings.Contains(second.translationError, "Repairing a rejected script:") {
		t.Fatalf("correction rejection repeats correction instructions:\n%s", second.translationError)
	}
}

func TestHPatchDisplayedCorrectionCanBeAcceptedWithoutRepeatingSource(t *testing.T) {
	base := "in script.sh\ntype 1:a793..2:1636 \"exit \\\"$status\\\"\\n\"\n"
	correctedCommand := "type 1:a793..2:1636 \"\\texit \\\"$status\\\"\\n\""
	calls := 0
	var evaluated string
	translator := hpatchResultTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			diagnostic := "hpatch: command 2 rejected: indentation-only change to preserved text\n" +
				"1:2a44 exit \"$status\"\n" +
				"indentation: proposed=\"\" correction=\"\\t\"\n"
			return hpatchTranslationResult{
				diagnostic:  diagnostic,
				corrections: map[int]string{2: correctedCommand},
			}, errors.New("indentation-only change")
		}
		evaluated = script
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)

	first, err := transform.translate("call-1", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(first.translationError, "1:2a44 exit \"$status\"\n") != 1 {
		t.Fatalf("first diagnostic repeats proposed source:\n%s", first.translationError)
	}
	if strings.Count(first.translationError, "Repairing a rejected script:") != 1 {
		t.Fatalf("first diagnostic does not contain one correction protocol:\n%s", first.translationError)
	}
	if !strings.Contains(first.translationError, "Apply the displayed correction with:\n2: accept\n") {
		t.Fatalf("first diagnostic lacks acceptance command:\n%s", first.translationError)
	}

	second, err := transform.translate("call-2", "2: accept\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.translationError != "" || calls != 2 {
		t.Fatalf("accepted correction = %+v, translations %d", second, calls)
	}
	want := "in script.sh\n" + correctedCommand + "\n"
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchCompactCorrectionRebuildsScriptBeforeTranslation(t *testing.T) {
	base := "new file.txt\ntype \"old\"\nrm\n"
	payload := "-2\n+2: type <<PATCH\nnew\nPATCH\n2+: rm\n"
	want := "new file.txt\ntype <<PATCH\nnew\nPATCH\nrm\nrm\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		evaluated = script
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	result, err := transform.translate("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.translationError != "" || calls != 2 {
		t.Fatalf("correction result = %+v, calls %d", result, calls)
	}
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchMultilineValueCorrectionRebuildsOnlyAddressedRow(t *testing.T) {
	base := "new file.go\ntype <<PATCH\npackage p\nvar =\nvar tail = 2\nPATCH\n"
	want := "new file.go\ntype <<PATCH\npackage p\nvar fixed = 1\nvar tail = 2\nPATCH\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		evaluated = script
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	result, err := transform.translate("call-2", "2.2: \"var fixed = 1\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.translationError != "" || calls != 2 {
		t.Fatalf("correction result = %+v, calls %d", result, calls)
	}
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchMultilineValueCorrectionRejectsComposedDelimiterBeforeTranslation(t *testing.T) {
	base := "new file.txt\ntype <<PATCH\nold\nPATCH\n"
	calls := 0
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, _ string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	result, err := transform.translate("call-2", "+2.1: \"PA\"\n+2.1: \"TCH\\n\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.unevaluated || !strings.Contains(result.translationError, "cannot materialize the fixed PATCH delimiter") || calls != 1 {
		t.Fatalf("composed delimiter correction = %+v, translations %d", result, calls)
	}
}

func TestHPatchMalformedDeletionPreservesCorrectableHistory(t *testing.T) {
	base := "new file.txt\ntype \"old\"\nrm\n"
	want := "new file.txt\ntype \"fixed\"\nrm\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(_ context.Context, _ routingWorkspace, script string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("rejected")
		}
		evaluated = script
		return []byte(testTranslatedPatch), nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	malformed, err := transform.translate("call-2", "-2: rm\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !malformed.unevaluated || malformed.correlationID != "call-1" || malformed.attempt != 2 ||
		!strings.Contains(malformed.translationError, "is not `INDEX: COMMAND`") || calls != 1 {
		t.Fatalf("malformed correction = %+v, calls %d", malformed, calls)
	}
	corrected, err := transform.translate("call-3", "2: type \"fixed\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.translationError != "" || corrected.correlationID != "call-1" || corrected.attempt != 3 || calls != 2 {
		t.Fatalf("corrected result = %+v, calls %d", corrected, calls)
	}
	if evaluated != want {
		t.Fatalf("evaluated script = %q, want %q", evaluated, want)
	}
}

func TestHPatchRetainedProxyRejectionAdvancesCorrectionAttempt(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-1": {
			toolName: hpatchToolName, script: testHPatchScript, translationError: "rejected",
			correlationID: "call-1", attempt: 1, sequence: 1,
		},
		"call-2": {
			toolName: hpatchToolName, script: "-2: rm\n", translationError: "malformed", unevaluated: true,
			correlationID: "call-1", attempt: 2, sequence: 2,
		},
	}); err != nil {
		t.Fatal(err)
	}
	base, err := proxy.correctableHistory("session")
	if err != nil {
		t.Fatal(err)
	}
	if base.attempt != 1 {
		t.Fatalf("correctable base attempt = %d, want 1", base.attempt)
	}
	if got := proxy.latestCorrectionAttempt("session", "call-1"); got != 2 {
		t.Fatalf("latest retained chain attempt = %d, want 2", got)
	}
}

func TestHPatchTranslationFailureReturnsImmediateDiagnosticExec(t *testing.T) {
	transform, proxy, _, workspace := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("selector is not unique")
	}))
	originalItem := mustTestJSON(t, testHPatchItem())
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{"type": "message", "future": "preserved", "metadata": map[string]any{"name": "apply_patch"}},
			json.RawMessage(originalItem),
		},
		"future": map[string]any{"kept": true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status string                       `json:"status"`
		Error  json.RawMessage              `json:"error"`
		Output []map[string]json.RawMessage `json:"output"`
		Future json.RawMessage              `json:"future"`
	}
	if err := json.Unmarshal(visible, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "completed" || len(response.Error) != 0 || len(response.Output) != 2 {
		t.Fatalf("translation rejection carrier = %s", visible)
	}
	carrier := response.Output[1]
	history, remembered := proxy.history(transform.historySessionID, "call-H")
	if !remembered {
		t.Fatal("translation rejection carrier was not remembered")
	}
	if jsonString(carrier, "name") != "exec" || jsonString(carrier, "input") != history.carrierInput() || jsonString(carrier, "call_id") != "call-H" || !strings.Contains(history.carrierInput(), "selector is not unique") {
		t.Fatalf("diagnostic exec carrier = %s", visible)
	}
	if jsonString(response.Output[0], "future") != "preserved" || string(response.Future) != `{"kept":true}` || !bytes.Contains(response.Output[0]["metadata"], []byte(`"name":"apply_patch"`)) {
		t.Fatalf("unrelated response data changed: %s", visible)
	}

	replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{
			testCodeModeAdditionalTools(testCodeModeDescription),
			carrier,
			map[string]any{"type": "custom_tool_call_output", "call_id": "call-H", "output": history.translationError, "future": true},
			map[string]any{"type": "custom_tool_call_output", "call_id": "other", "output": "keep"},
		},
		"tools": []any{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	metadata := codexTurnMetadata{RequestKind: "turn", Workspaces: map[string]json.RawMessage{workspace: nil}}
	continuation, err := proxy.prepareRequest(t.Context(), &replay, "session-1", metadata, true)
	if err != nil || continuation == nil {
		t.Fatalf("prepare rejection continuation = transform %v, error %v", continuation, err)
	}
	defer continuation.Close()
	var replayed []map[string]json.RawMessage
	if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 4 || jsonString(replayed[1], "name") != hpatchToolName || jsonString(replayed[1], "input") != testHPatchScript || string(replayed[2]["future"]) != "true" || jsonString(replayed[2], "output") != history.translationError || jsonString(replayed[3], "output") != "keep" {
		t.Fatalf("restored hpatch rejection = %s", replay.fields["input"])
	}
}

func TestHPatchStreamingTranslationFailureCompletesDiagnosticExecLifecycle(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("parent directory does not exist")
	}))
	item := testHPatchItem()
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""
	completed := map[string]any{
		"status":      "completed",
		"output":      []any{item},
		"future":      map[string]any{"kept": true},
		"tools":       []any{map[string]any{"type": "custom", "name": hpatchToolName}},
		"tool_choice": hpatchToolName,
	}
	body := "event: response.output_item.added\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})) + "\n\n" +
		"event: response.custom_tool_call_input.done\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript})) + "\n\n" +
		"event: response.future\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.future", "future": "future-preserved"})) + "\n\n" +
		"event: response.output_item.done\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": item})) + "\n\n" +
		"event: response.completed\n" +
		"data: " + string(mustTestJSON(t, map[string]any{"type": "response.completed", "response": completed, "future": "outer-preserved"})) + "\n\n"
	var output bytes.Buffer
	terminal, err := copySSETransformed(&output, strings.NewReader(body), transform, nil)
	if err != nil || terminal != responseTerminalCompleted {
		t.Fatalf("translation rejection stream = terminal %v, error %v, output %q", terminal, err, output.String())
	}
	visible := output.String()
	for _, required := range []string{"event: response.output_item.added", "event: response.custom_tool_call_input.done", "event: response.output_item.done", "event: response.completed", `"name":"exec"`, "text(\\\"parent directory does not exist", `"future":{"kept":true}`, `"future":"outer-preserved"`, "future-preserved", `"name":"lookup"`, `"tool_choice":"auto"`} {
		if !strings.Contains(visible, required) {
			t.Fatalf("translation carrier missing %q: %q", required, visible)
		}
	}
	for _, forbidden := range []string{"response.failed", "stream disconnected"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("translation carrier exposed %q: %q", forbidden, visible)
		}
	}
	if _, remembered := proxy.history(transform.historySessionID, "call-H"); !remembered {
		t.Fatal("streaming translation rejection carrier was not remembered")
	}
}

func TestHPatchStreamingTranslationFailureRejectsMalformedTerminal(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		return nil, errors.New("selector is not unique")
	}))
	added := testHPatchItem()
	added["status"] = "in_progress"
	added["input"] = ""
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil || visible != nil {
		t.Fatalf("buffer hpatch call = %q, error %v", visible, err)
	}
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "item-H", "input": testHPatchScript})); err != nil || len(visible) != 2 || !bytes.Contains(visible[1], []byte("selector is not unique")) {
		t.Fatalf("release hpatch rejection carrier = %q, error %v", visible, err)
	}
	if visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.done", "item": testHPatchItem()})); err != nil || len(visible) != 1 {
		t.Fatalf("complete hpatch rejection carrier = %q, error %v", visible, err)
	}
	visible, err := transform.TransformSSE([]byte(`{"type":"response.completed","response":null}`))
	if err == nil || visible != nil || !strings.Contains(err.Error(), "decode hpatch-enabled response") {
		t.Fatalf("malformed terminal = %q, error %v", visible, err)
	}
}

func TestHPatchMalformedCallStillFailsRequest(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	item := testHPatchItem()
	item["type"] = "message"
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{item}}))
	if err == nil || visible != nil {
		t.Fatalf("malformed hpatch call = visible %q, error %v", visible, err)
	}
	if _, remembered := proxy.history(transform.historySessionID, "call-H"); remembered {
		t.Fatal("malformed hpatch call created history")
	}
}

func TestHPatchTranslationCancellationRemainsRequestCancellation(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(ctx context.Context, _ routingWorkspace, _ string) ([]byte, error) {
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transform.ctx = ctx
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}}))
	if !errors.Is(err, context.Canceled) || visible != nil {
		t.Fatalf("canceled translation = visible %q, error %v", visible, err)
	}
	if _, remembered := proxy.history(transform.historySessionID, "call-H"); remembered {
		t.Fatal("canceled translation created rejection history")
	}
}

func TestHPatchHistoryByteAccountingIncludesExistingCalls(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-1": {script: "first"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-2": {script: "second"},
	}); err != nil {
		t.Fatal(err)
	}

	session := proxy.sessions["session"]
	want := 0
	for _, history := range session.calls {
		want += history.bytes
	}
	if session.bytes != want || proxy.historyBytes != want {
		t.Fatalf("history bytes = session %d, global %d, want %d", session.bytes, proxy.historyBytes, want)
	}
}

func TestHPatchHistoryDoesNotEvictActiveSessions(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Error(err)
		}
	})
	for index := range maxSessionHistories {
		sessionID := fmt.Sprintf("session-%03d", index)
		if err := proxy.rememberBatch(sessionID, map[string]hpatchHistory{"call": {script: sessionID}}); err != nil {
			t.Fatalf("remember session %d: %v", index, err)
		}
	}
	if err := proxy.activateSession("session-000"); err != nil {
		t.Fatal(err)
	}
	defer proxy.deactivateSession("session-000")

	if err := proxy.rememberBatch("session-new", map[string]hpatchHistory{"call": {script: "new"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := proxy.sessions["session-000"]; !ok {
		t.Fatal("active oldest session was evicted")
	}
	if _, ok := proxy.sessions["session-001"]; ok {
		t.Fatal("oldest inactive session was not evicted")
	}
}

func TestHPatchHistoryEvictsOldestCallsAndSessions(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	for index := range maxSessionTurns + 1 {
		callID := fmt.Sprintf("call-%03d", index)
		err := proxy.rememberBatch("session", map[string]hpatchHistory{
			callID: {
				toolName:         hpatchToolName,
				script:           callID,
				translationError: "rejected",
			},
		})
		if err != nil {
			t.Fatalf("remember call %d: %v", index, err)
		}
	}
	session := proxy.sessions["session"]
	if len(session.calls) != maxSessionTurns {
		t.Fatalf("retained calls = %d, want %d", len(session.calls), maxSessionTurns)
	}
	if _, ok := session.calls["call-000"]; ok {
		t.Fatal("oldest call was not evicted")
	}
	if _, ok := session.calls[fmt.Sprintf("call-%03d", maxSessionTurns)]; !ok {
		t.Fatal("newest call was not retained")
	}
	latest, err := proxy.correctableHistory("session")
	if err != nil || latest.script != fmt.Sprintf("call-%03d", maxSessionTurns) {
		t.Fatalf("latest history = %+v, error %v", latest, err)
	}

	for index := range maxSessionHistories {
		sessionID := fmt.Sprintf("session-%03d", index)
		if err := proxy.rememberBatch(sessionID, map[string]hpatchHistory{"call": {script: sessionID}}); err != nil {
			t.Fatalf("remember session %d: %v", index, err)
		}
	}
	if err := proxy.rememberBatch("session-new", map[string]hpatchHistory{"call": {script: "new"}}); err != nil {
		t.Fatalf("remember replacement session: %v", err)
	}
	if len(proxy.sessions) != maxSessionHistories {
		t.Fatalf("retained sessions = %d, want %d", len(proxy.sessions), maxSessionHistories)
	}
	if _, ok := proxy.sessions["session-000"]; ok {
		t.Fatal("oldest session was not evicted")
	}
	if _, ok := proxy.sessions["session-new"]; !ok {
		t.Fatal("new session was not retained")
	}
}

func TestHPatchBoundsTranslationAndHistory(t *testing.T) {
	t.Run("translation output capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			return make([]byte, maxHPatchPatchBytes+1), nil
		}))
		payload := mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}})
		visible, err := transform.TransformJSON(payload)
		if err == nil || visible != nil || !strings.Contains(err.Error(), "translation exceeds") {
			t.Fatalf("oversized translation = visible %q, error %v", visible, err)
		}
		if _, remembered := proxy.history(transform.historySessionID, "call-H"); remembered {
			t.Fatal("oversized translation created rejection history")
		}
	})

	t.Run("script capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			t.Fatal("translator called for oversized script")
			return nil, nil
		}))
		item := testHPatchItem()
		item["input"] = strings.Repeat("x", maxHPatchScriptBytes+1)
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{item}}))
		if err == nil || visible != nil || !strings.Contains(err.Error(), "script exceeds") {
			t.Fatalf("oversized script = visible bytes %d, error %v", len(visible), err)
		}
		if _, remembered := proxy.history(transform.historySessionID, "call-H"); remembered {
			t.Fatal("oversized script created rejection history")
		}
	})

	t.Run("translator capacity", func(t *testing.T) {
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
			return nil, fmt.Errorf("%w: diagnostic overflow", errHPatchCapacity)
		}))
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{testHPatchItem()}}))
		if !errors.Is(err, errHPatchCapacity) || visible != nil {
			t.Fatalf("translator capacity = visible %q, error %v", visible, err)
		}
		if _, remembered := proxy.history(transform.historySessionID, "call-H"); remembered {
			t.Fatal("translator capacity created rejection history")
		}
	})

	t.Run("global history", func(t *testing.T) {
		proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
		proxy.historyBytes = maxHPatchHistoryGlobalBytes
		if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call": {script: "x", patch: "y"}}); err == nil {
			t.Fatal("history exceeded global capacity")
		}
		if len(proxy.sessions) != 0 {
			t.Fatalf("failed history reservation created %d sessions", len(proxy.sessions))
		}
	})

	t.Run("batch history is atomic", func(t *testing.T) {
		proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
		proxy.historyBytes = maxHPatchHistoryGlobalBytes - 1
		err := proxy.rememberBatch("session", map[string]hpatchHistory{
			"call-first":  {script: "x", patch: "y"},
			"call-second": {script: "x", patch: "y"},
		})
		if err == nil {
			t.Fatal("history batch exceeded global capacity")
		}
		if len(proxy.sessions) != 0 {
			t.Fatalf("failed history batch created %d sessions", len(proxy.sessions))
		}
	})
}

func TestHPatchBoundsPendingStreamCallsAndRejectsRelatedFutureEvents(t *testing.T) {
	t.Run("duplicate item", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		payload := mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})
		if _, err := transform.TransformSSE(payload); err != nil {
			t.Fatal(err)
		}
		if visible, err := transform.TransformSSE(payload); err == nil || visible != nil {
			t.Fatalf("duplicate item = visible %q, error %v", visible, err)
		}
	})

	t.Run("pending count", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		for index := range maxHPatchPendingCalls + 1 {
			added := testHPatchItem()
			added["status"] = "in_progress"
			added["input"] = ""
			added["id"] = fmt.Sprintf("item-%d", index)
			added["call_id"] = fmt.Sprintf("call-%d", index)
			visible, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added}))
			if index < maxHPatchPendingCalls {
				if err != nil || visible != nil {
					t.Fatalf("pending item %d = visible %q, error %v", index, visible, err)
				}
				continue
			}
			if err == nil || visible != nil {
				t.Fatalf("excess pending item = visible %q, error %v", visible, err)
			}
		}
	})

	t.Run("malformed pending event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		malformed := []byte(`{"type":1,"item_id":"item-H","delta":"secret script"}`)
		if visible, err := transform.TransformSSE(malformed); err == nil || visible != nil {
			t.Fatalf("malformed pending event = visible %q, error %v", visible, err)
		}
	})

	t.Run("malformed unrelated event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		malformed := []byte(`{"type":1,"future":"kept"}`)
		visible, err := transform.TransformSSE(malformed)
		if err != nil || len(visible) != 1 || !bytes.Equal(visible[0], malformed) {
			t.Fatalf("malformed unrelated event = visible %q, error %v", visible, err)
		}
	})

	t.Run("incomplete terminal event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		completed := mustTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}}})
		if visible, err := transform.TransformSSE(completed); err == nil || visible != nil {
			t.Fatalf("incomplete terminal event = visible %q, error %v", visible, err)
		}
	})

	t.Run("related future event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		added := testHPatchItem()
		added["status"] = "in_progress"
		added["input"] = ""
		if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": added})); err != nil {
			t.Fatal(err)
		}
		future := mustTestJSON(t, map[string]any{"type": "response.future", "call_id": "call-H", "input": "secret"})
		if visible, err := transform.TransformSSE(future); err == nil || visible != nil {
			t.Fatalf("related future event = visible %q, error %v", visible, err)
		}
	})

	t.Run("unrelated future event", func(t *testing.T) {
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		future := mustTestJSON(t, map[string]any{"type": "response.future", "call_id": "other", "name": "other"})
		visible, err := transform.TransformSSE(future)
		if err != nil || len(visible) != 1 || !bytes.Equal(visible[0], future) {
			t.Fatalf("unrelated future event = visible %q, error %v", visible, err)
		}
	})
}

func TestInProcessHPatchToolDescription(t *testing.T) {
	translator := newInProcessHPatchTranslator(t.TempDir())
	if got, want := translator.ToolDescription(), hpatch.ToolDescription(); got != want {
		t.Fatalf("installed tool description differs from authoritative description:\n got %q\nwant %q", got, want)
	}
}

func TestInProcessHPatchTranslatorIsRootScopedAndDoesNotMutateWorkspace(t *testing.T) {
	translator := newInProcessHPatchTranslator(t.TempDir())
	workspacePath := t.TempDir()
	path := filepath.Join(workspacePath, "existing.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, ok := usableRoutingWorkspace(map[string]json.RawMessage{workspacePath: nil})
	if !ok {
		t.Fatal("workspace unavailable")
	}
	defer workspace.close()

	translated, err := translator.Translate(t.Context(), workspace, "in "+path+"\ntype+ 1:a793 \"inserted\\n\"\ntype 3:b1e9 \"THIRD\"\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"*** Update File: existing.txt", "+inserted", "+THIRD"} {
		if !bytes.Contains(translated.patch, []byte(required)) {
			t.Fatalf("translation does not contain %q: %s", required, translated.patch)
		}
	}
	if !strings.HasPrefix(translated.report, "in existing.txt\n") {
		t.Fatalf("translation report = %q", translated.report)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first\nsecond\nthird\n" {
		t.Fatalf("translation mutated workspace to %q", content)
	}

	conflict := "in " + path + "\ntype 1:a793..2:1636 \"replacement\"\ntype 2:1636..3:b1e9 \"overlap\"\n"
	if translated, err := translator.Translate(t.Context(), workspace, conflict); err == nil || !strings.Contains(translated.diagnostic, "conflicts with edit") {
		t.Fatalf("overlapping baseline translation error = %v", err)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := translator.Translate(t.Context(), workspace, "in "+outsidePath+"\n"); err == nil {
		t.Fatal("translation accepted an absolute path outside the trusted root")
	}
}

func mustTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func jsonQuoted(value string) string {
	return strconv.Quote(value)
}
