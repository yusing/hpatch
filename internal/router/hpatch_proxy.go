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
)

const (
	hpatchToolName = "hpatch"
	hreadToolName  = "hread"

	applyPatchToolName           = "apply_patch"
	hpatchApplyExecMarker        = "// hpatch-proxy: apply translated patch\n"
	maxHPatchScriptBytes         = 1 << 20
	maxHPatchPatchBytes          = 16 << 20
	maxHPatchHistorySessionBytes = 32 << 20
	maxHPatchHistoryGlobalBytes  = 128 << 20

	maxHPatchPendingCalls    = 128
	maxHPatchDiagnosticBytes = 1 << 20

	hreadWrapperName   = "hread"
	hreadWrapperPrefix = "hpatch-hread-"
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
	if len(translated.Patch) > maxHPatchPatchBytes || len(translated.Report) > maxHPatchDiagnosticBytes || len(translated.Diagnostic) > maxHPatchDiagnosticBytes {
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

	script string
	root   string
	// evaluated is the script hpatch actually received when it differs from the
	// model's payload, which happens when the payload was a correction. Replay
	// must restore what the model emitted, while a following correction must
	// resolve command indices against what was evaluated.
	evaluated   string
	patch       string
	carrierName string

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
	translator           hpatchTranslator
	hreadToolDescription string

	toolDescription string

	mu              sync.RWMutex
	sessions        map[string]*hpatchHistorySession
	activeSessions  map[string]int
	historyBytes    int
	sessionSequence uint64
	closed          bool
	hreadWrapper    string
}

func newHPatchProxy(translator hpatchTranslator) *hpatchProxy {
	if translator == nil {
		return nil
	}
	return &hpatchProxy{
		translator:           translator,
		hreadToolDescription: hpatch.HReadToolDescription(),

		toolDescription: translator.ToolDescription() + hpatchCorrectionInstructions,
		sessions:        make(map[string]*hpatchHistorySession),
		activeSessions:  make(map[string]int),
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
	p.closed = true
	wrapper := p.hreadWrapper
	clear(p.sessions)
	clear(p.activeSessions)
	p.historyBytes = 0
	p.mu.Unlock()

	if err := cleanupHReadWrapper(wrapper); err != nil {
		return err
	}
	p.mu.Lock()
	if p.hreadWrapper == wrapper {
		p.hreadWrapper = ""
	}
	p.mu.Unlock()
	return nil
}

func (p *hpatchProxy) ensureHReadWrapper() (string, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return "", errors.New("hpatch response proxy is closed")
	}
	existing := p.hreadWrapper
	p.mu.RUnlock()
	if existing != "" {
		return existing, nil
	}

	candidate, err := createHReadWrapper()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errors.Join(errors.New("hpatch response proxy is closed"), cleanupHReadWrapper(candidate))
	}
	if p.hreadWrapper != "" {
		return p.hreadWrapper, cleanupHReadWrapper(candidate)
	}
	p.hreadWrapper = candidate
	return candidate, nil
}

func createHReadWrapper() (wrapperDirectory string, err error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate hread worker executable: %w", err)
	}
	return createHReadWrapperForExecutable(executable)
}

func createHReadWrapperForExecutable(executable string) (wrapperDirectory string, err error) {
	directory, err := os.MkdirTemp("", hreadWrapperPrefix)
	if err != nil {
		return "", fmt.Errorf("create hread wrapper directory: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, cleanupHReadWrapper(directory))
		}
	}()
	script := "#!/bin/sh\n" +
		hreadWorkerEnvironment + "=1\n" +
		"export " + hreadWorkerEnvironment + "\n" +
		"exec " + shellSingleQuote(executable) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, hreadWrapperName), []byte(script), 0o700); err != nil {
		return "", fmt.Errorf("write hread wrapper: %w", err)
	}
	return directory, nil
}

func cleanupHReadWrapper(directory string) error {
	if directory == "" {
		return nil
	}
	if filepath.Base(directory) == directory || !strings.HasPrefix(filepath.Base(directory), hreadWrapperPrefix) {
		return fmt.Errorf("refuse to remove invalid hread wrapper directory %q", directory)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove hread wrapper directory: %w", err)
	}
	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (p *hpatchProxy) touchSession(session *hpatchHistorySession) {
	p.sessionSequence++
	session.lastUsed = p.sessionSequence
}

// hpatchCorrectionInstructions documents the correction payload the proxy accepts
// in place of a complete script. It is appended to hpatch's own tool help rather
// than exposed as a second tool, because a second tool would cost another cached
// definition payload and would have to appear mid-session, invalidating the
// prompt prefix at exactly the moment a rejection happens.
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

// hpatchCorrectionHint is appended to a rejection so the cheaper repair path is
// visible at the moment it applies. A diagnostic states what was wrong but not
// what to send next, and a model that has forgotten the protocol resends the whole
// script, which is the cost this feature exists to avoid.
const hpatchCorrectionHint = "\nRepair this with indexed operations: `INDEX: COMMAND`, `-INDEX`, `+INDEX: COMMAND`, or `INDEX+: COMMAND`. For a displayed multiline value row, use `INDEX.ROW: \"VALUE\"`, `-INDEX.ROW`, `+INDEX.ROW: \"VALUE\"`, or `INDEX.ROW+: \"VALUE\"`. Indices are the command and value-row numbers above.\n"

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
	hreadWrapperDirectory     string

	installedToolDefinition  string
	codeModeToolName         string
	baselineDefinition       string
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
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > maxSessionIDBytes {
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
	installedTools := customGrammarTools(p.toolDescription, p.hreadToolDescription)
	baselineDefinition, codeModeToolName, replaced, err := replaceAdditionalToolsApplyPatch(request.fields, installedTools)
	if err != nil {
		workspace.close()
		return nil, err
	}
	if !replaced {
		workspace.close()
		return nil, errors.New("responses request cannot satisfy the required hpatch rewrite")
	}
	historySessionID := workspace.canonical + "\x00" + sessionID
	hreadWrapperDirectory, err := p.ensureHReadWrapper()
	if err != nil {
		workspace.close()
		return nil, fmt.Errorf("initialize hread wrapper: %w", err)
	}
	if err := p.activateSession(historySessionID); err != nil {
		workspace.close()
		return nil, err
	}
	if err := p.restoreInputPrefix(request, historySessionID, hreadWrapperDirectory); err != nil {
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
		hreadWrapperDirectory:     hreadWrapperDirectory,

		installedToolDefinition: string(mustMarshalJSON(installedTools)),
		codeModeToolName:        codeModeToolName,
		baselineDefinition:      baselineDefinition,
	}, nil
}

type additionalToolsApplyPatchOwner struct {
	items               []json.RawMessage
	item                map[string]json.RawMessage
	itemIndex           int
	tools               []map[string]json.RawMessage
	toolIndex           int
	name                string
	direct              bool
	strippedDescription string
	// baselineDefinition is the native apply_patch definition hpatch displaces.
	// hpatch's definition cost is only meaningful net of it.
	baselineDefinition string
}

// replaceAdditionalToolsApplyPatch swaps a supported apply_patch surface for a
// standalone hpatch tool, reporting the native definition it displaced so the
// caller can attribute hpatch's definition cost net of it.
func replaceAdditionalToolsApplyPatch(fields map[string]json.RawMessage, installedTools []map[string]json.RawMessage) (string, string, bool, error) {
	var topTools []map[string]json.RawMessage
	if rawTools, exists := fields["tools"]; exists {
		if err := json.Unmarshal(rawTools, &topTools); err != nil {
			return "", "", false, fmt.Errorf("decode responses tools: %w", err)
		}
	}
	for _, tool := range topTools {
		if name := jsonString(tool, "name"); name == hpatchToolName || name == hreadToolName {
			return "", "", false, fmt.Errorf("responses request already defines %s", name)
		}
	}
	owner, err := findAdditionalToolsApplyPatch(fields)
	if err != nil || owner == nil {
		return "", "", false, err
	}
	if owner.direct {
		return "", "", false, errors.New("responses request has no Code Mode exec carrier required by hread")
	}
	if !strings.Contains(owner.strippedDescription, "exec_command(args:") {
		return "", "", false, errors.New("responses Code Mode carrier does not provide exec_command")
	}
	for _, tool := range topTools {
		if jsonString(tool, "name") == applyPatchToolName {
			return "", "", false, errors.New("responses request exposes an additional apply_patch owner")
		}
		name := jsonString(tool, "name")
		if name != "exec" && name != "functions.exec" {
			continue
		}
		if _, _, found, stripErr := stripCodeModeApplyPatchSection(jsonString(tool, "description")); stripErr != nil {
			return "", "", false, stripErr
		} else if found {
			return "", "", false, errors.New("responses request exposes apply_patch in top-level exec")
		}
	}
	if codeModeToolChoiceRestricted(fields, owner.name) {
		return "", "", false, nil
	}
	if err := exposeStandaloneHPatch(fields, topTools, owner, installedTools); err != nil {
		return "", "", false, err
	}
	return owner.baselineDefinition, owner.name, true, nil
}

func findAdditionalToolsApplyPatch(fields map[string]json.RawMessage) (*additionalToolsApplyPatchOwner, error) {
	var items []json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return nil, nil //nolint:nilerr // Unsupported input shapes are simply not Code Mode owners.
	}
	var owner *additionalToolsApplyPatchOwner
	for itemIndex, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil || jsonString(item, "type") != "additional_tools" {
			continue
		}
		var tools []map[string]json.RawMessage
		if json.Unmarshal(item["tools"], &tools) != nil {
			continue
		}
		for toolIndex, tool := range tools {
			name := jsonString(tool, "name")
			if name == hpatchToolName || name == hreadToolName {
				return nil, fmt.Errorf("responses additional_tools item defines direct %s", name)
			}
			if name == applyPatchToolName {
				if owner != nil {
					return nil, errors.New("responses request defines additional_tools apply_patch more than once")
				}
				baseline, err := json.Marshal(tool)
				if err != nil {
					return nil, fmt.Errorf("encode direct additional_tools apply_patch: %w", err)
				}
				owner = &additionalToolsApplyPatchOwner{
					items:              items,
					item:               item,
					itemIndex:          itemIndex,
					tools:              tools,
					toolIndex:          toolIndex,
					name:               name,
					direct:             true,
					baselineDefinition: string(baseline),
				}
				continue
			}
			if name != "exec" && name != "functions.exec" {
				continue
			}
			stripped, baseline, found, err := stripCodeModeApplyPatchSection(jsonString(tool, "description"))
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			if owner != nil {
				return nil, errors.New("responses request defines additional_tools apply_patch more than once")
			}
			owner = &additionalToolsApplyPatchOwner{
				items:               items,
				item:                item,
				itemIndex:           itemIndex,
				tools:               tools,
				toolIndex:           toolIndex,
				name:                name,
				strippedDescription: stripped,
				baselineDefinition:  baseline,
			}
		}
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
	encodedAdditionalTools, err := json.Marshal(owner.tools)
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

func customGrammarTools(toolDescription, hreadToolDescription string) []map[string]json.RawMessage {
	return []map[string]json.RawMessage{
		customGrammarTool(hpatchToolName, toolDescription, hpatch.ToolGrammar()),
		customGrammarTool(hreadToolName, hreadToolDescription, hpatch.HReadToolGrammar()),
	}
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

// stripCodeModeApplyPatchSection removes the Code Mode apply_patch section from a
// tool description. It also returns that removed section, which is the native
// patch tool definition hpatch displaces: the host pays for one or the other as
// request input, so measuring hpatch's definition cost requires the text it replaced.
func stripCodeModeApplyPatchSection(description string) (string, string, bool, error) {
	start := strings.Index(description, codeModeApplyPatchHeading)
	if start < 0 {
		return description, "", false, nil
	}
	if start > 0 && description[start-1] != '\n' {
		return description, "", false, nil
	}
	sectionEnd := len(description)
	remaining := description[start+len(codeModeApplyPatchHeading):]
	for _, nextHeading := range []string{"\n### `", "\n### ", "\n## "} {
		if offset := strings.Index(remaining, nextHeading); offset >= 0 {
			sectionEnd = min(sectionEnd, start+len(codeModeApplyPatchHeading)+offset+1)
		}
	}
	section := description[start:sectionEnd]
	const declaration = "declare const tools: { apply_patch(input: string): Promise<unknown>; };"
	if !strings.Contains(section, "exec tool declaration:") || !strings.Contains(section, declaration) {
		return description, "", false, nil
	}
	stripped := strings.TrimRight(description[:start], "\n")
	suffix := strings.TrimLeft(description[sectionEnd:], "\n")
	if stripped != "" && suffix != "" {
		stripped += "\n\n"
	}
	stripped += suffix
	if strings.Contains(stripped, codeModeApplyPatchHeading) {
		return "", "", false, errors.New("responses Code Mode tool defines nested apply_patch more than once")
	}
	return stripped, section, true, nil
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
		history.bytes = len(sessionID) + len(callID) + len(history.toolName) + len(history.script) + len(history.root) + len(history.evaluated) + len(history.patch) + len(history.carrierName) + len(history.report) + len(history.translationError) + len(history.correlationID) + len(encodedItem)
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
		if history.unevaluated || history.toolName == hreadToolName {
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
		if history.toolName != hreadToolName && history.correlationID == correlationID {
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

func (p *hpatchProxy) restoreInputPrefix(request *parsedResponsesRequest, sessionID, hreadWrapperDirectory string) error {
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
		kind := jsonString(item, "type")
		callID := jsonString(item, "call_id")
		history, known := p.history(sessionID, callID)
		if kind != "custom_tool_call" {
			if known && kind != "custom_tool_call_output" {
				return fmt.Errorf("replayed call %q changed item type", callID)
			}
			continue
		}
		if !known {
			continue
		}
		if jsonString(item, "name") != history.carrierName {
			return fmt.Errorf("replayed call %q changed carrier name", callID)
		}
		if validatedCarriers[callID] {
			return fmt.Errorf("replayed call %q appears more than once", callID)
		}
		if jsonString(item, "input") != history.carrierInput(hreadWrapperDirectory) {
			return fmt.Errorf("replayed call %q changed translated input", callID)
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
		if history.toolName == hreadToolName || history.script != input {
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
		if len(translated.corrections) != 0 {
			diagnostic += hpatchAcceptHint(translated.corrections)
		} else {
			diagnostic += hpatchCorrectionHint
		}
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
	if name == hreadToolName {
		return t.translateHRead(callID, input, upstreamItem)
	}
	return t.translate(callID, input, upstreamItem)
}

func (t *hpatchResponseTransform) translateHRead(callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != hreadToolName || history.script != input {
			return hpatchHistory{}, fmt.Errorf("hread call %q changed input", callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	if len(input) > maxHPatchScriptBytes {
		return hpatchHistory{}, fmt.Errorf("hread call %q input exceeds %d bytes", callID, maxHPatchScriptBytes)
	}
	if contextErr := t.ctx.Err(); contextErr != nil {
		return hpatchHistory{}, contextErr
	}
	if t.codeModeToolName != "exec" && t.codeModeToolName != "functions.exec" {
		return hpatchHistory{}, errors.New("hread translation requires a Code Mode exec carrier")
	}
	if t.hreadWrapperDirectory == "" {
		return hpatchHistory{}, errors.New("hread translation requires a shared wrapper directory")
	}
	if err := t.recordMetrics(hpatchMetricInputs{overheadOnly: true}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		toolName:     hreadToolName,
		script:       input,
		root:         t.workspace.canonical,
		carrierName:  t.codeModeToolName,
		upstreamItem: maps.Clone(upstreamItem),
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

func hreadExecInput(input, wrapperDirectory string) string {
	command := shellSingleQuote(filepath.Join(wrapperDirectory, hreadWrapperName)) + " " + shellSingleQuote(input)
	arguments := struct {
		Command string `json:"cmd"`
	}{
		Command: command,
	}
	return "const result = await tools.exec_command(" + string(mustMarshalJSON(arguments)) + ");\n" +
		"text(result.output);"
}

func (h hpatchHistory) carrierInput(hreadWrapperDirectory string) string {
	if h.toolName == hreadToolName {
		return hreadExecInput(h.script, hreadWrapperDirectory)
	}
	if h.translationError != "" {
		return hpatchDiagnosticExecInput(h.translationError)
	}
	return hpatchApplyExecInput(h.patch, h.report)
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

func (t *hpatchResponseTransform) claimRequestAccounting() (string, string) {
	if t.requestAccountingClaimed {
		return "", ""
	}
	t.requestAccountingClaimed = true
	return t.installedToolDefinition, t.baselineDefinition
}

func (t *hpatchResponseTransform) recordMetrics(inputs hpatchMetricInputs) error {
	inputs.definition, inputs.baselineDefinition = t.claimRequestAccounting()
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
		if !history.unevaluated && history.toolName != hreadToolName {
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
		if name != hpatchToolName && name != hreadToolName {
			return [][]byte{payload}, nil
		}
		itemID, callID := jsonString(item, "id"), jsonString(item, "call_id")
		if jsonString(item, "type") != "custom_tool_call" || itemID == "" || callID == "" {
			return nil, errors.New("upstream emitted malformed hpatch call")
		}
		if len(itemID) > maxSessionIDBytes || len(callID) > maxSessionIDBytes || len(t.pending) >= maxHPatchPendingCalls {
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
		addedItem["name"] = mustMarshalJSON(history.carrierName)
		addedItem["input"] = mustMarshalJSON("")
		itemPayload, err := json.Marshal(addedItem)
		if err != nil {
			return nil, err
		}
		addedEvent, err := replaceRawField(pending.added, "item", itemPayload)
		if err != nil {
			return nil, err
		}
		doneEvent, err := replaceRawField(payload, "input", mustMarshalJSON(history.carrierInput(t.hreadWrapperDirectory)))
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
		if _, pending := t.pending[envelope.ItemID]; pending || t.pendingCallKnown(envelope.CallID) || envelope.Name == hpatchToolName || envelope.Name == hreadToolName || envelope.Name == applyPatchToolName {
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
	if name != hpatchToolName && name != hreadToolName {
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
	item["name"] = mustMarshalJSON(history.carrierName)
	item["input"] = mustMarshalJSON(history.carrierInput(t.hreadWrapperDirectory))
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
