package router

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yusing/hpatch"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
	"github.com/yusing/hpatch/internal/router/toolplugin"
)

const (
	hpatchToolName     = "hpatch"
	applyPatchToolName = "apply_patch"

	maxHPatchScriptBytes = 1 << 20
	maxHPatchPatchBytes  = 16 << 20

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
	failures   []hpatch.HostFailure
	change     hpatch.HostChange
	aliases    []hpatch.TargetAlias
}

type hpatchTranslator interface {
	Translate(ctx context.Context, directory, script string) (hpatchTranslationResult, error)
	ToolDescription() string
}

type hpatchApplier interface {
	Apply(ctx context.Context, root *os.Root, script string) (hpatchTranslationResult, error)
}

type inProcessHPatchTranslator struct {
	dataDirectory string
}

func hpatchDataDirectory() (string, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determine hpatch data directory: %w", err)
	}
	return filepath.Join(configDirectory, "hpatch"), nil
}

func newInProcessHPatchTranslator(dataDirectory string) hpatchTranslator {
	return inProcessHPatchTranslator{dataDirectory: dataDirectory}
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

func (t inProcessHPatchTranslator) ReportOutcome(ctx context.Context, stage, outcome string) error {
	return hpatch.ReportHostOutcome(ctx, t.dataDirectory, stage, outcome)
}

func hpatchTranslationResultOf(translated hpatch.HostTranslation) hpatchTranslationResult {
	return hpatchTranslationResult{
		patch:      translated.Patch,
		report:     translated.Report,
		diagnostic: translated.Diagnostic,
		rejections: slices.Clone(translated.Rejections),
		failures:   slices.Clone(translated.Failures),
		change:     translated.Change,
		aliases:    slices.Clone(translated.TargetAliases),
	}
}

type hpatchProxy struct {
	translator             hpatchTranslator
	registry               *toolRegistry
	customizedInstructions bool
	modelInstructions      string
	shellDirectory         string
	titles                 *sessionTitleCache
	shellSessions          map[string]struct{}
	commentary             *commentaryBroker
	commentaryEndpoint     string

	mu              sync.RWMutex
	sessions        map[string]*hpatchHistorySession
	activeSessions  map[string]int
	historyBytes    int
	sessionSequence uint64
	closed          bool
}

func newHPatchProxy(translator hpatchTranslator, registry *toolRegistry, customizedInstructions, compactModelProtocol bool, titleCaches ...*sessionTitleCache) *hpatchProxy {
	if translator == nil || registry == nil {
		return nil
	}
	titles := newSessionTitleCache()
	if len(titleCaches) != 0 && titleCaches[0] != nil {
		titles = titleCaches[0]
	}
	directory := registry.runtimeDirectory
	modelInstructions := codexinstructions.NativeInstructions()
	if compactModelProtocol {
		modelInstructions = codexinstructions.Instructions()
	}
	return &hpatchProxy{
		translator:             translator,
		registry:               registry,
		customizedInstructions: customizedInstructions,
		modelInstructions:      modelInstructions,
		shellDirectory:         directory,
		titles:                 titles,
		shellSessions:          make(map[string]struct{}),
		commentary:             newCommentaryBroker(),
		sessions:               make(map[string]*hpatchHistorySession),
		activeSessions:         make(map[string]int),
	}
}

func (p *hpatchProxy) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	var cleanupErr error
	for directory := range p.shellSessions {
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(directory))
	}
	clear(p.shellSessions)
	clear(p.sessions)
	clear(p.activeSessions)
	p.historyBytes = 0
	if p.commentary != nil {
		p.commentary.close()
	}
	return cleanupErr
}

type hpatchPendingCall struct {
	callID     string
	toolName   string
	structured bool

	added         []byte
	argumentsDone []byte
}

type hpatchResponseTransform struct {
	ctx              context.Context
	proxy            *hpatchProxy
	sessionID        string
	shellDirectory   string
	model            string
	historySessionID string
	sessionActive    bool

	originalTools             json.RawMessage
	originalToolsPresent      bool
	originalToolChoice        json.RawMessage
	originalToolChoicePresent bool
	pending                   map[string]hpatchPendingCall
	nativeExecCalls           map[string]string
	local                     map[string]hpatchHistory
	directory                 string
	carriers                  codeModeCarrierCatalog
	commentaryTools           commentaryToolCatalog
	commentaryTokens          []string
	deferredCommentary        []publishedCommentary
	commentaryEmitted         map[string]struct{}
	subagentTools             map[string]struct{}
	subagentPending           map[string]subagentPendingCall
	subagentDeferred          []map[string]json.RawMessage
	subagentResponses         []map[string]json.RawMessage
	subagentTurn              bool
	parentModel               string
	parentReasoningEffort     string

	codeModeToolName string
	nativeTools      bool

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
	t.cancelCommentaryTokens()
	if t.sessionActive {
		t.proxy.deactivateSession(t.historySessionID)
		t.sessionActive = false
	}
}

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

func (p *hpatchProxy) prepareRequest(ctx context.Context, request *parsedResponsesRequest, sessionID, threadID string, metadata codexTurnMetadata, metadataValid bool) (*hpatchResponseTransform, error) {
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
	if err := rewriteReceivedModelInstructions(request, p.customizedInstructions, p.modelInstructions); err != nil {
		return nil, err
	}
	subagentTools := subagentToolCatalog(request.fields)
	subagentDeferred := prepareSubagentInputCommentary(request.fields)
	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(request.fields["reasoning"], &reasoning)
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
	codeModeToolName, replaced, err := replaceAdditionalToolsApplyPatch(request.fields, installedTools)
	if err != nil {
		return nil, err
	}
	nativeTools := false
	if !replaced {
		codeModeToolName, replaced, err = replaceNativeTools(request.fields, installedTools)
		if err != nil {
			return nil, err
		}
		nativeTools = replaced
	}
	if !replaced {
		return nil, errors.New("responses request cannot satisfy the required hpatch rewrite")
	}
	var commentaryTools commentaryToolCatalog
	if p.commentaryEndpoint != "" {
		commentaryTools, err = prepareCommentaryTools(request.fields)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("hpatch rewrite requires a valid Codex thread ID")
	}
	shellDirectory, err := p.storeShellRuntime(threadID)
	if err != nil {
		return nil, fmt.Errorf("store shell runtime: %w", err)
	}
	historySessionID := directory + "\x00" + sessionID
	if err := p.activateSession(historySessionID); err != nil {
		return nil, err
	}
	if err := p.reconcileInputPrefix(request, historySessionID); err != nil {
		p.deactivateSession(historySessionID)
		return nil, err
	}
	deferredCommentary := p.drainCommentarySession(historySessionID)
	return &hpatchResponseTransform{
		ctx:              ctx,
		proxy:            p,
		sessionID:        sessionID,
		shellDirectory:   shellDirectory,
		model:            request.modelDescription(),
		historySessionID: historySessionID,
		sessionActive:    true,

		originalTools:             originalTools,
		originalToolsPresent:      originalToolsPresent,
		originalToolChoice:        originalToolChoice,
		originalToolChoicePresent: originalToolChoicePresent,
		pending:                   make(map[string]hpatchPendingCall),
		nativeExecCalls:           make(map[string]string),
		local:                     make(map[string]hpatchHistory),
		directory:                 directory,
		carriers:                  carriers,
		subagentTools:             subagentTools,
		subagentPending:           make(map[string]subagentPendingCall),
		subagentDeferred:          subagentDeferred,
		subagentResponses:         subagentDeferred,
		subagentTurn:              metadata.SubagentKind != "",
		parentModel:               request.model(),
		parentReasoningEffort:     strings.TrimSpace(reasoning.Effort),
		commentaryTools:           commentaryTools,
		deferredCommentary:        deferredCommentary,
		commentaryEmitted:         make(map[string]struct{}),

		codeModeToolName: codeModeToolName,
		nativeTools:      nativeTools,
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

	strippedDescription          string
	execCommandParamsDescription string
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
func replaceAdditionalToolsApplyPatch(fields map[string]json.RawMessage, installedTools []map[string]json.RawMessage) (string, bool, error) {
	var topTools []map[string]json.RawMessage
	if rawTools, exists := fields["tools"]; exists {
		if err := json.Unmarshal(rawTools, &topTools); err != nil {
			return "", false, fmt.Errorf("decode responses tools: %w", err)
		}
	}
	installedNames := installedToolNames(installedTools)
	owner, err := findAdditionalToolsApplyPatch(fields, installedNames)
	if err != nil || owner == nil {
		return "", false, err
	}
	for _, tool := range topTools {
		name := jsonString(tool, "name")
		if _, exists := installedNames[name]; exists {
			return "", false, fmt.Errorf("responses request already defines %s", name)
		}
		if name == applyPatchToolName || name == "exec" || name == "functions.exec" {
			return "", false, fmt.Errorf("responses request exposes unsupported top-level %s", name)
		}
	}
	if codeModeToolChoiceRestricted(fields, owner.name) {
		return "", false, nil
	}
	if err := exposeStandaloneHPatch(fields, topTools, owner, installedTools); err != nil {
		return "", false, err
	}
	return owner.name, true, nil
}

// replaceNativeTools replaces the native apply_patch definition while retaining
// exec_command as the executor-owned carrier for translated results.
func replaceNativeTools(fields map[string]json.RawMessage, installedTools []map[string]json.RawMessage) (string, bool, error) {
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		return "", false, nil //nolint:nilerr // An absent native tool array belongs to another request shape.
	}
	installedNames := installedToolNames(installedTools)
	applyPatchIndex := -1
	execCommandIndex := -1
	for index, tool := range tools {
		name := jsonString(tool, "name")
		if _, exists := installedNames[name]; exists {
			return "", false, fmt.Errorf("responses request already defines %s", name)
		}
		switch name {
		case applyPatchToolName:
			if applyPatchIndex >= 0 {
				return "", false, errors.New("responses request defines native apply_patch more than once")
			}
			if jsonString(tool, "type") != "custom" {
				return "", false, errors.New("responses native apply_patch is not a custom tool")
			}
			applyPatchIndex = index
		case nativeExecCommandToolName:
			if execCommandIndex >= 0 {
				return "", false, errors.New("responses request defines native exec_command more than once")
			}
			if jsonString(tool, "type") != "function" {
				return "", false, errors.New("responses native exec_command is not a function tool")
			}
			execCommandIndex = index
		case "exec", "functions.exec":
			return "", false, fmt.Errorf("responses request exposes unsupported top-level %s", name)
		}
	}
	if applyPatchIndex < 0 || execCommandIndex < 0 {
		return "", false, nil
	}
	var choice map[string]json.RawMessage
	if json.Unmarshal(fields["tool_choice"], &choice) == nil {
		selected := jsonString(choice, "name")
		if selected == applyPatchToolName || selected == nativeExecCommandToolName {
			return "", false, nil
		}
	}
	tools = append(tools[:applyPatchIndex], tools[applyPatchIndex+1:]...)
	tools = append(tools, installedTools...)
	encoded, err := json.Marshal(tools)
	if err != nil {
		return "", false, fmt.Errorf("encode native Responses tools: %w", err)
	}
	fields["tools"] = encoded
	return nativeExecCommandToolName, true, nil
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
		stripped, found, err := stripCodeModeApplyPatchSection(jsonString(tool, "description"))
		if err != nil {
			return err
		}
		if !found || jsonString(tool, "type") != "custom" {
			return nil
		}
		var execCommandParamsDescription string
		stripped, execCommandParamsDescription, _, err = stripCodeModeExecCommandContract(stripped)
		if err != nil {
			return err
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
			execCommandParamsDescription: execCommandParamsDescription,
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
func stripCodeModeApplyPatchSection(description string) (string, bool, error) {
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
	stripped, _, found, err := stripCodeModeSection(
		description,
		findHeading,
		valid,
		"responses Code Mode tool defines nested apply_patch more than once",
	)
	return stripped, found, err
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
func stripCodeModeExecCommandContract(description string) (string, string, bool, error) {
	stripped, section, found, err := stripCodeModeExecCommandSection(description)
	if err != nil {
		return "", "", false, err
	}
	if !found {
		if strings.Contains(description, "exec_command") {
			return "", "", false, errors.New("responses Code Mode tool exposes exec_command without an owned section")
		}
		return description, "", false, nil
	}
	const example = " for example `await tools.exec_command(...)`."
	if count := strings.Count(stripped, example); count > 1 {
		return "", "", false, errors.New("responses Code Mode tool references tools.exec_command more than once outside its section")
	} else if count == 1 {
		stripped = strings.Replace(stripped, example, "", 1)
	}
	if strings.Contains(stripped, "exec_command") {
		return "", "", false, errors.New("responses Code Mode tool exposes exec_command outside its owned contract")
	}
	return stripped, execCommandParamsDescription(section), true, nil
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

	evaluated, err := hpatch.RewriteTargetAliases(input, t.proxy.targetAliases(t.historySessionID, t.directory))
	if err != nil {
		// Preserve evaluator-owned syntax diagnostics for malformed scripts.
		evaluated = input
	}
	attemptMetadata := hpatch.AttemptMetadata{
		SessionID:       t.sessionID,
		Title:           t.proxy.titles.title(t.sessionID),
		CorrelationID:   callID,
		CallID:          callID,
		Attempt:         1,
		Correction:      false,
		Model:           t.model,
		ToolName:        hpatchToolName,
		EmittedPayload:  input,
		EvaluatedScript: evaluated,
	}

	applied := false
	var translated hpatchTranslationResult
	retainedStart := len(evaluated) - len(strings.TrimLeft(evaluated, "\r\n"))
	retainedScript := evaluated
	retainedBody, retained := strings.CutPrefix(evaluated[retainedStart:], "in "+shellArtifactPrefix)
	if retained {
		retainedScript = evaluated[:retainedStart] + "in " + retainedBody
	}
	retainedApply := t.proxy.shellDirectory != "" && retained
	if retainedApply {
		attemptMetadata.EvaluatedScript = retainedScript
	}
	attemptContext := hpatch.WithAttemptMetadata(t.ctx, attemptMetadata)
	if retainedApply {
		root, openErr := os.OpenRoot(t.shellDirectory)
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
			diagnostic += hpatchRecoveryGuidance(evaluated, translated.rejections, false)
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
	alreadySatisfied := translated.change.AlreadySatisfied
	history := hpatchHistory{
		toolName: hpatchToolName,
		script:   input,

		root:             t.directory,
		evaluated:        retainedEvaluated(input, evaluated),
		patch:            patchText,
		applied:          applied,
		alreadySatisfied: alreadySatisfied,
		confirmed:        applied,
		aliases:          slices.Clone(translated.aliases),
		carrierName:      t.codeModeToolName,
		report:           hpatchReport(translated.report, translated.diagnostic),
		upstreamItem:     maps.Clone(upstreamItem),
		correlationID:    attemptMetadata.CorrelationID,
		attempt:          attemptMetadata.Attempt,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func (p *hpatchProxy) retainShell(directory, callID, script string) (string, bool) {
	if directory == "" {
		return "", false
	}
	reference := shellArtifactPrefix + callID
	path := filepath.Join(directory, callID)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", false
	}
	if p.shellSessions == nil {
		p.shellSessions = make(map[string]struct{})
	}
	p.shellSessions[directory] = struct{}{}
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
	attemptContext := hpatch.WithAttemptMetadata(t.ctx, hpatch.AttemptMetadata{
		SessionID:       t.sessionID,
		Title:           t.proxy.titles.title(t.sessionID),
		CorrelationID:   callID,
		CallID:          callID,
		Attempt:         1,
		Model:           t.model,
		ToolName:        reportIssueToolName,
		EmittedPayload:  input,
		EvaluatedScript: input,
	})
	report := "Issue reported."
	if err := t.proxy.registry.DiagnoseHooks.Report(attemptContext, input); err != nil {
		report = "Issue report was not delivered.\nhpatch: warning: " + strings.TrimSpace(err.Error()) + "\n"
	}
	history := hpatchHistory{
		toolName:     reportIssueToolName,
		script:       input,
		carrierName:  t.codeModeToolName,
		report:       report,
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
	pathPrefix := t.shellDirectory + string(os.PathSeparator)
	recovered := !t.nativeTools && lunaShellCodeModeProgram(contribution, input)
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
			reference, retained := t.proxy.retainShell(t.shellDirectory, callID, input)
			resultMetadata["retained"] = mustMarshalJSON(retained)
			if retained {
				resultMetadata["script_ref"] = mustMarshalJSON(reference)
			}
		}
	}

	kind := codeModeCarrierCustom
	if t.nativeTools {
		kind = codeModeCarrierFunction
	}
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
		if t.nativeTools {
			payload = nativeTextExecArguments(diagnostic)
		} else {
			payload = hpatchDiagnosticExecInput(diagnostic)
		}
	} else {
		switch translation.Carrier.Kind {
		case "exec":
			if err := t.carriers.require(name, kind); err != nil {
				return hpatchHistory{}, fmt.Errorf("%s exec carrier: %w", contribution.Name, err)
			}
			arguments := translation.Arguments
			commentaryToken := ""
			interpreter := ""
			if len(arguments) != 0 {
				interpreter = shellInterpreterName(arguments[0])
			}
			if contribution.PluginID == builtinToolsPluginID && contribution.Name == "shell" &&
				t.proxy.commentaryEndpoint != "" && (interpreter == "bash" || interpreter == "sh") {
				commentaryToken = t.proxy.commentary.subscribe(t.historySessionID, callID)
				if commentaryToken != "" {
					arguments = append([]string{
						commentaryEndpointArgument, t.proxy.commentaryEndpoint,
						commentaryTokenArgument, commentaryToken,
					}, arguments...)
				}
			}
			if t.nativeTools {
				payload, err = t.proxy.registry.nativeExecCarrierArguments(
					contribution,
					input,
					arguments,
					translation.Carrier.Template,
					translation.Carrier.Params,
					resultMetadata,
				)
			} else {
				payload, err = t.proxy.registry.execCarrierInput(
					contribution,
					input,
					arguments,
					translation.Carrier.Template,
					translation.Carrier.Params,
					resultMetadata,
				)
			}
			if err != nil {
				t.proxy.commentary.cancel(commentaryToken)
				return hpatchHistory{}, fmt.Errorf("%s exec carrier: %w", contribution.Name, err)
			}
			if commentaryToken != "" {
				t.commentaryTokens = append(t.commentaryTokens, commentaryToken)
			}
		case "custom":
			kind = codeModeCarrierCustom
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
	} else if t.nativeTools && len(misuseWarnings) != 0 {
		var arguments map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &arguments) != nil || arguments == nil {
			return hpatchHistory{}, fmt.Errorf("%s native exec carrier returned invalid arguments", contribution.Name)
		}
		command := jsonString(arguments, "cmd")
		for _, warning := range misuseWarnings {
			misuseWarning += warning + "\n"
		}
		arguments["cmd"] = mustMarshalJSON("printf %s " + shellQuoteArgument(misuseWarning) + "\n" + command)
		payload = string(mustMarshalJSON(arguments))
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

func retainedEvaluated(emitted, evaluated string) string {
	if emitted == evaluated {
		return ""
	}
	return evaluated
}

func (t *hpatchResponseTransform) TransformJSON(payload []byte) ([]byte, error) {
	transformed, _, err := t.transformResponse(payload)
	if err == nil {
		var response map[string]json.RawMessage
		if json.Unmarshal(transformed, &response) == nil && jsonString(response, "status") == "completed" {
			t.commentaryTokens = nil
		}
	}
	return transformed, err
}

func (t *hpatchResponseTransform) Finish(streamEvent bool) error {
	if streamEvent && len(t.pending) != 0 {
		return errors.New("upstream stream ended with an incomplete hpatch call")
	}
	if streamEvent && len(t.subagentPending) != 0 {
		return errors.New("upstream stream ended with an incomplete subagent call")
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
	case "response.created":
		visible := [][]byte{payload}
		for _, message := range t.subagentDeferred {
			visible = append(visible, assistantCommentaryDoneEvent(message))
			t.commentaryEmitted[jsonString(message, "id")] = struct{}{}
		}
		t.subagentDeferred = nil
		for _, publication := range t.deferredCommentary {
			if message := t.runtimeCommentaryMessage(publication); message != nil {
				visible = append(visible, assistantCommentaryDoneEvent(message))
			}
		}
		t.deferredCommentary = nil
		return visible, nil

	case "response.output_item.added":
		var item map[string]json.RawMessage
		if json.Unmarshal(envelope.Item, &item) != nil {
			return [][]byte{payload}, nil //nolint:nilerr // Unrelated output items pass through unchanged.
		}
		name := jsonString(item, "name")
		if t.codeModeToolName != "" && name == t.codeModeToolName {
			itemID := jsonString(item, "id")
			if jsonString(item, "type") == "custom_tool_call" && itemID != "" {
				t.nativeExecCalls[itemID] = jsonString(item, "call_id")
			}
			return [][]byte{payload}, nil
		}
		if jsonString(item, "type") == "function_call" {
			key := functionToolKey(jsonString(item, "namespace"), name)
			if _, instrumented := t.subagentTools[key]; instrumented {
				itemID, callID := jsonString(item, "id"), jsonString(item, "call_id")
				if itemID == "" || callID == "" || len(t.subagentPending) >= maxHPatchPendingCalls {
					return [][]byte{payload}, nil
				}
				if _, exists := t.subagentPending[itemID]; exists {
					return [][]byte{payload}, nil
				}
				t.subagentPending[itemID] = subagentPendingCall{callID: callID, added: bytes.Clone(payload)}
				return nil, nil
			}
			key = functionToolKey(jsonString(item, "namespace"), name)
			if _, instrumented := t.commentaryTools[key]; instrumented {
				itemID, callID := jsonString(item, "id"), jsonString(item, "call_id")
				if itemID == "" || callID == "" {
					return nil, errors.New("upstream emitted malformed commentary function call")
				}
				if len(t.pending) >= maxHPatchPendingCalls {
					return nil, errors.New("upstream commentary call capacity exceeded")
				}
				if _, exists := t.pending[itemID]; exists || t.pendingCallKnown(callID) {
					return nil, errors.New("upstream reused commentary call identity")
				}
				t.pending[itemID] = hpatchPendingCall{
					callID: callID, toolName: name, structured: true, added: bytes.Clone(payload),
				}
				return nil, nil
			}
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
		if pending, ok := t.pending[envelope.ItemID]; ok && !pending.structured {
			// Translation needs the complete input, but Codex's SSE idle timer only
			// observes dispatched events. Preserve liveness without exposing the
			// untranslated input fragment.
			return [][]byte{[]byte(`{"type":"response.in_progress"}`)}, nil
		}
		return [][]byte{payload}, nil

	case "response.function_call_arguments.delta":
		if _, pending := t.subagentPending[envelope.ItemID]; pending {
			return [][]byte{[]byte(`{"type":"response.in_progress"}`)}, nil
		}
		if pending, ok := t.pending[envelope.ItemID]; ok && pending.structured {
			return [][]byte{[]byte(`{"type":"response.in_progress"}`)}, nil
		}
		if _, pending := t.pending[envelope.ItemID]; pending {
			return nil, fmt.Errorf("unsupported hpatch-related stream event %q", envelope.Type)
		}
		return [][]byte{payload}, nil

	case "response.custom_tool_call_input.done":
		pending, ok := t.pending[envelope.ItemID]
		if !ok || pending.structured {
			if addedCallID, nativeExec := t.nativeExecCalls[envelope.ItemID]; nativeExec {
				if addedCallID != "" && envelope.CallID != "" && addedCallID != envelope.CallID {
					return nil, errors.New("upstream Code Mode call changed call ID")
				}
				callID := cmp.Or(addedCallID, envelope.CallID)
				t.nativeExecCalls[envelope.ItemID] = callID
				item := map[string]json.RawMessage{
					"type":    mustMarshalJSON("custom_tool_call"),
					"id":      mustMarshalJSON(envelope.ItemID),
					"call_id": mustMarshalJSON(callID),
					"name":    mustMarshalJSON(t.codeModeToolName),
					"input":   mustMarshalJSON(envelope.Input),
				}
				changed, err := t.transformOutputItem(item)
				if err != nil {
					return nil, err
				}
				event := payload
				if changed {
					event, err = replaceRawField(payload, "input", item["input"])
					if err != nil {
						return nil, err
					}
				}
				if message := t.localStartCommentary(item); message != nil {
					return [][]byte{assistantCommentaryDoneEvent(message), event}, nil
				}
				return [][]byte{event}, nil
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
		if err := t.commitLocalCall(pending.callID); err != nil {
			return nil, err
		}
		return [][]byte{addedEvent, doneEvent}, nil

	case "response.function_call_arguments.done":
		if pending, exists := t.subagentPending[envelope.ItemID]; exists {
			if len(pending.argumentsDone) != 0 {
				return [][]byte{payload}, nil
			}
			pending.argumentsDone = bytes.Clone(payload)
			t.subagentPending[envelope.ItemID] = pending
			return [][]byte{[]byte(`{"type":"response.in_progress"}`)}, nil
		}
		pending, ok := t.pending[envelope.ItemID]
		if !ok {
			return [][]byte{payload}, nil
		}
		if !pending.structured {
			return nil, fmt.Errorf("unsupported hpatch-related stream event %q", envelope.Type)
		}
		if len(pending.argumentsDone) != 0 {
			return nil, errors.New("upstream repeated commentary arguments completion")
		}
		pending.argumentsDone = bytes.Clone(payload)
		t.pending[envelope.ItemID] = pending
		return [][]byte{[]byte(`{"type":"response.in_progress"}`)}, nil

	case "response.output_item.done":
		var item map[string]json.RawMessage
		if json.Unmarshal(envelope.Item, &item) != nil {
			return [][]byte{payload}, nil //nolint:nilerr // Malformed unrelated output remains the upstream's responsibility.
		}
		itemID := jsonString(item, "id")
		callID := jsonString(item, "call_id")
		if expectedCallID, nativeExec := t.nativeExecCalls[itemID]; nativeExec {
			if jsonString(item, "type") != "custom_tool_call" || jsonString(item, "name") != t.codeModeToolName ||
				expectedCallID != callID {
				return nil, errors.New("upstream completed inconsistent Code Mode call")
			}
		}
		if pending, buffered := t.subagentPending[itemID]; buffered {
			delete(t.subagentPending, itemID)
			visible := make([][]byte, 0, 4)
			if jsonString(item, "call_id") == pending.callID {
				if message := t.subagentCallMessage(item); message != nil {
					visible = append(visible, assistantCommentaryDoneEvent(message))
				}
			}
			visible = append(visible, pending.added)
			if len(pending.argumentsDone) != 0 {
				visible = append(visible, pending.argumentsDone)
			}
			visible = append(visible, payload)
			return visible, nil
		}
		delete(t.nativeExecCalls, itemID)
		if pending, buffered := t.pending[itemID]; buffered && pending.structured {
			if pending.callID != callID || len(pending.argumentsDone) == 0 {
				return nil, errors.New("upstream completed inconsistent commentary function call")
			}
			message, err := t.transformStructuredCommentary(item)
			if err != nil {
				return nil, err
			}
			var addedEnvelope struct {
				Item json.RawMessage `json:"item"`
			}
			var addedItem map[string]json.RawMessage
			if json.Unmarshal(pending.added, &addedEnvelope) != nil || json.Unmarshal(addedEnvelope.Item, &addedItem) != nil {
				return nil, errors.New("decode buffered commentary call")
			}
			addedItem["arguments"] = item["arguments"]
			addedPayload, err := json.Marshal(addedItem)
			if err != nil {
				return nil, err
			}
			addedEvent, err := replaceRawField(pending.added, "item", addedPayload)
			if err != nil {
				return nil, err
			}
			argumentsDone, err := replaceRawField(pending.argumentsDone, "arguments", item["arguments"])
			if err != nil {
				return nil, err
			}
			itemPayload, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			itemDone, err := replaceRawField(payload, "item", itemPayload)
			if err != nil {
				return nil, err
			}
			delete(t.pending, itemID)
			if err := t.commitLocalCall(callID); err != nil {
				return nil, err
			}
			if message != nil {
				return [][]byte{assistantCommentaryDoneEvent(message), addedEvent, argumentsDone, itemDone}, nil
			}
			return [][]byte{addedEvent, argumentsDone, itemDone}, nil
		}
		message, err := t.transformStructuredCommentary(item)
		if err != nil {
			return nil, err
		}
		changed, err := t.transformOutputItem(item)
		if err != nil {
			return nil, err
		}
		if message == nil {
			message = t.localStartCommentary(item)
		}
		if err := t.commitLocalCall(callID); err != nil {
			return nil, err
		}
		if !changed && message == nil {
			return [][]byte{payload}, nil
		}
		delete(t.pending, itemID)
		transformed, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		event, err := replaceRawField(payload, "item", transformed)
		if err != nil {
			return nil, err
		}
		if message != nil {
			return [][]byte{assistantCommentaryDoneEvent(message), event}, nil
		}
		return [][]byte{event}, nil

	case "response.completed":
		clear(t.nativeExecCalls)
		if len(t.pending) != 0 {
			return nil, errors.New("upstream completed with an incomplete hpatch call")
		}
		if len(t.subagentPending) != 0 {
			return nil, errors.New("upstream completed with an incomplete subagent call")
		}
		transformed, usageMessage, err := t.transformResponse(envelope.Response)
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
		visible := make([][]byte, 0, len(t.commentaryTokens)+1)
		for _, token := range t.commentaryTokens {
			for _, publication := range t.proxy.commentary.drain(token) {
				if message := t.runtimeCommentaryMessage(publication); message != nil {
					visible = append(visible, assistantCommentaryDoneEvent(message))
				}
			}
		}
		t.commentaryTokens = nil
		if usageMessage != nil && !t.subagentTurn {
			visible = append(visible, assistantCommentaryDoneEvent(usageMessage))
		}
		visible = append(visible, event)
		return visible, nil

	case "response.failed", "response.incomplete":
		clear(t.pending)
		clear(t.nativeExecCalls)
		clear(t.subagentPending)
		t.cancelCommentaryTokens()
		object, usageMessage, err := responseWithTokenUsageCommentary(envelope.Response)
		if err != nil {
			return nil, err
		}
		transformed, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		event, err := replaceRawField(payload, "response", transformed)
		if err != nil {
			return nil, err
		}
		if err := t.commitHistory(); err != nil {
			return nil, err
		}
		if err := t.Finish(true); err != nil {
			return nil, err
		}
		visible := [][]byte{}
		if usageMessage != nil && !t.subagentTurn {
			visible = append(visible, assistantCommentaryDoneEvent(usageMessage))
		}
		return append(visible, event), nil
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

func (t *hpatchResponseTransform) transformResponse(payload []byte) ([]byte, map[string]json.RawMessage, error) {
	object, usageMessage, err := responseWithTokenUsageCommentary(payload)
	if err != nil {
		return nil, nil, err
	}
	if rawOutput, ok := object["output"]; ok {
		var output []map[string]json.RawMessage
		if err := json.Unmarshal(rawOutput, &output); err != nil {
			return nil, nil, errors.New("decode hpatch-enabled response output")
		}
		transformedOutput := make([]map[string]json.RawMessage, 0, len(t.deferredCommentary)+len(t.subagentResponses)+len(output))
		for _, publication := range t.deferredCommentary {
			if message := t.runtimeCommentaryMessage(publication); message != nil {
				transformedOutput = append(transformedOutput, message)
			}
		}
		t.deferredCommentary = nil
		for _, message := range t.subagentResponses {
			transformedOutput = append(transformedOutput, message)
		}
		t.subagentDeferred = nil
		for _, item := range output {
			subagentMessage, matched := subagentCallCommentary(
				item,
				t.subagentTools,
				t.parentModel,
				t.parentReasoningEffort,
			)
			if matched {
				transformedOutput = append(transformedOutput, subagentMessage)
			}
			if !matched {
				message, err := t.transformStructuredCommentary(item)
				if err != nil {
					return nil, nil, err
				}
				if message != nil {
					transformedOutput = append(transformedOutput, message)
				}
			}
			if _, err := t.transformOutputItem(item); err != nil {
				return nil, nil, err
			}
			if message := t.localStartCommentary(item); message != nil {
				transformedOutput = append(transformedOutput, message)
			}
			transformedOutput = append(transformedOutput, item)
		}
		encoded, err := json.Marshal(transformedOutput)
		if err != nil {
			return nil, nil, err
		}
		object["output"] = encoded
	}
	t.restoreResponseContract(object)
	switch jsonString(object, "status") {
	case "completed", "failed", "incomplete":
		if err := t.commitHistory(); err != nil {
			return nil, nil, err
		}
	}
	transformed, err := json.Marshal(object)
	return transformed, usageMessage, err
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
	if t.codeModeToolName != "" && name == t.codeModeToolName &&
		jsonString(item, "type") == "custom_tool_call" {
		callID := jsonString(item, "call_id")
		originalInput := jsonString(item, "input")
		if retained, exists := t.local[callID]; exists && retained.toolName == codeModeCommentaryHistoryTool {
			if retained.script != originalInput {
				return false, fmt.Errorf("Code Mode commentary call %q changed input", callID)
			}
			retained.upstreamItem = maps.Clone(item)
			t.local[callID] = retained
			item["input"] = mustMarshalJSON(retained.carrierPayload)
			return retained.carrierPayload != originalInput, nil
		}
		input, _, changed, detected := nativeExecCommandInput(originalInput)
		if detected {
			if changed {
				item["input"] = mustMarshalJSON(input)
			}
		}
		input, commentaryChanged, err := t.lowerCodeModeCommentary(callID, input)
		if err != nil {
			return false, err
		}
		changed = changed || commentaryChanged
		if t.proxy.commentaryEndpoint == "" {
			if changed {
				item["input"] = mustMarshalJSON(input)
			}
			return changed, nil
		}
		if callID == "" {
			return false, errors.New("Code Mode call has no call ID")
		}
		history := hpatchHistory{
			toolName: codeModeCommentaryHistoryTool,
			script:   originalInput, carrierKind: codeModeCarrierCustom,
			carrierName: name, carrierPayload: input, upstreamItem: maps.Clone(item),
		}
		if !commentaryChanged {
			history.replayCarrier = true
			history.commentaryMessageIDs = []string{commentaryMessageID(callID)}
		}
		t.recordLocal(callID, &history)
		if changed {
			item["input"] = mustMarshalJSON(input)
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
