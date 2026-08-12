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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/router/toolplugin"
)

const (
	hpatchToolName         = "hpatch"
	hpatchRecoveryToolName = "hpatch_recover"

	applyPatchToolName           = "apply_patch"
	hpatchApplyExecMarker        = "// hpatch-proxy: apply translated patch\n"
	maxHPatchScriptBytes         = 1 << 20
	maxHPatchPatchBytes          = 16 << 20
	maxHPatchHistorySessionBytes = 32 << 20
	maxHPatchHistoryGlobalBytes  = 128 << 20

	maxHPatchPendingCalls = 128

	shellArtifactPrefix = "@shell/"
)

var (
	errHPatchCapacity = errors.New("hpatch proxy capacity exceeded")
	shellArtifactTTL  = time.Hour
)

type hpatchTranslationResult struct {
	patch      []byte
	report     string
	diagnostic string
	rejections []hpatch.HostRejection
	invocation hpatch.InvocationMetrics
}

type hpatchTranslator interface {
	Translate(ctx context.Context, directory, script string) (hpatchTranslationResult, error)
	RecordMetrics(ctx context.Context, record hpatchMetricRecord) error
	ToolDescription() string
}

type hpatchApplier interface {
	Apply(ctx context.Context, root *os.Root, script string) (hpatchTranslationResult, error)
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

func (t notifyingHPatchTranslator) Translate(ctx context.Context, directory, script string) (hpatchTranslationResult, error) {
	return t.inner.Translate(ctx, directory, script)
}

func (t notifyingHPatchTranslator) Apply(ctx context.Context, root *os.Root, script string) (hpatchTranslationResult, error) {
	applier, ok := t.inner.(hpatchApplier)
	if !ok {
		return hpatchTranslationResult{}, errors.New("hpatch translator cannot apply retained shell edits")
	}
	return applier.Apply(ctx, root, script)
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

func (t inProcessHPatchTranslator) Translate(ctx context.Context, directory, script string) (hpatchTranslationResult, error) {
	translated, err := hpatch.TranslateForHostAt(ctx, directory, script, t.dataDirectory)
	if contextErr := ctx.Err(); contextErr != nil {
		return hpatchTranslationResult{}, contextErr
	}
	if len(translated.Patch) > maxHPatchPatchBytes {
		return hpatchTranslationResult{}, fmt.Errorf("%w: hpatch translation output exceeds its configured bound", errHPatchCapacity)
	}
	return hpatchTranslationResultOf(translated), err
}

func (t inProcessHPatchTranslator) Apply(ctx context.Context, root *os.Root, script string) (hpatchTranslationResult, error) {
	applied, err := hpatch.ApplyForHostRoot(ctx, root, script, t.dataDirectory)
	if contextErr := ctx.Err(); contextErr != nil {
		return hpatchTranslationResult{}, contextErr
	}
	return hpatchTranslationResultOf(applied), err
}

func hpatchTranslationResultOf(translated hpatch.HostTranslation) hpatchTranslationResult {
	return hpatchTranslationResult{
		patch:      translated.Patch,
		report:     translated.Report,
		diagnostic: translated.Diagnostic,
		rejections: slices.Clone(translated.Rejections),
		invocation: translated.Invocation,
	}
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
	// model's payload, which happens when the payload was a recovery edit. Replay
	// must restore what the model emitted, while a following recovery must target
	// the script that produced the latest diagnostic.
	evaluated      string
	patch          string
	applied        bool
	carrierName    string
	carrierKind    codeModeCarrierKind
	carrierPayload string

	report            string
	translationError  string
	evaluatorRejected bool
	rejections        []hpatch.HostRejection
	correlationID     string
	attempt           int
	upstreamItem      map[string]json.RawMessage
	replayCarrier     bool
	bytes             int
	// unevaluated marks a call the proxy rejected before hpatch saw it. Such a
	// recovery changed nothing and has no script of its own, so another recovery
	// looks past it to the rejected script it was trying to repair.
	unevaluated bool
	// sequence orders retained calls within a session. Calls are keyed by ID in
	// an unordered map, so recovery needs an explicit order to identify the
	// latest rejected script.
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
	translator     hpatchTranslator
	registry       *toolRegistry
	shellDirectory string
	titles         *sessionTitleCache
	shellSessions  map[string]struct{}

	mu              sync.RWMutex
	sessions        map[string]*hpatchHistorySession
	activeSessions  map[string]int
	historyBytes    int
	sessionSequence uint64
	closed          bool
}

func newHPatchProxy(translator hpatchTranslator, registry *toolRegistry, titleCaches ...*sessionTitleCache) *hpatchProxy {
	if translator == nil || registry == nil {
		return nil
	}
	titles := newSessionTitleCache()
	if len(titleCaches) != 0 && titleCaches[0] != nil {
		titles = titleCaches[0]
	}
	directory := "/tmp"
	return &hpatchProxy{
		translator:     translator,
		registry:       registry,
		shellDirectory: directory,
		titles:         titles,
		shellSessions:  make(map[string]struct{}),
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
	var cleanupErr error
	for sessionID := range p.shellSessions {
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(p.shellSessionDirectory(sessionID)))
	}
	clear(p.shellSessions)
	clear(p.sessions)
	clear(p.activeSessions)
	p.historyBytes = 0
	return cleanupErr
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

type hpatchPendingCall struct {
	callID   string
	toolName string

	added []byte
}

type hpatchResponseTransform struct {
	ctx              context.Context
	proxy            *hpatchProxy
	sessionID        string
	model            string
	historySessionID string
	sessionActive    bool

	originalTools             json.RawMessage
	originalToolsPresent      bool
	originalToolChoice        json.RawMessage
	originalToolChoicePresent bool
	pending                   map[string]hpatchPendingCall
	nativeExecItems           map[string]struct{}
	nativeExecWarningsMetered map[string]struct{}
	local                     map[string]hpatchHistory
	directory                 string
	carriers                  codeModeCarrierCatalog

	installedToolDefinition  string
	installedToolBreakdown   []hpatch.HostToolDefinition
	codeModeToolName         string
	baselineDefinition       string
	execCommandDefinitions   []string
	requestAccountingClaimed bool

	// localSequence orders the calls translated during this turn, so a
	// recovery resolves against the newest rejection rather than an arbitrary
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
	directory, _ := usableRoutingDirectory(metadata.Directories)
	originalTools, originalToolsPresent := request.fields["tools"]
	originalTools = bytes.Clone(originalTools)
	originalToolChoice, originalToolChoicePresent := request.fields["tool_choice"]
	originalToolChoice = bytes.Clone(originalToolChoice)
	carriers, err := buildCodeModeCarrierCatalog(request.fields, p.registry)
	if err != nil {
		return nil, err
	}
	installedTools, err := p.registry.specifications()
	if err != nil {
		return nil, err
	}
	baselineDefinition, execCommandDefinitions, codeModeToolName, replaced, err := replaceAdditionalToolsApplyPatch(request.fields, installedTools)
	if err != nil {
		return nil, err
	}
	if !replaced {
		return nil, errors.New("responses request cannot satisfy the required hpatch rewrite")
	}
	modelContributions := p.registry.modelContributions()
	installedToolBreakdown := make([]hpatch.HostToolDefinition, len(modelContributions))
	for index, contribution := range modelContributions {
		installedToolBreakdown[index] = hpatch.HostToolDefinition{
			PluginID:   contribution.PluginID,
			ToolName:   contribution.Name,
			Definition: string(mustMarshalJSON(installedTools[index])),
		}
	}
	historySessionID := directory + "\x00" + sessionID
	if err := p.activateSession(historySessionID); err != nil {
		return nil, err
	}
	if err := p.reconcileInputPrefix(request, historySessionID); err != nil {
		p.deactivateSession(historySessionID)
		return nil, err
	}
	return &hpatchResponseTransform{
		ctx:              ctx,
		proxy:            p,
		sessionID:        sessionID,
		model:            request.modelDescription(),
		historySessionID: historySessionID,
		sessionActive:    true,

		originalTools:             originalTools,
		originalToolsPresent:      originalToolsPresent,
		originalToolChoice:        originalToolChoice,
		originalToolChoicePresent: originalToolChoicePresent,
		pending:                   make(map[string]hpatchPendingCall),
		nativeExecItems:           make(map[string]struct{}),
		nativeExecWarningsMetered: make(map[string]struct{}),
		local:                     make(map[string]hpatchHistory),
		directory:                 directory,
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
	baselineDefinition           string
	execCommandParamsDescription string
	execCommandDefinitions       []string
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
		var execCommandParamsDescription string
		var execCommandDefinitions []string
		var definitions []string
		stripped, execCommandParamsDescription, definitions, found, err = stripCodeModeExecCommandContract(stripped)
		if err != nil {
			return err
		}
		if found {
			execCommandDefinitions = append(execCommandDefinitions, definitions...)
		}
		owner = &additionalToolsApplyPatchOwner{
			item:                         item,
			itemIndex:                    itemIndex,
			additionalTools:              additionalTools,
			additionalToolIndex:          additionalToolIndex,
			tools:                        tools,
			toolIndex:                    toolIndex,
			nested:                       nested,
			name:                         name,
			strippedDescription:          stripped,
			baselineDefinition:           baseline,
			execCommandParamsDescription: execCommandParamsDescription,
			execCommandDefinitions:       execCommandDefinitions,
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
	shellIndex := slices.IndexFunc(installedTools, func(tool map[string]json.RawMessage) bool {
		return jsonString(tool, "name") == "shell"
	})
	if shellIndex < 0 {
		return errors.New("built-in shell tool is unavailable")
	}
	if owner.execCommandParamsDescription != "" {
		description := strings.TrimRight(jsonString(installedTools[shellIndex], "description"), "\r\n")
		description += "\n\n" + owner.execCommandParamsDescription
		installedTools[shellIndex]["description"] = mustMarshalJSON(description)
	}
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
	contribution, ok := t.proxy.registry.contribution(name)
	return ok && contribution.ModelVisible
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

func customFreeformTool(name, description string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"type":        mustMarshalJSON("custom"),
		"name":        mustMarshalJSON(name),
		"description": mustMarshalJSON(description),
	}
}

const (
	codeModeApplyPatchHeading       = "### `apply_patch`"
	codeModeExecCommandHeading      = "### `exec_command`"
	codeModeExecCommandPlainHeading = "### exec_command"
)

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

func execCommandParamsDescription(section string) string {
	const heading = "### `#!params`"
	const appMarker = "exec_command(args:"

	if marker := strings.Index(section, appMarker); marker >= 0 {
		rest := strings.TrimLeft(section[marker+len(appMarker):], " \t")
		end := strings.Index(rest, "}): Promise")
		if !strings.HasPrefix(rest, "{") || end < 0 {
			return ""
		}
		shape := rest[:end+1]
		inside := shape[1 : len(shape)-1]
		cursor := 0
		for {
			cursor += len(inside[cursor:]) - len(strings.TrimLeft(inside[cursor:], " \t\r\n"))
			if !strings.HasPrefix(inside[cursor:], "//") {
				break
			}
			newline := strings.IndexByte(inside[cursor:], '\n')
			if newline < 0 {
				return ""
			}
			cursor += newline + 1
		}
		field := inside[cursor:]
		colon := strings.IndexByte(field, ':')
		semicolon := strings.IndexByte(field, ';')
		if colon < 0 || semicolon < colon || strings.TrimSpace(field[:colon]) != "cmd" {
			return ""
		}
		shape = "{" + inside[cursor+semicolon+1:] + "}"
		if strings.Contains(shape, "exec_command") {
			return ""
		}
		return heading + "\nThe leading `#!params={...}` directive accepts this request-specific JSON object shape. The script body supplies `cmd`, so omit it.\n\n```ts\n" + shape + "\n```"
	}

	normalized := strings.ReplaceAll(section, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	parameters := slices.IndexFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) == "Parameters:"
	})
	if parameters < 0 {
		return ""
	}
	kept := make([]string, 0, len(lines)-parameters)
	for _, line := range lines[parameters+1:] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if strings.HasPrefix(name, "`cmd`") || strings.HasPrefix(name, "cmd:") {
			continue
		}
		kept = append(kept, trimmed)
	}
	if len(kept) == 0 {
		return ""
	}
	fields := strings.Join(kept, "\n")
	if strings.Contains(fields, "exec_command") {
		return ""
	}
	return heading + "\nThe leading `#!params={...}` directive accepts a JSON object with these request-specific fields. The script body supplies `cmd`, so omit it.\n\n" + fields
}

// stripCodeModeExecCommandContract removes the command tool section and the
// introductory example from the model-visible Code Mode description. It derives
// a shell-specific parameter description without retaining the nested tool surface.
func stripCodeModeExecCommandContract(description string) (string, string, []string, bool, error) {
	stripped, section, found, err := stripCodeModeExecCommandSection(description)
	if err != nil {
		return "", "", nil, false, err
	}
	if !found {
		if strings.Contains(description, "exec_command") {
			return "", "", nil, false, errors.New("responses Code Mode tool exposes exec_command without an owned section")
		}
		return description, "", nil, false, nil
	}
	definitions := []string{section}
	const example = " for example `await tools.exec_command(...)`."
	if count := strings.Count(stripped, example); count > 1 {
		return "", "", nil, false, errors.New("responses Code Mode tool references tools.exec_command more than once outside its section")
	} else if count == 1 {
		stripped = strings.Replace(stripped, example, "", 1)
		definitions = append([]string{example}, definitions...)
	}
	if strings.Contains(stripped, "exec_command") {
		return "", "", nil, false, errors.New("responses Code Mode tool exposes exec_command outside its owned contract")
	}
	return stripped, execCommandParamsDescription(section), definitions, true, nil
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
		for _, rejection := range history.rejections {
			history.bytes += hpatchRejectionTextBytes(rejection)
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

func (p *hpatchProxy) recoverableHistory(sessionID string) (hpatchHistory, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return hpatchHistory{}, errors.New("no rejected hpatch script to recover; send a complete script")
	}
	return recoveryHistoryOf(maps.Values(session.calls))
}

// recoveryHistoryOf picks the newest call that hpatch actually evaluated.
// Proxy-rejected calls are skipped because they changed nothing. A successful
// newest call blocks recovery of an older rejection.
func recoveryHistoryOf(histories iter.Seq[hpatchHistory]) (hpatchHistory, error) {
	var latest hpatchHistory
	found := false
	for history := range histories {
		if history.unevaluated || (history.toolName != hpatchToolName && history.toolName != hpatchRecoveryToolName) {
			continue
		}
		if !found || history.sequence > latest.sequence {
			latest = history
			found = true
		}
	}
	if !found {
		return hpatchHistory{}, errors.New("no rejected hpatch script to recover; send a complete script")
	}
	if latest.translationError == "" {
		return hpatchHistory{}, errors.New("the most recent hpatch call succeeded; recovery edits require a rejected script, so send a complete script")
	}
	if !latest.evaluatorRejected {
		return hpatchHistory{}, errors.New("the most recent hpatch call did not produce an evaluator rejection; send a complete script")
	}
	return latest, nil
}

func latestRecoveryAttempt(histories iter.Seq[hpatchHistory], correlationID string) int {
	latest := 0
	for history := range histories {
		if (history.toolName == hpatchToolName || history.toolName == hpatchRecoveryToolName) && history.correlationID == correlationID {
			latest = max(latest, history.attempt)
		}
	}
	return latest
}

func (p *hpatchProxy) latestRecoveryAttempt(sessionID, correlationID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return 0
	}
	return latestRecoveryAttempt(maps.Values(session.calls), correlationID)
}

// recoveryBaseline is the complete rejected script a following recovery edits.
func (h hpatchHistory) recoveryBaseline() string {
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

// reconcileInputPrefix replays retained hpatch calls into the request's input
// and prunes the retained calls the conversation no longer shows.
func (p *hpatchProxy) reconcileInputPrefix(request *parsedResponsesRequest, sessionID string) error {
	raw, ok := request.fields["input"]
	if !ok {
		return nil
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil //nolint:nilerr // Non-array input cannot contain replayable hpatch calls.
	}
	changed := false
	newestRetained := uint64(0)
	validatedCarriers := make(map[string]bool)
	for index, item := range items {
		itemType := jsonString(item, "type")
		callID := jsonString(item, "call_id")
		history, known := p.history(sessionID, callID)
		if !known {
			continue
		}
		// Record before the output-item skip below, so a call the input shows
		// only as its output sibling still counts as surviving.
		newestRetained = max(newestRetained, history.sequence)
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
		if history.replayCarrier {
			continue
		}
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
	p.pruneSessionAfter(sessionID, newestRetained)
	return nil
}

// pruneSessionAfter drops every retained call newer than the newest one the
// current request's input still shows. Truncation only ever removes a suffix of
// the conversation, so the newer calls belong to turns the model no longer
// sees: keeping them would let recovery edit a discarded script, and would let
// them consume the history budget that a surviving call needs to replay.
func (p *hpatchProxy) pruneSessionAfter(sessionID string, newest uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// A second in-flight turn commits its calls only at response completion, so
	// this request's input cannot show them and pruning would discard them.
	if p.activeSessions[sessionID] > 1 {
		return
	}
	session := p.sessions[sessionID]
	if session == nil {
		return
	}
	pruned := 0
	for callID, history := range session.calls {
		if history.sequence > newest {
			pruned += history.bytes
			delete(session.calls, callID)
		}
	}
	if pruned == 0 {
		return
	}
	// nextSequence stays monotonic so a later call still outranks every
	// survivor and recovery keeps resolving "newest" correctly.
	session.bytes -= pruned
	p.historyBytes -= pruned
}

func (t *hpatchResponseTransform) translate(callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != hpatchToolName || history.pluginID != "" || history.script != input {
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
	attemptMetadata := hpatch.AttemptMetadata{
		SessionID:     t.sessionID,
		Title:         t.proxy.titles.title(t.sessionID),
		CorrelationID: callID,
		CallID:        callID,
		Attempt:       1,
		Correction:    false,
		Model:         t.model,
	}

	attemptContext := hpatch.WithAttemptMetadata(t.ctx, attemptMetadata)
	applied := false
	var translated hpatchTranslationResult
	var err error
	retainedStart := len(evaluated) - len(strings.TrimLeft(evaluated, "\r\n"))
	retainedScript := evaluated
	retainedBody, retained := strings.CutPrefix(evaluated[retainedStart:], "in "+shellArtifactPrefix)
	if retained {
		retainedScript = evaluated[:retainedStart] + "in " + retainedBody
	}
	if t.proxy.shellDirectory != "" && retained {
		directory := t.proxy.shellSessionDirectory(t.sessionID)
		root, openErr := os.OpenRoot(directory)
		if openErr != nil {
			return hpatchHistory{}, fmt.Errorf("open retained shell directory: %w", openErr)
		}
		defer root.Close()
		applier, ok := t.proxy.translator.(hpatchApplier)
		if !ok {
			return hpatchHistory{}, errors.New("hpatch translator cannot apply retained shell edits")
		}
		translated, err = applier.Apply(attemptContext, root, retainedScript)
		applied = err == nil
	} else {
		translated, err = t.proxy.translator.Translate(attemptContext, t.directory, evaluated)
	}
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		if errors.Is(err, errHPatchCapacity) {
			return hpatchHistory{}, err
		}
		evaluatorRejected := len(translated.rejections) != 0
		diagnostic := translated.diagnostic
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		if evaluatorRejected {
			diagnostic += hpatchRecoveryGuidance(evaluated, translated.rejections)
		}
		if err := t.recordMetrics(hpatchMetricInputs{
			invocation:    translated.invocation,
			rejections:    translated.rejections,
			attempt:       attemptMetadata,
			emittedScript: input,
			diagnostic:    diagnostic,
		}); err != nil {
			return hpatchHistory{}, err
		}
		history := hpatchHistory{
			toolName: hpatchToolName,
			script:   input,

			root:              t.directory,
			evaluated:         retainedEvaluated(input, evaluated),
			carrierName:       t.codeModeToolName,
			translationError:  diagnostic,
			evaluatorRejected: evaluatorRejected,
			rejections:        slices.Clone(translated.rejections),

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

		root:          t.directory,
		evaluated:     retainedEvaluated(input, evaluated),
		patch:         patchText,
		applied:       applied,
		carrierName:   t.codeModeToolName,
		report:        hpatchReport(translated.report, translated.diagnostic),
		upstreamItem:  maps.Clone(upstreamItem),
		correlationID: attemptMetadata.CorrelationID,
		attempt:       attemptMetadata.Attempt,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func (t *hpatchResponseTransform) translateRecovery(
	callID, input string,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != hpatchRecoveryToolName || history.pluginID != "" || history.script != input {
			return hpatchHistory{}, fmt.Errorf("hpatch recovery call %q changed input", callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	if len(input) > maxHPatchScriptBytes {
		return hpatchHistory{}, fmt.Errorf("hpatch recovery call %q payload exceeds %d bytes", callID, maxHPatchScriptBytes)
	}
	base, baseErr := t.recoveryHistory()
	attemptMetadata := hpatch.AttemptMetadata{
		SessionID:     t.sessionID,
		Title:         t.proxy.titles.title(t.sessionID),
		CorrelationID: callID,
		CallID:        callID,
		Attempt:       1,
		Correction:    true,
		Model:         t.model,
	}
	if baseErr != nil {
		return t.rejectUnevaluated(hpatchRecoveryToolName, callID, input, baseErr, attemptMetadata, "", nil, upstreamItem)
	}
	attemptMetadata.CorrelationID = base.correlationID
	if attemptMetadata.CorrelationID == "" {
		attemptMetadata.CorrelationID = callID
	}
	attemptMetadata.Attempt = t.nextRecoveryAttempt(attemptMetadata.CorrelationID, base.attempt)
	if base.root != t.directory {
		return t.rejectUnevaluated(
			hpatchRecoveryToolName,
			callID,
			input,
			errors.New("the rejected script belongs to a different worktree; send a complete script"),
			attemptMetadata,
			"",
			nil,
			upstreamItem,
		)
	}
	baseline := base.recoveryBaseline()
	rebuilt, err := recoverScript(t.ctx, baseline, input)
	if err != nil {
		return t.rejectUnevaluated(
			hpatchRecoveryToolName,
			callID,
			input,
			err,
			attemptMetadata,
			baseline,
			base.rejections,
			upstreamItem,
		)
	}
	history, err := t.translateRecovered(callID, input, rebuilt, attemptMetadata, upstreamItem)
	if err == nil {
		history.toolName = hpatchRecoveryToolName
		t.local[callID] = history
	}
	return history, err
}

func (t *hpatchResponseTransform) translateRecovered(
	callID, emitted, evaluated string,
	attemptMetadata hpatch.AttemptMetadata,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	attemptContext := hpatch.WithAttemptMetadata(t.ctx, attemptMetadata)
	translated, err := t.proxy.translator.Translate(attemptContext, t.directory, evaluated)
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		if errors.Is(err, errHPatchCapacity) {
			return hpatchHistory{}, err
		}
		evaluatorRejected := len(translated.rejections) != 0
		diagnostic := translated.diagnostic
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		if evaluatorRejected {
			diagnostic += hpatchRecoveryGuidance(evaluated, translated.rejections)
		}
		if err := t.recordMetrics(hpatchMetricInputs{
			invocation:    translated.invocation,
			rejections:    translated.rejections,
			attempt:       attemptMetadata,
			emittedScript: emitted,
			emittedTool:   hpatchRecoveryToolName,
			diagnostic:    diagnostic,
		}); err != nil {
			return hpatchHistory{}, err
		}
		history := hpatchHistory{
			toolName:          hpatchRecoveryToolName,
			script:            emitted,
			root:              t.directory,
			evaluated:         evaluated,
			carrierName:       t.codeModeToolName,
			translationError:  diagnostic,
			evaluatorRejected: evaluatorRejected,
			rejections:        slices.Clone(translated.rejections),
			upstreamItem:      maps.Clone(upstreamItem),
			correlationID:     attemptMetadata.CorrelationID,
			attempt:           attemptMetadata.Attempt,
		}
		t.recordLocal(callID, &history)
		return history, nil
	}
	patchText := string(translated.patch)
	if err := t.recordMetrics(hpatchMetricInputs{
		invocation:    translated.invocation,
		attempt:       attemptMetadata,
		emittedScript: emitted,
		emittedTool:   hpatchRecoveryToolName,
		report:        translated.report,
		patch:         patchText,
		successful:    true,
		diagnostic:    translated.diagnostic,
	}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		toolName:      hpatchRecoveryToolName,
		script:        emitted,
		root:          t.directory,
		evaluated:     evaluated,
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

func (p *hpatchProxy) shellSessionDirectory(sessionID string) string {
	return filepath.Join(p.shellDirectory, "hpatch-"+sessionID)
}

func (p *hpatchProxy) retainShell(sessionID, callID, script string) (string, bool) {
	if p.shellDirectory == "" {
		return "", false
	}
	reference := shellArtifactPrefix + callID
	path := filepath.Join(p.shellSessionDirectory(sessionID), callID)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", false
	}
	if p.shellSessions == nil {
		p.shellSessions = make(map[string]struct{})
	}
	p.shellSessions[sessionID] = struct{}{}
	p.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false
	}
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return "", false
	}
	time.AfterFunc(shellArtifactTTL, func() {
		_ = os.Remove(path)
	})
	return reference, true
}

func (t *hpatchResponseTransform) translateTool(name, callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	switch name {
	case hpatchToolName:
		return t.translate(callID, input, upstreamItem)
	case hpatchRecoveryToolName:
		return t.translateRecovery(callID, input, upstreamItem)
	case reportIssueToolName:
		return t.translateReportIssue(callID, input, upstreamItem)
	}
	contribution, ok := t.proxy.registry.contribution(name)
	if !ok || contribution.Builtin {
		return hpatchHistory{}, fmt.Errorf("registered tool %q is unavailable", name)
	}
	return t.translateRegisteredTool(contribution, callID, input, upstreamItem)
}

func (t *hpatchResponseTransform) translateReportIssue(callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != reportIssueToolName || history.script != input {
			return hpatchHistory{}, fmt.Errorf("report_issue call %q changed input", callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	if err := t.proxy.registry.DiagnoseHooks.Report(t.ctx, input); err != nil {
		return hpatchHistory{}, fmt.Errorf("report issue: %w", err)
	}
	history := hpatchHistory{
		toolName:     reportIssueToolName,
		script:       input,
		carrierName:  t.codeModeToolName,
		report:       "Issue reported.",
		applied:      true,
		upstreamItem: maps.Clone(upstreamItem),
	}
	t.recordLocal(callID, &history)
	return history, nil
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
	pathPrefix := t.proxy.shellSessionDirectory(t.sessionID) + string(os.PathSeparator)
	recovered := lunaShellCodeModeProgram(contribution, input)
	var translation toolplugin.Translation
	var err error
	if !recovered {
		translation, err = toolplugin.Translate(
			t.ctx,
			t.proxy.registry.NodeExecutable,
			t.proxy.registry.RuntimeRoot,
			contribution.Module,
			contribution.ModuleIndex,
			input,
			pathPrefix,
		)
		if err != nil {
			return hpatchHistory{}, fmt.Errorf("translate registered tool %s: %w", contribution.Name, err)
		}
	}
	var resultMetadata map[string]json.RawMessage
	if translation.Carrier.RetainInput != nil {
		resultMetadata = map[string]json.RawMessage{"retained": mustMarshalJSON(false)}
		if *translation.Carrier.RetainInput {
			reference, retained := t.proxy.retainShell(t.sessionID, callID, input)
			resultMetadata["retained"] = mustMarshalJSON(retained)
			if retained {
				resultMetadata["script_ref"] = mustMarshalJSON(reference)
			}
		}
	}

	kind := codeModeCarrierCustom
	name := t.codeModeToolName
	payload := ""
	diagnostic := translation.Diagnostic
	var misuseWarnings []string
	if recovered {
		misuseWarnings = append(misuseWarnings, lunaShellRecoveryWarning)
		payload = input
		if err := t.carriers.require(name, kind); err != nil {
			return hpatchHistory{}, fmt.Errorf("%s Code Mode exec recovery: %w", contribution.Name, err)
		}
	} else if translation.Rejected {
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
					resultMetadata,
				)
			} else {
				payload, err = workerTemplateExecInputWithParams(
					contribution.Name,
					translation.Arguments,
					translation.Carrier.Template,
					translation.Carrier.Params,
					resultMetadata,
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
	if !translation.Rejected {
		for _, misuse := range shellInterpreterWrapperMisuses(contribution, input) {
			misuseWarnings = append(misuseWarnings, shellInterpreterWrapperWarning(misuse))
		}
	}
	misuseWarning := ""
	if recovered {
		for _, warning := range misuseWarnings {
			misuseWarning += misuseWarningProjection(warning)
		}
		payload = misuseWarning + payload
	} else {
		for _, warning := range misuseWarnings {
			warnedPayload, warningInput, _, warningErr := insertExecCommandWarning(payload, warning)
			if warningErr != nil {
				return hpatchHistory{}, fmt.Errorf("%s interpreter-wrapper warning: %w", contribution.Name, warningErr)
			}
			misuseWarning += warningInput
			payload = warnedPayload
		}
	}

	metricName := name
	metricPayload := payload
	if !translation.Rejected && translation.Carrier.Kind == "exec" && translation.Carrier.StockCommand != "" {
		stockCommand := translation.Carrier.StockCommand
		if translation.Carrier.Template != "" {
			stockCommand = strings.Replace(translation.Carrier.Template, "{.}", stockCommand, 1)
		}
		if stockPayload, stockErr := workerCommandExecInputWithParams(stockCommand, translation.Carrier.Params); stockErr == nil {
			metricName = "functions.exec"
			metricPayload = stockPayload
		}
	}

	metricCall := &hpatch.HostToolCall{
		PluginID:          contribution.PluginID,
		ToolName:          contribution.Name,
		EmittedName:       contribution.Name,
		EmittedInput:      input,
		FailedTranslation: translation.Rejected,
	}
	if recovered {
		metricCall.Recovery = hpatch.HostToolRecoveryCodeModeShell
	}
	if !translation.Rejected {
		metricCall.TranslatedName = metricName
		metricCall.TranslatedPayload = metricPayload
	}
	if err := t.recordMetrics(hpatchMetricInputs{
		overheadOnly:  true,
		toolCall:      metricCall,
		diagnostic:    diagnostic,
		misuseWarning: misuseWarning,
	}); err != nil {
		return hpatchHistory{}, err
	}

	history := hpatchHistory{
		toolName:         contribution.Name,
		pluginID:         contribution.PluginID,
		script:           input,
		root:             t.directory,
		carrierKind:      kind,
		carrierName:      name,
		carrierPayload:   payload,
		translationError: diagnostic,
		upstreamItem:     maps.Clone(upstreamItem),
		replayCarrier:    recovered,
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

func directBashExecCommand(arguments []string) (string, bool) {
	if len(arguments) != 2 || arguments[0] != "bash" || arguments[1] == "" {
		return "", false
	}
	command := strings.TrimSuffix(arguments[1], "\n")
	command = strings.TrimSuffix(command, "\r")
	for _, prefix := range []string{"set -e\n", "set -e\r\n"} {
		if candidate, found := strings.CutPrefix(command, prefix); found &&
			candidate != "" && !strings.ContainsAny(candidate, "\r\n;&|`(){}") {
			command = candidate
			break
		}
	}
	return command, true
}

func workerCommand(executable string, arguments []string) string {
	command := shellQuoteArgument(executable)
	for _, argument := range arguments {
		command += " " + shellQuoteArgument(argument)
	}
	return command
}

func workerExecInputWithParams(executable string, arguments []string, params map[string]json.RawMessage, resultMetadata ...map[string]json.RawMessage) (string, error) {
	command := workerCommand(executable, arguments)
	if executable == "shell" {
		if direct, ok := directBashExecCommand(arguments); ok {
			command = direct
		}
	}
	return workerCommandExecInputWithResult(command, params, executable == "shell", resultMetadata...)
}

func workerTemplateExecInputWithParams(executable string, arguments []string, template string, params map[string]json.RawMessage, resultMetadata ...map[string]json.RawMessage) (string, error) {
	if strings.Count(template, "{.}") != 1 {
		return "", errors.New("exec command template must contain exactly one {.} placeholder")
	}
	command := strings.Replace(template, "{.}", workerCommand(executable, arguments), 1)
	return workerCommandExecInputWithResult(command, params, executable == "shell", resultMetadata...)
}

func workerCommandExecInputWithParams(command string, params map[string]json.RawMessage) (string, error) {
	return workerCommandExecInputWithResult(command, params, false)
}

func workerCommandExecInputWithResult(command string, params map[string]json.RawMessage, forwardNativeResult bool, resultMetadata ...map[string]json.RawMessage) (string, error) {
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
	resultOutput := "text(result.output);"
	if forwardNativeResult || len(resultMetadata) > 0 && len(resultMetadata[0]) > 0 {
		resultOutput = "text(JSON.stringify(result));"
		if len(resultMetadata) > 0 && len(resultMetadata[0]) > 0 {
			resultOutput = "text(JSON.stringify(Object.assign({}, result, " + string(mustMarshalJSON(resultMetadata[0])) + ")));"
		}
	}
	return "const result = await tools.exec_command(" + string(mustMarshalJSON(arguments)) + ");\n" +
		resultOutput, nil
}

func misuseWarningProjection(warning string) string {
	return "text(" + string(mustMarshalJSON(warning+"\n")) + ");\n"
}

func insertExecCommandWarning(input, warning string) (string, string, bool, error) {
	call := strings.Index(input, codeModeExecCallPrefix)
	if call < 0 {
		return input, "", false, errors.New("Code Mode input has no exec_command call")
	}
	projectionStart := -1
	for _, projection := range []string{
		codeModeOutputProjection,
		codeModeJSONProjection,
		codeModeMetadataProjection,
	} {
		if index := strings.Index(input[call:], "\n"+projection); index >= 0 {
			index += call + 1
			if projectionStart < 0 || index < projectionStart {
				projectionStart = index
			}
		}
	}
	if projectionStart < 0 {
		return input, "", false, errors.New("Code Mode input has no result projection")
	}
	warningInput := misuseWarningProjection(warning)
	if strings.Contains(input[call:projectionStart], warningInput) {
		return input, warningInput, false, nil
	}
	return input[:projectionStart] + warningInput + input[projectionStart:], warningInput, true, nil
}

func (h hpatchHistory) carrierInput() string {
	if h.pluginID != "" || h.carrierKind != "" {
		return h.carrierPayload
	}
	if h.translationError != "" {
		return hpatchDiagnosticExecInput(h.translationError)
	}
	if h.applied {
		return hpatchDiagnosticExecInput(h.report)
	}
	return hpatchApplyExecInput(h.patch, h.report)
}

func (h hpatchHistory) effectiveCarrierKind() codeModeCarrierKind {
	if h.carrierKind != "" {
		return h.carrierKind
	}
	return codeModeCarrierCustom
}

func (t *hpatchResponseTransform) rejectUnevaluated(
	toolName, callID, input string,
	rejection error,
	attempt hpatch.AttemptMetadata,
	referenceScript string,
	rejections []hpatch.HostRejection,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	diagnostic := rejection.Error()
	if referenceScript != "" {
		diagnostic += hpatchRecoveryGuidance(referenceScript, rejections)
	}
	if err := t.recordMetrics(hpatchMetricInputs{attempt: attempt, emittedScript: input, emittedTool: toolName, diagnostic: diagnostic}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		toolName: toolName,
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

// recoveryHistory is the rejected call a recovery in this turn edits. A
// rejection this turn is newer than retained history, which commits only after
// the response completes.
func (t *hpatchResponseTransform) recoveryHistory() (hpatchHistory, error) {
	for _, history := range t.local {
		if !history.unevaluated &&
			(history.toolName == hpatchToolName || history.toolName == hpatchRecoveryToolName) {
			return recoveryHistoryOf(maps.Values(t.local))
		}
	}
	return t.proxy.recoverableHistory(t.historySessionID)
}

func (t *hpatchResponseTransform) nextRecoveryAttempt(correlationID string, baseAttempt int) int {
	latest := max(baseAttempt, t.proxy.latestRecoveryAttempt(t.historySessionID, correlationID))
	latest = max(latest, latestRecoveryAttempt(maps.Values(t.local), correlationID))
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
		if t.codeModeToolName != "" && name == t.codeModeToolName {
			itemID := jsonString(item, "id")
			if jsonString(item, "type") == "custom_tool_call" && itemID != "" {
				t.nativeExecItems[itemID] = struct{}{}
			}
			return [][]byte{payload}, nil
		}
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
			if _, nativeExec := t.nativeExecItems[envelope.ItemID]; nativeExec {
				input, _, changed, _ := nativeExecCommandInput(envelope.Input)
				if changed {
					event, err := replaceRawField(payload, "input", mustMarshalJSON(input))
					return onePayload(event, err)
				}
			}
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
		delete(t.nativeExecItems, jsonString(item, "id"))
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
		clear(t.nativeExecItems)
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
		clear(t.nativeExecItems)
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

const (
	nativeExecCommandWarning = "Warning: Use `functions.shell`"
	lunaShellRecoveryWarning = "Recovered Code Mode JavaScript submitted through `functions.shell`; use `functions.exec` directly next time"

	codeModeExecCallPrefix        = "const result = await tools.exec_command("
	codeModeOutputProjection      = "text(result.output);"
	codeModeJSONProjection        = "text(JSON.stringify(result));"
	codeModeMetadataProjection    = "text(JSON.stringify(Object.assign({}, result, "
	codeModeMetadataProjectionEnd = ")));"

	shellInterpreterNamePattern = `python(?:[0-9]+(?:\.[0-9]+)*)?|pypy[0-9]*|node(?:js)?|bun|` +
		`bash|dash|fish|ksh|mksh|sh|yash|zsh|perl|ruby|php|lua(?:jit)?|` +
		`r(?:script)?|psql|mysql|sqlite3|pwsh|powershell`
)

var (
	codeModeToolProgramPattern = regexp.MustCompile(
		`^(?:(?:const|let|var)[ \t]+[A-Za-z_$][A-Za-z0-9_$]*[ \t]*=[ \t]*)?await[ \t]+tools\.[A-Za-z_$][A-Za-z0-9_$]*[ \t]*\(`,
	)
	codeModeProjectionPattern             = regexp.MustCompile(`(?m)(?:^|;)[ \t]*(?:text|image|audio|generatedImage)[ \t]*\(`)
	shellHeredocPattern                   = regexp.MustCompile(`<<-?`)
	shellInterpreterCommandWrapperPattern = regexp.MustCompile(
		`(?i)(?:^|\\[nrt]|[^[:alnum:]_./+-])/?(?:[[:alnum:]_.+-]+/)*(` + shellInterpreterNamePattern + `)` +
			`((?:[ \t]+-[^ \t\r\n]+)*)[ \t]+(-[[:alpha:]]*[cer]|--(?:command|execute))(?:[^[:alpha:]]|$)`,
	)
	shellInterpreterHeredocPattern = regexp.MustCompile(
		`(?i)(?:^|\\[nrt]|[^[:alnum:]_./+-])/?(?:[[:alnum:]_.+-]+/)*(` + shellInterpreterNamePattern + `)` +
			`((?:[ \t]+-[^ \t\r\n<]+)*)[ \t]+(-?[ \t]*<<-?)`,
	)
)

type shellWrapperMisuse struct {
	Kind            string
	Interpreter     string
	InterpreterArgs []string
	wrapper         string
}

// Match the raw input intentionally: every heredoc and quoted or commented example warns too.
func shellInterpreterWrapperMisuses(contribution toolContribution, input string) []shellWrapperMisuse {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" {
		return nil
	}

	var misuses []shellWrapperMisuse
	seenKinds := make(map[string]bool)
	for _, match := range shellInterpreterCommandWrapperPattern.FindAllStringSubmatch(input, -1) {
		wrapper := match[3]
		kind := strings.ToLower(wrapper)
		interpreterArgs := strings.Fields(match[2])
		if strings.HasPrefix(wrapper, "-") && !strings.HasPrefix(wrapper, "--") && len(wrapper) > 2 {
			kind = "-" + strings.ToLower(wrapper[len(wrapper)-1:])
			interpreterArgs = append(interpreterArgs, "-"+wrapper[1:len(wrapper)-1])
		}
		if seenKinds[kind] {
			continue
		}
		seenKinds[kind] = true
		misuses = append(misuses, shellWrapperMisuse{
			Kind:            kind,
			Interpreter:     match[1],
			InterpreterArgs: interpreterArgs,
			wrapper:         wrapper,
		})
	}

	if match := shellInterpreterHeredocPattern.FindStringSubmatch(input); match != nil {
		misuse := shellWrapperMisuse{Kind: "heredoc"}
		if !shellInterpreterCommandWrapperPattern.MatchString(match[0]) {
			wrapper := "<<"
			if strings.HasPrefix(strings.TrimSpace(match[3]), "-") {
				wrapper = "- <<"
			}
			misuse.Interpreter = match[1]
			misuse.InterpreterArgs = strings.Fields(match[2])
			misuse.wrapper = wrapper
		}
		misuses = append(misuses, misuse)
	} else if shellHeredocPattern.MatchString(input) {
		misuses = append(misuses, shellWrapperMisuse{Kind: "heredoc"})
	}
	return misuses
}

func shellInterpreterWrapperWarning(misuse shellWrapperMisuse) string {
	if misuse.Interpreter == "" {
		return "functions.shell: warning: heredoc detected; submit the script body directly instead of wrapping it in a heredoc"
	}

	program := "the program"
	interpreter := strings.ToLower(misuse.Interpreter)
	switch {
	case strings.HasPrefix(interpreter, "python"), strings.HasPrefix(interpreter, "pypy"):
		program = "the Python program"
	case interpreter == "node", interpreter == "nodejs", interpreter == "bun":
		program = "the JavaScript program"
	}

	invocationArgs := misuse.InterpreterArgs
	if strings.HasPrefix(misuse.wrapper, "-") && !strings.HasPrefix(misuse.wrapper, "--") &&
		len(misuse.wrapper) > 2 && misuse.Kind != "heredoc" {
		invocationArgs = invocationArgs[:len(invocationArgs)-1]
	}
	invocation := strings.Join(append([]string{misuse.Interpreter}, invocationArgs...), " ")
	if invocation != "" {
		invocation += " "
	}
	invocation += misuse.wrapper

	if strings.EqualFold(misuse.Interpreter, "bash") {
		if misuse.Kind == "heredoc" {
			return fmt.Sprintf(
				"functions.shell: warning: remove the `%s...` heredoc wrapper and submit the Bash script body directly without a shebang",
				invocation,
			)
		}
		return fmt.Sprintf(
			"functions.shell: warning: remove the `%s` wrapper and submit the Bash script body directly without a shebang",
			invocation,
		)
	}

	shebang := strings.Join(append([]string{"#!" + misuse.Interpreter}, misuse.InterpreterArgs...), " ")
	if misuse.Kind == "heredoc" {
		return fmt.Sprintf(
			"functions.shell: warning: remove the `%s...` heredoc wrapper; start the script with `%s` and put %s directly in the body",
			invocation,
			shebang,
			program,
		)
	}
	return fmt.Sprintf(
		"functions.shell: warning: replace `%s ...` with `%s` on the first line and put %s directly in the body",
		invocation,
		shebang,
		program,
	)
}

// Luna occasionally submits Code Mode JavaScript through functions.shell.
// Recover only programs whose first statement invokes a nested Code Mode tool
// and which project a result through a Code Mode output helper. A shebang,
// directive, comment, or ordinary shell statement at the start keeps the
// documented shell semantics.
func lunaShellCodeModeProgram(contribution toolContribution, input string) bool {
	if contribution.PluginID != builtinToolsPluginID || contribution.Name != "shell" {
		return false
	}
	program := strings.TrimLeft(input, " \t\r\n")
	return codeModeToolProgramPattern.MatchString(program) &&
		codeModeProjectionPattern.MatchString(program)
}

func nativeExecCommandInput(input string) (string, string, bool, bool) {
	rewritten, warningInput, changed, err := insertExecCommandWarning(input, nativeExecCommandWarning)
	if err != nil {
		return input, "", false, false
	}
	return rewritten, warningInput, changed, true
}

func (t *hpatchResponseTransform) transformOutputItem(item map[string]json.RawMessage) (bool, error) {
	name := jsonString(item, "name")
	if t.codeModeToolName != "" && name == t.codeModeToolName &&
		jsonString(item, "type") == "custom_tool_call" {
		input, misuseWarning, changed, detected := nativeExecCommandInput(jsonString(item, "input"))
		if detected {
			if changed {
				item["input"] = mustMarshalJSON(input)
			}
			identity := jsonString(item, "call_id")
			if identity == "" {
				identity = jsonString(item, "id")
			}
			_, metered := t.nativeExecWarningsMetered[identity]
			if identity == "" || !metered {
				if err := t.recordMetrics(hpatchMetricInputs{overheadOnly: true, misuseWarning: misuseWarning}); err != nil {
					return false, err
				}
				if identity != "" {
					t.nativeExecWarningsMetered[identity] = struct{}{}
				}
			}
		}
		return changed, nil
	}
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
