package router

import (
	"cmp"
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
		encodedItem, err := json.Marshal(history.upstreamItem)
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

func (p *hpatchProxy) confirmHistory(sessionID, callID, output string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.sessions[sessionID]
	if session == nil {
		return
	}
	history, ok := session.calls[callID]
	if !ok || history.translationError != "" || output != history.report {
		return
	}
	history.confirmed = true
	session.calls[callID] = history
}

func (p *hpatchProxy) targetAliases(sessionID, root string) []hpatch.TargetAlias {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return nil
	}
	histories := make([]hpatchHistory, 0, len(session.calls))
	for _, history := range session.calls {
		if history.root == root && (history.confirmed || history.applied) && len(history.aliases) != 0 {
			histories = append(histories, history)
		}
	}
	slices.SortFunc(histories, func(first, second hpatchHistory) int {
		return cmp.Compare(first.sequence, second.sequence)
	})
	var aliases []hpatch.TargetAlias
	for _, history := range histories {
		aliases = append(aliases, history.aliases...)
	}
	return aliases
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
	commentaryIDs := p.commentaryMessageIDs(sessionID)
	if len(commentaryIDs) != 0 {
		items = slices.DeleteFunc(items, func(item map[string]json.RawMessage) bool {
			if jsonString(item, "type") != "message" {
				return false
			}
			_, generated := commentaryIDs[jsonString(item, "id")]
			changed = changed || generated
			return generated
		})
	}
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
			p.confirmHistory(sessionID, callID, jsonString(item, "output"))
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
	if err := t.proxy.rememberBatch(t.historySessionID, t.local); err != nil {
		return err
	}
	t.historyCommitted = true
	return nil
}

func (t *hpatchResponseTransform) commitLocalCall(callID string) error {
	history, exists := t.local[callID]
	if !exists {
		return nil
	}
	return t.proxy.rememberBatch(t.historySessionID, map[string]hpatchHistory{callID: history})
}
