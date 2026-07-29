package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yusing/hpatch"
)

func TestUsageFromResponsePayload(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		stream bool
		want   tokenCounts
		ok     bool
	}{
		{
			name: "JSON response",
			body: `{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":35},"output_tokens":24,"output_tokens_details":{"reasoning_tokens":9}}}`,
			want: tokenCounts{InputTokens: 100, UncachedInputTokens: 65, OutputTokens: 24, ReasoningTokens: 9}, ok: true,
		},
		{
			name:   "stream completion",
			body:   `{"type":"response.completed","response":{"usage":{"input_tokens":80,"input_tokens_details":{"cached_tokens":120},"output_tokens":12,"output_tokens_details":{"reasoning_tokens":5}}}}`,
			stream: true, want: tokenCounts{InputTokens: 80, OutputTokens: 12, ReasoningTokens: 5}, ok: true,
		},
		{name: "nonterminal stream event", body: `{"type":"response.output_text.delta","usage":{"input_tokens":10}}`, stream: true},
		{name: "malformed", body: `{"usage":`},
		{name: "unrelated usage collision", body: `{"future":{"usage":{"input_tokens":10}}}`},
		{name: "future usage fields", body: `{"usage":{"input_tokens":7,"output_tokens":3,"future":true}}`, want: tokenCounts{InputTokens: 7, UncachedInputTokens: 7, OutputTokens: 3}, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := usageFromResponsePayload([]byte(test.body), test.stream)
			if ok != test.ok || got != test.want {
				t.Fatalf("got (%#v, %v), want (%#v, %v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestMetricsStoreSnapshotAndAPI(t *testing.T) {
	store := newMetricsStore("")
	store.record("session-b", "model-b", tokenCounts{InputTokens: 4, UncachedInputTokens: 3, OutputTokens: 2, ReasoningTokens: 1})
	store.record("session-a", "model-a", tokenCounts{InputTokens: 10, UncachedInputTokens: 8, OutputTokens: 6, ReasoningTokens: 2})
	store.record("", "model-a", tokenCounts{InputTokens: 1})
	activeB := store.beginRequest("session-b", "model-b")
	activeAOld := store.beginRequest("session-a", "model-old")
	activeANew := store.beginRequest("session-a", "model-a")
	defer activeB.end()
	defer activeAOld.end()
	defer activeANew.end()

	snapshot := store.snapshot()
	if snapshot.Total.InputTokens != 15 || len(snapshot.Sessions) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Sessions[0].SessionID != "session-a" || snapshot.Sessions[0].Model != "model-a" {
		t.Fatalf("active session = %#v", snapshot.Sessions[0])
	}
	if snapshot.ByModel["model-a"].InputTokens != 11 || snapshot.ByModel["model-b"].OutputTokens != 2 {
		t.Fatalf("breakdowns = %#v", snapshot)
	}
	if snapshot.GainError != "" || len(snapshot.Gain.Commands) == 0 || snapshot.Gain.SuccessfulReduction != "0.0" {
		t.Fatalf("empty gain = %#v error %q", snapshot.Gain, snapshot.GainError)
	}

	recorder := httptest.NewRecorder()
	store.serveAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, %#v", recorder.Code, recorder.Header())
	}
	var decoded metricsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Total != snapshot.Total {
		t.Fatalf("decoded total = %#v, want %#v", decoded.Total, snapshot.Total)
	}
	if decoded.Gain.SuccessfulReduction != snapshot.Gain.SuccessfulReduction || len(decoded.Gain.Commands) != len(snapshot.Gain.Commands) {
		t.Fatalf("decoded gain = %#v, want %#v", decoded.Gain, snapshot.Gain)
	}
}

func TestMetricsAPIStreamsInitialAndChangedSnapshots(t *testing.T) {
	store := newMetricsStore("")
	updates, unsubscribe, ok := store.subscribe()
	if !ok {
		t.Fatal("metrics subscription was rejected")
	}
	handle := store.beginRequest("live", "gpt-5.6-sol")
	select {
	case <-updates:
	default:
		t.Fatal("request start did not publish an update")
	}
	handle.end()
	select {
	case <-updates:
	default:
		t.Fatal("coalesced current-state update is missing")
	}
	unsubscribe()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/metrics", nil).WithContext(ctx)
	request.Header.Set("Accept", "application/json, text/event-stream; charset=utf-8")
	recorder := httptest.NewRecorder()
	store.serveAPI(recorder, request)
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("content type = %q", contentType)
	}
	if body := recorder.Body.String(); !strings.HasPrefix(body, "data: {") || !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("event body = %q", body)
	}
	if len(store.subscribers) != 0 {
		t.Fatalf("subscribers after cancellation = %d", len(store.subscribers))
	}
}

func TestMetricsAPIRejectsUnrelatedAcceptCollision(t *testing.T) {
	store := newMetricsStore("")
	request := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	request.Header.Set("Accept", "application/x-text/event-stream-future")
	recorder := httptest.NewRecorder()
	store.serveAPI(recorder, request)
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestMetricsSnapshotInitializesActiveSessionBreakdown(t *testing.T) {
	store := newMetricsStore("")
	handle := store.beginRequest("active", "model-a")
	if handle == nil {
		t.Fatal("active request was not tracked")
	}
	defer handle.end()
	snapshot := store.snapshot()
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snapshot.Sessions))
	}
	if snapshot.Sessions[0].ByModel == nil {
		t.Fatal("active session by_model is nil")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"by_model":null`) {
		t.Fatalf("active snapshot violates dashboard schema: %s", payload)
	}
}

func TestMetricsStoreActiveRequestLifecycle(t *testing.T) {
	store := newMetricsStore("")
	if handle := store.beginRequest("", "model"); handle != nil {
		t.Fatal("unidentified request became active")
	}
	if handle := store.beginRequest(string(make([]byte, maxSessionIDBytes+1)), "model"); handle != nil {
		t.Fatal("oversized session became active")
	}

	handle := store.beginRequest("session", "")
	active := store.snapshot().Sessions
	if len(active) != 1 || active[0].Model != "unknown" {
		t.Fatalf("active = %#v", active)
	}
	handle.end()
	handle.end()
	if active := store.snapshot().Sessions; len(active) != 0 {
		t.Fatalf("ghost sessions = %#v", active)
	}
}

func TestMetricsStoreRetainsCompletedSession(t *testing.T) {
	store := newMetricsStore("")
	handle := store.beginRequest("session", "gpt-5.6-sol")
	store.record("session", "gpt-5.6-sol", tokenCounts{InputTokens: 12})
	handle.end()

	sessions := store.snapshot().Sessions
	if len(sessions) != 1 {
		t.Fatalf("completed sessions = %#v", sessions)
	}
	if sessions[0].SessionID != "session" || sessions[0].Model != "gpt-5.6-sol" || sessions[0].Total.InputTokens != 12 {
		t.Fatalf("completed session = %#v", sessions[0])
	}
}

func TestMetricsStoreRejectsWhitespaceSession(t *testing.T) {
	store := newMetricsStore("")
	store.record(" \t ", "model", tokenCounts{InputTokens: 1})

	snapshot := store.snapshot()
	if snapshot.Total.InputTokens != 1 {
		t.Fatalf("process input tokens = %d, want 1", snapshot.Total.InputTokens)
	}
	if len(snapshot.Sessions) != 0 || len(store.retainedSessions) != 0 {
		t.Fatalf("whitespace session was retained: %#v", snapshot.Sessions)
	}
}

func TestMetricsStoreGroupsOnlyByModel(t *testing.T) {
	store := newMetricsStore("")
	store.record("", "gpt-5.6-sol", tokenCounts{InputTokens: 11})
	store.record("", "gpt-5.6-sol", tokenCounts{InputTokens: 7})
	store.record("", "future-model", tokenCounts{InputTokens: 2})
	store.record("", "", tokenCounts{InputTokens: 1})
	store.record("", "ignored", tokenCounts{})

	byModel := store.snapshot().ByModel
	if got := byModel["gpt-5.6-sol"].InputTokens; got != 18 {
		t.Fatalf("model input tokens = %d, want 18", got)
	}
	if got := byModel["future-model"].InputTokens; got != 2 {
		t.Fatalf("future model input tokens = %d, want 2", got)
	}
	if got := byModel["unknown"].InputTokens; got != 1 {
		t.Fatalf("unknown model input tokens = %d, want 1", got)
	}
	if _, exists := byModel["ignored"]; exists {
		t.Fatal("zero-count metric created a model row")
	}
}

func TestMetricsStoreBoundsCallerControlledDimensions(t *testing.T) {
	store := newMetricsStore("")
	for index := range maxSessionHistories + 1 {
		store.record(fmt.Sprintf("session-%03d", index), "model", tokenCounts{InputTokens: 1})
	}
	if got := len(store.retainedSessions); got != maxSessionHistories {
		t.Fatalf("session totals = %d", got)
	}
	if _, exists := store.retainedSessions["session-000"]; exists {
		t.Fatal("oldest retained session was not evicted")
	}

	for index := range maxMetricModels + 2 {
		store.record("bounded", fmt.Sprintf("model-%03d", index), tokenCounts{InputTokens: 1})
	}
	if got := len(store.all.ByModel); got != maxMetricModels+1 {
		t.Fatalf("model dimensions = %d", got)
	}
	if got := store.all.ByModel[otherMetricModelKey].InputTokens; got != 3 {
		t.Fatalf("other model tokens = %d", got)
	}
}

func TestParsedResponsesRequestModel(t *testing.T) {
	parsed, err := parseResponsesRequest([]byte(`{"model":" gpt-5.6-sol ","input":"task"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.model(); got != "gpt-5.6-sol" {
		t.Fatalf("model = %q", got)
	}
}

func TestMetricsEventStreamAcceptQuality(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{accept: "text/event-stream;q=0", want: false},
		{accept: "text/event-stream;q=0.5", want: true},
		{accept: "text/event-stream;q=invalid", want: false},
		{accept: "application/json, text/event-stream;q=0", want: false},
	}
	for _, test := range tests {
		headers := http.Header{"Accept": []string{test.accept}}
		if got := acceptsEventStream(headers); got != test.want {
			t.Errorf("acceptsEventStream(%q) = %v, want %v", test.accept, got, test.want)
		}
	}
}

func TestMetricsSnapshotIncludesDurableGain(t *testing.T) {
	dataDirectory := t.TempDir()
	if err := hpatch.RecordHostMetrics(t.Context(), dataDirectory, hpatch.HostMetricRecord{
		HPatchTokens:     20,
		ApplyPatchTokens: 50,
	}); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(dataDirectory)
	snapshot := store.snapshot()
	if snapshot.Gain.HPatchTokens != 20 || snapshot.Gain.ApplyPatchTokens != 50 || snapshot.Gain.SuccessfulReduction != "60.0" {
		t.Fatalf("gain snapshot = %#v", snapshot.Gain)
	}
	if snapshot.GainError != "" {
		t.Fatalf("gain error = %q", snapshot.GainError)
	}
}

func TestNotifyingTranslatorPublishesGainUpdates(t *testing.T) {
	dataDirectory := t.TempDir()
	store := newMetricsStore(dataDirectory)
	updates, unsubscribe, ok := store.subscribe()
	if !ok {
		t.Fatal("subscribe rejected")
	}
	defer unsubscribe()
	translator := notifyingHPatchTranslator{
		inner:   newInProcessHPatchTranslator(dataDirectory),
		metrics: store,
	}
	if err := translator.RecordMetrics(t.Context(), hpatch.HostMetricRecord{
		HPatchTokens:     4,
		ApplyPatchTokens: 8,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
	default:
		t.Fatal("gain record did not publish a metrics update")
	}
	snapshot := store.snapshot()
	if snapshot.Gain.HPatchTokens != 4 || snapshot.Gain.ApplyPatchTokens != 8 {
		t.Fatalf("gain after notify = %#v", snapshot.Gain)
	}
}

func TestMetricsStoreBoundsActiveRequestsAndSubscribers(t *testing.T) {
	store := newMetricsStore("")
	handles := make([]*activeRequestHandle, 0, maxActiveMetricRequests)
	for range maxActiveMetricRequests {
		handle := store.beginRequest("session", "model")
		if handle == nil {
			t.Fatal("active request limit rejected an in-range request")
		}
		handles = append(handles, handle)
	}
	if handle := store.beginRequest("overflow", "model"); handle != nil {
		t.Fatal("active request limit accepted overflow")
	}
	for _, handle := range handles {
		handle.end()
	}
	if store.activeRequests != 0 || len(store.activeSessions) != 0 {
		t.Fatalf("active telemetry leaked: requests %d, sessions %d", store.activeRequests, len(store.activeSessions))
	}

	unsubscribers := make([]func(), 0, maxMetricSubscribers)
	for range maxMetricSubscribers {
		_, unsubscribe, ok := store.subscribe()
		if !ok {
			t.Fatal("subscriber limit rejected an in-range subscriber")
		}
		unsubscribers = append(unsubscribers, unsubscribe)
	}
	if _, _, ok := store.subscribe(); ok {
		t.Fatal("subscriber limit accepted overflow")
	}
	for _, unsubscribe := range unsubscribers {
		unsubscribe()
	}
}
