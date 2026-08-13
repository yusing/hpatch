package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	testHPatchReport    = "in created.txt\nlast type created.txt 1 ranges 1:1-1:1\nfiles add=1 update=0 move=0 delete=0\nrefs 2 type created.txt\n1:239f payload\n"
)

const testHPatchToolDescription = "fixture hpatch description\nwith exact trailing newline\n"

const testCodeModeDescription = "Run JavaScript.\n- All nested tools are available on the global `tools` object, for example `await tools.exec_command(...)`. Tool names are exposed as normalized JavaScript identifiers.\n\n### `exec_command`\nRun a shell command.\n\nexec tool declaration:\n```ts\ndeclare const tools: { exec_command(args: { cmd: string; workdir?: string }): Promise<unknown>; };\n```\n\n### `apply_patch`\nThe default editor.\n\nexec tool declaration:\n```ts\ndeclare const tools: { apply_patch(input: string): Promise<unknown>; };\n```\n\n### `create_goal`\nCreate a goal."

const testCLICodeModeDescription = "Run JavaScript.\n- All nested tools are available on the global `tools` object, for example `await tools.exec_command(...)`. Tool names are exposed as normalized JavaScript identifiers.\n\n### exec_command\nRun a command in a PTY.\n\nParameters:\n- `cmd`: required command text.\n- `workdir`: optional working directory.\n- `tty`: optional terminal allocation.\n\n### `apply_patch`\nThe default editor.\n\nexec tool declaration:\n```ts\ndeclare const tools: { apply_patch(input: string): Promise<unknown>; };\n```\n\n### `create_goal`\nCreate a goal."

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
		{
			"type":        mustMarshalJSON("custom"),
			"name":        mustMarshalJSON("shell"),
			"description": mustMarshalJSON("shell base description"),
		},
	}
}

type hpatchTranslatorFunc func(context.Context, string, string) ([]byte, error)

func (f hpatchTranslatorFunc) Translate(ctx context.Context, workspace string, script string) (hpatchTranslationResult, error) {
	patch, err := f(ctx, workspace, script)
	return hpatchTranslationResult{patch: patch, report: testHPatchReport}, err
}

func (hpatchTranslatorFunc) ToolDescription() string {
	return testHPatchToolDescription
}

func (hpatchTranslatorFunc) RecordMetrics(context.Context, hpatchMetricRecord) error {
	return nil
}

type hpatchResultTranslatorFunc func(context.Context, string, string) (hpatchTranslationResult, error)

func (f hpatchResultTranslatorFunc) Translate(ctx context.Context, workspace string, script string) (hpatchTranslationResult, error) {
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
	return newManagedHPatchProxyWithDataDirectory(t, translator, t.TempDir())
}

func newManagedHPatchProxyWithDataDirectory(t *testing.T, translator hpatchTranslator, dataDirectory string) *hpatchProxy {
	t.Helper()
	if translator == nil {
		return nil
	}
	registry, err := buildToolRegistry(t.Context(), dataDirectory, translator.ToolDescription(), false)
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
	return newHPatchTestTransformWithProxy(t, newManagedHPatchProxy(t, translator))
}

func newHPatchTestTransformWithProxy(t *testing.T, proxy *hpatchProxy) (*hpatchResponseTransform, *hpatchProxy, *parsedResponsesRequest, string) {
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
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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
	return hpatchTranslatorFunc(func(_ context.Context, directory string, script string) ([]byte, error) {
		*calls++
		if directory == "" {
			t.Fatal("translator received no base directory")
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
		"input":        []any{testCodeModeAdditionalTools(testCodeModeDescription)},
		"tools":        []any{map[string]any{"type": "function", "name": "lookup", "future": true}},
		"tool_choice":  "auto",
		"instructions": "existing base\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	originalInstructions := bytes.Clone(request.fields["instructions"])
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))

	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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
	if got := request.fields["instructions"]; !bytes.Equal(got, originalInstructions) {
		t.Fatalf("request instructions = %s, want byte-equivalent %s", got, originalInstructions)
	}
}

func TestHPatchPrepareRequestExposesEditToolsAndShell(t *testing.T) {
	transform, _, request, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	if !transform.originalToolsPresent || len(transform.originalTools) == 0 {
		t.Fatal("original top-level tools were not retained")
	}
	var topTools []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["tools"], &topTools); err != nil {
		t.Fatal(err)
	}
	if len(topTools) != 4 || jsonString(topTools[0], "name") != "lookup" || jsonString(topTools[1], "name") != hpatchToolName || jsonString(topTools[2], "name") != hpatchRecoveryToolName || jsonString(topTools[3], "name") != "shell" {
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
	if description := jsonString(topTools[3], "description"); !strings.HasPrefix(description, "Run one free-form script. The selected interpreter receives the exact script body, and frontend standard input remains available as program data.\n\n### `#!params`") ||
		strings.Contains(description, "#!cmd=") || strings.Contains(description, "@shell/") {
		t.Fatalf("standalone shell description = %q", description)
	}
	if _, exists := request.fields["instructions"]; exists {
		t.Fatalf("prepareRequest added instructions: %s", request.fields["instructions"])
	}
	for _, obsoleteGuidance := range []string{
		"Repairing a rejected script:",
		"INDEX: COMMAND",
		`INDEX.ROW: "VALUE"`,
		": accept",
	} {
		if strings.Contains(exposed, obsoleteGuidance) {
			t.Fatalf("standalone hpatch description includes obsolete recovery guidance %q: %q", obsoleteGuidance, exposed)
		}
	}
	if strings.Contains(exposed, "type <<PATCH replacement or insertion consumes") {
		t.Fatalf("standalone hpatch description retains obsolete indexed recovery framing: %q", exposed)
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
		strings.Contains(description, codeModeExecCommandHeading) ||
		strings.Contains(description, "tools.exec_command") ||
		!strings.Contains(description, "### `create_goal`") {
		t.Fatalf("native apply_patch or exec_command was not hidden: %q", description)
	}
	if !bytes.Contains(items[0]["future"], []byte(`"kept":true`)) || !bytes.Contains(request.fields["future_request"], []byte(`"kept":true`)) {
		t.Fatalf("future fields were not preserved: %#v", request.fields)
	}
	if string(request.fields["parallel_tool_calls"]) != "true" {
		t.Fatalf("parallel_tool_calls = %s", request.fields["parallel_tool_calls"])
	}
}

func TestHPatchRoutesOnlyModelVisibleRegistryTools(t *testing.T) {
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	for name, want := range map[string]bool{
		hpatchToolName: true,
		"shell":        true,
		"hread":        false,
		"hgrep":        false,
		"inspect_file": false,
		"lookup":       false,
	} {
		if got := transform.routesTool(name); got != want {
			t.Errorf("routesTool(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestReportIssueRunsRouterHookWithoutWorker(t *testing.T) {
	dataDirectory := t.TempDir()
	bodyPath := filepath.Join(t.TempDir(), "body.md")
	settings := fmt.Sprintf(
		`{"hooks":{"diagnose":[%q]}}`,
		"printf '%s\n%s' {{shellquote .Title}} {{shellquote (format_markdown .)}} > "+bodyPath,
	)
	if err := os.WriteFile(filepath.Join(dataDirectory, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, true)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "session_index.jsonl")
	if err := os.WriteFile(indexPath, []byte(`{"id":"session-1","thread_name":"Fix loop flaws"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := newHPatchProxy(
		testTranslator(t, new(int)),
		registry,
		newSessionTitleCacheAt(indexPath),
	)
	t.Cleanup(func() {
		if err := errors.Join(proxy.Close(), registry.Close()); err != nil {
			t.Error(err)
		}
	})
	updatedBodyPath := filepath.Join(t.TempDir(), "updated-body.md")
	updatedSettings := fmt.Sprintf(
		`{"hooks":{"diagnose":[%q]}}`,
		"printf '%s\n%s' {{shellquote .Title}} {{shellquote (format_markdown .)}} > "+updatedBodyPath,
	)
	if err := os.WriteFile(filepath.Join(dataDirectory, "settings.json"), []byte(updatedSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, proxy)
	markdown := "# hpatch issue\n\nRepair context did not identify the stale row."
	history, err := transform.translateTool(reportIssueToolName, "call-report", markdown, nil)
	if err != nil {
		t.Fatal(err)
	}
	if history.toolName != reportIssueToolName ||
		history.carrierName != transform.codeModeToolName ||
		history.report != "Issue reported." ||
		history.carrierInput() != hpatchDiagnosticExecInput("Issue reported.") {
		t.Fatalf("report issue history = %+v", history)
	}
	body, err := os.ReadFile(updatedBodyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "Fix loop flaws\n" + markdown
	if string(body) != want {
		t.Fatalf("diagnose hook output = %q, want %q", body, want)
	}
	if _, ok := registry.wrapper(reportIssueToolName); ok {
		t.Fatal("report issue unexpectedly installed a worker wrapper")
	}
}

func TestReportIssueHookFailureDoesNotFailRouting(t *testing.T) {
	dataDirectory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dataDirectory, "settings.json"),
		[]byte(`{"hooks":{"diagnose":["exit 9"]}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHPatchProxy(testTranslator(t, new(int)), registry)
	t.Cleanup(func() {
		if err := errors.Join(proxy.Close(), registry.Close()); err != nil {
			t.Error(err)
		}
	})
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, proxy)

	history, err := transform.translateTool(reportIssueToolName, "call-report", "diagnostic", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "Issue report was not delivered.\nhpatch: warning: running diagnose hook 1: exit status 9\n"
	if history.report != want || history.carrierInput() != hpatchDiagnosticExecInput(want) {
		t.Fatalf("report issue history = %+v, want report %q", history, want)
	}
}

func TestReportIssueCallIDCannotBeReusedByHPatch(t *testing.T) {
	dataDirectory := t.TempDir()
	registry, err := buildToolRegistry(t.Context(), dataDirectory, testHPatchToolDescription, true)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	proxy := newHPatchProxy(testTranslator(t, &calls), registry)
	t.Cleanup(func() {
		if err := errors.Join(proxy.Close(), registry.Close()); err != nil {
			t.Error(err)
		}
	})
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, proxy)
	input := "same input"
	if _, err := transform.translateTool(reportIssueToolName, "shared-call", input, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transform.translateTool(hpatchToolName, "shared-call", input, nil); err == nil ||
		!strings.Contains(err.Error(), "hpatch call \"shared-call\" changed input") {
		t.Fatalf("reused cross-tool call error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("hpatch translations = %d, want 0", calls)
	}
}

func TestHPatchReplacementReplacesNamespacedExecCommandWithShellParams(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(testCLICodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}
	installed := testInstalledTools()
	applyPatchDefinition, execCommandDefinitions, owner, replaced, err := replaceAdditionalToolsApplyPatch(fields, installed)
	if err != nil || !replaced || owner != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", owner, replaced, err)
	}
	if !strings.HasPrefix(applyPatchDefinition, codeModeApplyPatchHeading) || len(execCommandDefinitions) != 2 {
		t.Fatalf("removed definitions = apply %q, exec %q", applyPatchDefinition, execCommandDefinitions)
	}
	if !slices.ContainsFunc(execCommandDefinitions, func(definition string) bool {
		return strings.Contains(definition, codeModeExecCommandPlainHeading)
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
	wantShellDescription := "shell base description\n\n### `#!params`\nThe leading `#!params={...}` directive accepts a JSON object with these request-specific fields. The script body supplies `cmd`, so omit it.\n\n- `workdir`: optional working directory.\n- `tty`: optional terminal allocation."
	if shellIndex < 0 || jsonString(tools[shellIndex], "description") != wantShellDescription {
		t.Fatalf("shell description = %#v, want %q", tools, wantShellDescription)
	}
}

func TestHPatchReplacementReplacesFlatExecCommandWithShellParams(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testFlatCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}
	installed := testInstalledTools()
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

	var installedTools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &installedTools); err != nil {
		t.Fatal(err)
	}
	shellIndex := slices.IndexFunc(installedTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "shell"
	})
	wantShellDescription := "shell base description\n\n### `#!params`\nThe leading `#!params={...}` directive accepts this request-specific JSON object shape. The script body supplies `cmd`, so omit it.\n\n```ts\n{ workdir?: string }\n```"
	if shellIndex < 0 || jsonString(installedTools[shellIndex], "description") != wantShellDescription {
		t.Fatalf("shell description = %#v, want %q", installedTools, wantShellDescription)
	}
}

func TestHPatchReplacementKeepsBaseShellDescriptionWithoutExecCommandContract(t *testing.T) {
	description := "Run JavaScript.\n\n### `apply_patch`\nThe default editor.\n\nexec tool declaration:\n```ts\ndeclare const tools: { apply_patch(input: string): Promise<unknown>; };\n```\n\n### `create_goal`\nCreate a goal."
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(description)}),
		"tools": mustTestJSON(t, []any{}),
	}
	installed := testInstalledTools()

	_, execCommandDefinitions, _, replaced, err := replaceAdditionalToolsApplyPatch(fields, installed)
	if err != nil || !replaced || len(execCommandDefinitions) != 0 {
		t.Fatalf("replacement = %t, definitions %q, error %v", replaced, execCommandDefinitions, err)
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
	if _, _, _, _, err := stripCodeModeExecCommandContract(description); err == nil {
		t.Fatal("unowned exec_command reference was accepted")
	}

	description = "Run JavaScript.\n\n### `create_goal`\nKeep this."
	stripped, paramsDescription, definitions, found, err := stripCodeModeExecCommandContract(description)
	if err != nil || found || paramsDescription != "" || len(definitions) != 0 || stripped != description {
		t.Fatalf("absent contract = stripped %q, params %q, definitions %q, found %t, error %v", stripped, paramsDescription, definitions, found, err)
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
					map[string]any{"type": "custom", "name": "unrelated", "future": true},
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
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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

func TestHPatchAdditionalToolsReplacementAlwaysRemovesExecCommand(t *testing.T) {
	fields := map[string]json.RawMessage{
		"input": mustTestJSON(t, []any{testCodeModeAdditionalTools(testCodeModeDescription)}),
		"tools": mustTestJSON(t, []any{}),
	}

	_, removedExecCommandDefinitions, owner, replaced, err := replaceAdditionalToolsApplyPatch(fields, testInstalledTools())
	if err != nil || !replaced || owner != "exec" {
		t.Fatalf("owner = %q, replaced %v, error %v", owner, replaced, err)
	}
	if len(removedExecCommandDefinitions) != 2 {
		t.Fatalf("removed exec_command definitions = %q", removedExecCommandDefinitions)
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
		strings.Contains(description, codeModeExecCommandHeading) ||
		strings.Contains(description, "tools.exec_command") ||
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
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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

func TestHPatchTranslationWithoutWorkspaceUsesNoBaseDirectory(t *testing.T) {
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"input": []any{testCodeModeAdditionalTools(testCodeModeDescription)},
	}))
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	translator := hpatchTranslatorFunc(func(_ context.Context, directory, script string) ([]byte, error) {
		calls++
		if directory != "" {
			t.Fatalf("directory = %q, want no base directory", directory)
		}
		if script != testHPatchScript {
			t.Fatalf("script = %q", script)
		}
		return []byte(testTranslatedPatch), nil
	})
	proxy := newManagedHPatchProxy(t, translator)
	transform, err := proxy.prepareRequest(
		t.Context(),
		&request,
		"session-without-workspace",
		codexTurnMetadata{RequestKind: "turn"},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer transform.Close()

	if _, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{testHPatchItem()},
	})); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("translations = %d, want 1", calls)
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
	if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
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

func TestNativeExecCommandAddsShellWarning(t *testing.T) {
	const nativeInput = "const result = await tools.exec_command({\"cmd\":\"printf ok\"});\ntext(result.output);"
	const unrelatedInput = "text(\"ok\");"
	warningInput := misuseWarningProjection(nativeExecCommandWarning)
	wantInput := "const result = await tools.exec_command({\"cmd\":\"printf ok\"});\n" +
		warningInput + codeModeOutputProjection
	rewritten, gotWarning, changed, detected := nativeExecCommandInput(nativeInput)
	if !changed || !detected || rewritten != wantInput || gotWarning != warningInput {
		t.Fatalf(
			"rewrite native exec input: changed %t, detected %t, warning %q\n%s",
			changed,
			detected,
			gotWarning,
			rewritten,
		)
	}
	var arguments struct {
		Command string `json:"cmd"`
	}
	decodeExecCarrierArguments(t, rewritten, &arguments)
	if arguments.Command != "printf ok" {
		t.Fatalf("native warning changed command to %q", arguments.Command)
	}
	repeated, repeatedWarning, repeatedChange, repeatedDetection := nativeExecCommandInput(rewritten)
	if repeatedChange || !repeatedDetection || repeated != rewritten || repeatedWarning != warningInput {
		t.Fatalf(
			"repeated native rewrite: changed %t, detected %t, warning %q\n%s",
			repeatedChange,
			repeatedDetection,
			repeatedWarning,
			repeated,
		)
	}
	combined, _, combinedChange, err := insertExecCommandWarning(rewritten, "functions.shell: warning: secondary warning")
	if err != nil || !combinedChange {
		t.Fatalf("insert second warning: changed %t, error %v", combinedChange, err)
	}
	idempotent, _, duplicateChange, err := insertExecCommandWarning(combined, nativeExecCommandWarning)
	if err != nil || duplicateChange || idempotent != combined {
		t.Fatalf("reinsert first warning: changed %t, error %v\n%s", duplicateChange, err, idempotent)
	}

	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "native", "input": nativeInput},
			map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "unrelated", "input": unrelatedInput},
		},
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
		jsonString(response.Output[0], "input") != rewritten ||
		jsonString(response.Output[1], "input") != unrelatedInput {
		t.Fatalf("native exec response = %s", visible)
	}

	stream, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	added := mustTestJSON(t, map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"type": "custom_tool_call", "id": "item-native", "name": "exec",
			"call_id": "native-stream", "input": "", "status": "in_progress",
		},
	})
	if output, err := stream.TransformSSE(added); err != nil || len(output) != 1 || !bytes.Equal(output[0], added) {
		t.Fatalf("native exec added event = %q, error %v", output, err)
	}
	done := mustTestJSON(t, map[string]any{
		"type": "response.custom_tool_call_input.done", "item_id": "item-native", "input": nativeInput,
	})
	output, err := stream.TransformSSE(done)
	if err != nil || len(output) != 1 {
		t.Fatalf("native exec input.done event = %q, error %v", output, err)
	}
	var event struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(output[0], &event); err != nil || event.Input != rewritten {
		t.Fatalf("native exec input.done event = %q, error %v", output, err)
	}
}

func decodeExecCarrierArguments(t *testing.T, carrierInput string, destination any) {
	t.Helper()
	encoded := strings.TrimPrefix(carrierInput, "const result = await tools.exec_command(")
	end := strings.Index(encoded, ");\n")
	if end < 0 {
		t.Fatalf("translated exec carrier is malformed: %s", carrierInput)
	}
	if err := json.Unmarshal([]byte(encoded[:end]), destination); err != nil {
		t.Fatalf("decode translated exec arguments: %v\n%s", err, carrierInput)
	}
}

func TestShellJSONTranslatesBashCasesEndToEnd(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, proxy)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single line", input: "foo", want: "foo"},
		{name: "final newline", input: "foo\n", want: "foo"},
		{name: "redundant errexit", input: "set -e\nfoo\n", want: "foo"},
		{
			name:  "multiline",
			input: "printf one\nprintf two\n",
			want:  "printf one\nprintf two",
		},
		{
			name:  "meaningful options",
			input: "set -euo pipefail\nfoo\n",
			want:  "set -euo pipefail\nfoo",
		},
		{
			name:  "multiple commands",
			input: "set -e\nfalse; echo survived\n",
			want:  "set -e\nfalse; echo survived",
		},
	}
	output := make([]any, 0, len(tests))
	for index, test := range tests {
		output = append(output, map[string]any{
			"type":    "custom_tool_call",
			"id":      fmt.Sprintf("item-shell-%d", index),
			"call_id": fmt.Sprintf("call-shell-%d", index),
			"name":    "shell",
			"input":   test.input,
			"status":  "completed",
		})
	}
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": output,
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
	if len(response.Output) != len(tests) {
		t.Fatalf("translated shell output count = %d, want %d: %s", len(response.Output), len(tests), visible)
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := response.Output[index]
			if jsonString(item, "name") != "exec" {
				t.Fatalf("translated carrier name = %q", jsonString(item, "name"))
			}
			carrierInput := jsonString(item, "input")
			var arguments struct {
				Command string `json:"cmd"`
			}
			decodeExecCarrierArguments(t, carrierInput, &arguments)
			if arguments.Command != test.want {
				t.Fatalf("translated exec command = %q, want %q", arguments.Command, test.want)
			}
		})
	}
}

func TestShellRecoversLunaCodeModePrograms(t *testing.T) {
	const legacy = "const result = await tools.exec_command({\"cmd\":\"git status --short\",\"login\":false,\"max_output_tokens\":24000});\n" +
		"text(JSON.stringify(Object.assign({}, result, {\"retained\":false})));"
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "legacy exec_command carrier",
			input: legacy,
		},
		{
			name:  "exec program",
			input: "const r = await tools.exec({command:\"git show --stat\",workdir:\"/workspace\"});\ntext(r);",
		},
		{
			name:  "write_stdin program",
			input: "const r = await tools.write_stdin({chars:\"\",session_id:83733,yield_time_ms:30000});\ntext(r.output);",
		},
		{
			name:  "same-line exec projection",
			input: "const r = await tools.exec({command:\"printf ok\"}); text(r);",
		},
		{
			name:  "same-line write_stdin projection",
			input: "const r = await tools.write_stdin({chars:\"\",session_id:83733}); text(r.output);",
		},
		{
			name: "multiple tool calls remain unchanged",
			input: "const r = await tools.exec({command:\"git show --stat\"});\n" +
				"text(r);\nconst r = await tools.write_stdin({chars:\"\",session_id:83733});\ntext(r.output);",
		},
		{
			name:  "leading whitespace",
			input: "\n \tconst r = await tools.exec({command:\"printf ok\"});\ntext(r);",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recovered := lunaShellCodeModeProgram(
				toolContribution{PluginID: "builtin.shell", Name: "shell"},
				test.input,
			)
			if !recovered {
				t.Fatal("Code Mode program was not recovered")
			}
			warningInput := misuseWarningProjection(lunaShellRecoveryWarning)
			want := warningInput + test.input

			dataDirectory := t.TempDir()
			translator := metricsObservingTranslator{
				translate: func(context.Context, string, string) ([]byte, error) {
					return []byte(testTranslatedPatch), nil
				},
				record: func(ctx context.Context, record hpatchMetricRecord) error {
					return hpatch.RecordHostMetrics(ctx, dataDirectory, record.HostMetricRecord)
				},
			}
			transform, proxy, _, _ := newHPatchTestTransform(t, translator)
			visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed",
				"output": []any{map[string]any{
					"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
					"name": "shell", "input": test.input, "status": "completed",
				}},
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
				jsonString(response.Output[0], "input") != want {
				t.Fatalf("recovered shell carrier = %s", visible)
			}
			history, ok := proxy.history(transform.historySessionID, "call-shell")
			if !ok || history.toolName != "shell" || history.carrierPayload != want || !history.replayCarrier {
				t.Fatalf("recovered shell history = %+v, available %t", history, ok)
			}

			replay, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
				"input": []any{
					response.Output[0],
					map[string]any{
						"type": "custom_tool_call_output", "call_id": "call-shell", "output": "command output",
					},
				},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
				t.Fatal(err)
			}
			firstReplay := bytes.Clone(replay.fields["input"])
			if err := proxy.reconcileInputPrefix(&replay, transform.historySessionID); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(replay.fields["input"], firstReplay) {
				t.Fatalf("recovered carrier replay was not idempotent: %s", replay.fields["input"])
			}
			var replayed []map[string]json.RawMessage
			if err := json.Unmarshal(replay.fields["input"], &replayed); err != nil {
				t.Fatal(err)
			}
			if len(replayed) != 2 || jsonString(replayed[0], "name") != "exec" ||
				jsonString(replayed[0], "input") != want ||
				jsonString(replayed[1], "output") != "command output" {
				t.Fatalf("recovered carrier replay = %s", replay.fields["input"])
			}
			gain, err := hpatch.LoadGainMetrics(dataDirectory)
			if err != nil {
				t.Fatal(err)
			}
			var lunaMisuse uint64
			for _, recovery := range gain.Recoveries {
				if recovery.Name == "luna misuse" {
					lunaMisuse = recovery.Count
				}
			}
			if lunaMisuse != 1 {
				t.Fatalf("luna misuse recoveries after replay = %d, want 1", lunaMisuse)
			}
		})
	}
}

func TestLunaShellCodeModeProgramRejectsNearMisses(t *testing.T) {
	contribution := toolContribution{PluginID: "builtin.shell", Name: "shell"}
	valid := "const r = await tools.exec({command:\"printf ok\"});\ntext(r);"
	for _, input := range []string{
		"printf ok",
		"#!node\n" + valid,
		"#!params={}\n" + valid,
		"# const r = await tools.exec({command:\"printf ok\"});\ntext(r);",
		"// comment\n" + valid,
		"const r = tools.exec({command:\"printf ok\"});\ntext(r);",
		"const r = await other.exec({command:\"printf ok\"});\ntext(r);",
		"const r = await tools.exec({command:\"printf ok\"});\nconsole.log(r);",
		"printf '%s' 'const r = await tools.exec({command:\"printf ok\"}); text(r);'",
		"text(\"before\");\n" + valid,
	} {
		if lunaShellCodeModeProgram(contribution, input) {
			t.Errorf("near-miss shell input recovered: %q", input)
		}
	}
	if lunaShellCodeModeProgram(
		toolContribution{PluginID: "configured", Name: "shell"},
		valid,
	) {
		t.Error("configured shell plugin received Luna recovery")
	}
}

func TestShellInterpreterWrapperAddsWarning(t *testing.T) {
	contribution := toolContribution{PluginID: "builtin.shell", Name: "shell"}
	for _, test := range []struct {
		input  string
		want   string
		misuse shellWrapperMisuse
	}{
		{
			input:  "python3 - <<'PY'\nprint('ok')\nPY",
			want:   "functions.shell: warning: remove the `python3 - <<...` heredoc wrapper; start the script with `#!python3` and put the Python program directly in the body",
			misuse: shellWrapperMisuse{Kind: "heredoc", Interpreter: "python3", wrapper: "- <<"},
		},
		{
			input:  "python3 -I -c 'print(1)'",
			want:   "functions.shell: warning: replace `python3 -I -c ...` with `#!python3 -I` on the first line and put the Python program directly in the body",
			misuse: shellWrapperMisuse{Kind: "-c", Interpreter: "python3", InterpreterArgs: []string{"-I"}, wrapper: "-c"},
		},
		{
			input:  "node --input-type=module -e 'console.log(1)'",
			want:   "functions.shell: warning: replace `node --input-type=module -e ...` with `#!node --input-type=module` on the first line and put the JavaScript program directly in the body",
			misuse: shellWrapperMisuse{Kind: "-e", Interpreter: "node", InterpreterArgs: []string{"--input-type=module"}, wrapper: "-e"},
		},
		{
			input:  "bash -c 'printf ok'",
			want:   "functions.shell: warning: remove the `bash -c` wrapper and submit the Bash script body directly without a shebang",
			misuse: shellWrapperMisuse{Kind: "-c", Interpreter: "bash", wrapper: "-c"},
		},
		{
			input:  "sh -ec 'printf ok'",
			want:   "functions.shell: warning: replace `sh -ec ...` with `#!sh -e` on the first line and put the program directly in the body",
			misuse: shellWrapperMisuse{Kind: "-c", Interpreter: "sh", InterpreterArgs: []string{"-e"}, wrapper: "-ec"},
		},
		{
			input:  "cat <<'EOF'\nhello\nEOF",
			want:   "functions.shell: warning: heredoc detected; submit the script body directly instead of wrapping it in a heredoc",
			misuse: shellWrapperMisuse{Kind: "heredoc"},
		},
		{
			input:  "/usr/bin/python3 -c 'print(1)'",
			want:   "functions.shell: warning: replace `python3 -c ...` with `#!python3` on the first line and put the Python program directly in the body",
			misuse: shellWrapperMisuse{Kind: "-c", Interpreter: "python3", wrapper: "-c"},
		},
		{
			input:  "psql --command 'select 1'",
			want:   "functions.shell: warning: replace `psql --command ...` with `#!psql` on the first line and put the program directly in the body",
			misuse: shellWrapperMisuse{Kind: "--command", Interpreter: "psql", wrapper: "--command"},
		},
	} {
		misuses := shellInterpreterWrapperMisuses(contribution, test.input)
		if len(misuses) != 1 {
			t.Errorf("misuses for %q = %#v; want %#v", test.input, misuses, test.misuse)
			continue
		}
		misuse := misuses[0]
		if misuse.Kind != test.misuse.Kind || misuse.Interpreter != test.misuse.Interpreter ||
			!slices.Equal(misuse.InterpreterArgs, test.misuse.InterpreterArgs) || misuse.wrapper != test.misuse.wrapper {
			t.Errorf("misuse for %q = %#v; want %#v", test.input, misuse, test.misuse)
			continue
		}
		if warning := shellInterpreterWrapperWarning(misuse); warning != test.want {
			t.Errorf("warning for %q = %q; want %q", test.input, warning, test.want)
		}
	}

	for _, input := range []string{
		"python3 -c 'print(1)'",
		"python3 -I -c 'print(1)'",
		"node -e 'console.log(1)'",
		"node --input-type=module -e 'console.log(1)'",
		"bash -c 'printf ok'",
		"bash -lc 'printf ok'",
		"bash -x -c 'printf ok'",
		"python3 <<'PY'\nprint('ok')\nPY",
		"export PYTHONDONTWRITEBYTECODE=1\npython3 - <<'PY'\nprint('ok')\nPY",
		"env PYTHONDONTWRITEBYTECODE=1 python3 - <<'PY'\nprint('ok')\nPY",
		"PYTHONDONTWRITEBYTECODE=1 python3 - <<'PY'\nprint('ok')\nPY",
		"printf before\npython3 - <<'PY'\nprint('ok')\nPY",
		"/usr/bin/python3 -c 'print(1)'",
		"pypy3 -c 'print(1)'",
		"pypy3 - <<'PY'\nprint('ok')\nPY",
		"nodejs - <<'JS'\nconsole.log(1)\nJS",
		"bun -e 'console.log(1)'",
		"bun - <<'JS'\nconsole.log(1)\nJS",
		"env NODE_NO_WARNINGS=1 node - <<'JS'\nconsole.log(1)\nJS",
		"bash - <<'SH'\nprintf ok\nSH",
		"sh -ec 'printf ok'",
		"sh - <<'SH'\nprintf ok\nSH",
		"cat <<'EOF'\nhello\nEOF",
		"cat input.txt <<'EOF'\nhello\nEOF",
		"zsh -c 'printf ok'",
		"fish -c 'printf ok'",
		"perl -e 'print 1'",
		"ruby -e 'puts 1'",
		"php -r 'echo 1;'",
		"lua -e 'print(1)'",
		"psql -c 'select 1'",
		"psql --command 'select 1'",
		"mysql -e 'select 1'",
		"mysql --execute 'select 1'",
		"printf '%s' 'normal command merely contains node -e'",
		"# example: python3 -c 'print(1)'",
	} {
		misuses := shellInterpreterWrapperMisuses(contribution, input)
		if len(misuses) == 0 || shellInterpreterWrapperWarning(misuses[0]) == "" {
			t.Errorf("interpreter wrapper was not detected: %q", input)
		}
	}
	for _, input := range []string{
		"python3 script.py",
		"python3 -m module",
		"python3 -I script.py",
		"node app.js",
		"node --trace-warnings app.js",
		"bash script.sh",
		"bash -x script.sh",
		"git -c key=value status",
		"grep -e pattern file",
		"#!cat\ncat shebang executes",
		"printf ok",
	} {
		if misuses := shellInterpreterWrapperMisuses(contribution, input); len(misuses) != 0 {
			t.Errorf("ordinary shell input misuses = %#v for %q", misuses, input)
		}
	}
	if misuses := shellInterpreterWrapperMisuses(
		toolContribution{PluginID: "configured", Name: "shell"},
		"python3 -c pass",
	); len(misuses) != 0 {
		t.Error("configured shell plugin received interpreter-wrapper warning")
	}

	const input = "printf '%s' 'python3 -c'"
	misuses := shellInterpreterWrapperMisuses(contribution, input)
	if len(misuses) == 0 {
		t.Fatal("quoted interpreter wrapper was not detected")
	}
	warningInput := misuseWarningProjection(shellInterpreterWrapperWarning(misuses[0]))
	transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{map[string]any{
			"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
			"name": "shell", "input": input, "status": "completed",
		}},
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
	if len(response.Output) != 1 || jsonString(response.Output[0], "name") != "exec" {
		t.Fatalf("warned shell carrier = %s", visible)
	}
	carrierInput := jsonString(response.Output[0], "input")
	var arguments struct {
		Command string `json:"cmd"`
	}
	decodeExecCarrierArguments(t, carrierInput, &arguments)
	if arguments.Command == "" || strings.Contains(arguments.Command, warningInput) ||
		!strings.Contains(arguments.Command, input) ||
		!strings.Contains(carrierInput, warningInput+codeModeMetadataProjection) {
		t.Fatalf("warned shell carrier = %q, command %q, want original input %q", carrierInput, arguments.Command, input)
	}
}

func TestShellStacksDistinctMisuseWarnings(t *testing.T) {
	contribution := toolContribution{PluginID: "builtin.shell", Name: "shell"}
	wrapperMisuses := shellInterpreterWrapperMisuses(
		contribution,
		"python3 -c 'print(1)'\nnode -e 'console.log(1)'\nruby -e 'puts 1'",
	)
	if len(wrapperMisuses) != 2 || wrapperMisuses[0].Kind != "-c" || wrapperMisuses[1].Kind != "-e" {
		t.Fatalf("distinct wrapper misuses = %#v, want -c then -e", wrapperMisuses)
	}
	const script = "bun -e <<'JS'\nconsole.log('ok')\nJS"
	misuses := shellInterpreterWrapperMisuses(contribution, script)
	if len(misuses) != 2 || misuses[0].Kind != "-e" || misuses[1].Kind != "heredoc" {
		t.Fatalf("stacked shell misuses = %#v, want -e then heredoc", misuses)
	}
	wrapperInput := misuseWarningProjection(shellInterpreterWrapperWarning(misuses[0]))
	heredocInput := misuseWarningProjection(shellInterpreterWrapperWarning(misuses[1]))

	call := func(t *testing.T, input string) string {
		t.Helper()
		transform, _, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
		visible, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
			"status": "completed",
			"output": []any{map[string]any{
				"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
				"name": "shell", "input": input, "status": "completed",
			}},
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
		if len(response.Output) != 1 || jsonString(response.Output[0], "name") != "exec" {
			t.Fatalf("stacked shell carrier = %s", visible)
		}
		return jsonString(response.Output[0], "input")
	}

	direct := call(t, script)
	if !strings.Contains(direct, wrapperInput+heredocInput+codeModeMetadataProjection) {
		t.Fatalf("direct shell warnings did not stack in order: %q", direct)
	}

	command := "curl -fsSL 'https://example.com' |\n" + script
	recoveredInput := "const result = await tools.exec_command({\"cmd\":" +
		jsonQuoted(command) +
		",\"login\":false});\ntext(JSON.stringify(result));"
	recovered := call(t, recoveredInput)
	wantPrefix := misuseWarningProjection(lunaShellRecoveryWarning) + wrapperInput + heredocInput
	if recovered != wantPrefix+recoveredInput {
		t.Fatalf("recovered shell warnings did not stack in order:\n%s", recovered)
	}
}

func TestMisuseWarningsRecordDistinctInputOverhead(t *testing.T) {
	dataDirectory := t.TempDir()
	var records []hpatchMetricRecord
	translator := metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
			return []byte(testTranslatedPatch), nil
		},
		record: func(ctx context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return hpatch.RecordHostMetrics(ctx, dataDirectory, record.HostMetricRecord)
		},
	}
	nativeWarningInput := misuseWarningProjection(nativeExecCommandWarning)
	lunaWarningInput := misuseWarningProjection(lunaShellRecoveryWarning)
	interpreterMisuses := shellInterpreterWrapperMisuses(
		toolContribution{PluginID: "builtin.shell", Name: "shell"},
		"python3 -c 'print(1)'",
	)
	if len(interpreterMisuses) == 0 {
		t.Fatal("interpreter-wrapper metric warning was not detected")
	}
	interpreterWarningInput := misuseWarningProjection(shellInterpreterWrapperWarning(interpreterMisuses[0]))
	cases := []struct {
		name       string
		toolName   string
		input      string
		warning    string
		toolCalls  uint64
		recoveries uint64
	}{
		{
			name:       "native exec_command",
			toolName:   "exec",
			input:      "const result = await tools.exec_command({\"cmd\":\"printf ok\"});\ntext(result.output);",
			warning:    nativeWarningInput,
			toolCalls:  0,
			recoveries: 0,
		},
		{
			name:       "Luna carrier",
			toolName:   "shell",
			input:      "const result = await tools.exec_command({\"cmd\":\"printf ok\",\"login\":false});\ntext(result.output);",
			warning:    lunaWarningInput,
			toolCalls:  1,
			recoveries: 1,
		},
		{
			name:       "interpreter wrapper",
			toolName:   "shell",
			input:      "python3 -c 'print(1)'",
			warning:    interpreterWarningInput,
			toolCalls:  1,
			recoveries: 0,
		},
	}
	var gotTotal, wantTotal, wantRecoveryTotal uint64
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			transform, _, _, _ := newHPatchTestTransform(t, translator)
			_, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed",
				"output": []any{map[string]any{
					"type": "custom_tool_call", "name": test.toolName, "call_id": test.name, "input": test.input,
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("metric records = %d, want 1", len(records))
			}
			record := records[0]
			records = nil
			want, err := hpatch.ClassifyHostMetrics(hpatch.HostMetricInput{MisuseWarning: test.warning})
			if err != nil {
				t.Fatal(err)
			}
			if record.MisuseWarningInputTokens != want.MisuseWarningInputTokens {
				t.Fatalf("misuse warning tokens = %d, want %d", record.MisuseWarningInputTokens, want.MisuseWarningInputTokens)
			}
			var calls, translated uint64
			for _, metric := range record.ToolMetrics {
				calls += metric.Calls
				translated += metric.TranslatedTokens
			}
			if calls != test.toolCalls {
				t.Fatalf("translated tool calls = %d, want %d", calls, test.toolCalls)
			}
			if test.toolCalls != 0 && translated == 0 {
				t.Fatal("translated tool metrics lost their translated tokens")
			}
			wantRecoveryTotal += test.recoveries
			gain, err := hpatch.LoadGainMetrics(dataDirectory)
			if err != nil {
				t.Fatal(err)
			}
			var lunaMisuse uint64
			for _, recovery := range gain.Recoveries {
				if recovery.Name == "luna misuse" {
					lunaMisuse = recovery.Count
				}
			}
			if lunaMisuse != wantRecoveryTotal {
				t.Fatalf("luna misuse recoveries = %d, want %d", lunaMisuse, wantRecoveryTotal)
			}
			gotTotal += record.MisuseWarningInputTokens
			wantTotal += want.MisuseWarningInputTokens
		})
	}
	if gotTotal != wantTotal {
		t.Fatalf("misuse warning total = %d, want %d", gotTotal, wantTotal)
	}
}

func TestNativeExecMisuseWarningMeteredOnceAcrossStreamLifecycle(t *testing.T) {
	var records []hpatchMetricRecord
	transform, _, _, _ := newHPatchTestTransform(t, metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
			return []byte(testTranslatedPatch), nil
		},
		record: func(_ context.Context, record hpatchMetricRecord) error {
			records = append(records, record)
			return nil
		},
	})
	item := map[string]any{
		"type": "custom_tool_call", "id": "item-native", "call_id": "call-native",
		"name": "exec", "input": "const result = await tools.exec_command({\"cmd\":\"printf ok\"});\ntext(result.output);",
	}
	for _, event := range []map[string]any{
		{"type": "response.output_item.added", "item": map[string]any{
			"type": "custom_tool_call", "id": "item-native", "call_id": "call-native",
			"name": "exec", "input": "", "status": "in_progress",
		}},
		{"type": "response.custom_tool_call_input.done", "item_id": "item-native", "input": item["input"]},
		{"type": "response.output_item.done", "item": item},
		{"type": "response.completed", "response": map[string]any{
			"status": "completed", "output": []any{item},
		}},
	} {
		if _, err := transform.TransformSSE(mustTestJSON(t, event)); err != nil {
			t.Fatal(err)
		}
	}
	if len(records) != 1 {
		t.Fatalf("native warning metric records = %d, want 1", len(records))
	}
	warningInput := misuseWarningProjection(nativeExecCommandWarning)
	want, err := hpatch.ClassifyHostMetrics(hpatch.HostMetricInput{MisuseWarning: warningInput})
	if err != nil {
		t.Fatal(err)
	}
	if records[0].MisuseWarningInputTokens != want.MisuseWarningInputTokens {
		t.Fatalf("native warning tokens = %d, want %d", records[0].MisuseWarningInputTokens, want.MisuseWarningInputTokens)
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
	var arguments struct {
		Command string `json:"cmd"`
	}
	decodeExecCarrierArguments(t, carrierInput, &arguments)
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

func TestDirectBashExecCommand(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		want      string
		ok        bool
	}{
		{name: "single line", arguments: []string{"bash", "foo"}, want: "foo", ok: true},
		{name: "final newline", arguments: []string{"bash", "foo\n"}, want: "foo", ok: true},
		{name: "redundant errexit", arguments: []string{"bash", "set -e\nfoo\n"}, want: "foo", ok: true},
		{
			name:      "meaningful options",
			arguments: []string{"bash", "set -euo pipefail\nfoo\n"},
			want:      "set -euo pipefail\nfoo",
			ok:        true,
		},
		{
			name:      "multiple commands",
			arguments: []string{"bash", "set -e\nfalse; echo survived\n"},
			want:      "set -e\nfalse; echo survived",
			ok:        true,
		},
		{name: "interpreter arguments", arguments: []string{"bash", "-x", "foo"}},
		{name: "other interpreter", arguments: []string{"python3", "foo"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := directBashExecCommand(test.arguments)
			if got != test.want || ok != test.ok {
				t.Fatalf("directBashExecCommand(%q) = %q, %t; want %q, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
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
	if strings.Count(carrierInput, "tools.exec_command(") != 1 || strings.Contains(carrierInput, "write_stdin") {
		t.Fatalf("shell carrier did not preserve one yielded execution: %s", carrierInput)
	}
	var arguments struct {
		Command string `json:"cmd"`
		Workdir string `json:"workdir"`
		TTY     bool   `json:"tty"`
		Login   bool   `json:"login"`
	}
	decodeExecCarrierArguments(t, carrierInput, &arguments)
	if arguments.Command != "printf ok" || arguments.Workdir != "/tmp/example" ||
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

func TestShellExecCarriersForwardNativeResultWithoutPolling(t *testing.T) {
	carrierInput, err := workerTemplateExecInputWithParams(
		"shell",
		[]string{"python3", "print('ok')"},
		"before | {.} | after",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(carrierInput, "tools.exec_command(") != 1 ||
		!strings.HasSuffix(carrierInput, "text(JSON.stringify(result));") ||
		strings.Contains(carrierInput, "write_stdin") {
		t.Fatalf("shell template carrier did not forward one native result: %s", carrierInput)
	}

	plainInput, err := workerExecInputWithParams("hread", []string{"line.txt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(plainInput, "text(result.output);") {
		t.Fatalf("non-shell carrier output projection changed: %s", plainInput)
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
			Directories: map[string]json.RawMessage{workspace: nil},
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

	history, err := second.translateRecovery("call-recovery", "C1:ffff drop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.translationError, "no rejected hpatch script to recover") {
		t.Fatalf("cross-workspace recovery history = %+v", history)
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
	if err := proxy.reconcileInputPrefix(&request, "session"); err != nil {
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
	if err := proxy.reconcileInputPrefix(&changed, "session"); err == nil {
		t.Fatal("changed replay was accepted")
	}
	unrelated, _ := parseResponsesRequest([]byte(`{"input":[{"type":"custom_tool_call","name":"exec","call_id":"native","input":"unchanged"}]}`))
	before := bytes.Clone(unrelated.fields["input"])
	if err := proxy.reconcileInputPrefix(&unrelated, "session"); err != nil || !bytes.Equal(before, unrelated.fields["input"]) {
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

func TestHPatchAlreadySatisfiedUsesDiagnosticCarrier(t *testing.T) {
	translator := hpatchResultTranslatorFunc(func(context.Context, string, string) (hpatchTranslationResult, error) {
		return hpatchTranslationResult{
			report: "in file.txt\nlast none\n",
			change: hpatch.HostChange{AlreadySatisfied: true},
		}, nil
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	history, err := transform.translate("call-noop", "in file.txt\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !history.alreadySatisfied || strings.Contains(history.carrierInput(), "apply_patch") ||
		history.carrierInput() != hpatchDiagnosticExecInput("in file.txt\nlast none\n") {
		t.Fatalf("already-satisfied history = %+v, carrier = %s", history, history.carrierInput())
	}
}

func TestHPatchStreamingTerminalFinalizesRequestAccounting(t *testing.T) {
	records := 0
	transform, _, _, _ := newHPatchTestTransform(t, metricsObservingTranslator{
		translate: func(context.Context, string, string) ([]byte, error) {
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
		translate: func(context.Context, string, string) ([]byte, error) {
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

func TestNonHPatchHistoryIsExcludedFromRecovery(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-H": {
			toolName: hpatchToolName, script: testHPatchScript,
			translationError: "rejected", evaluatorRejected: true, sequence: 1,
		},
		"call-S": {
			toolName: "shell", script: `hread file.txt`,
			report: "8ed3: alpha\n", sequence: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := proxy.recoverableHistory("session")
	if err != nil {
		t.Fatal(err)
	}
	if history.toolName != hpatchToolName || history.script != testHPatchScript {
		t.Fatalf("recoverable history = %+v", history)
	}

	transform := &hpatchResponseTransform{
		proxy:            proxy,
		sessionID:        "session",
		historySessionID: "session",
		local: map[string]hpatchHistory{
			"call-local-shell": {
				toolName: "shell",
				script:   `hgrep alpha .`,
				sequence: 1,
			},
		},
	}
	history, err = transform.recoveryHistory()
	if err != nil {
		t.Fatal(err)
	}
	if history.toolName != hpatchToolName || history.script != testHPatchScript {
		t.Fatalf("recovery after local read-only call = %+v", history)
	}
}

func TestHPatchNonEvaluatorFailureDoesNotBecomeRecoveryBaseline(t *testing.T) {
	calls := 0
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
		calls++
		return nil, errors.New("translator failed")
	}))
	first, err := transform.translate("call-1", testHPatchScript, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.evaluatorRejected || strings.Contains(first.translationError, "Use hpatch without `in`") {
		t.Fatalf("non-evaluator failure exposed recovery guidance: %+v", first)
	}
	second, err := transform.translateRecovery("call-2", "C1:ffff drop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.unevaluated ||
		!strings.Contains(second.translationError, "did not produce an evaluator rejection") ||
		calls != 1 {
		t.Fatalf("recovery after non-evaluator failure = %+v, translator calls %d", second, calls)
	}
}

func TestHPatchUnevaluatedRecoveryRunsOutcomeHookOnce(t *testing.T) {
	dataDirectory := t.TempDir()
	outcomePath := filepath.Join(t.TempDir(), "outcome.txt")
	settings := fmt.Sprintf(
		`{"hooks":{"outcome":["printf '%%s' {{shellquote .Stage}}'|'{{shellquote .Outcome}}'|'{{shellquote .ToolName}}'|'{{.EmittedBytes}}'|'{{.EvaluatedBytes}} > %s"]}}`,
		shellQuoteArgument(outcomePath),
	)
	if err := os.WriteFile(filepath.Join(dataDirectory, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	transform, _, _, _ := newHPatchTestTransformWithProxy(
		t,
		newManagedHPatchProxyWithDataDirectory(t, newInProcessHPatchTranslator(dataDirectory), dataDirectory),
	)
	payload := "C1:ffff drop"
	history, err := transform.translateRecovery("call-recovery", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !history.unevaluated {
		t.Fatalf("recovery history = %+v", history)
	}
	got, err := os.ReadFile(outcomePath)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("unevaluated|rejected|hpatch_recover|%d|0", len(payload))
	if string(got) != want {
		t.Fatalf("outcome hook = %q, want %q", got, want)
	}
}

func testRecoveryRow(t *testing.T, text string, row int) string {
	t.Helper()
	reference := hpatch.TextReferences(text, row)
	fields := strings.Fields(reference)
	if len(fields) == 0 {
		t.Fatalf("no row %d reference in %q", row, text)
	}
	return fields[0]
}

func TestHPatchRecoveryRetainsCorrelationAndRebuildsBeforeTranslation(t *testing.T) {
	base := "new created.txt\ntype \"bad\"\n"
	want := "new created.txt\ntype \"payload\"\n"
	calls := 0
	var evaluated string
	translator := hpatchResultTranslatorFunc(func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			return hpatchTranslationResult{
				diagnostic: "type: command 2, reason rejected: bad value\n",
				rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
			}, errors.New("rejected")
		}
		evaluated = script
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
	})
	transform, proxy, _, _ := newHPatchTestTransform(t, translator)
	first, err := transform.translate("call-1", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := recoveryCommands(base)[1].handle + ` value "payload"` + "\n"
	second, err := transform.translateRecovery("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.correlationID != "call-1" || first.attempt != 1 ||
		second.translationError != "" || second.correlationID != "call-1" || second.attempt != 2 {
		t.Fatalf("recovery metadata: first=%+v second=%+v", first, second)
	}
	if calls != 2 || evaluated != want {
		t.Fatalf("translations = %d, evaluated script = %q, want %q", calls, evaluated, want)
	}
	if _, ok := proxy.history(transform.historySessionID, "call-1"); ok {
		t.Fatal("local history committed before response completion")
	}
}

func TestHPatchRecoveryRerejectionExposesCurrentHandles(t *testing.T) {
	base := "new created.txt\ntype \"bad\"\n"
	rebuilt := "new created.txt\ntype \"worse\"\n"
	translator := hpatchResultTranslatorFunc(func(_ context.Context, _ string, _ string) (hpatchTranslationResult, error) {
		return hpatchTranslationResult{
			diagnostic: "type: command 2, reason rejected: bad value\n",
			rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
		}, errors.New("rejected")
	})
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	first, err := transform.translate("call-1", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := recoveryCommands(base)[1].handle + ` value "worse"` + "\n"
	second, err := transform.translateRecovery("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, history := range []hpatchHistory{first, second} {
		if strings.Count(history.translationError, "Current rejected-script command manifest (complete):") != 1 ||
			strings.Contains(history.translationError, "Use hpatch without `in`") ||
			strings.Contains(history.translationError, "accept") {
			t.Fatalf("recovery guidance = %q", history.translationError)
		}
	}
	if !strings.Contains(second.translationError, "This re-rejection changed no workspace file") ||
		!strings.Contains(second.translationError, "Every C... and V... handle from earlier diagnostics is stale") {
		t.Fatalf("re-rejection lacks stale-handle guidance:\n%s", second.translationError)
	}
	if want := recoveryCommands(rebuilt)[1].handle; !strings.Contains(second.translationError, want) {
		t.Fatalf("re-rejection lacks current command handle %q:\n%s", want, second.translationError)
	}
}

func TestHPatchRecoveryMutatesHeredocBodyByOrdinaryRow(t *testing.T) {
	base := "new file.go\ntype <<PATCH\npackage p\nvar =\nvar tail = 2\nPATCH\n"
	want := "new file.go\ntype <<PATCH\npackage p\nvar fixed = 1\nvar tail = 2\nPATCH\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchResultTranslatorFunc(func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			return hpatchTranslationResult{
				diagnostic: "type: command 2, reason rejected: bad value\n",
				rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
			}, errors.New("rejected")
		}
		evaluated = script
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	payload := recoveryCommands(base)[1].handle + " " + recoveryCommands(base)[1].valueRows[1].handle + ` value "var fixed = 1"` + "\n"
	result, err := transform.translateRecovery("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.translationError != "" || calls != 2 || evaluated != want {
		t.Fatalf("recovery = %+v, translations %d, evaluated %q", result, calls, evaluated)
	}
}

func TestHPatchRecoveryFixesAllEmittedTargetsAtomically(t *testing.T) {
	base := "new first.go\n" +
		"type <<PATCH\n" +
		"package p\n" +
		"var =\n" +
		"var middle = 2\n" +
		"var =\n" +
		"PATCH\n" +
		"new second.go\n" +
		"type <<PATCH\n" +
		"package p\n" +
		"var =\n" +
		"PATCH\n"
	want := strings.ReplaceAll(base, "var =", "var fixed = 1")
	rejections := []hpatch.HostRejection{
		{Command: 2, SourceLine: 2, Operation: "type", ValueLine: 2},
		{Command: 2, SourceLine: 2, Operation: "type", ValueLine: 4},
		{Command: 4, SourceLine: 9, Operation: "type", ValueLine: 2},
	}

	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchResultTranslatorFunc(func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			return hpatchTranslationResult{
				diagnostic: "type: command 2, reason language-syntax: 2 distinct syntax failures\n" +
					"type: command 4, reason language-syntax: expected declaration\n",
				rejections: rejections,
			}, errors.New("rejected")
		}
		evaluated = script
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
	}))
	first, err := transform.translate("call-1", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstCommands := recoveryCommands(base)
	for _, want := range []string{
		firstCommands[1].valueRows[1].handle,
		firstCommands[1].valueRows[3].handle,
		firstCommands[3].valueRows[1].handle,
	} {
		if !strings.Contains(first.translationError, want) {
			t.Fatalf("guidance lacks value handle %q:\n%s", want, first.translationError)
		}
	}

	payload := strings.Join([]string{
		firstCommands[1].handle + " " + firstCommands[1].valueRows[1].handle + ` value "var fixed = 1"`,
		firstCommands[1].handle + " " + firstCommands[1].valueRows[3].handle + ` value "var fixed = 1"`,
		firstCommands[3].handle + " " + firstCommands[3].valueRows[1].handle + ` value "var fixed = 1"`,
	}, "\n") + "\n"
	result, err := transform.translateRecovery("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.translationError != "" || calls != 2 || evaluated != want {
		t.Fatalf("recovery = %+v, translations %d, evaluated %q, want %q", result, calls, evaluated, want)
	}
}

func TestHPatchRecoveryIntegratesAggregatedEngineRejections(t *testing.T) {
	base := "new first.go\n" +
		"type <<PATCH\n" +
		"package p\n" +
		"var =\n" +
		"var middle = 2\n" +
		"var =\n" +
		"PATCH\n" +
		"new second.go\n" +
		"type <<PATCH\n" +
		"package p\n" +
		"var =\n" +
		"PATCH\n"
	transform, _, _, _ := newHPatchTestTransform(t, inProcessHPatchTranslator{dataDirectory: t.TempDir()})
	first, err := transform.translate("call-1", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstCommands := recoveryCommands(base)
	for _, want := range []string{
		firstCommands[1].valueRows[1].handle,
		firstCommands[1].valueRows[3].handle,
		firstCommands[3].valueRows[1].handle,
	} {
		if !strings.Contains(first.translationError, want) {
			t.Fatalf("guidance lacks value handle %q:\n%s", want, first.translationError)
		}
	}

	payload := strings.Join([]string{
		firstCommands[1].handle + " " + firstCommands[1].valueRows[1].handle + ` value "var fixed = 1"`,
		firstCommands[1].handle + " " + firstCommands[1].valueRows[3].handle + ` value "var fixed = 1"`,
		firstCommands[3].handle + " " + firstCommands[3].valueRows[1].handle + ` value "var fixed = 1"`,
	}, "\n") + "\n"
	recovered, err := transform.translateRecovery("call-2", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.translationError != "" || len(recovered.patch) == 0 ||
		strings.Count(string(recovered.patch), "+var fixed = 1") != 3 {
		t.Fatalf("recovered translation = %+v", recovered)
	}
}

func TestHPatchFailedRecoveryPreservesEvaluatedBaseline(t *testing.T) {
	base := "new file.txt\ntype \"old\"\n"
	want := "new file.txt\ntype \"fixed\"\n"
	calls := 0
	var evaluated string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchResultTranslatorFunc(func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
		calls++
		if calls == 1 {
			return hpatchTranslationResult{
				diagnostic: "type: command 2, reason rejected: bad value\n",
				rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
			}, errors.New("rejected")
		}
		evaluated = script
		return hpatchTranslationResult{patch: []byte(testTranslatedPatch)}, nil
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	failed, err := transform.translateRecovery("call-2", "C2:ffff drop\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !failed.unevaluated || failed.correlationID != "call-1" || failed.attempt != 2 ||
		!strings.Contains(failed.translationError, `command handle "C2:ffff" is stale`) || calls != 1 {
		t.Fatalf("failed recovery = %+v, translations %d", failed, calls)
	}
	payload := recoveryCommands(base)[1].handle + ` value "fixed"` + "\n"
	recovered, err := transform.translateRecovery("call-3", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.translationError != "" || recovered.correlationID != "call-1" ||
		recovered.attempt != 3 || calls != 2 || evaluated != want {
		t.Fatalf("recovered = %+v, translations %d, evaluated %q", recovered, calls, evaluated)
	}
}

func TestHPatchRecoveryUsesLatestRejectedRecoveryInSameResponse(t *testing.T) {
	base := "new file.txt\ntype \"old\"\n"
	firstRebuilt := "new file.txt\ntype \"first\"\n"
	secondRebuilt := "new file.txt\ntype \"second\"\n"
	var evaluated []string
	transform, _, _, _ := newHPatchTestTransform(t, hpatchResultTranslatorFunc(func(_ context.Context, _ string, script string) (hpatchTranslationResult, error) {
		evaluated = append(evaluated, script)
		return hpatchTranslationResult{
			diagnostic: "type: command 2, reason rejected: bad value\n",
			rejections: []hpatch.HostRejection{{Command: 2, SourceLine: 2, Operation: "type"}},
		}, errors.New("rejected")
	}))
	if _, err := transform.translate("call-1", base, nil); err != nil {
		t.Fatal(err)
	}
	baseCommand := recoveryCommands(base)[1]
	first, err := transform.translateRecovery("call-2", baseCommand.handle+` value "first"`+"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstCommand := recoveryCommands(firstRebuilt)[1]
	second, err := transform.translateRecovery("call-3", firstCommand.handle+` value "second"`+"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluated) != 3 || evaluated[1] != firstRebuilt || evaluated[2] != secondRebuilt {
		t.Fatalf("evaluated scripts = %q", evaluated)
	}
	if first.correlationID != "call-1" || second.correlationID != first.correlationID ||
		first.attempt != 2 || second.attempt != 3 || !second.evaluatorRejected {
		t.Fatalf("recovery chain = %+v then %+v", first, second)
	}
}

func TestHPatchRetainedProxyRejectionAdvancesRecoveryAttempt(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{
		"call-1": {
			toolName: hpatchToolName, script: testHPatchScript, translationError: "rejected",
			evaluatorRejected: true, correlationID: "call-1", attempt: 1, sequence: 1,
		},
		"call-2": {
			toolName: hpatchToolName, script: `type 2:ffff "bad"` + "\n",
			translationError: "stale", unevaluated: true,
			correlationID: "call-1", attempt: 2, sequence: 2,
		},
	}); err != nil {
		t.Fatal(err)
	}
	base, err := proxy.recoverableHistory("session")
	if err != nil {
		t.Fatal(err)
	}
	if base.attempt != 1 {
		t.Fatalf("recoverable base attempt = %d, want 1", base.attempt)
	}
	if got := proxy.latestRecoveryAttempt("session", "call-1"); got != 2 {
		t.Fatalf("latest retained chain attempt = %d, want 2", got)
	}
}

func TestHPatchTranslationFailureReturnsImmediateDiagnosticExec(t *testing.T) {
	transform, proxy, _, workspace := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
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
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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
	transform, _, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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
	transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(ctx context.Context, _ string, _ string) ([]byte, error) {
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
				toolName:          hpatchToolName,
				script:            callID,
				translationError:  "rejected",
				evaluatorRejected: true,
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
	latest, err := proxy.recoverableHistory("session")
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
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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
		transform, proxy, _, _ := newHPatchTestTransform(t, hpatchTranslatorFunc(func(context.Context, string, string) ([]byte, error) {
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

func TestInProcessHPatchTranslatorUsesBaseDirectoryWithoutConfinement(t *testing.T) {
	translator := newInProcessHPatchTranslator(t.TempDir())
	parent := t.TempDir()
	directory := filepath.Join(parent, "base")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "existing.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	translated, err := translator.Translate(t.Context(), directory, "in existing.txt\ntype+ 1:a793 \"inserted\\n\"\ntype 3:b1e9 \"THIRD\"\n")
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
	for _, required := range []string{
		"refs 2 type+ existing.txt\n",
		"refs 3 type existing.txt\n",
		"2:e8dd inserted\n",
		"4:1186 THIRD\n",
	} {
		if !strings.Contains(translated.report, required) {
			t.Fatalf("translation report %q lacks %q", translated.report, required)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first\nsecond\nthird\n" {
		t.Fatalf("translation mutated file to %q", content)
	}

	conflict := "in existing.txt\ntype 1:a793..2:1636 \"replacement\"\ntype 2:1636..3:b1e9 \"overlap\"\n"
	if translated, err := translator.Translate(t.Context(), directory, conflict); err == nil || !strings.Contains(translated.diagnostic, "conflicts with edit") {
		t.Fatalf("overlapping baseline translation error = %v", err)
	}

	outsidePath := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"../outside.txt", outsidePath} {
		translated, err := translator.Translate(t.Context(), directory, "in "+target+"\ntype 1:3120 \"changed\"\n")
		if err != nil {
			t.Fatalf("translate %q: %v", target, err)
		}
		wantPath := filepath.Clean(target)
		if !bytes.Contains(translated.patch, []byte("*** Update File: "+wantPath)) {
			t.Fatalf("translation for %q does not contain cleaned target %q: %s", target, wantPath, translated.patch)
		}
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
