package router

// Source: hpatch_proxy.go:1:1226 hpatch discovery, request rewriting, translation, replay, and response restoration.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/router/toolplugin"
)

const (
	hpatchToolName = "hpatch"

	applyPatchToolName           = "apply_patch"
	hpatchApplyExecMarker        = "// hpatch-proxy: apply translated patch\n"
	maxHPatchScriptBytes         = 1 << 20
	maxHPatchPatchBytes          = 16 << 20
	maxHPatchHistorySessionBytes = 32 << 20
	maxHPatchHistoryGlobalBytes  = 128 << 20

	maxHPatchPendingCalls = 128
)

var (
	errHPatchCapacity         = errors.New("hpatch proxy capacity exceeded")
	errHPatchWorkspaceChanged = errors.New("hpatch workspace changed during translation")
)

type hpatchTranslationResult struct {
	patch       []byte
	report      string
	diagnostic  string
	corrections map[int]string
	rejections  []hpatch.HostRejection
	invocation  hpatch.InvocationMetrics
}

type hpatchTranslator interface {
	Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error)
	RecordMetrics(ctx context.Context, record hpatchMetricRecord) error
	ToolDescription() string
}

type inProcessHPatchTranslator struct {
	dataDirectory string
}

func hpatchMetricsDirectory() (string, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determine hpatch metrics directory: %w", err)
	}
	return filepath.Join(configDirectory, "hpatch"), nil
}

func newInProcessHPatchTranslator(dataDirectory string) hpatchTranslator {
	return inProcessHPatchTranslator{dataDirectory: dataDirectory}
}

// notifyingHPatchTranslator refreshes dashboard subscribers after durable gain metrics change.
type notifyingHPatchTranslator struct {
	inner   hpatchTranslator
	metrics *metricsStore
}

func (t notifyingHPatchTranslator) ToolDescription() string {
	return t.inner.ToolDescription()
}

func (t notifyingHPatchTranslator) Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error) {
	return t.inner.Translate(ctx, workspace, script)
}

func (t notifyingHPatchTranslator) RecordMetrics(ctx context.Context, record hpatchMetricRecord) error {
	if err := t.inner.RecordMetrics(ctx, record); err != nil {
		return err
	}
	if t.metrics != nil {
		t.metrics.recordHPatch(record)
	}
	return nil
}

func (inProcessHPatchTranslator) ToolDescription() string {
	return hpatch.ToolDescription()
}

func (t inProcessHPatchTranslator) Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error) {
	if !workspace.unchanged() {
		return hpatchTranslationResult{}, errHPatchWorkspaceChanged
	}
	translated, err := hpatch.TranslateForHost(ctx, hpatch.Workspace{Root: workspace.root}, script, t.dataDirectory)
	if !workspace.unchanged() {
		return hpatchTranslationResult{}, errHPatchWorkspaceChanged
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return hpatchTranslationResult{}, contextErr
	}
	if len(translated.Patch) > maxHPatchPatchBytes {
		return hpatchTranslationResult{}, fmt.Errorf("%w: hpatch translation output exceeds its configured bound", errHPatchCapacity)
	}
	corrections := make(map[int]string, len(translated.Corrections))
	for _, correction := range translated.Corrections {
		corrections[correction.Command] = correction.Replacement
	}
	result := hpatchTranslationResult{
		patch:       translated.Patch,
		report:      translated.Report,
		diagnostic:  translated.Diagnostic,
		corrections: corrections,
		rejections:  slices.Clone(translated.Rejections),
		invocation:  translated.Invocation,
	}
	return result, err
}

func (t inProcessHPatchTranslator) RecordMetrics(ctx context.Context, record hpatchMetricRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return hpatch.RecordHostMetrics(ctx, t.dataDirectory, record.HostMetricRecord)
}

type hpatchHistory struct {
	toolName string
	pluginID string

	script string
	root   string
	// evaluated is the script hpatch actually received when it differs from the
	// model's payload, which happens when the payload was a correction. Replay
	// must restore what the model emitted, while a following correction must
	// resolve command indices against what was evaluated.
	evaluated      string
	patch          string
	carrierName    string
	carrierKind    codeModeCarrierKind
	carrierPayload string

	report           string
	translationError string
	corrections      map[int]string
	correlationID    string
	attempt          int
	upstreamItem     map[string]json.RawMessage
	bytes            int
	// unevaluated marks a call the proxy rejected before hpatch saw it, which
	// happens when a correction payload names no usable script. Such a call
	// changed nothing and has no script of its own, so a following correction
	// looks past it to the rejection it was trying to repair.
	unevaluated bool
	// sequence orders retained calls within a session. Calls are keyed by ID in
	// an unordered map, so a correction that must resolve "the script that was
	// just rejected" needs an explicit order.
	sequence uint64
}

type hpatchHistorySession struct {
	calls map[string]hpatchHistory
	bytes int
	// nextSequence is the order to assign the session's next retained call.
	nextSequence uint64
	lastUsed     uint64
}

type hpatchProxy struct {
	translator hpatchTranslator
	registry   *toolRegistry

	mu              sync.RWMutex
	sessions        map[string]*hpatchHistorySession
	activeSessions  map[string]int
	historyBytes    int
	sessionSequence uint64
	closed          bool
}

func newHPatchProxy(translator hpatchTranslator, registry *toolRegistry) *hpatchProxy {
	if translator == nil || registry == nil {
		return nil
	}
	return &hpatchProxy{
		translator:     translator,
		registry:       registry,
		sessions:       make(map[string]*hpatchHistorySession),
		activeSessions: make(map[string]int),
	}
}

func (p *hpatchProxy) activateSession(sessionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("hpatch response proxy is closed")
	}
	p.activeSessions[sessionID]++
	if session := p.sessions[sessionID]; session != nil {
		p.touchSession(session)
	}
	return nil
}

func (p *hpatchProxy) deactivateSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeSessions[sessionID] <= 1 {
		delete(p.activeSessions, sessionID)
		return
	}
	p.activeSessions[sessionID]--
}

func (p *hpatchProxy) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	clear(p.sessions)
	clear(p.activeSessions)
	p.historyBytes = 0
	return nil
}

func shellQuoteArgument(value string) string {
	if value != "" {
		safe := true
		for _, char := range value {
			switch {
			case 'a' <= char && char <= 'z',
				'A' <= char && char <= 'Z',
				'0' <= char && char <= '9',
				strings.ContainsRune("_@%+=:,./-", char):
			default:
				safe = false
			}
			if !safe {
				break
			}
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (p *hpatchProxy) touchSession(session *hpatchHistorySession) {
	p.sessionSequence++
	session.lastUsed = p.sessionSequence
}

// hpatchCorrectionInstructions documents the correction payload the proxy accepts
// in place of a complete script. It is returned with the first rejection in a
// correction chain, when the protocol is actionable, rather than installed in
// every request's tool definition.
const hpatchCorrectionInstructions = `
Repairing a rejected script:
  When the latest hpatch evaluation was rejected, you may send indexed operations
  instead of the complete script:

    INDEX: COMMAND
    INDEX: accept
    -INDEX
    +INDEX: COMMAND
    INDEX+: COMMAND

  A command whose value uses the fixed <<PATCH frame also exposes its physical
  value rows as INDEX.ROW. Repair only the malformed row without resending the
  unaffected multiline value with:

    INDEX.ROW: "VALUE"
    -INDEX.ROW
    +INDEX.ROW: "VALUE"
    INDEX.ROW+: "VALUE"

  Command operations replace, accept a displayed safe correction for, delete, insert
  before, or insert after a command. Indices count the nonblank command headers in the
  complete script evaluated for the latest rejection; they are not source-line numbers,
  indices into the first attempt, or indices into a compact correction payload. A fixed
  <<PATCH heredoc and its body count as one command. When an edit diagnostic reflects a
  bad span, correct the target in that mutation.

  ROW counts physical body rows between that command's fixed <<PATCH opener and closing
  PATCH line in the latest evaluated script; decoded inline multiline strings expose no
  rows. VALUE is one JSON-compatible quoted physical row. A value-row replacement
  preserves the original row terminator when VALUE omits one, while an explicit
  terminator wins. Insertions never synthesize a terminator, and deletion removes the
  row's terminator. A value-row replacement or insertion cannot materialize the exact
  PATCH delimiter row. Value rows cannot use accept. A whole-command replacement,
  acceptance, or deletion cannot be combined with a value-row mutation of the same command.
  Replace, accept, or delete an index at most once; repeated insertions retain payload order
  even if the anchor is deleted. If the mapping is uncertain, replace the complete command
  or resend the complete script. The rebuilt script is revalidated atomically.
`

func hpatchCorrectionGuidance(correction bool, corrections map[int]string) string {
	if correction {
		return ""
	}
	if len(corrections) == 0 {
		return hpatchCorrectionInstructions
	}
	return hpatchCorrectionInstructions + hpatchAcceptHint(corrections)
}

func hpatchAcceptHint(corrections map[int]string) string {
	commands := slices.Sorted(maps.Keys(corrections))
	var hint strings.Builder
	hint.WriteString("\nApply the displayed correction with:\n")
	for _, command := range commands {
		fmt.Fprintf(&hint, "%d: accept\n", command)
	}
	return hint.String()
}

type hpatchPendingCall struct {
	callID   string
	toolName string

	added []byte
}

type hpatchResponseTransform struct {
	ctx              context.Context
	proxy            *hpatchProxy
	sessionID        string
	historySessionID string
	sessionActive    bool

	originalTools             json.RawMessage
	originalToolsPresent      bool
	originalToolChoice        json.RawMessage
	originalToolChoicePresent bool
	pending                   map[string]hpatchPendingCall
	local                     map[string]hpatchHistory
	workspace                 routingWorkspace
	carriers                  codeModeCarrierCatalog

	installedToolDefinition  string
	installedToolBreakdown   []hpatch.HostToolDefinition
	codeModeToolName         string
	baselineDefinition       string
	execCommandDefinitions   []string
	requestAccountingClaimed bool

	// localSequence orders the calls translated during this turn, so a
	// correction resolves against the newest rejection rather than an arbitrary
	// map entry. Retained order is reassigned when the turn commits.
	localSequence    uint64
	historyCommitted bool
}

func (t *hpatchResponseTransform) Close() {
	if t == nil {
		return
	}
	if t.sessionActive {
		t.proxy.deactivateSession(t.historySessionID)
		t.sessionActive = false
	}
	t.workspace.close()
}

// validateHPatchCompactionRequest recognizes local Codex compaction requests,
// which stream through /responses without exposing model tools.
// Source: openai/codex codex-rs/core/src/compact.rs:228:273 and client.rs:795:881.
func validateHPatchCompactionRequest(request *parsedResponsesRequest, metadata codexTurnMetadata) error {
	var compaction struct {
		Trigger        string `json:"trigger"`
		Reason         string `json:"reason"`
		Implementation string `json:"implementation"`
		Phase          string `json:"phase"`
		Strategy       string `json:"strategy"`
	}
	if err := json.Unmarshal(metadata.Compaction, &compaction); err != nil || slices.ContainsFunc(
		[]string{compaction.Trigger, compaction.Reason, compaction.Implementation, compaction.Phase, compaction.Strategy},
		func(value string) bool { return strings.TrimSpace(value) == "" },
	) {
		return errors.New("hpatch rewrite requires valid compaction metadata")
	}
	if !request.streamResponse {
		return errors.New("hpatch compaction bypass requires a streaming request")
	}

	var tools []json.RawMessage
	if rawTools, exists := request.fields["tools"]; exists {
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return fmt.Errorf("decode compaction tools: %w", err)
		}
	}
	if len(tools) != 0 {
		return errors.New("hpatch compaction request cannot expose tools")
	}

	var items []json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &items); err != nil {
		return fmt.Errorf("decode compaction input: %w", err)
	}
	if len(items) == 0 {
		return errors.New("hpatch compaction request requires nonempty input")
	}
	for _, rawItem := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &item); err != nil || item == nil {
			return errors.New("hpatch compaction request contains a malformed input item")
		}
		if jsonString(item, "type") != "additional_tools" {
			continue
		}
		var additionalTools []json.RawMessage
		if err := json.Unmarshal(item["tools"], &additionalTools); err != nil {
			return fmt.Errorf("decode compaction additional tools: %w", err)
		}
		if len(additionalTools) != 0 {
			return errors.New("hpatch compaction request cannot expose tools")
		}
	}

	var toolChoice string
	if json.Unmarshal(request.fields["tool_choice"], &toolChoice) != nil || toolChoice != "auto" {
		return errors.New("hpatch compaction request requires automatic tool choice")
	}
	var parallelToolCalls bool
	if err := json.Unmarshal(request.fields["parallel_tool_calls"], &parallelToolCalls); err != nil || parallelToolCalls {
		return errors.New("hpatch compaction request requires disabled parallel tool calls")
	}
	return nil
}

func (p *hpatchProxy) prepareRequest(ctx context.Context, request *parsedResponsesRequest, sessionID string, metadata codexTurnMetadata, metadataValid bool) (*hpatchResponseTransform, error) {
	if metadataValid && metadata.RequestKind == "compaction" {
		if err := validateHPatchCompactionRequest(request, metadata); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if p == nil {
		return nil, errors.New("hpatch response proxy is unavailable")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("hpatch rewrite requires a valid session ID")
	}
	if !metadataValid || metadata.RequestKind != "turn" {
		return nil, errors.New("hpatch rewrite requires valid turn metadata")
	}
	workspace, ok := usableRoutingWorkspace(metadata.Workspaces)
	if !ok {
		return nil, errors.New("hpatch rewrite requires exactly one usable workspace")
	}
	originalTools, originalToolsPresent := request.fields["tools"]
	originalTools = bytes.Clone(originalTools)
	originalToolChoice, originalToolChoicePresent := request.fields["tool_choice"]
	originalToolChoice = bytes.Clone(originalToolChoice)
	carriers, err := buildCodeModeCarrierCatalog(request.fields, p.registry)
	if err != nil {
		workspace.close()
		return nil, err
	}
	installedTools, err := p.registry.specifications()
	if err != nil {
		workspace.close()
		return nil, err
	}
	baselineDefinition, execCommandDefinitions, codeModeToolName, replaced, err := replaceAdditionalToolsApplyPatch(request.fields, installedTools)
	if err != nil {
		workspace.close()
		return nil, err
	}
	if !replaced {
		workspace.close()
		return nil, errors.New("responses request cannot satisfy the required hpatch rewrite")
	}
	installedToolBreakdown := make([]hpatch.HostToolDefinition, len(installedTools))
	for index, contribution := range p.registry.ordered {
		installedToolBreakdown[index] = hpatch.HostToolDefinition{
			PluginID:   contribution.PluginID,
			ToolName:   contribution.Name,
			Definition: string(mustMarshalJSON(installedTools[index])),
		}
	}
	historySessionID := workspace.canonical + "\x00" + sessionID
	if err := p.activateSession(historySessionID); err != nil {
		workspace.close()
		return nil, err
	}
	if err := p.restoreInputPrefix(request, historySessionID); err != nil {
		p.deactivateSession(historySessionID)
		workspace.close()
		return nil, err
	}
	return &hpatchResponseTransform{
		ctx:              ctx,
		proxy:            p,
		sessionID:        sessionID,
		historySessionID: historySessionID,
		sessionActive:    true,

		originalTools:             originalTools,
		originalToolsPresent:      originalToolsPresent,
		originalToolChoice:        originalToolChoice,
		originalToolChoicePresent: originalToolChoicePresent,
		pending:                   make(map[string]hpatchPendingCall),
		local:                     make(map[string]hpatchHistory),
		workspace:                 workspace,
		carriers:                  carriers,

		installedToolDefinition: string(mustMarshalJSON(installedTools)),
		installedToolBreakdown:  installedToolBreakdown,
		codeModeToolName:        codeModeToolName,
		baselineDefinition:      baselineDefinition,
		execCommandDefinitions:  execCommandDefinitions,
	}, nil
}

type additionalToolsApplyPatchOwner struct {
	items               []json.RawMessage
	item                map[string]json.RawMessage
	itemIndex           int
	additionalTools     []map[string]json.RawMessage
	additionalToolIndex int
	tools               []map[string]json.RawMessage
	toolIndex           int
	nested              bool
	name                string

	strippedDescription string
	// baselineDefinition is the native apply_patch definition hpatch displaces.
	// hpatch's definition cost is only meaningful net of it.
	baselineDefinition     string
	execCommandDefinitions []string
}

func installedToolNames(tools []map[string]json.RawMessage) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[jsonString(tool, "name")] = struct{}{}
	}
	return names
}

// replaceAdditionalToolsApplyPatch rewrites the Code Mode exec tool from an
// app or CLI additional_tools owner and exposes the router's standalone tools.
func replaceAdditionalToolsApplyPatch(fields map[string]json.RawMessage, installedTools []map[string]json.RawMessage) (string, []string, string, bool, error) {
	var topTools []map[string]json.RawMessage
	if rawTools, exists := fields["tools"]; exists {
		if err := json.Unmarshal(rawTools, &topTools); err != nil {
			return "", nil, "", false, fmt.Errorf("decode responses tools: %w", err)
		}
	}
	installedNames := installedToolNames(installedTools)
	for _, tool := range topTools {
		name := jsonString(tool, "name")
		if _, exists := installedNames[name]; exists {
			return "", nil, "", false, fmt.Errorf("responses request already defines %s", name)
		}
		if name == applyPatchToolName || name == "exec" || name == "functions.exec" {
			return "", nil, "", false, fmt.Errorf("responses request exposes unsupported top-level %s", name)
		}
	}

	owner, err := findAdditionalToolsApplyPatch(fields, installedNames)
	if err != nil || owner == nil {
		return "", nil, "", false, err
	}
	if codeModeToolChoiceRestricted(fields, owner.name) {
		return "", nil, "", false, nil
	}
	if err := exposeStandaloneHPatch(fields, topTools, owner, installedTools); err != nil {
		return "", nil, "", false, err
	}
	return owner.baselineDefinition, owner.execCommandDefinitions, owner.name, true, nil
}

func findAdditionalToolsApplyPatch(fields map[string]json.RawMessage, installedNames map[string]struct{}) (*additionalToolsApplyPatchOwner, error) {
	var items []json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return nil, nil //nolint:nilerr // Unsupported input shapes are simply not Code Mode owners.
	}
	_, shellInstalled := installedNames["shell"]
	var owner *additionalToolsApplyPatchOwner
	claim := func(
		item map[string]json.RawMessage,
		itemIndex int,
		additionalTools []map[string]json.RawMessage,
		additionalToolIndex int,
		tools []map[string]json.RawMessage,
		toolIndex int,
		nested bool,
	) error {
		tool := tools[toolIndex]
		name := jsonString(tool, "name")
		if _, exists := installedNames[name]; exists {
			if nested {
				return fmt.Errorf("responses functions namespace defines %s", name)
			}
			return fmt.Errorf("responses additional_tools item defines direct %s", name)
		}
		if name == applyPatchToolName {
			if nested {
				return errors.New("responses functions namespace exposes direct apply_patch")
			}
			return errors.New("responses additional_tools item exposes unsupported flat apply_patch")
		}
		if name != "exec" {
			return nil
		}
		if owner != nil {
			return errors.New("responses request defines Code Mode exec more than once")
		}
		stripped, baseline, found, err := stripCodeModeApplyPatchSection(jsonString(tool, "description"))
		if err != nil {
			return err
		}
		if !found || jsonString(tool, "type") != "custom" {
			return nil
		}
		var execCommandDefinitions []string
		if shellInstalled {
			var definitions []string
			stripped, definitions, found, err = stripCodeModeExecCommandContract(stripped)
			if err != nil {
				return err
			}
			if found {
				execCommandDefinitions = append(execCommandDefinitions, definitions...)
			}
		}
		owner = &additionalToolsApplyPatchOwner{
			item:                   item,
			itemIndex:              itemIndex,
			additionalTools:        additionalTools,
			additionalToolIndex:    additionalToolIndex,
			tools:                  tools,
			toolIndex:              toolIndex,
			nested:                 nested,
			name:                   name,
			strippedDescription:    stripped,
			baselineDefinition:     baseline,
			execCommandDefinitions: execCommandDefinitions,
		}
		return nil
	}
	for itemIndex, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil || jsonString(item, "type") != "additional_tools" {
			continue
		}
		var additionalTools []map[string]json.RawMessage
		if json.Unmarshal(item["tools"], &additionalTools) != nil {
			continue
		}
		for additionalToolIndex, additionalTool := range additionalTools {
			name := jsonString(additionalTool, "name")
			if jsonString(additionalTool, "type") != "namespace" {
				if name == "functions.exec" {
					return nil, errors.New("responses additional_tools item exposes unsupported flat functions.exec")
				}
				if err := claim(item, itemIndex, additionalTools, additionalToolIndex, additionalTools, additionalToolIndex, false); err != nil {
					return nil, err
				}
				continue
			}
			if name == "exec" || name == "functions.exec" || name == applyPatchToolName {
				return nil, fmt.Errorf("responses additional_tools item exposes unsupported flat %s", name)
			}
			if _, exists := installedNames[name]; exists {
				return nil, fmt.Errorf("responses additional_tools item defines direct %s", name)
			}
			if name != "functions" {
				continue
			}

			var tools []map[string]json.RawMessage
			if json.Unmarshal(additionalTool["tools"], &tools) != nil {
				continue
			}
			for toolIndex := range tools {
				if err := claim(item, itemIndex, additionalTools, additionalToolIndex, tools, toolIndex, true); err != nil {
					return nil, err
				}
			}
		}
	}
	if owner != nil {
		owner.items = items
	}
	return owner, nil
}

func codeModeToolChoiceRestricted(fields map[string]json.RawMessage, codeToolName string) bool {
	var choice map[string]json.RawMessage
	if json.Unmarshal(fields["tool_choice"], &choice) != nil {
		return false
	}
	return jsonString(choice, "type") == "custom" && jsonString(choice, "name") == codeToolName
}

func exposeStandaloneHPatch(fields map[string]json.RawMessage, topTools []map[string]json.RawMessage, owner *additionalToolsApplyPatchOwner, installedTools []map[string]json.RawMessage) error {
	owner.tools[owner.toolIndex]["description"] = mustMarshalJSON(owner.strippedDescription)
	if owner.nested {
		encodedNestedTools, err := json.Marshal(owner.tools)
		if err != nil {
			return fmt.Errorf("encode functions namespace tools: %w", err)
		}
		owner.additionalTools[owner.additionalToolIndex]["tools"] = encodedNestedTools
	}
	encodedAdditionalTools, err := json.Marshal(owner.additionalTools)
	if err != nil {
		return fmt.Errorf("encode additional tools: %w", err)
	}
	owner.item["tools"] = encodedAdditionalTools
	encodedItem, err := json.Marshal(owner.item)
	if err != nil {
		return fmt.Errorf("encode additional_tools item: %w", err)
	}
	owner.items[owner.itemIndex] = encodedItem
	encodedInput, err := json.Marshal(owner.items)
	if err != nil {
		return fmt.Errorf("encode Responses input: %w", err)
	}
	topTools = append(topTools, installedTools...)
	encodedTopTools, err := json.Marshal(topTools)
	if err != nil {
		return fmt.Errorf("encode Responses tools: %w", err)
	}
	fields["input"] = encodedInput
	fields["tools"] = encodedTopTools
	return nil
}

func (t *hpatchResponseTransform) routesTool(name string) bool {
	if t == nil || t.proxy == nil {
		return false
	}
	_, ok := t.proxy.registry.contribution(name)
	return ok
}

func customGrammarTool(name, description, grammar string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"type":        mustMarshalJSON("custom"),
		"name":        mustMarshalJSON(name),
		"description": mustMarshalJSON(description),
		"format": mustMarshalJSON(map[string]string{
			"type":       "grammar",
			"syntax":     "lark",
			"definition": grammar,
		}),
	}
}

const codeModeApplyPatchHeading = "### `apply_patch`"
const codeModeExecCommandHeading = "### `exec_command`"
const codeModeExecCommandPlainHeading = "### exec_command"

type codeModeSectionMatcher func(string) (int, string)

func stripCodeModeSection(description string, findHeading codeModeSectionMatcher, valid func(string) bool, duplicateError string) (string, string, bool, error) {
	start, heading := findHeading(description)
	if start < 0 {
		return description, "", false, nil
	}
	sectionEnd := len(description)
	remaining := description[start+len(heading):]
	for _, nextHeading := range []string{"\n### ", "\n## "} {
		if offset := strings.Index(remaining, nextHeading); offset >= 0 {
			sectionEnd = min(sectionEnd, start+len(heading)+offset+1)
		}
	}
	section := description[start:sectionEnd]
	if valid != nil && !valid(section) {
		return description, "", false, nil
	}
	lineEnding := "\n"
	if strings.Contains(description, "\r\n") {
		lineEnding = "\r\n"
	}
	stripped := strings.TrimRight(description[:start], "\r\n")
	suffix := strings.TrimLeft(description[sectionEnd:], "\r\n")
	if stripped != "" && suffix != "" {
		stripped += lineEnding + lineEnding
	}
	stripped += suffix
	if duplicateStart, _ := findHeading(stripped); duplicateStart >= 0 {
		return "", "", false, errors.New(duplicateError)
	}
	return stripped, section, true, nil
}

// stripCodeModeApplyPatchSection removes the Code Mode apply_patch section from a
// tool description. It also returns that removed section, which is the native
// patch tool definition hpatch displaces: the host pays for one or the other as
// request input, so measuring hpatch's definition cost requires the text it replaced.
func stripCodeModeApplyPatchSection(description string) (string, string, bool, error) {
	findHeading := func(text string) (int, string) {
		start := strings.Index(text, codeModeApplyPatchHeading)
		if start < 0 || start > 0 && text[start-1] != '\n' {
			return -1, ""
		}
		return start, codeModeApplyPatchHeading
	}
	const declaration = "declare const tools: { apply_patch(input: string): Promise<unknown>; };"
	valid := func(section string) bool {
		return strings.Contains(section, "exec tool declaration:") && strings.Contains(section, declaration)
	}
	return stripCodeModeSection(
		description,
		findHeading,
		valid,
		"responses Code Mode tool defines nested apply_patch more than once",
	)
}

// stripCodeModeExecCommandSection removes only the nested command-execution
// section. It recognizes app and CLI description bodies without parsing either
// parameter schema. The apply_patch extractor remains an independent contract.
func stripCodeModeExecCommandSection(description string) (string, string, bool, error) {
	findHeading := func(text string) (int, string) {
		best := -1
		matched := ""
		for _, heading := range []string{codeModeExecCommandHeading, codeModeExecCommandPlainHeading} {
			searchFrom := 0
			for searchFrom < len(text) {
				offset := strings.Index(text[searchFrom:], heading)
				if offset < 0 {
					break
				}
				index := searchFrom + offset
				end := index + len(heading)
				for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
					end++
				}
				lineStart := index == 0 || text[index-1] == '\n'
				lineEnd := end == len(text) || text[end] == '\n' || text[end] == '\r'
				if lineStart && lineEnd {
					if best < 0 || index < best {
						best = index
						matched = heading
					}
					break
				}
				searchFrom = index + len(heading)
			}
		}
		return best, matched
	}
	return stripCodeModeSection(
		description,
		findHeading,
		nil,
		"responses Code Mode tool defines nested exec_command more than once",
	)
}

// stripCodeModeExecCommandContract removes the command tool section and the
// introductory example that points models at tools.exec_command.
func stripCodeModeExecCommandContract(description string) (string, []string, bool, error) {
	stripped, section, found, err := stripCodeModeExecCommandSection(description)
	if err != nil {
		return "", nil, false, err
	}
	if !found {
		if strings.Contains(description, "exec_command") {
			return "", nil, false, errors.New("responses Code Mode tool exposes exec_command without an owned section")
		}
		return description, nil, false, nil
	}
	definitions := []string{section}
	const example = " for example `await tools.exec_command(...)`."
	if count := strings.Count(stripped, example); count > 1 {
		return "", nil, false, errors.New("responses Code Mode tool references tools.exec_command more than once outside its section")
	} else if count == 1 {
		stripped = strings.Replace(stripped, example, "", 1)
		definitions = append([]string{example}, definitions...)
	}
	if strings.Contains(stripped, "exec_command") {
		return "", nil, false, errors.New("responses Code Mode tool exposes exec_command outside its owned contract")
	}
	return stripped, definitions, true, nil
}

func mustMarshalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func jsonString(object map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(object[name], &value)
	return value
}

func (p *hpatchProxy) rememberBatch(sessionID string, histories map[string]hpatchHistory) error {
	if len(histories) == 0 {
		return nil
	}
	prepared := make(map[string]hpatchHistory, len(histories))
	for callID, history := range histories {
		encodedItem, err := json.Marshal(history.upstreamItem)
		if err != nil {
			return fmt.Errorf("encode hpatch history item: %w", err)
		}
		history.bytes = len(sessionID) + len(callID) + len(history.toolName) + len(history.pluginID) + len(history.script) + len(history.root) + len(history.evaluated) + len(history.patch) + len(history.carrierKind) + len(history.carrierName) + len(history.carrierPayload) + len(history.report) + len(history.translationError) + len(history.correlationID) + len(encodedItem)
		for _, correction := range history.corrections {
			history.bytes += len(correction)
		}
		prepared[callID] = history
	}
	if len(prepared) > maxSessionTurns {
		return errors.New("hpatch history batch exceeds call capacity")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	existing := p.sessions[sessionID]
	calls := make(map[string]hpatchHistory, len(prepared))
	nextSequence := uint64(0)
	oldSessionBytes := 0
	sessionBytes := 0
	if existing != nil {
		calls = maps.Clone(existing.calls)
		nextSequence = existing.nextSequence
		oldSessionBytes = existing.bytes
		sessionBytes = existing.bytes
	}

	callIDs := slices.Collect(maps.Keys(prepared))
	slices.SortFunc(callIDs, func(first, second string) int {
		if order := cmp.Compare(prepared[first].sequence, prepared[second].sequence); order != 0 {
			return order
		}
		return strings.Compare(first, second)
	})
	protected := make(map[string]bool, len(prepared))
	for _, callID := range callIDs {
		history := prepared[callID]
		if previous, ok := calls[callID]; ok {
			sessionBytes -= previous.bytes
			history.sequence = previous.sequence
		} else {
			nextSequence++
			history.sequence = nextSequence
		}
		sessionBytes += history.bytes
		calls[callID] = history
		protected[callID] = true
	}

	for len(calls) > maxSessionTurns || sessionBytes > maxHPatchHistorySessionBytes {
		oldest, ok := oldestHistoryCall(calls, protected)
		if !ok {
			if len(calls) > maxSessionTurns {
				return errors.New("hpatch history call capacity reached")
			}
			return errors.New("hpatch history byte capacity reached")
		}
		sessionBytes -= calls[oldest].bytes
		delete(calls, oldest)
	}

	totalBytes := p.historyBytes - oldSessionBytes + sessionBytes
	sessionCount := len(p.sessions)
	if existing == nil {
		sessionCount++
	}
	type sessionCandidate struct {
		id       string
		lastUsed uint64
	}
	candidates := make([]sessionCandidate, 0, len(p.sessions))
	for id, session := range p.sessions {
		if id == sessionID || p.activeSessions[id] != 0 {
			continue
		}
		candidates = append(candidates, sessionCandidate{id: id, lastUsed: session.lastUsed})
	}
	slices.SortFunc(candidates, func(first, second sessionCandidate) int {
		if order := cmp.Compare(first.lastUsed, second.lastUsed); order != 0 {
			return order
		}
		return strings.Compare(first.id, second.id)
	})

	evicted := make([]string, 0)
	for sessionCount > maxSessionHistories || totalBytes > maxHPatchHistoryGlobalBytes {
		if len(evicted) == len(candidates) {
			if sessionCount > maxSessionHistories {
				return errors.New("hpatch history session capacity reached")
			}
			return errors.New("hpatch history byte capacity reached")
		}
		id := candidates[len(evicted)].id
		evicted = append(evicted, id)
		sessionCount--
		totalBytes -= p.sessions[id].bytes
	}
	for _, id := range evicted {
		delete(p.sessions, id)
	}

	if existing == nil {
		existing = &hpatchHistorySession{}
		p.sessions[sessionID] = existing
	}
	existing.calls = calls
	existing.bytes = sessionBytes
	existing.nextSequence = nextSequence
	p.touchSession(existing)
	p.historyBytes = totalBytes
	return nil
}

func oldestHistoryCall(histories map[string]hpatchHistory, protected map[string]bool) (string, bool) {
	oldestID := ""
	var oldest hpatchHistory
	found := false
	for callID, history := range histories {
		if protected[callID] {
			continue
		}
		if !found || history.sequence < oldest.sequence || history.sequence == oldest.sequence && callID < oldestID {
			oldestID = callID
			oldest = history
			found = true
		}
	}
	return oldestID, found
}

func (p *hpatchProxy) correctableHistory(sessionID string) (hpatchHistory, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return hpatchHistory{}, errors.New("no hpatch call to correct; send a complete script")
	}
	return correctionHistoryOf(maps.Values(session.calls))
}

// correctionHistoryOf picks the call a correction repairs: the newest call
// that hpatch actually evaluated. Proxy-rejected calls are skipped because they
// changed nothing. A successful newest call makes correction indices invalid,
// since they only address the script from the latest rejection diagnostic.
func correctionHistoryOf(histories iter.Seq[hpatchHistory]) (hpatchHistory, error) {
	var latest hpatchHistory
	found := false
	for history := range histories {
		if history.unevaluated || history.toolName != hpatchToolName {
			continue
		}
		if !found || history.sequence > latest.sequence {
			latest = history
			found = true
		}
	}
	if !found {
		return hpatchHistory{}, errors.New("no hpatch call to correct; send a complete script")
	}
	if latest.translationError == "" {
		return hpatchHistory{}, errors.New("the most recent hpatch call succeeded; corrections repair a rejected script, so send a complete script")
	}
	return latest, nil
}

func latestCorrectionAttempt(histories iter.Seq[hpatchHistory], correlationID string) int {
	latest := 0
	for history := range histories {
		if history.toolName == hpatchToolName && history.correlationID == correlationID {
			latest = max(latest, history.attempt)
		}
	}
	return latest
}

func (p *hpatchProxy) latestCorrectionAttempt(sessionID, correlationID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return 0
	}
	return latestCorrectionAttempt(maps.Values(session.calls), correlationID)
}

// correctable is the script a following correction addresses: what hpatch
// evaluated, so that a chain of corrections keeps resolving indices against the
// script the latest diagnostic described.
func (h hpatchHistory) correctable() string {
	if h.evaluated != "" {
		return h.evaluated
	}
	return h.script
}

func (p *hpatchProxy) history(sessionID, callID string) (hpatchHistory, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return hpatchHistory{}, false
	}
	history, ok := session.calls[callID]
	return history, ok
}

func (p *hpatchProxy) restoreInputPrefix(request *parsedResponsesRequest, sessionID string) error {
	raw, ok := request.fields["input"]
	if !ok {
		return nil
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil //nolint:nilerr // Non-array input cannot contain replayable hpatch calls.
	}
	changed := false
	validatedCarriers := make(map[string]bool)
	for index, item := range items {
		itemType := jsonString(item, "type")
		callID := jsonString(item, "call_id")
		history, known := p.history(sessionID, callID)
		if !known {
			continue
		}
		carrierKind := history.effectiveCarrierKind()
		if itemType == carrierOutputItemType(carrierKind) {
			continue
		}
		if itemType != carrierItemType(carrierKind) {
			return fmt.Errorf("replayed call %q changed item type", callID)
		}
		if jsonString(item, "name") != history.carrierName {
			return fmt.Errorf("replayed call %q changed carrier name", callID)
		}
		if validatedCarriers[callID] {
			return fmt.Errorf("replayed call %q appears more than once", callID)
		}
		if jsonString(item, carrierPayloadField(carrierKind)) != history.carrierInput() {
			return fmt.Errorf("replayed call %q changed translated payload", callID)
		}
		validatedCarriers[callID] = true
		if len(history.upstreamItem) != 0 {
			items[index] = maps.Clone(history.upstreamItem)
		} else {
			name := history.toolName
			if name == "" {
				name = hpatchToolName
			}
			item["name"] = mustMarshalJSON(name)

			item["input"] = mustMarshalJSON(history.script)
		}
		changed = true
	}
	if changed {
		encoded, err := json.Marshal(items)
		if err != nil {
			return fmt.Errorf("encode replayed Responses input: %w", err)
		}
		request.fields["input"] = encoded
	}
	return nil
}

func (t *hpatchResponseTransform) translate(callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.pluginID != "" || history.script != input {
			return hpatchHistory{}, fmt.Errorf("hpatch call %q changed input", callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	if len(input) > maxHPatchScriptBytes {
		return hpatchHistory{}, fmt.Errorf("hpatch call %q script exceeds %d bytes", callID, maxHPatchScriptBytes)
	}

	evaluated := input
	correction := isHPatchCorrection(input)
	correctionStats := hpatchCorrectionStats{}
	attemptMetadata := hpatch.AttemptMetadata{
		SessionID:     t.sessionID,
		CorrelationID: callID,
		CallID:        callID,
		Attempt:       1,
		Correction:    correction,
	}
	if correction {
		correctionStats.scope = hpatchCorrectionScope(input)
		base, baseErr := t.correctionHistory()
		if baseErr != nil {
			return t.rejectUnevaluated(callID, input, baseErr, attemptMetadata, correctionStats, upstreamItem)
		}
		attemptMetadata.CorrelationID = base.correlationID
		if attemptMetadata.CorrelationID == "" {
			attemptMetadata.CorrelationID = callID
		}
		attemptMetadata.Attempt = t.nextCorrectionAttempt(attemptMetadata.CorrelationID, base.attempt)
		if base.root != t.workspace.canonical {
			return t.rejectUnevaluated(callID, input, errors.New("the rejected script belongs to a different worktree; send a complete script"), attemptMetadata, correctionStats, upstreamItem)
		}
		corrections, parseErr := parseHPatchCorrections(input)
		if parseErr != nil {
			return t.rejectUnevaluated(callID, input, parseErr, attemptMetadata, correctionStats, upstreamItem)
		}
		correctionStats = hpatchCorrectionStatsOf(corrections).withBase(base.correctable(), corrections)
		corrected, correctionErr := applyHPatchCorrections(base.correctable(), corrections, base.corrections)
		if correctionErr != nil {
			return t.rejectUnevaluated(callID, input, correctionErr, attemptMetadata, correctionStats, upstreamItem)
		}
		evaluated = corrected
		if len(evaluated) > maxHPatchScriptBytes {
			return hpatchHistory{}, fmt.Errorf("hpatch call %q corrected script exceeds %d bytes", callID, maxHPatchScriptBytes)
		}
	}

	attemptContext := hpatch.WithAttemptMetadata(t.ctx, attemptMetadata)
	translated, err := t.proxy.translator.Translate(attemptContext, t.workspace, evaluated)
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		if errors.Is(err, errHPatchCapacity) || errors.Is(err, errHPatchWorkspaceChanged) {
			return hpatchHistory{}, err
		}
		diagnostic := translated.diagnostic
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		diagnostic += hpatchCorrectionGuidance(correction, translated.corrections)
		if err := t.recordMetrics(hpatchMetricInputs{
			invocation:    translated.invocation,
			rejections:    translated.rejections,
			attempt:       attemptMetadata,
			correction:    correctionStats,
			emittedScript: input,
			diagnostic:    diagnostic,
		}); err != nil {
			return hpatchHistory{}, err
		}
		history := hpatchHistory{
			toolName: hpatchToolName,
			script:   input,

			root:             t.workspace.canonical,
			evaluated:        retainedEvaluated(input, evaluated),
			carrierName:      t.codeModeToolName,
			translationError: diagnostic,
			corrections:      maps.Clone(translated.corrections),

			upstreamItem:  maps.Clone(upstreamItem),
			correlationID: attemptMetadata.CorrelationID,
			attempt:       attemptMetadata.Attempt,
		}
		t.recordLocal(callID, &history)
		return history, nil
	}
	patch := translated.patch
	if len(patch) > maxHPatchPatchBytes {
		return hpatchHistory{}, fmt.Errorf("hpatch call %q translation exceeds %d bytes", callID, maxHPatchPatchBytes)
	}
	patchText := string(patch)
	if err := t.recordMetrics(hpatchMetricInputs{
		invocation:    translated.invocation,
		attempt:       attemptMetadata,
		correction:    correctionStats,
		emittedScript: input,
		report:        translated.report,
		patch:         patchText,
		successful:    true,
		diagnostic:    translated.diagnostic,
	}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		toolName: hpatchToolName,
		script:   input,

		root:          t.workspace.canonical,
		evaluated:     retainedEvaluated(input, evaluated),
		patch:         patchText,
		carrierName:   t.codeModeToolName,
		report:        hpatchReport(translated.report, translated.diagnostic),
		upstreamItem:  maps.Clone(upstreamItem),
		correlationID: attemptMetadata.CorrelationID,
		attempt:       attemptMetadata.Attempt,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func (t *hpatchResponseTransform) translateTool(name, callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if name == hpatchToolName {
		return t.translate(callID, input, upstreamItem)
	}
	contribution, ok := t.proxy.registry.contribution(name)
	if !ok || contribution.Builtin {
		return hpatchHistory{}, fmt.Errorf("registered tool %q is unavailable", name)
	}
	return t.translateRegisteredTool(contribution, callID, input, upstreamItem)
}

func (t *hpatchResponseTransform) translateRegisteredTool(contribution toolContribution, callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != contribution.Name || history.pluginID != contribution.PluginID || history.script != input {
			return hpatchHistory{}, fmt.Errorf("%s call %q changed input", contribution.Name, callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	translation, err := toolplugin.Translate(
		t.ctx,
		t.proxy.registry.NodeExecutable,
		t.proxy.registry.RuntimeRoot,
		contribution.Module,
		contribution.ModuleIndex,
		input,
	)
	if err != nil {
		return hpatchHistory{}, fmt.Errorf("translate registered tool %s: %w", contribution.Name, err)
	}

	kind := codeModeCarrierCustom
	name := t.codeModeToolName
	payload := ""
	diagnostic := translation.Diagnostic
	if translation.Rejected {
		if err := t.carriers.require(name, kind); err != nil {
			return hpatchHistory{}, fmt.Errorf("%s input rejection: %w", contribution.Name, err)
		}
		if diagnostic == "" {
			diagnostic = contribution.Name + " rejected the model input"
		}
		payload = hpatchDiagnosticExecInput(diagnostic)
	} else {
		switch translation.Carrier.Kind {
		case "exec":
			if err := t.carriers.require(name, kind); err != nil {
				return hpatchHistory{}, fmt.Errorf("%s exec carrier: %w", contribution.Name, err)
			}
			_, ok := t.proxy.registry.wrapper(contribution.Name)
			if !ok {
				return hpatchHistory{}, fmt.Errorf("%s worker is unavailable", contribution.Name)
			}
			if translation.Carrier.Template == "" {
				payload, err = workerExecInputWithParams(
					contribution.Name,
					translation.Arguments,
					translation.Carrier.Params,
				)
			} else {
				payload, err = workerTemplateExecInputWithParams(
					contribution.Name,
					translation.Arguments,
					translation.Carrier.Template,
					translation.Carrier.Params,
				)
			}
			if err != nil {
				return hpatchHistory{}, fmt.Errorf("%s exec carrier: %w", contribution.Name, err)
			}
		case "custom":
			name = translation.Carrier.Name
			payload = translation.Carrier.Payload
			if err := t.carriers.require(name, kind); err != nil {
				return hpatchHistory{}, fmt.Errorf("%s custom carrier: %w", contribution.Name, err)
			}
		case "function":
			kind = codeModeCarrierFunction
			name = translation.Carrier.Name
			payload = translation.Carrier.Payload
			if err := t.carriers.require(name, kind); err != nil {
				return hpatchHistory{}, fmt.Errorf("%s function carrier: %w", contribution.Name, err)
			}
			var arguments map[string]json.RawMessage
			if json.Unmarshal([]byte(payload), &arguments) != nil || arguments == nil {
				return hpatchHistory{}, fmt.Errorf("%s function carrier returned invalid JSON object arguments", contribution.Name)
			}
		default:
			return hpatchHistory{}, fmt.Errorf(
				"%s translator returned unsupported carrier kind %q",
				contribution.Name,
				translation.Carrier.Kind,
			)
		}
	}

	metricCall := &hpatch.HostToolCall{
		PluginID:          contribution.PluginID,
		ToolName:          contribution.Name,
		EmittedName:       contribution.Name,
		EmittedInput:      input,
		FailedTranslation: translation.Rejected,
	}
	if !translation.Rejected {
		metricCall.TranslatedName = name
		metricCall.TranslatedPayload = payload
	}
	if err := t.recordMetrics(hpatchMetricInputs{
		overheadOnly: true,
		toolCall:     metricCall,
		diagnostic:   diagnostic,
	}); err != nil {
		return hpatchHistory{}, err
	}

	history := hpatchHistory{
		toolName:         contribution.Name,
		pluginID:         contribution.PluginID,
		script:           input,
		root:             t.workspace.canonical,
		carrierKind:      kind,
		carrierName:      name,
		carrierPayload:   payload,
		translationError: diagnostic,
		upstreamItem:     maps.Clone(upstreamItem),
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func hpatchReport(report, diagnostic string) string {
	if diagnostic == "" {
		return report
	}
	if report != "" && !strings.HasSuffix(report, "\n") {
		report += "\n"
	}
	return report + diagnostic
}

func hpatchApplyExecInput(patch, report string) string {
	return hpatchApplyExecMarker +
		"await tools.apply_patch(" + strconv.Quote(patch) + ");\n" +
		"text(" + strconv.Quote(report) + ");"
}

func hpatchDiagnosticExecInput(diagnostic string) string {
	return "text(" + strconv.Quote(diagnostic) + ");"
}

func workerCommand(executable string, arguments []string) string {
	command := shellQuoteArgument(executable)
	for _, argument := range arguments {
		command += " " + shellQuoteArgument(argument)
	}
	return command
}

func workerExecInputWithParams(executable string, arguments []string, params map[string]json.RawMessage) (string, error) {
	return workerCommandExecInputWithParams(workerCommand(executable, arguments), params)
}

func workerTemplateExecInputWithParams(executable string, arguments []string, template string, params map[string]json.RawMessage) (string, error) {
	if strings.Count(template, "{.}") != 1 {
		return "", errors.New("exec command template must contain exactly one {.} placeholder")
	}
	command := strings.Replace(template, "{.}", workerCommand(executable, arguments), 1)
	return workerCommandExecInputWithParams(command, params)
}

func workerCommandExecInputWithParams(command string, params map[string]json.RawMessage) (string, error) {
	if _, exists := params["cmd"]; exists {
		return "", errors.New("exec params must not contain cmd")
	}
	if login, exists := params["login"]; exists && !bytes.Equal(bytes.TrimSpace(login), []byte("false")) {
		return "", errors.New("exec params login must be false")
	}
	arguments := maps.Clone(params)
	if arguments == nil {
		arguments = make(map[string]json.RawMessage)
	}
	arguments["cmd"] = mustMarshalJSON(command)
	if _, exists := arguments["login"]; !exists {
		arguments["login"] = mustMarshalJSON(false)
	}
	return "const result = await tools.exec_command(" + string(mustMarshalJSON(arguments)) + ");\n" +
		"text(result.output);", nil
}

func (h hpatchHistory) carrierInput() string {
	if h.pluginID != "" || h.carrierKind != "" {
		return h.carrierPayload
	}
	if h.translationError != "" {
		return hpatchDiagnosticExecInput(h.translationError)
	}
	return hpatchApplyExecInput(h.patch, h.report)
}

func (h hpatchHistory) effectiveCarrierKind() codeModeCarrierKind {
	if h.carrierKind != "" {
		return h.carrierKind
	}
	return codeModeCarrierCustom
}

func (t *hpatchResponseTransform) rejectUnevaluated(callID, input string, rejection error, attempt hpatch.AttemptMetadata, correction hpatchCorrectionStats, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	diagnostic := rejection.Error()
	if err := t.recordMetrics(hpatchMetricInputs{attempt: attempt, correction: correction, emittedScript: input, diagnostic: diagnostic}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		toolName: hpatchToolName,
		script:   input,

		carrierName:      t.codeModeToolName,
		translationError: diagnostic,
		correlationID:    attempt.CorrelationID,
		attempt:          attempt.Attempt,
		upstreamItem:     maps.Clone(upstreamItem),
		unevaluated:      true,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func retainedEvaluated(emitted, evaluated string) string {
	if emitted == evaluated {
		return ""
	}
	return evaluated
}

func (t *hpatchResponseTransform) claimRequestAccounting() (string, []hpatch.HostToolDefinition, string, []string) {
	if t.requestAccountingClaimed {
		return "", nil, "", nil
	}
	t.requestAccountingClaimed = true
	return t.installedToolDefinition, slices.Clone(t.installedToolBreakdown), t.baselineDefinition, slices.Clone(t.execCommandDefinitions)
}

func (t *hpatchResponseTransform) recordMetrics(inputs hpatchMetricInputs) error {
	inputs.definition, inputs.definitions, inputs.baselineDefinition, inputs.execCommandDefinitions = t.claimRequestAccounting()
	inputs.sessionID = t.sessionID
	record, err := calculateHPatchMetricRecord(inputs)
	if err == nil {
		err = t.proxy.translator.RecordMetrics(t.ctx, record)
	}
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return contextErr
		}
		// Gain metrics are auxiliary telemetry and cannot change a tool result.
		return nil
	}
	return nil
}

func (t *hpatchResponseTransform) recordLocal(callID string, history *hpatchHistory) {
	t.localSequence++
	history.sequence = t.localSequence
	t.local[callID] = *history
}

// correctionHistory is the rejected call a correction in this turn repairs. A
// rejection this turn is newer than retained history, which commits only after
// the response completes.
func (t *hpatchResponseTransform) correctionHistory() (hpatchHistory, error) {
	for _, history := range t.local {
		if !history.unevaluated && history.toolName == hpatchToolName {
			return correctionHistoryOf(maps.Values(t.local))
		}
	}
	return t.proxy.correctableHistory(t.historySessionID)
}

func (t *hpatchResponseTransform) nextCorrectionAttempt(correlationID string, baseAttempt int) int {
	latest := max(baseAttempt, t.proxy.latestCorrectionAttempt(t.historySessionID, correlationID))
	latest = max(latest, latestCorrectionAttempt(maps.Values(t.local), correlationID))
	return max(latest+1, 2)
}

func (t *hpatchResponseTransform) TransformJSON(payload []byte) ([]byte, error) {
	return t.transformResponse(payload)
}

func (t *hpatchResponseTransform) Finish(streamEvent bool) error {
	if streamEvent && len(t.pending) != 0 {
		return errors.New("upstream stream ended with an incomplete hpatch call")
	}
	if !t.requestAccountingClaimed {
		return t.recordMetrics(hpatchMetricInputs{overheadOnly: true})
	}
	return nil
}

func (t *hpatchResponseTransform) TransformSSE(payload []byte) ([][]byte, error) {
	var envelope struct {
		Type     string          `json:"type"`
		ItemID   string          `json:"item_id"`
		CallID   string          `json:"call_id"`
		Name     string          `json:"name"`
		Input    string          `json:"input"`
		Item     json.RawMessage `json:"item"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		if len(t.pending) != 0 {
			return nil, errors.New("decode pending hpatch stream event")
		}
		return [][]byte{payload}, nil
	}
	switch envelope.Type {
	case "response.output_item.added":
		var item map[string]json.RawMessage
		if json.Unmarshal(envelope.Item, &item) != nil {
			return [][]byte{payload}, nil //nolint:nilerr // Unrelated output items pass through unchanged.
		}
		name := jsonString(item, "name")
		if !t.routesTool(name) {
			return [][]byte{payload}, nil
		}
		itemID, callID := jsonString(item, "id"), jsonString(item, "call_id")
		if jsonString(item, "type") != "custom_tool_call" || itemID == "" || callID == "" {
			return nil, errors.New("upstream emitted malformed hpatch call")
		}
		if len(t.pending) >= maxHPatchPendingCalls {
			return nil, errors.New("upstream hpatch call identity capacity exceeded")
		}
		if _, exists := t.pending[itemID]; exists {
			return nil, errors.New("upstream reused hpatch item ID")
		}
		t.pending[itemID] = hpatchPendingCall{callID: callID, toolName: name, added: bytes.Clone(payload)}
		return nil, nil

	case "response.custom_tool_call_input.delta":
		if _, ok := t.pending[envelope.ItemID]; ok {
			return nil, nil
		}
		return [][]byte{payload}, nil

	case "response.custom_tool_call_input.done":
		pending, ok := t.pending[envelope.ItemID]
		if !ok {
			return [][]byte{payload}, nil
		}
		history, err := t.translateTool(pending.toolName, pending.callID, envelope.Input, nil)
		if err != nil {
			return nil, err
		}
		var addedEnvelope struct {
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal(pending.added, &addedEnvelope) != nil {
			return nil, errors.New("decode buffered hpatch item")
		}
		var addedItem map[string]json.RawMessage
		if json.Unmarshal(addedEnvelope.Item, &addedItem) != nil {
			return nil, errors.New("decode buffered hpatch call")
		}
		kind := history.effectiveCarrierKind()
		renderCarrierItem(addedItem, kind, history.carrierName, "")
		itemPayload, err := json.Marshal(addedItem)
		if err != nil {
			return nil, err
		}
		addedEvent, err := replaceRawField(pending.added, "item", itemPayload)
		if err != nil {
			return nil, err
		}
		doneEvent, err := renderCarrierDoneEvent(payload, kind, history.carrierInput())
		if err != nil {
			return nil, err
		}
		return [][]byte{addedEvent, doneEvent}, nil

	case "response.output_item.done":
		var item map[string]json.RawMessage
		if json.Unmarshal(envelope.Item, &item) != nil {
			return [][]byte{payload}, nil //nolint:nilerr // Malformed unrelated output remains the upstream's responsibility.
		}
		changed, err := t.transformOutputItem(item)
		if err != nil {
			return nil, err
		}
		if !changed {
			return [][]byte{payload}, nil
		}
		delete(t.pending, jsonString(item, "id"))
		transformed, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		event, err := replaceRawField(payload, "item", transformed)
		return onePayload(event, err)

	case "response.completed":
		if len(t.pending) != 0 {
			return nil, errors.New("upstream completed with an incomplete hpatch call")
		}
		transformed, err := t.transformResponse(envelope.Response)
		if err != nil {
			return nil, err
		}
		event, err := replaceRawField(payload, "response", transformed)
		if err != nil {
			return nil, err
		}
		var terminal struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(transformed, &terminal) == nil && terminal.Status == "failed" {
			event, err = replaceRawField(event, "type", mustMarshalJSON("response.failed"))
		}
		if err != nil {
			return nil, err
		}
		if err := t.Finish(true); err != nil {
			return nil, err
		}
		return [][]byte{event}, nil

	case "response.failed", "response.incomplete":
		clear(t.pending)
		if err := t.Finish(true); err != nil {
			return nil, err
		}
		return [][]byte{payload}, nil
	default:
		if _, pending := t.pending[envelope.ItemID]; pending || t.pendingCallKnown(envelope.CallID) || t.routesTool(envelope.Name) || envelope.Name == applyPatchToolName {
			return nil, fmt.Errorf("unsupported hpatch-related stream event %q", envelope.Type)
		}
		return [][]byte{payload}, nil
	}
}

func (t *hpatchResponseTransform) pendingCallKnown(callID string) bool {
	for _, pending := range t.pending {
		if callID != "" && pending.callID == callID {
			return true
		}
	}
	return false
}

func onePayload(payload []byte, err error) ([][]byte, error) {
	if err != nil {
		return nil, err
	}
	return [][]byte{payload}, nil
}

func (t *hpatchResponseTransform) transformResponse(payload []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, errors.New("decode hpatch-enabled response")
	}
	if rawOutput, ok := object["output"]; ok {
		var output []map[string]json.RawMessage
		if err := json.Unmarshal(rawOutput, &output); err != nil {
			return nil, errors.New("decode hpatch-enabled response output")
		}
		for _, item := range output {
			if _, err := t.transformOutputItem(item); err != nil {
				return nil, err
			}
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		object["output"] = encoded
	}
	t.restoreResponseContract(object)
	if jsonString(object, "status") == "completed" {
		if err := t.commitHistory(); err != nil {
			return nil, err
		}
	}
	return json.Marshal(object)
}

func (t *hpatchResponseTransform) commitHistory() error {
	if t.historyCommitted {
		return nil
	}
	if err := t.proxy.rememberBatch(t.historySessionID, t.local); err != nil {
		return err
	}
	t.historyCommitted = true
	return nil
}

func (t *hpatchResponseTransform) restoreResponseContract(object map[string]json.RawMessage) {
	if _, ok := object["tools"]; ok {
		if !t.originalToolsPresent {
			delete(object, "tools")
		} else {
			object["tools"] = bytes.Clone(t.originalTools)
		}
	}
	if _, ok := object["tool_choice"]; ok {
		if !t.originalToolChoicePresent {
			delete(object, "tool_choice")
		} else {
			object["tool_choice"] = bytes.Clone(t.originalToolChoice)
		}
	}
}

func (t *hpatchResponseTransform) transformOutputItem(item map[string]json.RawMessage) (bool, error) {
	name := jsonString(item, "name")
	if !t.routesTool(name) {
		return false, nil
	}
	callID := jsonString(item, "call_id")
	if jsonString(item, "type") != "custom_tool_call" || callID == "" {
		return false, fmt.Errorf("upstream emitted malformed %s call", name)
	}
	history, err := t.translateTool(name, callID, jsonString(item, "input"), maps.Clone(item))
	if err != nil {
		return false, err
	}
	renderCarrierItem(item, history.effectiveCarrierKind(), history.carrierName, history.carrierInput())
	return true, nil
}

func replaceRawField(payload []byte, name string, value json.RawMessage) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, errors.New("decode stream event")
	}
	object[name] = value
	return json.Marshal(object)
}
