package router

// Source: metrics.go:11:368 bounded token telemetry, snapshots, and SSE delivery.

import (
	"encoding/json"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/yusing/hpatch"
)

const (
	maxMetricModels         = 128
	maxActiveMetricRequests = 1024
	maxMetricSubscribers    = 128
	otherMetricModelKey     = "other"
)

type tokenCounts struct {
	InputTokens         uint64 `json:"input_tokens"`
	UncachedInputTokens uint64 `json:"uncached_input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	ReasoningTokens     uint64 `json:"reasoning_tokens"`
}

func (c *tokenCounts) add(other tokenCounts) {
	c.InputTokens += other.InputTokens
	c.UncachedInputTokens += other.UncachedInputTokens
	c.OutputTokens += other.OutputTokens
	c.ReasoningTokens += other.ReasoningTokens
}

type metricGroup struct {
	Total   tokenCounts            `json:"total"`
	ByModel map[string]tokenCounts `json:"by_model"`
}

func newMetricGroup() metricGroup {
	return metricGroup{ByModel: map[string]tokenCounts{}}
}

func (g *metricGroup) add(model string, counts tokenCounts) {
	g.Total.add(counts)
	if _, exists := g.ByModel[model]; !exists && len(g.ByModel) >= maxMetricModels {
		model = otherMetricModelKey
	}
	byModel := g.ByModel[model]
	byModel.add(counts)
	g.ByModel[model] = byModel
}

type sessionMetrics struct {
	metricGroup

	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

type metricsSnapshot struct {
	metricGroup

	Sessions  []sessionMetrics   `json:"sessions"`
	Gain      hpatch.GainMetrics `json:"gain"`
	GainError string             `json:"gain_error,omitempty"`
	Mode      string             `json:"mode,omitempty"`
}

type metricsStore struct {
	mu               sync.RWMutex
	all              metricGroup
	retainedSessions map[string]retainedSessionMetrics
	activeSessions   map[string]map[uint64]activeRequest
	activeRequests   int
	requestSequence  uint64
	sessionUsed      map[string]uint64
	subscribers      map[uint64]chan struct{}
	subscriberSeq    uint64
	gainDirectory    string
	mode             string
}

type retainedSessionMetrics struct {
	metricGroup

	model string
}

type activeRequest struct {
	model string
	order uint64
}

type activeRequestHandle struct {
	store     *metricsStore
	sessionID string
	requestID uint64
}

func newMetricsStore(gainDirectory string) *metricsStore {
	return &metricsStore{
		all:              newMetricGroup(),
		retainedSessions: map[string]retainedSessionMetrics{},
		activeSessions:   map[string]map[uint64]activeRequest{},
		sessionUsed:      map[string]uint64{},
		subscribers:      map[uint64]chan struct{}{},
		gainDirectory:    gainDirectory,
	}
}

// notify publishes a snapshot refresh to live dashboard subscribers.
func (m *metricsStore) notify() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyLocked()
}

func (m *metricsStore) beginRequest(sessionID, model string) *activeRequestHandle {
	if !validMetricSessionID(sessionID) {
		return nil
	}
	if model == "" {
		model = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeRequests >= maxActiveMetricRequests {
		return nil
	}
	m.activeRequests++
	m.requestSequence++
	if m.activeSessions[sessionID] == nil {
		m.activeSessions[sessionID] = map[uint64]activeRequest{}
	}
	m.activeSessions[sessionID][m.requestSequence] = activeRequest{model: model, order: m.requestSequence}
	m.notifyLocked()
	return &activeRequestHandle{store: m, sessionID: sessionID, requestID: m.requestSequence}
}

func validMetricSessionID(sessionID string) bool {
	return strings.TrimSpace(sessionID) != "" && len(sessionID) <= maxSessionIDBytes
}

func (h *activeRequestHandle) end() {
	if h == nil {
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	requests := h.store.activeSessions[h.sessionID]
	if _, exists := requests[h.requestID]; !exists {
		return
	}
	delete(requests, h.requestID)
	h.store.activeRequests--
	if len(requests) == 0 {
		delete(h.store.activeSessions, h.sessionID)
	}
	h.store.notifyLocked()
}

func (m *metricsStore) record(sessionID, model string, counts tokenCounts) {
	if counts == (tokenCounts{}) {
		return
	}
	if model == "" {
		model = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.notifyLocked()
	m.all.add(model, counts)
	if !validMetricSessionID(sessionID) {
		return
	}
	retained, ok := m.retainedSessions[sessionID]
	if !ok {
		if len(m.retainedSessions) >= maxSessionHistories {
			m.evictOldestSessionTotals()
		}
		retained.metricGroup = newMetricGroup()
	}
	retained.add(model, counts)
	retained.model = model
	m.requestSequence++
	m.sessionUsed[sessionID] = m.requestSequence
	m.retainedSessions[sessionID] = retained
}

func (m *metricsStore) subscribe() (<-chan struct{}, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subscribers) >= maxMetricSubscribers {
		return nil, nil, false
	}
	m.subscriberSeq++
	id := m.subscriberSeq
	updates := make(chan struct{}, 1)
	m.subscribers[id] = updates
	return updates, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.subscribers, id)
	}, true
}

func (m *metricsStore) notifyLocked() {
	for _, updates := range m.subscribers {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
}

func (m *metricsStore) evictOldestSessionTotals() {
	oldestID := ""
	for sessionID := range m.retainedSessions {
		if oldestID == "" || m.sessionUsed[sessionID] < m.sessionUsed[oldestID] {
			oldestID = sessionID
		}
	}
	delete(m.retainedSessions, oldestID)
	delete(m.sessionUsed, oldestID)
}

func (m *metricsStore) snapshot() metricsSnapshot {
	m.mu.RLock()
	sessions := make(map[string]sessionMetrics, len(m.retainedSessions)+len(m.activeSessions))
	for id, retained := range m.retainedSessions {
		sessions[id] = sessionMetrics{
			SessionID: id, Model: retained.model,
			metricGroup: cloneMetricGroup(retained.metricGroup),
		}
	}
	for id, requests := range m.activeSessions {
		var current activeRequest
		for _, request := range requests {
			if request.order > current.order {
				current = request
			}
		}
		sessions[id] = sessionMetrics{
			SessionID: id, Model: current.model,
			metricGroup: cloneMetricGroup(m.retainedSessions[id].metricGroup),
		}
	}
	snapshot := metricsSnapshot{metricGroup: cloneMetricGroup(m.all), Sessions: make([]sessionMetrics, 0, len(sessions)), Mode: m.mode}
	for _, session := range sessions {
		snapshot.Sessions = append(snapshot.Sessions, session)
	}
	slices.SortFunc(snapshot.Sessions, func(a, b sessionMetrics) int {
		if a.SessionID < b.SessionID {
			return -1
		}
		if a.SessionID > b.SessionID {
			return 1
		}
		return 0
	})
	gainDirectory := m.gainDirectory
	m.mu.RUnlock()

	// Durable gain metrics use their own file lock; keep that I/O off the
	// in-memory telemetry mutex so request lifecycle writers are not blocked.
	snapshot.Gain, snapshot.GainError = loadGainMetrics(gainDirectory)
	return snapshot
}

func loadGainMetrics(gainDirectory string) (hpatch.GainMetrics, string) {
	if gainDirectory == "" {
		return hpatch.EmptyGainMetrics(), ""
	}
	gain, err := hpatch.LoadGainMetrics(gainDirectory)
	if err != nil {
		return hpatch.EmptyGainMetrics(), err.Error()
	}
	return gain, ""
}

func cloneMetricGroup(group metricGroup) metricGroup {
	byModel := maps.Clone(group.ByModel)
	if byModel == nil {
		byModel = map[string]tokenCounts{}
	}
	return metricGroup{Total: group.Total, ByModel: byModel}
}

func (m *metricsStore) serveAPI(w http.ResponseWriter, r *http.Request) {
	if acceptsEventStream(r.Header) {
		m.serveEvents(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(m.snapshot()); err != nil {
		http.Error(w, "encode metrics", http.StatusInternalServerError)
	}
}

func acceptsEventStream(headers http.Header) bool {
	for _, value := range headers.Values("Accept") {
		for mediaRange := range strings.SplitSeq(value, ",") {
			mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(mediaRange))
			if err != nil || mediaType != "text/event-stream" {
				continue
			}
			quality := 1.0
			if rawQuality, exists := parameters["q"]; exists {
				quality, err = strconv.ParseFloat(rawQuality, 64)
			}
			if err == nil && quality > 0 && quality <= 1 {
				return true
			}
		}
	}
	return false
}

func (m *metricsStore) serveEvents(w http.ResponseWriter, r *http.Request) {
	updates, unsubscribe, ok := m.subscribe()
	if !ok {
		http.Error(w, "too many metrics subscribers", http.StatusServiceUnavailable)
		return
	}
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	for {
		payload, err := json.Marshal(m.snapshot())
		if err != nil {
			return
		}
		if _, err := w.Write(append(append([]byte("data: "), payload...), '\n', '\n')); err != nil {
			return
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case <-updates:
		case <-r.Context().Done():
			return
		}
	}
}

func usageFromResponsePayload(body []byte, streamEvent bool) (tokenCounts, bool) {
	var envelope struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
		Usage    json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return tokenCounts{}, false
	}
	raw := envelope.Usage
	if streamEvent {
		if envelope.Type != "response.completed" {
			return tokenCounts{}, false
		}
		var completed struct {
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(envelope.Response, &completed) != nil {
			return tokenCounts{}, false
		}
		raw = completed.Usage
	}
	var usage struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
		InputDetails struct {
			CachedTokens uint64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails struct {
			ReasoningTokens uint64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &usage) != nil {
		return tokenCounts{}, false
	}
	return tokenCounts{
		InputTokens:         usage.InputTokens,
		UncachedInputTokens: usage.InputTokens - min(usage.InputTokens, usage.InputDetails.CachedTokens),
		OutputTokens:        usage.OutputTokens,
		ReasoningTokens:     usage.OutputDetails.ReasoningTokens,
	}, true
}
