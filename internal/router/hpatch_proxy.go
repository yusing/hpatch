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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	hpatchToolName               = "hpatch"
	applyPatchToolName           = "apply_patch"
	hpatchApplyExecMarker        = "// hpatch-proxy: apply translated patch\n"
	maxHPatchScriptBytes         = 1 << 20
	maxHPatchPatchBytes          = 16 << 20
	maxHPatchHistorySessionBytes = 32 << 20
	maxHPatchHistoryGlobalBytes  = 128 << 20
	maxHPatchPendingCalls        = 128
	maxHPatchDiagnosticBytes     = 1 << 20
	hpatchDiscoveryTimeout       = 5 * time.Second
	hpatchDiscoveryWaitDelay     = 100 * time.Millisecond
)

var (
	errHPatchCapacity         = errors.New("hpatch proxy capacity exceeded")
	errHPatchWorkspaceChanged = errors.New("hpatch workspace changed during translation")
)

const (
	hpatchImmutableBaselineMarker = "The first in for an existing file captures an immutable baseline."
	hpatchHostMetricsMarker       = "Host metrics schema: caller-v2\n"
)

type hpatchTranslationResult struct {
	patch      []byte
	report     string
	invocation json.RawMessage
}

type hpatchTranslator interface {
	Translate(ctx context.Context, workspace routingWorkspace, script string) (hpatchTranslationResult, error)
	RecordMetrics(ctx context.Context, record hpatchMetricRecord) error
	ToolDescription() string
}

type commandHPatchTranslator struct {
	executable      string
	toolDescription string
}

func discoverHPatchTranslator(ctx context.Context) (hpatchTranslator, error) {
	executable, err := exec.LookPath(hpatchToolName)
	if err != nil {
		return nil, fmt.Errorf("find hpatch executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve hpatch executable: %w", err)
	}
	probeContext, cancel := context.WithTimeout(ctx, hpatchDiscoveryTimeout)
	defer cancel()
	toolDescription, err := loadHPatchToolDescription(probeContext, executable)
	if err != nil {
		return nil, err
	}
	return commandHPatchTranslator{executable: executable, toolDescription: toolDescription}, nil
}

func loadHPatchToolDescription(ctx context.Context, executable string) (string, error) {
	command := exec.CommandContext(ctx, executable, "--tool-help")
	command.WaitDelay = hpatchDiscoveryWaitDelay
	stdout := hpatchLimitedBuffer{limit: maxHPatchDiagnosticBytes}
	stderr := hpatchLimitedBuffer{limit: maxHPatchDiagnosticBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if contextErr := ctx.Err(); contextErr != nil {
		return "", fmt.Errorf("inspect hpatch tool help: %w", contextErr)
	}
	if stdout.overflow || stderr.overflow {
		return "", fmt.Errorf("%w: hpatch tool help exceeds %d bytes", errHPatchCapacity, maxHPatchDiagnosticBytes)
	}
	if runErr != nil {
		return "", fmt.Errorf("inspect hpatch tool help: %w", hpatchCommandError{cause: runErr, diagnostic: stderr.String()})
	}
	if stdout.Len() == 0 {
		return "", errors.New("hpatch tool help is empty")
	}
	if stderr.Len() != 0 {
		return "", fmt.Errorf("hpatch tool help wrote to stderr: %s", strings.TrimSpace(stderr.String()))
	}
	if !utf8.Valid(stdout.Bytes()) {
		return "", errors.New("hpatch tool help is not valid UTF-8")
	}
	if !bytes.Contains(stdout.Bytes(), []byte(hpatchImmutableBaselineMarker)) {
		return "", errors.New("hpatch executable does not support immutable-baseline selectors")
	}
	description, supported := strings.CutSuffix(string(stdout.Bytes()), hpatchHostMetricsMarker)
	if !supported {
		return "", errors.New("hpatch executable does not support caller metrics")
	}
	return description, nil
}

func (t commandHPatchTranslator) ToolDescription() string {
	return t.toolDescription
}

func (t commandHPatchTranslator) Translate(ctx context.Context, workspace routingWorkspace, script string) (result hpatchTranslationResult, err error) {
	if !workspace.unchanged() {
		return hpatchTranslationResult{}, errHPatchWorkspaceChanged
	}
	metricsFile, err := os.CreateTemp("", "hpatch-router-metrics-*")
	if err != nil {
		return hpatchTranslationResult{}, fmt.Errorf("%w: create hpatch metrics output: %w", errHPatchMetricsProtocol, err)
	}
	metricsPath := metricsFile.Name()
	if closeErr := metricsFile.Close(); closeErr != nil {
		_ = os.Remove(metricsPath)
		return hpatchTranslationResult{}, fmt.Errorf("%w: close hpatch metrics output: %w", errHPatchMetricsProtocol, closeErr)
	}
	defer func() {
		if removeErr := os.Remove(metricsPath); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("%w: remove hpatch metrics output: %w", errHPatchMetricsProtocol, removeErr))
		}
	}()

	command := exec.CommandContext(ctx, t.executable, "translate", "--root", workspace.canonical)
	command.WaitDelay = hpatchDiscoveryWaitDelay
	command.Dir = workspace.canonical
	command.Stdin = strings.NewReader(script)
	command.Env = hpatchCommandEnvironment(metricsPath)
	stdout := hpatchLimitedBuffer{limit: maxHPatchPatchBytes}
	stderr := hpatchLimitedBuffer{limit: maxHPatchDiagnosticBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if !workspace.unchanged() {
		return hpatchTranslationResult{}, errHPatchWorkspaceChanged
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return hpatchTranslationResult{}, contextErr
	}
	if stdout.overflow || stderr.overflow {
		return hpatchTranslationResult{}, fmt.Errorf("%w: hpatch translation output exceeds its configured bound", errHPatchCapacity)
	}
	invocation, metricsErr := readHPatchInvocationMetrics(metricsPath)
	if metricsErr != nil {
		return hpatchTranslationResult{}, metricsErr
	}
	result.invocation = invocation
	if runErr != nil {
		return result, hpatchCommandError{cause: runErr, diagnostic: stderr.String()}
	}
	if stderr.Len() == 0 {
		return hpatchTranslationResult{}, fmt.Errorf("%w: hpatch translation produced no final-state report", errHPatchMetricsProtocol)
	}
	if !utf8.Valid(stderr.Bytes()) {
		return hpatchTranslationResult{}, fmt.Errorf("%w: hpatch final-state report is not valid UTF-8", errHPatchMetricsProtocol)
	}
	result.patch = bytes.Clone(stdout.Bytes())
	result.report = stderr.String()
	return result, nil
}

func (t commandHPatchTranslator) RecordMetrics(ctx context.Context, record hpatchMetricRecord) error {
	encoded, err := encodeHPatchMetricRecord(record)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, t.executable, "record-metrics")
	command.WaitDelay = hpatchDiscoveryWaitDelay
	command.Stdin = bytes.NewReader(encoded)
	command.Env = hpatchCommandEnvironment("")
	stdout := hpatchLimitedBuffer{limit: maxHPatchDiagnosticBytes}
	stderr := hpatchLimitedBuffer{limit: maxHPatchDiagnosticBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if runErr := command.Run(); runErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if stdout.overflow || stderr.overflow {
			return fmt.Errorf("%w: hpatch metrics output exceeds %d bytes", errHPatchCapacity, maxHPatchDiagnosticBytes)
		}
		return fmt.Errorf("record hpatch metrics: %w", hpatchCommandError{cause: runErr, diagnostic: stderr.String()})
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		return fmt.Errorf("%w: hpatch metrics recording produced output", errHPatchMetricsProtocol)
	}
	return nil
}

type hpatchCommandError struct {
	cause      error
	diagnostic string
}

func (e hpatchCommandError) Error() string {
	if e.diagnostic == "" {
		return e.cause.Error()
	}
	return fmt.Sprintf("%v: %s", e.cause, strings.TrimSpace(e.diagnostic))
}

func (e hpatchCommandError) Unwrap() error {
	return e.cause
}

func hpatchDiagnostic(err error) string {
	var commandError hpatchCommandError
	if errors.As(err, &commandError) && commandError.diagnostic != "" {
		return commandError.diagnostic
	}
	return err.Error()
}

type hpatchLimitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *hpatchLimitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *hpatchLimitedBuffer) Len() int {
	return b.buffer.Len()
}

func (b *hpatchLimitedBuffer) String() string {
	return b.buffer.String()
}

func (b *hpatchLimitedBuffer) Write(content []byte) (int, error) {
	remaining := max(0, b.limit-b.Len())
	if len(content) > remaining {
		_, _ = b.buffer.Write(content[:remaining])
		b.overflow = true
		return len(content), nil
	}
	return b.buffer.Write(content)
}

type hpatchHistory struct {
	script      string
	workspaceID string
	// evaluated is the script hpatch actually received when it differs from the
	// model's payload, which happens when the payload was a correction. Replay
	// must restore what the model emitted, while a following correction must
	// resolve command indices against what was evaluated.
	evaluated        string
	patch            string
	carrierName      string
	report           string
	translationError string
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
}

type hpatchProxy struct {
	translator      hpatchTranslator
	toolDescription string

	mu           sync.RWMutex
	sessions     map[string]*hpatchHistorySession
	historyBytes int
}

func newHPatchProxy(translator hpatchTranslator) *hpatchProxy {
	if translator == nil {
		return nil
	}
	return &hpatchProxy{
		translator:      translator,
		toolDescription: translator.ToolDescription() + hpatchWorkspaceToolInstructions + hpatchCorrectionInstructions,
		sessions:        make(map[string]*hpatchHistorySession),
	}
}

// hpatchCorrectionInstructions documents the correction payload the proxy accepts
// in place of a complete script. It is appended to hpatch's own tool help rather
// than exposed as a second tool, because a second tool would cost another cached
// definition payload and would have to appear mid-session, invalidating the
// prompt prefix at exactly the moment a rejection happens.
const hpatchCorrectionInstructions = `
Repairing a rejected script:
  When the previous hpatch call was rejected, you may send a correction instead of
  the complete script. A correction replaces named commands of that rejected script;
  every other command is reused unchanged. Send one line per replaced command:

    INDEX: COMMAND

  INDEX is the one-based command number the diagnostic reported, counting only
  nonblank command lines. Every line of the payload must have this form, each index
  may appear once, and the replacement must be a complete command. The repaired
  script is revalidated in full against unchanged files, so a correction is atomic
  exactly as a script is. To add or remove a command, send the complete script.
`

// hpatchCorrectionHint is appended to a rejection so the cheaper repair path is
// visible at the moment it applies. A diagnostic states what was wrong but not
// what to send next, and a model that has forgotten the protocol resends the whole
// script, which is the cost this feature exists to avoid.
const hpatchCorrectionHint = "\nRepair this by replacing only the failed commands: send one `INDEX: COMMAND` line per correction, using the command numbers above. Send a complete script instead to add or remove commands.\n"

type hpatchPendingCall struct {
	callID string
	added  []byte
}

type hpatchResponseTransform struct {
	ctx       context.Context
	proxy     *hpatchProxy
	sessionID string

	originalTools             json.RawMessage
	originalToolsPresent      bool
	originalToolChoice        json.RawMessage
	originalToolChoicePresent bool
	pending                   map[string]hpatchPendingCall
	local                     map[string]hpatchHistory
	workspaces                []routingWorkspace
	toolDescription           string
	codeModeToolName          string
	baselineDefinition        string
	carriedMetadata           []string
	requestAccountingClaimed  bool

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
	closeRoutingWorkspaces(t.workspaces)
	t.workspaces = nil
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
	workspaces := usableRoutingWorkspaces(metadata.Workspaces)
	if len(workspaces) == 0 {
		closeRoutingWorkspaces(workspaces)
		return nil, errors.New("hpatch rewrite requires at least one usable workspace")
	}
	originalTools, originalToolsPresent := request.fields["tools"]
	originalTools = bytes.Clone(originalTools)
	originalToolChoice, originalToolChoicePresent := request.fields["tool_choice"]
	originalToolChoice = bytes.Clone(originalToolChoice)
	baselineDefinition, codeModeToolName, replaced, err := replaceAdditionalToolsApplyPatch(request.fields, p.toolDescription)
	if err != nil {
		closeRoutingWorkspaces(workspaces)
		return nil, err
	}
	if !replaced {
		closeRoutingWorkspaces(workspaces)
		return nil, errors.New("responses request cannot satisfy the required hpatch rewrite")
	}
	carriedMetadata, err := p.restoreInputPrefixWithMetadata(request, sessionID)
	if err != nil {
		closeRoutingWorkspaces(workspaces)
		return nil, err
	}
	workspaceMetadata, err := appendHPatchWorkspaceMessage(request.fields, workspaces)
	if err != nil {
		closeRoutingWorkspaces(workspaces)
		return nil, err
	}
	carriedMetadata = append(carriedMetadata, workspaceMetadata)
	request.fields["parallel_tool_calls"] = json.RawMessage("false")
	return &hpatchResponseTransform{
		ctx:       ctx,
		proxy:     p,
		sessionID: sessionID,

		originalTools:             originalTools,
		originalToolsPresent:      originalToolsPresent,
		originalToolChoice:        originalToolChoice,
		originalToolChoicePresent: originalToolChoicePresent,
		pending:                   make(map[string]hpatchPendingCall),
		local:                     make(map[string]hpatchHistory),
		workspaces:                workspaces,
		toolDescription:           p.toolDescription,
		codeModeToolName:          codeModeToolName,

		baselineDefinition: baselineDefinition,
		carriedMetadata:    carriedMetadata,
	}, nil
}

type additionalToolsApplyPatchOwner struct {
	items               []json.RawMessage
	item                map[string]json.RawMessage
	itemIndex           int
	tools               []map[string]json.RawMessage
	toolIndex           int
	name                string
	strippedDescription string
	// baselineDefinition is the apply_patch section removed from the Code Mode
	// description. hpatch's definition cost is only meaningful net of it.
	baselineDefinition string
}

// replaceAdditionalToolsApplyPatch swaps the Code Mode apply_patch surface for a
// standalone hpatch tool, reporting the native definition it displaced so the
// caller can attribute hpatch's definition cost net of it.
func replaceAdditionalToolsApplyPatch(fields map[string]json.RawMessage, toolDescription string) (string, string, bool, error) {
	var topTools []map[string]json.RawMessage
	if rawTools, exists := fields["tools"]; exists {
		if err := json.Unmarshal(rawTools, &topTools); err != nil {
			return "", "", false, fmt.Errorf("decode responses tools: %w", err)
		}
	}
	for _, tool := range topTools {
		if jsonString(tool, "name") == hpatchToolName {
			return "", "", false, errors.New("responses request already defines hpatch")
		}
	}
	owner, err := findAdditionalToolsApplyPatch(fields)
	if err != nil || owner == nil {
		return "", "", false, err
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
	if err := exposeStandaloneHPatch(fields, topTools, owner, toolDescription); err != nil {
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
			if name == applyPatchToolName || name == hpatchToolName {
				return nil, fmt.Errorf("responses additional_tools item defines direct %s", name)
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

func exposeStandaloneHPatch(fields map[string]json.RawMessage, topTools []map[string]json.RawMessage, owner *additionalToolsApplyPatchOwner, toolDescription string) error {
	owner.tools[owner.toolIndex]["description"] = mustMarshalJSON(owner.strippedDescription)
	encodedAdditionalTools, err := json.Marshal(owner.tools)
	if err != nil {
		return fmt.Errorf("encode additional Code Mode tools: %w", err)
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
	topTools = append(topTools, map[string]json.RawMessage{
		"type":        mustMarshalJSON("custom"),
		"name":        mustMarshalJSON(hpatchToolName),
		"description": mustMarshalJSON(toolDescription),
		"format":      json.RawMessage(`{"type":"text"}`),
	})
	encodedTopTools, err := json.Marshal(topTools)
	if err != nil {
		return fmt.Errorf("encode Responses tools: %w", err)
	}
	fields["input"] = encodedInput
	fields["tools"] = encodedTopTools
	return nil
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

func mustMarshalJSON(value string) json.RawMessage {
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
		history.bytes = len(sessionID) + len(callID) + len(history.script) + len(history.workspaceID) + len(history.evaluated) + len(history.patch) + len(history.carrierName) + len(history.report) + len(history.translationError) + len(encodedItem)
		prepared[callID] = history
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.sessions[sessionID]
	if session == nil && len(p.sessions) >= maxSessionHistories {
		return errors.New("hpatch history session capacity reached")
	}
	sessionBytes := 0
	existingCalls := map[string]hpatchHistory(nil)
	if session != nil {
		sessionBytes = session.bytes
		existingCalls = session.calls
	}
	newCalls := 0
	delta := 0
	for callID, history := range prepared {
		previous, exists := existingCalls[callID]
		if !exists {
			newCalls++
		}
		delta += history.bytes - previous.bytes
	}
	if len(existingCalls)+newCalls > maxSessionTurns {
		return errors.New("hpatch history call capacity reached")
	}
	if sessionBytes+delta > maxHPatchHistorySessionBytes || p.historyBytes+delta > maxHPatchHistoryGlobalBytes {
		return errors.New("hpatch history byte capacity reached")
	}
	if session == nil {
		session = &hpatchHistorySession{calls: make(map[string]hpatchHistory, len(prepared))}
		p.sessions[sessionID] = session
	}
	// Preserve response order when a transform supplied it. Manually assembled
	// batches have zero sequences and use call-ID order only as a deterministic
	// fallback. Replaying an existing call never changes its retained order.
	callIDs := slices.Collect(maps.Keys(prepared))
	slices.SortFunc(callIDs, func(first, second string) int {
		if order := cmp.Compare(prepared[first].sequence, prepared[second].sequence); order != 0 {
			return order
		}
		return strings.Compare(first, second)
	})
	for _, callID := range callIDs {
		history := prepared[callID]
		if previous, exists := session.calls[callID]; exists {
			history.sequence = previous.sequence
		} else {
			session.nextSequence++
			history.sequence = session.nextSequence
		}
		session.calls[callID] = history
	}
	session.bytes += delta
	p.historyBytes += delta
	return nil
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
		if history.unevaluated {
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

func (p *hpatchProxy) restoreInputPrefixWithMetadata(request *parsedResponsesRequest, sessionID string) ([]string, error) {
	raw, ok := request.fields["input"]
	if !ok {
		return nil, nil
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, nil //nolint:nilerr // Non-array input cannot contain replayable hpatch calls.
	}
	changed := false
	var carriedMetadata []string
	validatedCarriers := make(map[string]bool)
	for index, item := range items {
		kind := jsonString(item, "type")
		callID := jsonString(item, "call_id")
		history, known := p.history(sessionID, callID)
		if kind != "custom_tool_call" {
			if known && kind != "custom_tool_call_output" {
				return nil, fmt.Errorf("replayed call %q changed item type", callID)
			}
			continue
		}
		if !known {
			continue
		}
		if jsonString(item, "name") != history.carrierName {
			return nil, fmt.Errorf("replayed call %q changed carrier name", callID)
		}
		if validatedCarriers[callID] {
			return nil, fmt.Errorf("replayed call %q appears more than once", callID)
		}
		if jsonString(item, "input") != history.carrierInput() {
			return nil, fmt.Errorf("replayed call %q changed translated input", callID)
		}
		validatedCarriers[callID] = true
		if framing := hpatchWorkspaceDirectivePrefix(history.script); framing != "" {
			carriedMetadata = append(carriedMetadata, framing)
		}
		if len(history.upstreamItem) != 0 {
			items[index] = maps.Clone(history.upstreamItem)
		} else {
			item["name"] = mustMarshalJSON(hpatchToolName)
			item["input"] = mustMarshalJSON(history.script)
		}
		changed = true
	}
	if changed {
		encoded, err := json.Marshal(items)
		if err != nil {
			return nil, fmt.Errorf("encode replayed Responses input: %w", err)
		}
		request.fields["input"] = encoded
	}
	return carriedMetadata, nil
}

func (t *hpatchResponseTransform) translate(callID, input string, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.script != input {
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

	workspace, script, explicitWorkspace, err := parseHPatchWorkspaceDirective(input, t.workspaces)
	if err != nil {
		return t.rejectUnevaluated(callID, input, err, upstreamItem)
	}
	evaluated := script
	if isHPatchCorrection(script) {
		base, baseErr := t.correctionHistory()
		if baseErr != nil {
			return t.rejectUnevaluated(callID, input, baseErr, upstreamItem)
		}
		if base.workspaceID == "" && len(t.workspaces) != 1 {
			return t.rejectUnevaluated(callID, input, errors.New("the rejected script predates workspace selection; send a complete script with workspace_id"), upstreamItem)
		}
		if workspace == nil {
			if base.workspaceID == "" {
				workspace = &t.workspaces[0]
			} else if selected, ok := hpatchWorkspaceByID(t.workspaces, base.workspaceID); ok {
				workspace = selected
			} else {
				return t.rejectUnevaluated(callID, input, errors.New("the rejected script's workspace is no longer available; send a complete script"), upstreamItem)
			}
		} else if base.workspaceID != "" && workspace.id != base.workspaceID {
			return t.rejectUnevaluated(callID, input, errors.New("a correction must use the rejected script's workspace_id"), upstreamItem)
		}
		corrections, parseErr := parseHPatchCorrections(script)
		if parseErr != nil {
			return t.rejectUnevaluated(callID, input, parseErr, upstreamItem)
		}
		evaluated, err = applyHPatchCorrections(base.correctable(), corrections)
		if err != nil {
			return t.rejectUnevaluated(callID, input, err, upstreamItem)
		}
		if len(evaluated) > maxHPatchScriptBytes {
			return hpatchHistory{}, fmt.Errorf("hpatch call %q corrected script exceeds %d bytes", callID, maxHPatchScriptBytes)
		}
	} else if workspace == nil {
		if len(t.workspaces) != 1 {
			return t.rejectUnevaluated(callID, input, errorsForHPatchWorkspace("a complete script requires workspace_id", t.workspaces), upstreamItem)
		}
		workspace = &t.workspaces[0]
	}

	translated, err := t.proxy.translator.Translate(t.ctx, *workspace, evaluated)
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		if errors.Is(err, errHPatchCapacity) || errors.Is(err, errHPatchMetricsProtocol) || errors.Is(err, errHPatchWorkspaceChanged) {
			return hpatchHistory{}, err
		}
		diagnostic := hpatchDiagnostic(err) + hpatchCorrectionHint
		carrier := hpatchDiagnosticExecInput(diagnostic)
		if err := t.recordMetrics(hpatchMetricInputs{
			invocation:    translated.invocation,
			emittedScript: input,
			diagnostic:    diagnostic,
			carrier:       carrier,
		}); err != nil {
			return hpatchHistory{}, err
		}
		history := hpatchHistory{
			script:           input,
			workspaceID:      workspace.id,
			evaluated:        retainedEvaluated(input, evaluated),
			carrierName:      t.codeModeToolName,
			translationError: diagnostic,
			upstreamItem:     maps.Clone(upstreamItem),
		}
		t.recordLocal(callID, &history)
		return history, nil
	}
	patch := translated.patch
	if explicitWorkspace || len(t.workspaces) > 1 {
		patch, err = rebaseHPatchPatch(patch, workspace.canonical)
		if err != nil {
			return hpatchHistory{}, err
		}
	}
	if len(patch) > maxHPatchPatchBytes {
		return hpatchHistory{}, fmt.Errorf("hpatch call %q translation exceeds %d bytes", callID, maxHPatchPatchBytes)
	}
	carrier := hpatchApplyExecInput(string(patch), translated.report)
	if err := t.recordMetrics(hpatchMetricInputs{
		invocation:    translated.invocation,
		emittedScript: input,
		report:        translated.report,
		carrier:       carrier,
		successful:    true,
	}); err != nil {
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		script:       input,
		workspaceID:  workspace.id,
		evaluated:    retainedEvaluated(input, evaluated),
		patch:        string(patch),
		carrierName:  t.codeModeToolName,
		report:       translated.report,
		upstreamItem: maps.Clone(upstreamItem),
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func hpatchApplyExecInput(patch, report string) string {
	return hpatchApplyExecMarker +
		"await tools.apply_patch(" + strconv.Quote(patch) + ");\n" +
		"text(" + strconv.Quote(report) + ");"
}

func hpatchDiagnosticExecInput(diagnostic string) string {
	return "text(" + strconv.Quote(diagnostic) + ");"
}

func (h hpatchHistory) carrierInput() string {
	if h.translationError != "" {
		return hpatchDiagnosticExecInput(h.translationError)
	}
	return hpatchApplyExecInput(h.patch, h.report)
}

func (t *hpatchResponseTransform) rejectUnevaluated(callID, input string, rejection error, upstreamItem map[string]json.RawMessage) (hpatchHistory, error) {
	diagnostic := rejection.Error()
	carrier := hpatchDiagnosticExecInput(diagnostic)
	if err := t.recordMetrics(hpatchMetricInputs{emittedScript: input, diagnostic: diagnostic, carrier: carrier}); err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		return hpatchHistory{}, err
	}
	history := hpatchHistory{
		script:           input,
		carrierName:      t.codeModeToolName,
		translationError: diagnostic,
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

func (t *hpatchResponseTransform) claimRequestAccounting() (string, string, []string) {
	if t.requestAccountingClaimed {
		return "", "", nil
	}
	t.requestAccountingClaimed = true
	return t.toolDescription, t.baselineDefinition, slices.Clone(t.carriedMetadata)
}

func (t *hpatchResponseTransform) recordMetrics(inputs hpatchMetricInputs) error {
	inputs.definition, inputs.baselineDefinition, inputs.carriedMetadata = t.claimRequestAccounting()
	inputs.sessionID = t.sessionID
	record, err := calculateHPatchMetricRecord(inputs)
	if err != nil {
		return err
	}
	return t.proxy.translator.RecordMetrics(t.ctx, record)
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
		if !history.unevaluated {
			return correctionHistoryOf(maps.Values(t.local))
		}
	}
	return t.proxy.correctableHistory(t.sessionID)
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
		if json.Unmarshal(envelope.Item, &item) != nil || jsonString(item, "name") != hpatchToolName {
			return [][]byte{payload}, nil //nolint:nilerr // Unrelated output items pass through unchanged.
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
		t.pending[itemID] = hpatchPendingCall{callID: callID, added: bytes.Clone(payload)}
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
		history, err := t.translate(pending.callID, envelope.Input, nil)
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
		doneEvent, err := replaceRawField(payload, "input", mustMarshalJSON(history.carrierInput()))
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
		return onePayload(event, err)

	case "response.failed", "response.incomplete":
		clear(t.pending)
		return [][]byte{payload}, nil
	default:
		if _, pending := t.pending[envelope.ItemID]; pending || t.pendingCallKnown(envelope.CallID) || envelope.Name == hpatchToolName || envelope.Name == applyPatchToolName {
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
	if err := t.proxy.rememberBatch(t.sessionID, t.local); err != nil {
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
	if jsonString(item, "name") != hpatchToolName {
		return false, nil
	}
	callID := jsonString(item, "call_id")
	if jsonString(item, "type") != "custom_tool_call" || callID == "" {
		return false, errors.New("upstream emitted malformed hpatch call")
	}
	history, err := t.translate(callID, jsonString(item, "input"), maps.Clone(item))
	if err != nil {
		return false, err
	}
	item["name"] = mustMarshalJSON(history.carrierName)
	item["input"] = mustMarshalJSON(history.carrierInput())
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
