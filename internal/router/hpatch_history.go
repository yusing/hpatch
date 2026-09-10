package router

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yusing/hpatch"
)

const (
	maxHPatchHistorySessionBytes = 32 << 20
	maxHPatchHistoryGlobalBytes  = 128 << 20
)

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

	report               string
	translationError     string
	evaluatorRejected    bool
	rejections           []hpatch.HostRejection
	correlationID        string
	attempt              int
	upstreamItem         map[string]json.RawMessage
	replayCarrier        bool
	commentaryMessageIDs []string
	bytes                int
	// unevaluated marks a call the proxy rejected before hpatch saw it. Such a
	// recovery changed nothing and has no script of its own, so another recovery
	// looks past it to the rejected script it was trying to repair.
	unevaluated      bool
	alreadySatisfied bool
	confirmed        bool
	aliases          []hpatch.TargetAlias
	// sequence orders a request-visible view (or the bounded memory cache).
	// It is never durable: replay derives recovery order from the input.
	sequence uint64
}

type hpatchHistorySession struct {
	calls map[string]hpatchHistory
	bytes int
	// nextSequence is the order to assign the session's next retained call.
	nextSequence uint64
	lastUsed     uint64
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

func (p *hpatchProxy) touchSession(session *hpatchHistorySession) {
	p.sessionSequence++
	session.lastUsed = p.sessionSequence
}

func (p *hpatchProxy) rememberBatch(sessionID string, histories map[string]hpatchHistory) error {
	if len(histories) == 0 {
		return nil
	}
	prepared := make(map[string]hpatchHistory, len(histories))
	for callID, history := range histories {
		encodedItem, err := marshalProtocolJSON(history.upstreamItem)
		if err != nil {
			return fmt.Errorf("encode hpatch history item: %w", err)
		}
		history.bytes = len(sessionID) + len(callID) + len(history.toolName) + len(history.pluginID) + len(history.script) + len(history.root) + len(history.evaluated) + len(history.patch) + len(history.carrierKind) + len(history.carrierName) + len(history.carrierPayload) + len(history.report) + len(history.translationError) + len(history.correlationID) + len(encodedItem)
		for _, rejection := range history.rejections {
			history.bytes += hpatchRejectionTextBytes(rejection)
		}
		for _, alias := range history.aliases {
			history.bytes += len(alias.Path) + len(alias.Before) + len(alias.After)
		}
		for _, messageID := range history.commentaryMessageIDs {
			history.bytes += len(messageID)
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

func hpatchRejectionTextBytes(rejection hpatch.HostRejection) int {
	return len(rejection.Operation) + len(rejection.Target) + len(rejection.TargetAliasRelation) +
		len(rejection.Reason) + len(rejection.Path)
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

// reconcileVisibleInput constructs recovery ancestry from the validated request,
// never from a routing session's most recent turn. All changes stay local until
// the entire input is valid, including output confirmations.
func (p *hpatchProxy) reconcileVisibleInput(ctx context.Context, request *parsedResponsesRequest, workspace, sessionID string) (map[string]hpatchHistory, error) {
	visible := make(map[string]hpatchHistory)
	raw, ok := request.fields["input"]
	if !ok {
		return visible, nil
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return visible, nil
	}
	changed := false
	commentaryIDs := p.commentaryMessageIDs(sessionID)
	filtered := make([]map[string]json.RawMessage, 0, len(items))
	removedCached := 0
	for index, item := range items {
		if jsonString(item, "type") == "message" {
			id := jsonString(item, "id")
			_, generated := commentaryIDs[id]
			if p.replayStore != nil {
				var err error
				generated, err = p.replayStore.hasCommentary(ctx, workspace, id)
				if err != nil {
					return nil, err
				}
			}
			if generated && index < request.cachedInput {
				removedCached++
			}
			if generated {
				changed = true
				continue
			}
		}
		filtered = append(filtered, item)
	}
	request.cachedInput -= removedCached
	items = filtered
	validatedCarriers := make(map[string]bool)
	for index, item := range items {
		itemType := jsonString(item, "type")
		if itemType != "custom_tool_call" && itemType != "function_call" && itemType != "custom_tool_call_output" && itemType != "function_call_output" {
			continue
		}
		callID := jsonString(item, "call_id")
		history, known := visible[callID]
		if !known {
			if p.replayStore != nil {
				var err error
				history, known, err = p.replayStore.lookup(ctx, workspace, callID)
				if err != nil {
					return nil, err
				}
			} else {
				history, known = p.history(sessionID, callID)
			}
			if !known {
				continue
			}
			history.sequence = uint64(len(visible) + 1)
			history.confirmed = false
		}
		carrierKind := history.effectiveCarrierKind()
		if itemType == carrierOutputItemType(carrierKind) {
			if history.translationError == "" && jsonString(item, "output") == history.report {
				history.confirmed = true
			}
			visible[callID] = history
			if !history.replayCarrier {
				upstreamKind := codeModeCarrierCustom
				if jsonString(history.upstreamItem, "type") == carrierItemType(codeModeCarrierFunction) {
					upstreamKind = codeModeCarrierFunction
				}
				item["type"] = mustMarshalJSON(carrierOutputItemType(upstreamKind))
				changed = true
			}
			continue
		}
		if itemType != carrierItemType(carrierKind) {
			return nil, fmt.Errorf("replayed call %q changed item type", callID)
		}
		if jsonString(item, "name") != history.carrierName {
			return nil, fmt.Errorf("replayed call %q changed carrier name", callID)
		}
		if validatedCarriers[callID] {
			return nil, fmt.Errorf("replayed call %q appears more than once", callID)
		}
		if jsonString(item, carrierPayloadField(carrierKind)) != history.carrierInput() {
			return nil, fmt.Errorf("replayed call %q changed translated payload", callID)
		}
		validatedCarriers[callID] = true
		visible[callID] = history
		if history.replayCarrier {
			continue
		}
		if len(history.upstreamItem) != 0 {
			items[index] = maps.Clone(history.upstreamItem)
		} else {
			item["name"] = mustMarshalJSON(cmp.Or(history.toolName, hpatchToolName))
			item["input"] = mustMarshalJSON(history.script)
		}
		changed = true
	}
	if changed {
		encoded, err := marshalProtocolJSON(items)
		if err != nil {
			return nil, fmt.Errorf("encode replayed Responses input: %w", err)
		}
		request.setInput(encoded)
	}
	return visible, nil
}

func (t *hpatchResponseTransform) recordLocal(callID string, history *hpatchHistory) {
	if t.nativeTools && history.carrierKind == "" {
		history.carrierKind = codeModeCarrierFunction
		history.carrierName = nativeExecCommandToolName
		history.carrierPayload = renderExecCarrier(
			codeModeCarrierFunction,
			execCommandArguments(hpatchNativeCommand(*history), nil),
			false,
			nil,
		)
	}
	t.localSequence++
	history.sequence = t.localSequence
	t.local[callID] = *history
}

func (t *hpatchResponseTransform) commitHistory() error {
	if t.historyCommitted {
		return nil
	}
	if err := t.proxy.replayStore.put(t.ctx, t.directory, t.local); err != nil {
		return err
	}
	if err := t.proxy.rememberBatch(t.historySessionID, t.local); err != nil && t.proxy.replayStore == nil {
		return err
	}
	t.historyCommitted = true
	for callID := range t.local {
		t.handOffCommentary(callID)
	}
	return nil
}

func (t *hpatchResponseTransform) commitLocalCall(callID string) error {
	history, exists := t.local[callID]
	if !exists {
		return nil
	}
	if err := t.proxy.replayStore.put(t.ctx, t.directory, map[string]hpatchHistory{callID: history}); err != nil {
		return err
	}
	if err := t.proxy.rememberBatch(t.historySessionID, map[string]hpatchHistory{callID: history}); err != nil && t.proxy.replayStore == nil {
		return err
	}
	t.handOffCommentary(callID)
	return nil
}

// targetAliases includes only visible ancestry and calls applied in this turn.
func (t *hpatchResponseTransform) targetAliases() []hpatch.TargetAlias {
	histories := slices.Collect(maps.Values(t.visible))
	slices.SortFunc(histories, func(a, b hpatchHistory) int { return cmp.Compare(a.sequence, b.sequence) })
	local := slices.Collect(maps.Values(t.local))
	slices.SortFunc(local, func(a, b hpatchHistory) int { return cmp.Compare(a.sequence, b.sequence) })
	histories = append(histories, local...)
	var aliases []hpatch.TargetAlias
	for _, history := range histories {
		if history.root == t.directory && (history.confirmed || history.applied) {
			aliases = append(aliases, history.aliases...)
		}
	}
	return aliases
}
