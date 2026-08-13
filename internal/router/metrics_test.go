package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
		{
			name:   "stream failure",
			body:   `{"type":"response.failed","response":{"usage":{"input_tokens":13,"output_tokens":2}}}`,
			stream: true, want: tokenCounts{InputTokens: 13, UncachedInputTokens: 13, OutputTokens: 2}, ok: true,
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

func finishMetricsTestRequest(store *metricsStore, sessionID, model string, counts tokenCounts) {
	handle := store.beginRequest(sessionID, model)
	handle.finish(requestObservation{
		outcome:       requestOutcomeCompleted,
		usageCounts:   counts,
		usageObserved: counts != (tokenCounts{}),
	})
}

func TestMetricsSnapshotUsesCachedSessionTitle(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "session_index.jsonl")
	if err := os.WriteFile(indexPath, []byte(`{"id":"session","thread_name":"Named task"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore("", newSessionTitleCacheAt(indexPath))
	finishMetricsTestRequest(store, "session", "model", tokenCounts{InputTokens: 1})
	snapshot := store.snapshot()
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Title != "Named task" {
		t.Fatalf("sessions = %#v", snapshot.Sessions)
	}
}

func TestMetricsStoreSnapshotAndAPI(t *testing.T) {
	store := newMetricsStore("")
	finishMetricsTestRequest(store, "session-b", "model-b", tokenCounts{InputTokens: 4, UncachedInputTokens: 3, OutputTokens: 2, ReasoningTokens: 1})
	finishMetricsTestRequest(store, "session-a", "model-a", tokenCounts{InputTokens: 10, UncachedInputTokens: 8, OutputTokens: 6, ReasoningTokens: 2})
	finishMetricsTestRequest(store, "", "model-a", tokenCounts{InputTokens: 1})
	activeB := store.beginRequest("session-b", "model-b")
	activeAOld := store.beginRequest("session-a", "model-old")
	activeANew := store.beginRequest("session-a", "model-a")
	defer activeB.finish(requestObservation{outcome: requestOutcomeCompleted})
	defer activeAOld.finish(requestObservation{outcome: requestOutcomeCompleted})
	defer activeANew.finish(requestObservation{outcome: requestOutcomeCompleted})

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
	if decoded.Requests != snapshot.Requests {
		t.Fatalf("decoded lifecycle = %#v, want %#v", decoded.Requests, snapshot.Requests)
	}
	if len(decoded.Sessions) != len(snapshot.Sessions) || decoded.Sessions[0].Requests != snapshot.Sessions[0].Requests {
		t.Fatalf("decoded session lifecycle = %#v, want %#v", decoded.Sessions, snapshot.Sessions)
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
	handle.finish(requestObservation{outcome: requestOutcomeCompleted})
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
	defer handle.finish(requestObservation{outcome: requestOutcomeCompleted})
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
	invalid := store.beginRequest("", "model")
	if invalid == nil {
		t.Fatal("unidentified request was not tracked globally")
	}
	invalid.finish(requestObservation{outcome: requestOutcomeFailed})

	handle := store.beginRequest("session", "")
	active := store.snapshot()
	if len(active.Sessions) != 1 || active.Sessions[0].Model != "unknown" {
		t.Fatalf("active = %#v", active.Sessions)
	}
	if active.Sessions[0].Requests.Started != 1 || active.Sessions[0].Requests.Active != 1 {
		t.Fatalf("active lifecycle = %#v", active.Sessions[0].Requests)
	}
	handle.finish(requestObservation{outcome: requestOutcomeCompleted})
	handle.finish(requestObservation{outcome: requestOutcomeFailed})

	snapshot := store.snapshot()
	if snapshot.Requests.Started != 2 || snapshot.Requests.Active != 0 || snapshot.Requests.Completed != 1 || snapshot.Requests.Failed != 1 {
		t.Fatalf("global lifecycle = %#v", snapshot.Requests)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Requests.Started != 1 || snapshot.Sessions[0].Requests.Completed != 1 {
		t.Fatalf("retained lifecycle = %#v", snapshot.Sessions)
	}
}

func TestMetricsStorePreservesLatestStartedModel(t *testing.T) {
	store := newMetricsStore("")
	oldRequest := store.beginRequest("session", "model-old")
	newRequest := store.beginRequest("session", "model-new")
	newRequest.finish(requestObservation{outcome: requestOutcomeFailed})
	if got := store.snapshot().Sessions[0].Model; got != "model-new" {
		t.Fatalf("model while older request remains active = %q, want model-new", got)
	}
	oldRequest.finish(requestObservation{outcome: requestOutcomeFailed})
	snapshot := store.snapshot()
	if got := snapshot.Sessions[0].Model; got != "model-new" {
		t.Fatalf("retained model = %q, want model-new", got)
	}
	if snapshot.Sessions[0].Requests.Started != 2 || snapshot.Sessions[0].Requests.Failed != 2 {
		t.Fatalf("session lifecycle = %#v", snapshot.Sessions[0].Requests)
	}
}

func TestMetricsStoreRetainsCompletedSession(t *testing.T) {
	store := newMetricsStore("")
	handle := store.beginRequest("session", "gpt-5.6-sol")
	handle.finish(requestObservation{outcome: requestOutcomeCompleted, usageCounts: tokenCounts{InputTokens: 12}, usageObserved: true})

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
	finishMetricsTestRequest(store, " \t ", "model", tokenCounts{InputTokens: 1})

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
	finishMetricsTestRequest(store, "", "gpt-5.6-sol", tokenCounts{InputTokens: 11})
	finishMetricsTestRequest(store, "", "gpt-5.6-sol", tokenCounts{InputTokens: 7})
	finishMetricsTestRequest(store, "", "future-model", tokenCounts{InputTokens: 2})
	finishMetricsTestRequest(store, "", "", tokenCounts{InputTokens: 1})
	finishMetricsTestRequest(store, "", "ignored", tokenCounts{})

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
		finishMetricsTestRequest(store, fmt.Sprintf("session-%03d", index), "model", tokenCounts{InputTokens: 1})
	}
	if got := len(store.retainedSessions); got != maxSessionHistories {
		t.Fatalf("session totals = %d", got)
	}
	if _, exists := store.retainedSessions["session-000"]; exists {
		t.Fatal("oldest retained session was not evicted")
	}

	for index := range maxMetricModels + 2 {
		finishMetricsTestRequest(store, "bounded", fmt.Sprintf("model-%03d", index), tokenCounts{InputTokens: 1})
	}
	if got := len(store.all.ByModel); got != maxMetricModels+1 {
		t.Fatalf("model dimensions = %d", got)
	}
	if got := store.all.ByModel[otherMetricModelKey].InputTokens; got != 3 {
		t.Fatalf("other model tokens = %d", got)
	}
}

func TestMetricsStoreBoundsLifecycleOnlySessions(t *testing.T) {
	store := newMetricsStore("")
	for index := range maxSessionHistories + 1 {
		handle := store.beginRequest(fmt.Sprintf("lifecycle-%03d", index), "model")
		handle.finish(requestObservation{
			outcome:          requestOutcomeFailed,
			totalDuration:    2 * time.Millisecond,
			upstreamDuration: time.Millisecond,
		})
	}
	if got := len(store.retainedSessions); got != maxSessionHistories {
		t.Fatalf("retained lifecycle sessions = %d, want %d", got, maxSessionHistories)
	}
	if _, exists := store.retainedSessions["lifecycle-000"]; exists {
		t.Fatal("oldest lifecycle-only session was not evicted")
	}
	snapshot := store.snapshot()
	if snapshot.Requests.Started != maxSessionHistories+1 ||
		snapshot.Requests.Failed != maxSessionHistories+1 ||
		snapshot.Requests.Active != 0 {
		t.Fatalf("global lifecycle = %#v", snapshot.Requests)
	}
	if len(snapshot.Sessions) != maxSessionHistories {
		t.Fatalf("snapshot sessions = %d, want %d", len(snapshot.Sessions), maxSessionHistories)
	}
	for _, session := range snapshot.Sessions {
		if session.Requests.Started != 1 || session.Requests.Failed != 1 || session.Requests.Active != 0 {
			t.Fatalf("session lifecycle = %#v", session)
		}
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
		ToolMetrics: []hpatch.ToolMetricRecord{{
			PluginID: "builtin.hpatch", ToolName: "hpatch", Calls: 1,
			EmittedTokens: 20, TranslatedTokens: 50,
			Executions: 1, CurrentInputTokens: 12, StockInputTokens: 8,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	store := newMetricsStore(dataDirectory)
	snapshot := store.snapshot()
	if snapshot.Gain.HPatchTokens != 20 || snapshot.Gain.ApplyPatchTokens != 50 || snapshot.Gain.SuccessfulReduction != "60.0" {
		t.Fatalf("gain snapshot = %#v", snapshot.Gain)
	}
	if len(snapshot.Gain.Tools) != 1 || snapshot.Gain.Tools[0].ToolName != "hpatch" || snapshot.Gain.AllTools.TranslatedTokens != 50 {
		t.Fatalf("per-tool gain snapshot = %#v", snapshot.Gain)
	}
	if len(snapshot.Gain.ToolInputs) != 1 || snapshot.Gain.ToolInputs[0].CurrentTokens != 12 ||
		snapshot.Gain.AllToolInputs.StockTokens != 8 || snapshot.Gain.NetAddedInput != "4" {
		t.Fatalf("per-tool input snapshot = %#v", snapshot.Gain)
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
	if err := translator.RecordMetrics(t.Context(), hpatchMetricRecord{HostMetricRecord: hpatch.HostMetricRecord{
		HPatchTokens:     4,
		ApplyPatchTokens: 8,
	}}); err != nil {
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

func TestNotifyingTranslatorAttributesHPatchCallsToSession(t *testing.T) {
	dataDirectory := t.TempDir()
	store := newMetricsStore(dataDirectory)
	translator := notifyingHPatchTranslator{
		inner:   newInProcessHPatchTranslator(dataDirectory),
		metrics: store,
	}
	for _, record := range []hpatchMetricRecord{
		{
			HostMetricRecord: hpatch.HostMetricRecord{
				Attempt:   hpatch.AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call-1", Attempt: 1},
				SessionID: "session", IneffectiveHPatchTokens: 5, ConfirmedAliasRewrite: true,
				FailedApplyPatchTokens: 2, DiagnosticInputTokens: 3,
				Rejections: []hpatch.HostRejection{{
					Command: 2, SourceLine: 3, Operation: "type", Target: "line",
					TargetAliasRelation: hpatch.TargetAliasRelationContained,
					Reason:              "language-syntax", Path: "file.go", GeneratedLine: 8, GeneratedColumn: 3, ValueLine: 2,
				}},
			},
		},
		{
			HostMetricRecord: hpatch.HostMetricRecord{
				Attempt:   hpatch.AttemptMetadata{SessionID: "session", CorrelationID: "chain", CallID: "call-2", Attempt: 2, Correction: true},
				SessionID: "session", HPatchTokens: 4, ApplyPatchTokens: 8,
			},
		},
	} {
		if err := translator.RecordMetrics(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	snapshot := store.snapshot()
	want := hpatchCallMetrics{Successful: 1, Rejected: 1, DiagnosticInputTokens: 3}
	if snapshot.HPatchCalls != want {
		t.Fatalf("aggregate hpatch calls = %+v, want %+v", snapshot.HPatchCalls, want)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].SessionID != "session" || snapshot.Sessions[0].HPatchCalls != want {
		t.Fatalf("session hpatch calls = %+v, want session metrics %+v", snapshot.Sessions, want)
	}
	wantRejection := hpatch.HostRejection{
		Command: 2, SourceLine: 3, Operation: "type", Target: "line",
		TargetAliasRelation: hpatch.TargetAliasRelationContained,
		Reason:              "language-syntax", Path: "file.go", GeneratedLine: 8, GeneratedColumn: 3, ValueLine: 2,
	}
	if got := snapshot.Sessions[0].HPatchRejections; len(got) != 1 || got[0] != wantRejection {
		t.Fatalf("session hpatch rejections = %+v, want %+v", got, wantRejection)
	}
	wantAttempts := []hpatchAttemptMetrics{
		{Sequence: 1, CorrelationID: "chain", CallID: "call-1", Attempt: 1, Outcome: "rejected", EmittedHPatchTokens: 5, ApplyPatchTokens: 2, DiagnosticInputTokens: 3, ConfirmedAliasRewrite: true, Rejections: []hpatch.HostRejection{wantRejection}},
		{Sequence: 2, CorrelationID: "chain", CallID: "call-2", Attempt: 2, Correction: true, Outcome: "successful", EmittedHPatchTokens: 4, ApplyPatchTokens: 8},
	}
	if got := snapshot.Sessions[0].HPatchAttempts; !reflect.DeepEqual(got, wantAttempts) {
		t.Fatalf("session hpatch attempts = %+v, want %+v", got, wantAttempts)
	}
	recorder := httptest.NewRecorder()
	store.serveAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	var decoded metricsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || decoded.HPatchCalls != want || len(decoded.Sessions) != 1 || decoded.Sessions[0].HPatchCalls != want {
		t.Fatalf("metrics API hpatch calls = status %d, aggregate %+v, sessions %+v", recorder.Code, decoded.HPatchCalls, decoded.Sessions)
	}
	if got := decoded.Sessions[0].HPatchRejections; len(got) != 1 || got[0] != wantRejection {
		t.Fatalf("metrics API hpatch rejections = %+v, want %+v", got, wantRejection)
	}
	if got := decoded.Sessions[0].HPatchAttempts; !reflect.DeepEqual(got, wantAttempts) {
		t.Fatalf("metrics API hpatch attempts = %+v, want %+v", got, wantAttempts)
	}
}

func TestMetricsStoreBoundsSessionHPatchAttempts(t *testing.T) {
	store := newMetricsStore("")
	for index := range maxSessionHPatchAttempts + 1 {
		store.recordHPatch(hpatchMetricRecord{HostMetricRecord: hpatch.HostMetricRecord{
			Attempt: hpatch.AttemptMetadata{
				SessionID: "session", CorrelationID: "chain", CallID: fmt.Sprintf("call-%d", index+1), Attempt: index + 1,
			},
			SessionID: "session", IneffectiveHPatchTokens: 1,
			Rejections: []hpatch.HostRejection{{Command: index + 1, Reason: "script-syntax"}},
		}})
	}

	got := store.snapshot().Sessions[0].HPatchAttempts
	if len(got) != maxSessionHPatchAttempts || got[0].Sequence != 2 || got[len(got)-1].Sequence != maxSessionHPatchAttempts+1 {
		t.Fatalf("bounded hpatch attempts = %+v", got)
	}
	got[0].Rejections[0].Command = 999
	if next := store.snapshot().Sessions[0].HPatchAttempts[0].Rejections[0].Command; next != 2 {
		t.Fatalf("snapshot mutation changed retained attempt rejection command to %d", next)
	}
}

func TestMetricsStoreBoundsSessionHPatchRejectionEvidence(t *testing.T) {
	store := newMetricsStore("")
	rejections := make([]hpatch.HostRejection, maxSessionHPatchRejections+1)
	for index := range rejections {
		rejections[index] = hpatch.HostRejection{Command: index + 1, Reason: "script-syntax"}
	}
	store.recordHPatch(hpatchMetricRecord{HostMetricRecord: hpatch.HostMetricRecord{
		SessionID: "session", IneffectiveHPatchTokens: 1, Rejections: rejections,
	}})

	snapshot := store.snapshot()
	got := snapshot.Sessions[0].HPatchRejections
	if len(got) != maxSessionHPatchRejections || got[0].Command != 2 || got[len(got)-1].Command != maxSessionHPatchRejections+1 {
		t.Fatalf("bounded hpatch rejections = %+v", got)
	}
	got[0].Command = 999
	if next := store.snapshot().Sessions[0].HPatchRejections[0].Command; next != 2 {
		t.Fatalf("snapshot mutation changed retained rejection command to %d", next)
	}
}

func TestMetricsStoreBoundsSessionHPatchEvidenceText(t *testing.T) {
	store := newMetricsStore("")
	store.recordHPatch(hpatchMetricRecord{HostMetricRecord: hpatch.HostMetricRecord{
		Attempt: hpatch.AttemptMetadata{
			SessionID: "session", CorrelationID: "chain", CallID: "call-1", Attempt: 1,
		},
		SessionID: "session", IneffectiveHPatchTokens: 1,
		Rejections: []hpatch.HostRejection{{Reason: "path-resolution", Path: strings.Repeat("x", maxSessionHPatchAttemptTextBytes+1)}},
	}})

	snapshot := store.snapshot()
	if got := snapshot.Sessions[0].HPatchAttempts; len(got) != 0 {
		t.Fatalf("oversized attempt evidence was retained: %+v", got)
	}
	if got := snapshot.Sessions[0].HPatchRejections; len(got) != 0 {
		t.Fatalf("oversized rejection evidence was retained: %+v", got)
	}

	store.recordHPatch(hpatchMetricRecord{HostMetricRecord: hpatch.HostMetricRecord{
		Attempt: hpatch.AttemptMetadata{
			SessionID: "session", CorrelationID: "chain", CallID: "call-2", Attempt: 2,
		},
		SessionID: "session", IneffectiveHPatchTokens: 1,
		Rejections: []hpatch.HostRejection{{Reason: "script-syntax"}},
	}})
	snapshot = store.snapshot()
	if got := snapshot.Sessions[0].HPatchAttempts; len(got) != 1 || got[0].Sequence != 2 {
		t.Fatalf("bounded attempt evidence did not retain the later small record: %+v", got)
	}
	if got := snapshot.Sessions[0].HPatchRejections; len(got) != 1 || got[0].Reason != "script-syntax" {
		t.Fatalf("bounded rejection evidence did not retain the later small record: %+v", got)
	}
	if got := snapshot.Sessions[0].HPatchCalls.Rejected; got != 2 {
		t.Fatalf("retention changed the durable rejected-call count to %d", got)
	}
}

func TestRoutedEvaluatorRejectionReachesSessionEvidence(t *testing.T) {
	store := newMetricsStore("")
	translator := notifyingHPatchTranslator{
		inner:   newInProcessHPatchTranslator(t.TempDir()),
		metrics: store,
	}
	transform, _, _, _ := newHPatchTestTransform(t, translator)
	history, err := transform.translate("call-evaluator", "bad\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if history.translationError == "" {
		t.Fatal("evaluator rejection produced no client diagnostic")
	}

	snapshot := store.snapshot()
	if snapshot.HPatchCalls.Rejected != 1 || len(snapshot.Sessions) != 1 {
		t.Fatalf("evaluator rejection metrics = %+v", snapshot)
	}
	want := hpatch.HostRejection{
		Command: 1, SourceLine: 1, Operation: "bad", Reason: "script-syntax",
	}
	if got := snapshot.Sessions[0].HPatchRejections; len(got) != 1 || got[0] != want {
		t.Fatalf("routed evaluator rejection = %+v, want %+v", got, want)
	}
	if got := snapshot.Sessions[0].HPatchAttempts; len(got) != 1 || got[0].Outcome != "rejected" || got[0].EvaluatedCommands != 0 || got[0].Correction {
		t.Fatalf("routed evaluator attempt = %+v", got)
	}

	proxyAttempt := hpatch.AttemptMetadata{
		SessionID: transform.sessionID, CorrelationID: "call-evaluator", CallID: "call-proxy", Attempt: 2, Correction: true,
	}
	if _, err := transform.rejectUnevaluated(hpatchRecoveryToolName, "call-proxy", `type 1:ffff \"bad\"`, fmt.Errorf("proxy rejection"), proxyAttempt, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	snapshot = store.snapshot()
	if snapshot.HPatchCalls.Rejected != 2 {
		t.Fatalf("all routed rejections = %d, want 2", snapshot.HPatchCalls.Rejected)
	}
	if got := snapshot.Sessions[0].HPatchRejections; len(got) != 1 || got[0] != want {
		t.Fatalf("proxy rejection fabricated evaluator evidence: %+v", got)
	}
	if got := snapshot.Sessions[0].HPatchAttempts; len(got) != 2 || got[1].Outcome != "rejected" || got[1].EvaluatedCommands != 0 || !got[1].Correction {
		t.Fatalf("proxy rejection attempt = %+v", got)
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
		handle.finish(requestObservation{outcome: requestOutcomeCompleted})
	}
	if store.requests.Active != 0 || len(store.activeSessions) != 0 {
		t.Fatalf("active telemetry leaked: requests %d, sessions %d", store.requests.Active, len(store.activeSessions))
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
