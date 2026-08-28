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

	"github.com/tiktoken-go/tokenizer"
	"github.com/yusing/hpatch"
)

const (
	maxMetricModels                    = 128
	maxActiveMetricRequests            = 1024
	maxMetricSubscribers               = 128
	maxSessionHPatchRejections         = 32
	maxSessionHPatchAttempts           = 128
	maxSessionRequestObservations      = 128
	maxSessionCTPObservations          = 128
	maxSessionHPatchRejectionTextBytes = 16 << 10
	maxSessionHPatchAttemptTextBytes   = 48 << 10
	otherMetricModelKey                = "other"
)

type tokenCounts struct {
	InputTokens         uint64 `json:"input_tokens"`
	UncachedInputTokens uint64 `json:"uncached_input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	ReasoningTokens     uint64 `json:"reasoning_tokens"`
}

type ctpCompressionTokens struct {
	NativeTokens  uint64 `json:"native_tokens"`
	CompactTokens uint64 `json:"compact_tokens"`
}

type ctpCompressionBytes struct {
	NativeBytes  uint64 `json:"native_bytes"`
	CompactBytes uint64 `json:"compact_bytes"`
}

type ctpDictionaryMetrics struct {
	Definitions uint64 `json:"definitions"`
	Bytes       uint64 `json:"bytes"`
}

type ctpCodecMetrics struct {
	EncodeOperations  uint64 `json:"encode_operations"`
	EncodeNanoseconds uint64 `json:"encode_nanoseconds"`
	DecodeOperations  uint64 `json:"decode_operations"`
	DecodeNanoseconds uint64 `json:"decode_nanoseconds"`
	DecodeFailures    uint64 `json:"decode_failures"`
}

type ctpCompressionMetrics struct {
	ConsideredRequests        uint64               `json:"considered_requests"`
	ActiveRequests            uint64               `json:"active_requests"`
	MissingCarrier            uint64               `json:"missing_carrier"`
	RequestStrings            uint64               `json:"request_strings"`
	RequestVisibleReferences  uint64               `json:"request_visible_references"`
	AssistantTexts            uint64               `json:"assistant_texts"`
	ResponseStrings           uint64               `json:"response_strings"`
	ResponseVisibleReferences uint64               `json:"response_visible_references"`
	Input                     ctpCompressionTokens `json:"input"`
	Output                    ctpCompressionTokens `json:"output"`
	InputBytes                ctpCompressionBytes  `json:"input_bytes"`
	OutputBytes               ctpCompressionBytes  `json:"output_bytes"`
	RequestDictionary         ctpDictionaryMetrics `json:"request_dictionary"`
	ResponseDictionary        ctpDictionaryMetrics `json:"response_dictionary"`
	Codec                     ctpCodecMetrics      `json:"codec"`
}

type ctpRepresentationMetrics struct {
	NativeTokens  uint64 `json:"native_tokens"`
	CompactTokens uint64 `json:"compact_tokens"`
	NativeBytes   uint64 `json:"native_bytes"`
	CompactBytes  uint64 `json:"compact_bytes"`
}

type ctpInputObservation struct {
	ctpRepresentationMetrics

	RequestSequence   uint64 `json:"request_sequence"`
	Decision          string `json:"decision"`
	Definitions       uint64 `json:"definitions"`
	DictionaryBytes   uint64 `json:"dictionary_bytes"`
	Strings           uint64 `json:"strings"`
	VisibleReferences uint64 `json:"visible_references"`
	EncodeNanoseconds uint64 `json:"encode_nanoseconds"`
}

type ctpOutputObservation struct {
	ctpRepresentationMetrics

	RequestSequence   uint64 `json:"request_sequence"`
	Definitions       uint64 `json:"definitions"`
	DictionaryBytes   uint64 `json:"dictionary_bytes"`
	Strings           uint64 `json:"strings"`
	VisibleReferences uint64 `json:"visible_references"`
}

type ctpSessionMetrics struct {
	ctpCompressionMetrics

	InputObservations         []ctpInputObservation  `json:"input_observations"`
	OutputObservations        []ctpOutputObservation `json:"output_observations"`
	InputObservationsDropped  uint64                 `json:"input_observations_dropped"`
	OutputObservationsDropped uint64                 `json:"output_observations_dropped"`
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
	requestOutcomeStreamIdleTimedOut
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
	case requestOutcomeStreamIdleTimedOut:
		return "stream_idle_timed_out"
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

type requestMetricObservation struct {
	Sequence                     uint64      `json:"sequence"`
	Outcome                      string      `json:"outcome"`
	TotalDurationMilliseconds    uint64      `json:"total_duration_ms"`
	UpstreamDurationMilliseconds uint64      `json:"upstream_duration_ms"`
	Usage                        tokenCounts `json:"usage"`
	UsageObserved                bool        `json:"usage_observed"`
}

type requestLifecycleMetrics struct {
	Started                      uint64 `json:"started"`
	Active                       uint64 `json:"active"`
	Completed                    uint64 `json:"completed"`
	Failed                       uint64 `json:"failed"`
	CanceledBeforeResponse       uint64 `json:"canceled_before_response"`
	CanceledAfterResponse        uint64 `json:"canceled_after_response"`
	TimedOut                     uint64 `json:"timed_out"`
	StreamIdleTimedOut           uint64 `json:"stream_idle_timed_out"`
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
	case requestOutcomeStreamIdleTimedOut:
		m.StreamIdleTimedOut++
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

	Requests                   requestLifecycleMetrics    `json:"requests"`
	RequestObservations        []requestMetricObservation `json:"request_observations"`
	RequestObservationsDropped uint64                     `json:"request_observations_dropped"`
	HPatchCalls                hpatchCallMetrics          `json:"hpatch_calls"`
	HPatchAttempts             []hpatchAttemptMetrics     `json:"hpatch_attempts"`
	HPatchRejections           []hpatch.HostRejection     `json:"hpatch_rejections"`
	CTP                        ctpSessionMetrics          `json:"ctp"`
	Commentary                 commentaryMetrics          `json:"commentary"`
	SessionID                  string                     `json:"session_id"`
	Title                      string                     `json:"title"`
	Model                      string                     `json:"model"`
}

type hpatchAttemptMetrics struct {
	Sequence              uint64                 `json:"sequence"`
	CorrelationID         string                 `json:"correlation_id"`
	CallID                string                 `json:"call_id"`
	Attempt               int                    `json:"attempt"`
	Correction            bool                   `json:"correction"`
	Outcome               string                 `json:"outcome"`
	EmittedHPatchTokens   uint64                 `json:"emitted_hpatch_tokens"`
	ApplyPatchTokens      uint64                 `json:"apply_patch_tokens"`
	EvaluatedCommands     uint64                 `json:"evaluated_commands"`
	DiagnosticInputTokens uint64                 `json:"diagnostic_input_tokens"`
	ConfirmedAliasRewrite bool                   `json:"confirmed_alias_rewrite"`
	Rejections            []hpatch.HostRejection `json:"rejections"`
}

func hpatchAttemptMetricsOf(record hpatchMetricRecord) (hpatchAttemptMetrics, bool) {
	metadata := record.Attempt
	if metadata.SessionID != record.SessionID || metadata.Attempt < 1 ||
		strings.TrimSpace(metadata.CorrelationID) == "" ||
		strings.TrimSpace(metadata.CallID) == "" {
		return hpatchAttemptMetrics{}, false
	}
	rejections := record.Rejections
	if excess := len(rejections) - maxSessionHPatchRejections; excess > 0 {
		rejections = rejections[excess:]
	}
	attempt := hpatchAttemptMetrics{
		CorrelationID:         metadata.CorrelationID,
		CallID:                metadata.CallID,
		Attempt:               metadata.Attempt,
		Correction:            metadata.Correction,
		EvaluatedCommands:     record.Invocation.EvaluatedCommandCount(),
		DiagnosticInputTokens: record.DiagnosticInputTokens,
		ConfirmedAliasRewrite: record.ConfirmedAliasRewrite,
		Rejections:            slices.Clone(rejections),
	}
	switch {
	case record.HPatchTokens != 0 && record.IneffectiveHPatchTokens == 0:
		attempt.Outcome = "successful"
		attempt.EmittedHPatchTokens = record.attemptHPatchTokens
		attempt.ApplyPatchTokens = record.attemptApplyPatchTokens
		if attempt.EmittedHPatchTokens == 0 {
			attempt.EmittedHPatchTokens = record.HPatchTokens
			attempt.ApplyPatchTokens = record.ApplyPatchTokens
		}
	case record.HPatchTokens == 0 && record.IneffectiveHPatchTokens != 0:
		attempt.Outcome = "rejected"
		attempt.EmittedHPatchTokens = record.attemptHPatchTokens
		attempt.ApplyPatchTokens = record.attemptApplyPatchTokens
		if attempt.EmittedHPatchTokens == 0 {
			attempt.EmittedHPatchTokens = record.IneffectiveHPatchTokens
			attempt.ApplyPatchTokens = record.FailedApplyPatchTokens
		}
	default:
		return hpatchAttemptMetrics{}, false
	}
	return attempt, true
}

func hpatchRejectionTextBytes(rejection hpatch.HostRejection) int {
	return len(rejection.Operation) + len(rejection.Target) + len(rejection.TargetAliasRelation) +
		len(rejection.Reason) + len(rejection.Path)
}

func hpatchAttemptTextBytes(attempt hpatchAttemptMetrics) int {
	bytes := len(attempt.CorrelationID) + len(attempt.CallID) + len(attempt.Outcome)
	for _, rejection := range attempt.Rejections {
		bytes += hpatchRejectionTextBytes(rejection)
	}
	return bytes
}

func boundedHPatchAttempts(attempts []hpatchAttemptMetrics) []hpatchAttemptMetrics {
	start := max(len(attempts)-maxSessionHPatchAttempts, 0)
	retainedBytes := 0
	for index := len(attempts) - 1; index >= start; index-- {
		retainedBytes += hpatchAttemptTextBytes(attempts[index])
		if retainedBytes > maxSessionHPatchAttemptTextBytes {
			start = index + 1
			break
		}
	}
	if start == 0 {
		return attempts
	}
	return slices.Clone(attempts[start:])
}

func boundedHPatchRejections(rejections []hpatch.HostRejection) []hpatch.HostRejection {
	start := max(len(rejections)-maxSessionHPatchRejections, 0)
	retainedBytes := 0
	for index := len(rejections) - 1; index >= start; index-- {
		retainedBytes += hpatchRejectionTextBytes(rejections[index])
		if retainedBytes > maxSessionHPatchRejectionTextBytes {
			start = index + 1
			break
		}
	}
	if start == 0 {
		return rejections
	}
	return slices.Clone(rejections[start:])
}

type hpatchCallMetrics struct {
	Successful            uint64 `json:"successful"`
	Rejected              uint64 `json:"rejected"`
	DiagnosticInputTokens uint64 `json:"diagnostic_input_tokens"`
}

type commentaryMetric struct {
	Count        uint64 `json:"count"`
	NativeTokens uint64 `json:"native_tokens"`
	FormTokens   uint64 `json:"form_tokens"`
}

type commentaryMetrics struct {
	Explicit   commentaryMetric `json:"explicit"`
	Default    commentaryMetric `json:"default"`
	Failure    commentaryMetric `json:"failure"`
	Cancelled  commentaryMetric `json:"cancelled"`
	Timeout    commentaryMetric `json:"timeout"`
	Suppressed commentaryMetric `json:"suppressed"`
}

func (m *commentaryMetrics) metric(kind string) *commentaryMetric {
	switch kind {
	case "explicit":
		return &m.Explicit
	case "default":
		return &m.Default
	case "failure":
		return &m.Failure
	case "cancelled":
		return &m.Cancelled
	case "timeout":
		return &m.Timeout
	case "suppressed":
		return &m.Suppressed
	default:
		return nil
	}
}

func (m *commentaryMetrics) add(kind string, nativeTokens, formTokens uint64) {
	metric := m.metric(kind)
	if metric == nil {
		return
	}
	metric.Count++
	metric.NativeTokens += nativeTokens
	metric.FormTokens += formTokens
}

func (m *hpatchCallMetrics) add(record hpatchMetricRecord) {
	if record.HPatchTokens != 0 {
		m.Successful++
	}
	if record.IneffectiveHPatchTokens != 0 {
		m.Rejected++
	}
	m.DiagnosticInputTokens += record.DiagnosticInputTokens
}

type metricsSnapshot struct {
	metricGroup

	Requests    requestLifecycleMetrics `json:"requests"`
	HPatchCalls hpatchCallMetrics       `json:"hpatch_calls"`
	CTP         ctpCompressionMetrics   `json:"ctp"`
	Commentary  commentaryMetrics       `json:"commentary"`
	Sessions    []sessionMetrics        `json:"sessions"`
	Gain        hpatch.GainMetrics      `json:"gain"`
	GainError   string                  `json:"gain_error,omitempty"`
	Mode        string                  `json:"mode,omitempty"`
}

type metricsStore struct {
	mu               sync.RWMutex
	all              metricGroup
	requests         requestLifecycleMetrics
	hpatchCalls      hpatchCallMetrics
	ctp              ctpCompressionMetrics
	commentary       commentaryMetrics
	commentaryTokens tokenizer.Codec
	retainedSessions map[string]retainedSessionMetrics
	activeSessions   map[string]map[uint64]activeRequest

	requestSequence uint64
	sessionUsed     map[string]uint64
	subscribers     map[uint64]chan struct{}
	subscriberSeq   uint64
	gainDirectory   string
	titles          *sessionTitleCache
	mode            string
}

type retainedSessionMetrics struct {
	metricGroup

	requests                   requestLifecycleMetrics
	requestObservations        []requestMetricObservation
	requestObservationsDropped uint64
	hpatchCalls                hpatchCallMetrics
	hpatchAttempts             []hpatchAttemptMetrics
	hpatchRejections           []hpatch.HostRejection
	hpatchSequence             uint64
	ctp                        ctpSessionMetrics
	commentary                 commentaryMetrics
	model                      string
	modelOrder                 uint64
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

func newMetricsStore(gainDirectory string, titleCaches ...*sessionTitleCache) *metricsStore {
	titles := newSessionTitleCache()
	if len(titleCaches) != 0 && titleCaches[0] != nil {
		titles = titleCaches[0]
	}
	tokens, _ := tokenizer.ForModel(tokenizer.GPT5)
	return &metricsStore{
		all:              newMetricGroup(),
		retainedSessions: map[string]retainedSessionMetrics{},
		activeSessions:   map[string]map[uint64]activeRequest{},
		sessionUsed:      map[string]uint64{},
		subscribers:      map[uint64]chan struct{}{},
		gainDirectory:    gainDirectory,
		titles:           titles,
		commentaryTokens: tokens,
	}
}

func (m *metricsStore) countCommentaryTokens(value string) uint64 {
	if m == nil || m.commentaryTokens == nil || value == "" {
		return 0
	}
	count, err := m.commentaryTokens.Count(value)
	if err != nil || count < 0 {
		return 0
	}
	return uint64(count)
}

func (m *metricsStore) recordCommentary(sessionID, kind, visibleText, form string, formTokensOverride *uint64) {
	if m == nil {
		return
	}
	nativeTokens := m.countCommentaryTokens(visibleText)
	formTokens := m.countCommentaryTokens(form)
	if formTokensOverride != nil {
		formTokens = *formTokensOverride
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commentary.add(kind, nativeTokens, formTokens)
	if validMetricSessionID(sessionID) {
		retained := m.retainedSessionLocked(sessionID)
		retained.commentary.add(kind, nativeTokens, formTokens)
		m.retainedSessions[sessionID] = retained
	}
	m.notifyLocked()
}

func (m *metricsStore) recordHPatch(record hpatchMetricRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hpatchCalls.add(record)
	if validMetricSessionID(record.SessionID) {
		retained := m.retainedSessionLocked(record.SessionID)
		retained.hpatchCalls.add(record)
		if attempt, ok := hpatchAttemptMetricsOf(record); ok {
			retained.hpatchSequence++
			attempt.Sequence = retained.hpatchSequence
			retained.hpatchAttempts = append(retained.hpatchAttempts, attempt)
			retained.hpatchAttempts = boundedHPatchAttempts(retained.hpatchAttempts)
		}
		retained.hpatchRejections = append(retained.hpatchRejections, record.Rejections...)
		retained.hpatchRejections = boundedHPatchRejections(retained.hpatchRejections)
		m.retainedSessions[record.SessionID] = retained
	}
	m.notifyLocked()
}

func ctpRequestMetricName(decision ctp2RequestDecision) string {
	switch decision {
	case ctp2RequestMissingCarrier:
		return "missing_instruction_carrier"
	case ctp2RequestActive:
		return "active"
	default:
		return "disabled"
	}
}

func (c *ctpCompressionMetrics) recordRequest(
	decision ctp2RequestDecision,
	representation ctpRepresentationMetrics,
	details ctp2RepresentationMetrics,
	encodeDuration time.Duration,
) {
	c.ConsideredRequests++
	c.Codec.EncodeOperations++
	c.Codec.EncodeNanoseconds += uint64(max(encodeDuration, 0))
	switch decision {
	case ctp2RequestMissingCarrier:
		c.MissingCarrier++
	case ctp2RequestActive:
		c.ActiveRequests++
		c.Input.NativeTokens += representation.NativeTokens
		c.Input.CompactTokens += representation.CompactTokens
		c.InputBytes.NativeBytes += representation.NativeBytes
		c.InputBytes.CompactBytes += representation.CompactBytes
		c.RequestDictionary.Definitions += details.Definitions
		c.RequestDictionary.Bytes += details.DictionaryBytes
		c.RequestStrings += details.Strings
		c.RequestVisibleReferences += details.VisibleReferences
	}
}

func (c *ctpCompressionMetrics) recordOutput(
	representation ctpRepresentationMetrics,
	details ctp2RepresentationMetrics,
) {
	c.AssistantTexts++
	c.Output.NativeTokens += representation.NativeTokens
	c.Output.CompactTokens += representation.CompactTokens
	c.OutputBytes.NativeBytes += representation.NativeBytes
	c.OutputBytes.CompactBytes += representation.CompactBytes
	c.ResponseDictionary.Definitions += details.Definitions
	c.ResponseDictionary.Bytes += details.DictionaryBytes
	c.ResponseStrings += details.Strings
	c.ResponseVisibleReferences += details.VisibleReferences
}

func (c *ctpCompressionMetrics) recordDecode(duration time.Duration, failed bool) {
	c.Codec.DecodeOperations++
	c.Codec.DecodeNanoseconds += uint64(max(duration, 0))
	if failed {
		c.Codec.DecodeFailures++
	}
}

func appendLatestMetricObservation[T any](observations []T, observation T, limit int) ([]T, bool) {
	if len(observations) < limit {
		return append(observations, observation), false
	}
	copy(observations, observations[1:])
	observations[len(observations)-1] = observation
	return observations, true
}

func (m *metricsStore) recordCTPRequest(
	sessionID string,
	requestSequence uint64,
	decision ctp2RequestDecision,
	representation ctpRepresentationMetrics,
	details ctp2RepresentationMetrics,
	encodeDuration time.Duration,
) {
	if m == nil || decision == ctp2RequestDisabled {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctp.recordRequest(decision, representation, details, encodeDuration)
	if validMetricSessionID(sessionID) {
		retained := m.retainedSessionLocked(sessionID)
		retained.ctp.recordRequest(decision, representation, details, encodeDuration)
		observation := ctpInputObservation{
			ctpRepresentationMetrics: representation,
			RequestSequence:          requestSequence,
			Decision:                 ctpRequestMetricName(decision),
			Definitions:              details.Definitions,
			DictionaryBytes:          details.DictionaryBytes,
			Strings:                  details.Strings,
			VisibleReferences:        details.VisibleReferences,
			EncodeNanoseconds:        uint64(max(encodeDuration, 0)),
		}
		var dropped bool
		retained.ctp.InputObservations, dropped = appendLatestMetricObservation(
			retained.ctp.InputObservations, observation, maxSessionCTPObservations,
		)
		if dropped {
			retained.ctp.InputObservationsDropped++
		}
		m.retainedSessions[sessionID] = retained
	}
	m.notifyLocked()
}

func (m *metricsStore) recordCTPOutput(
	sessionID string,
	requestSequence uint64,
	representation ctpRepresentationMetrics,
	details ctp2RepresentationMetrics,
) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctp.recordOutput(representation, details)
	if validMetricSessionID(sessionID) {
		retained := m.retainedSessionLocked(sessionID)
		retained.ctp.recordOutput(representation, details)
		observation := ctpOutputObservation{
			ctpRepresentationMetrics: representation,
			RequestSequence:          requestSequence,
			Definitions:              details.Definitions,
			DictionaryBytes:          details.DictionaryBytes,
			Strings:                  details.Strings,
			VisibleReferences:        details.VisibleReferences,
		}
		var dropped bool
		retained.ctp.OutputObservations, dropped = appendLatestMetricObservation(
			retained.ctp.OutputObservations, observation, maxSessionCTPObservations,
		)
		if dropped {
			retained.ctp.OutputObservationsDropped++
		}
		m.retainedSessions[sessionID] = retained
	}
	m.notifyLocked()
}

func (m *metricsStore) recordCTPDecode(sessionID string, duration time.Duration, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctp.recordDecode(duration, failed)
	if validMetricSessionID(sessionID) {
		retained := m.retainedSessionLocked(sessionID)
		retained.ctp.recordDecode(duration, failed)
		m.retainedSessions[sessionID] = retained
	}
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
	return strings.TrimSpace(sessionID) != ""
}

func (h *activeRequestHandle) metricIdentity() (string, uint64) {
	if h == nil {
		return "", 0
	}
	return h.sessionID, h.requestID
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
		requestMetric := requestMetricObservation{
			Sequence:                     h.requestID,
			Outcome:                      observation.outcome.String(),
			TotalDurationMilliseconds:    durationMilliseconds(observation.totalDuration),
			UpstreamDurationMilliseconds: durationMilliseconds(observation.upstreamDuration),
			Usage:                        observation.usageCounts,
			UsageObserved:                observation.usageObserved,
		}
		var dropped bool
		retained.requestObservations, dropped = appendLatestMetricObservation(
			retained.requestObservations, requestMetric, maxSessionRequestObservations,
		)
		if dropped {
			retained.requestObservationsDropped++
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
			RequestObservations:        slices.Clone(retained.requestObservations),
			RequestObservationsDropped: retained.requestObservationsDropped,
			HPatchCalls:                retained.hpatchCalls, HPatchAttempts: cloneHPatchAttempts(retained.hpatchAttempts),
			HPatchRejections: slices.Clone(retained.hpatchRejections),
			CTP:              cloneCTPSessionMetrics(retained.ctp),
			Commentary:       retained.commentary,
			metricGroup:      cloneMetricGroup(retained.metricGroup),
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
		HPatchCalls: m.hpatchCalls,
		CTP:         m.ctp,
		Commentary:  m.commentary,
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
	for index := range snapshot.Sessions {
		snapshot.Sessions[index].Title = m.titles.title(snapshot.Sessions[index].SessionID)
	}

	// Durable title and gain lookups use their own synchronization; keep that I/O
	// off the in-memory telemetry mutex so request lifecycle writers are not blocked.
	snapshot.Gain, snapshot.GainError = loadGainMetrics(gainDirectory)
	return snapshot
}

func cloneCTPSessionMetrics(metrics ctpSessionMetrics) ctpSessionMetrics {
	metrics.InputObservations = slices.Clone(metrics.InputObservations)
	metrics.OutputObservations = slices.Clone(metrics.OutputObservations)
	return metrics
}

func cloneHPatchAttempts(attempts []hpatchAttemptMetrics) []hpatchAttemptMetrics {
	cloned := slices.Clone(attempts)
	for index := range cloned {
		cloned[index].Rejections = slices.Clone(cloned[index].Rejections)
	}
	return cloned
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
