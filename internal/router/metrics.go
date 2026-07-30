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
	"time"

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

type requestOutcome uint8

const (
	requestOutcomeUnknown requestOutcome = iota
	requestOutcomeCompleted
	requestOutcomeFailed
	requestOutcomeCanceledBeforeResponse
	requestOutcomeCanceledAfterResponse
	requestOutcomeTimedOut
	requestOutcomeBackgroundPending
)

func (outcome requestOutcome) String() string {
	switch outcome {
	case requestOutcomeCompleted:
		return "completed"
	case requestOutcomeFailed:
		return "failed"
	case requestOutcomeCanceledBeforeResponse:
		return "canceled_before_response"
	case requestOutcomeCanceledAfterResponse:
		return "canceled_after_response"
	case requestOutcomeTimedOut:
		return "timed_out"
	case requestOutcomeBackgroundPending:
		return "background_pending"
	default:
		return "unknown"
	}
}

type requestObservation struct {
	outcome requestOutcome

	totalDuration    time.Duration
	upstreamDuration time.Duration
	usageCounts      tokenCounts
	usageObserved    bool
}

type requestLifecycleMetrics struct {
	Started                      uint64 `json:"started"`
	Active                       uint64 `json:"active"`
	Completed                    uint64 `json:"completed"`
	Failed                       uint64 `json:"failed"`
	CanceledBeforeResponse       uint64 `json:"canceled_before_response"`
	CanceledAfterResponse        uint64 `json:"canceled_after_response"`
	TimedOut                     uint64 `json:"timed_out"`
	BackgroundPending            uint64 `json:"background_pending"`
	UsageObserved                uint64 `json:"usage_observed"`
	UsageMissing                 uint64 `json:"usage_missing"`
	TotalDurationMilliseconds    uint64 `json:"total_duration_ms"`
	UpstreamDurationMilliseconds uint64 `json:"upstream_duration_ms"`
}

func (m *requestLifecycleMetrics) addFinished(observation requestObservation) {
	switch observation.outcome {
	case requestOutcomeCompleted:
		m.Completed++
	case requestOutcomeCanceledBeforeResponse:
		m.CanceledBeforeResponse++
	case requestOutcomeCanceledAfterResponse:
		m.CanceledAfterResponse++
	case requestOutcomeTimedOut:
		m.TimedOut++
	case requestOutcomeBackgroundPending:
		m.BackgroundPending++
	default:
		m.Failed++
	}
	if observation.usageObserved {
		m.UsageObserved++
	} else {
		m.UsageMissing++
	}
	m.TotalDurationMilliseconds += durationMilliseconds(observation.totalDuration)
	m.UpstreamDurationMilliseconds += durationMilliseconds(observation.upstreamDuration)
}

func durationMilliseconds(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(duration.Milliseconds())
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

	Requests  requestLifecycleMetrics `json:"requests"`
	SessionID string                  `json:"session_id"`
	Model     string                  `json:"model"`
}

type metricsSnapshot struct {
	metricGroup

	Requests  requestLifecycleMetrics `json:"requests"`
	Sessions  []sessionMetrics        `json:"sessions"`
	Gain      hpatch.GainMetrics      `json:"gain"`
	GainError string                  `json:"gain_error,omitempty"`
	Mode      string                  `json:"mode,omitempty"`
}

type metricsStore struct {
	mu               sync.RWMutex
	all              metricGroup
	requests         requestLifecycleMetrics
	retainedSessions map[string]retainedSessionMetrics
	activeSessions   map[string]map[uint64]activeRequest

	requestSequence uint64
	sessionUsed     map[string]uint64
	subscribers     map[uint64]chan struct{}
	subscriberSeq   uint64
	gainDirectory   string
	mode            string
}

type retainedSessionMetrics struct {
	metricGroup

	requests   requestLifecycleMetrics
	model      string
	modelOrder uint64
}

type activeRequest struct {
	model string
	order uint64
}

type activeRequestHandle struct {
	store     *metricsStore
	sessionID string
	model     string
	requestID uint64
	finished  bool
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
	if model == "" {
		model = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.requests.Active >= maxActiveMetricRequests {
		return nil
	}

	m.requests.Started++
	m.requests.Active++
	m.requestSequence++
	handle := &activeRequestHandle{store: m, model: model}
	if validMetricSessionID(sessionID) {
		handle.sessionID = sessionID
		handle.requestID = m.requestSequence
		if m.activeSessions[sessionID] == nil {
			m.activeSessions[sessionID] = map[uint64]activeRequest{}
		}
		m.activeSessions[sessionID][handle.requestID] = activeRequest{model: model, order: handle.requestID}
	}
	m.notifyLocked()
	return handle
}

func validMetricSessionID(sessionID string) bool {
	return strings.TrimSpace(sessionID) != "" && len(sessionID) <= maxSessionIDBytes
}

func (h *activeRequestHandle) finish(observation requestObservation) {
	if h == nil {
		return
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.finished {
		return
	}
	h.finished = true

	h.store.requests.Active--
	h.store.requests.addFinished(observation)
	if observation.usageObserved && observation.usageCounts != (tokenCounts{}) {
		h.store.all.add(h.model, observation.usageCounts)
	}
	if h.sessionID != "" {
		requests := h.store.activeSessions[h.sessionID]
		delete(requests, h.requestID)
		if len(requests) == 0 {
			delete(h.store.activeSessions, h.sessionID)
		}
		retained := h.store.retainedSessionLocked(h.sessionID)
		if h.requestID >= retained.modelOrder {
			retained.model = h.model
			retained.modelOrder = h.requestID
		}

		retained.requests.Started++
		retained.requests.addFinished(observation)
		if observation.usageObserved && observation.usageCounts != (tokenCounts{}) {
			retained.add(h.model, observation.usageCounts)
		}
		h.store.retainedSessions[h.sessionID] = retained
	}
	h.store.notifyLocked()
}

func (m *metricsStore) retainedSessionLocked(sessionID string) retainedSessionMetrics {
	retained, ok := m.retainedSessions[sessionID]
	if !ok {
		if len(m.retainedSessions) >= maxSessionHistories {
			m.evictOldestSessionTotals()
		}
		retained.metricGroup = newMetricGroup()
	}
	m.requestSequence++
	m.sessionUsed[sessionID] = m.requestSequence
	return retained
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
			SessionID: id, Model: retained.model, Requests: retained.requests,
			metricGroup: cloneMetricGroup(retained.metricGroup),
		}
	}
	for id, active := range m.activeSessions {
		session, exists := sessions[id]
		if !exists {
			session = sessionMetrics{SessionID: id, metricGroup: newMetricGroup()}
		}
		var current activeRequest
		for _, request := range active {
			if request.order > current.order {
				current = request
			}
		}
		activeCount := uint64(len(active))
		if retained, exists := m.retainedSessions[id]; !exists || current.order >= retained.modelOrder {
			session.Model = current.model
		}

		session.Requests.Started += activeCount
		session.Requests.Active += activeCount
		sessions[id] = session
	}
	snapshot := metricsSnapshot{
		metricGroup: cloneMetricGroup(m.all),
		Requests:    m.requests,
		Sessions:    make([]sessionMetrics, 0, len(sessions)),
		Mode:        m.mode,
	}
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
		switch envelope.Type {
		case "response.completed", "response.failed", "response.incomplete":
		default:
			return tokenCounts{}, false
		}
		var terminal struct {
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(envelope.Response, &terminal) != nil {
			return tokenCounts{}, false
		}
		raw = terminal.Usage
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
