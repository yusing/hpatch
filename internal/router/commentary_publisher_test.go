package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yusing/hpatch/internal/router/toolplugin"
)

type failingCommentaryWriter struct{}

func (failingCommentaryWriter) Write([]byte) (int, error) {
	return 0, errors.New("downstream unavailable")
}

func TestCommentaryPublisherAuthenticatesAndForwardsLiveEvents(t *testing.T) {
	broker := newCommentaryBroker()
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	sink := &httpShellCommentarySink{client: server.Client(), endpoint: server.URL, token: subscription.token}
	want := shellCommentaryEvent{Text: "Running check", Form: "commentary Running check"}
	if err := sink.Publish(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if err := sink.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, ready := subscription.beginForward()
	if !ready {
		t.Fatal("live publication was deferred")
	}
	var got []publishedCommentary
	if err := subscription.forward(t.Context(), first, func(event publishedCommentary) error {
		got = append(got, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].callID != "call-shell" || got[0].event != want {
		t.Fatalf("publications = %+v", got)
	}

	request := httptest.NewRequest(http.MethodPost, commentaryPublisherPath, strings.NewReader(`{"done":true}`))
	request.Header.Set("Authorization", "Bearer wrong")
	response := httptest.NewRecorder()
	broker.serveHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.Code)
	}
}

func TestCommentaryPublisherDefersLateEventsToNextTurn(t *testing.T) {
	broker := newCommentaryBroker()
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := subscription.beginForward(); ready {
		t.Fatal("empty live subscription was ready")
	}
	event := shellCommentaryEvent{Text: "Failed: Running check", Outcome: "failure", Reason: "exit status 1"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	if err := subscription.publish(t.Context(), commentaryPublication{Done: true}); err != nil {
		t.Fatal(err)
	}
	got := broker.drain("session")
	if len(got) != 1 || got[0].callID != "call-shell" || got[0].event != event {
		t.Fatalf("deferred publications = %+v", got)
	}
}

func TestCommentaryPublisherSuppressesBeyondToolOutputBudget(t *testing.T) {
	broker := newCommentaryBroker()
	metrics := newMetricsStore("")
	broker.recordSuppressed = func(sessionID string, event shellCommentaryEvent, warning string) {
		metrics.recordCommentary(sessionID, "suppressed", warning, event.Form, nil)
	}
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	subscription.publishedBytes = toolplugin.ExecutionOutputBudgetBytes - 100
	event := shellCommentaryEvent{Text: strings.Repeat("x", 101), Form: "commentary oversized"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	if err := subscription.publish(t.Context(), commentaryPublication{Done: true}); err != nil {
		t.Fatal(err)
	}
	first, ready := subscription.beginForward()
	if !ready {
		t.Fatal("suppression warning was not published")
	}
	var events []publishedCommentary
	if err := subscription.forward(t.Context(), first, func(event publishedCommentary) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].event.Outcome != "suppressed" || !strings.Contains(events[0].event.Text, "suppressed") {
		t.Fatalf("suppression events = %+v", events)
	}
	metric := metrics.snapshot().Commentary.Suppressed
	if metric.Count != 2 || metric.NativeTokens != metrics.countCommentaryTokens(events[0].event.Text) ||
		metric.FormTokens != 2*metrics.countCommentaryTokens(event.Form) {
		t.Fatalf("suppression metrics = %+v", metric)
	}
}

func TestCommentaryPublisherSharesShellOutputBudget(t *testing.T) {
	broker := newCommentaryBroker()
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	budget := newShellOutputBudget()
	sink := &httpShellCommentarySink{
		client: server.Client(), endpoint: server.URL, token: subscription.token, budget: budget,
	}
	capture := newShellOutputCapture(func() {}, budget)
	written := toolplugin.ExecutionOutputBudgetBytes - len(shellOverflowDiagnostic) - 3 - 100
	if _, err := capture.stdout.Write(bytes.Repeat([]byte{'x'}, written)); err != nil {
		t.Fatal(err)
	}
	event := shellCommentaryEvent{Text: strings.Repeat("y", 101), Form: "commentary oversized"}
	if err := sink.Publish(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := sink.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, ready := subscription.beginForward()
	if !ready {
		t.Fatal("shared-budget suppression warning was not published")
	}
	var events []publishedCommentary
	if err := subscription.forward(t.Context(), first, func(event publishedCommentary) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, overflow, truncated := capture.result()
	if overflow || truncated || len(events) != 1 || events[0].event.Outcome != "suppressed" {
		t.Fatalf("stdout bytes = %d, overflow %v, truncated %v, events = %+v", len(stdout), overflow, truncated, events)
	}
	if got := len(stdout) + len(shellCommentaryVisibleText(events[0].event)); got > toolplugin.ExecutionOutputBudgetBytes {
		t.Fatalf("combined visible bytes = %d", got)
	}
}

func TestShellOutputTruncatesAfterCommentaryWithoutChangingStatusAllowance(t *testing.T) {
	broker := newCommentaryBroker()
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	budget := newShellOutputBudget()
	sink := &httpShellCommentarySink{
		client: server.Client(), endpoint: server.URL, token: subscription.token, budget: budget,
	}
	capture := newShellOutputCapture(func() {}, budget)
	event := shellCommentaryEvent{Text: "Running verbose command", Form: "commentary Running verbose command"}
	if err := sink.Publish(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	nativeAllowance := toolplugin.ExecutionOutputBudgetBytes -
		max(len(shellOverflowDiagnostic), len(shellCommentaryTruncationDiagnostic)) - 3
	if _, err := capture.stdout.Write(bytes.Repeat([]byte{'x'}, nativeAllowance)); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, overflow, truncated := capture.result()
	if overflow || !truncated {
		t.Fatalf("overflow = %v, truncated = %v", overflow, truncated)
	}
	stderr += shellCommentaryTruncationDiagnostic
	if got := len(stdout) + len(stderr) + len(shellCommentaryVisibleText(event)); got > toolplugin.ExecutionOutputBudgetBytes {
		t.Fatalf("combined visible bytes = %d", got)
	}
}

func TestShellDefaultTerminalCommentaryReclaimsPresentationWithoutOverflow(t *testing.T) {
	budget := newShellOutputBudget()
	recorder := new(recordingShellCommentarySink)
	runtime := newShellCommentaryRuntime(&budgetedShellCommentarySink{next: recorder, budget: budget})
	runtime.startDefault(t.Context())
	capture := newShellOutputCapture(func() {}, budget)
	nativeAllowance := toolplugin.ExecutionOutputBudgetBytes - len("Running the requested commands.") -
		max(len(shellOverflowDiagnostic), len(shellCommentaryTruncationDiagnostic)) - 3
	if _, err := capture.stdout.Write(bytes.Repeat([]byte{'x'}, nativeAllowance)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.terminal(t.Context(), "failure", "exit status 1"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, overflow, truncated := capture.result()
	if overflow || !truncated || len(recorder.events) != 2 {
		t.Fatalf("overflow = %v, truncated = %v, events = %+v", overflow, truncated, recorder.events)
	}
	stderr += shellCommentaryTruncationDiagnostic
	visible := len(stdout) + len(stderr)
	for _, event := range recorder.events {
		visible += len(shellCommentaryVisibleText(event))
	}
	if visible > toolplugin.ExecutionOutputBudgetBytes {
		t.Fatalf("combined visible bytes = %d", visible)
	}
}

func TestCommentaryPublisherCompletionCannotBlockBehindFullEventBuffer(t *testing.T) {
	broker := newCommentaryBroker()
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < commentaryPublicationBuffer; index++ {
		event := shellCommentaryEvent{Text: "status"}
		if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
			t.Fatal(err)
		}
	}
	if err := subscription.publish(t.Context(), commentaryPublication{Done: true}); err != nil {
		t.Fatal(err)
	}
	first, ready := subscription.beginForward()
	if !ready {
		t.Fatal("full live subscription was not ready")
	}
	if err := subscription.forward(t.Context(), first, func(publishedCommentary) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestCommentaryPublisherExpiresAndCancelsUnusedRoutes(t *testing.T) {
	originalTTL := commentarySubscriptionTTL
	commentarySubscriptionTTL = 5 * time.Millisecond
	t.Cleanup(func() { commentarySubscriptionTTL = originalTTL })
	broker := newCommentaryBroker()
	subscription, err := broker.subscribe("session", "session", "call-expire", true, false)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		broker.mu.Lock()
		_, active := broker.routes[subscription.token]
		broker.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unused commentary route did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	second, err := broker.subscribe("session", "session", "call-cancel", true, false)
	if err != nil {
		t.Fatal(err)
	}
	broker.cancelCall("session", "call-cancel")
	if err := second.publish(t.Context(), commentaryPublication{Done: true}); err == nil {
		t.Fatal("cancelled commentary route remained publishable")
	}
}

func TestCommentaryPublisherBoundsRoutesPerSession(t *testing.T) {
	broker := newCommentaryBroker()
	t.Cleanup(broker.close)
	for index := range maxCommentarySessionRoutes {
		if _, err := broker.subscribe("session", "session", fmt.Sprintf("call-%d", index), true, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := broker.subscribe("session", "session", "call-overflow", true, false); !errors.Is(err, errCommentaryCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestCommentaryPublisherBoundsAndExpiresDeferredEvents(t *testing.T) {
	originalTTL := commentarySubscriptionTTL
	commentarySubscriptionTTL = 5 * time.Millisecond
	t.Cleanup(func() { commentarySubscriptionTTL = originalTTL })
	broker := newCommentaryBroker()
	suppressed := 0
	broker.recordSuppressed = func(string, shellCommentaryEvent, string) { suppressed++ }
	for index := range maxDeferredSessionCommentary + 1 {
		broker.deferEvent("session", publishedCommentary{
			callID: "call", metricSessionID: "session", event: shellCommentaryEvent{Text: fmt.Sprintf("event-%d", index)},
		})
	}
	broker.mu.Lock()
	retained := len(broker.deferred["session"])
	broker.mu.Unlock()
	if retained != maxDeferredSessionCommentary || suppressed != 1 {
		t.Fatalf("retained = %d, suppressed = %d", retained, suppressed)
	}
	deadline := time.Now().Add(time.Second)
	for {
		broker.mu.Lock()
		retained = len(broker.deferred["session"])
		broker.mu.Unlock()
		if retained == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deferred commentary did not expire")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCommentaryPublisherDoesNotChargeRejectedDeferredEvent(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.setMetrics(newMetricsStore(""))
	retainCommentaryTestCall(t, transform, proxy, "call-overflow")
	broker := proxy.commentary
	broker.mu.Lock()
	for index := range maxDeferredSessionCommentary {
		if !broker.appendDeferredLocked(transform.historySessionID, publishedCommentary{
			callID: "retained", event: shellCommentaryEvent{Text: fmt.Sprintf("event-%d", index)},
		}) {
			t.Fatal("failed to fill deferred commentary capacity")
		}
	}
	broker.mu.Unlock()
	suppressed := 0
	broker.recordSuppressed = func(string, shellCommentaryEvent, string) { suppressed++ }
	subscription, err := broker.subscribe(transform.historySessionID, transform.sessionID, "call-overflow", false, false)
	if err != nil {
		t.Fatal(err)
	}
	beforeSessionBytes := proxy.sessions[transform.historySessionID].bytes
	beforeHistoryBytes := proxy.historyBytes
	event := shellCommentaryEvent{Text: "not retained", Form: "commentary not retained"}
	visibleBytes, err := subscription.publishVisible(t.Context(), commentaryPublication{Event: &event})
	if err != nil {
		t.Fatal(err)
	}
	if visibleBytes != 0 || subscription.publishedBytes != 0 || subscription.publishedCount != 0 || suppressed != 1 {
		t.Fatalf("visible = %d, bytes = %d, count = %d, suppressed = %d", visibleBytes, subscription.publishedBytes, subscription.publishedCount, suppressed)
	}
	history := proxy.sessions[transform.historySessionID].calls["call-overflow"]
	if len(history.commentaryMessageIDs) != 0 || proxy.sessions[transform.historySessionID].bytes != beforeSessionBytes || proxy.historyBytes != beforeHistoryBytes {
		t.Fatalf("rejected publication retained history = %+v", history)
	}
}

func TestCommentaryPublisherReleasesReservationWhenLiveBufferIsFull(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	retainCommentaryTestCall(t, transform, proxy, "call-full")
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-full", true, false)
	if err != nil {
		t.Fatal(err)
	}
	for range cap(subscription.events) {
		subscription.events <- commentaryPublication{}
	}
	beforeSessionBytes := proxy.sessions[transform.historySessionID].bytes
	beforeHistoryBytes := proxy.historyBytes
	event := shellCommentaryEvent{Text: "not queued", Form: "commentary not queued"}
	if _, err := subscription.publishVisible(t.Context(), commentaryPublication{Event: &event}); err == nil {
		t.Fatal("full live buffer accepted commentary")
	}
	history := proxy.sessions[transform.historySessionID].calls["call-full"]
	if len(history.commentaryMessageIDs) != 0 || proxy.sessions[transform.historySessionID].bytes != beforeSessionBytes || proxy.historyBytes != beforeHistoryBytes {
		t.Fatalf("full-buffer publication retained history = %+v", history)
	}
}

func TestSuppressionCapabilityMetersEveryRuntimePublication(t *testing.T) {
	broker := newCommentaryBroker()
	t.Cleanup(broker.close)
	metrics := newMetricsStore("")
	broker.recordSuppressed = func(sessionID string, event shellCommentaryEvent, warning string) {
		metrics.recordCommentary(sessionID, "suppressed", warning, event.Form, nil)
	}
	token, err := broker.suppressionCapability("session")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	sink := &httpShellCommentarySink{client: server.Client(), endpoint: server.URL, token: token}
	event := shellCommentaryEvent{Text: "Running loop", Form: "commentary Running loop ($i/3)"}
	for range 3 {
		if err := sink.Publish(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	commentary := metrics.snapshot().Commentary.Suppressed
	if commentary.Count != 3 || commentary.NativeTokens != 0 || commentary.FormTokens != 3*metrics.countCommentaryTokens(event.Form) {
		t.Fatalf("runtime suppression metrics = %+v", commentary)
	}
	broker.mu.Lock()
	routes, deferred := len(broker.routes), broker.deferredCount
	broker.mu.Unlock()
	if routes != 0 || deferred != 0 {
		t.Fatalf("suppression capability retained routes = %d, deferred = %d", routes, deferred)
	}
}

func TestBlankRuntimeCommentaryIsMeteredWithoutAVisibleMessage(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.setMetrics(newMetricsStore(""))
	publication := publishedCommentary{
		callID: "call-blank", metricSessionID: transform.sessionID,
		event: shellCommentaryEvent{Form: `commentary "$EMPTY"`},
	}
	if message, visible := commentaryMessageForPublication(publication); visible || message != nil {
		t.Fatalf("blank commentary message = %v, visible = %v", message, visible)
	}
	transform.recordPublishedCommentary(publication)
	commentary := proxy.metrics.snapshot().Commentary
	if commentary.Explicit.Count != 1 || commentary.Explicit.NativeTokens != 0 || commentary.Explicit.FormTokens == 0 {
		t.Fatalf("blank commentary metrics = %+v", commentary)
	}
}

func TestCommentaryMessageIDReservationHonorsHistoryByteLimits(t *testing.T) {
	newProxy := func(sessionBytes, globalBytes int) *hpatchProxy {
		return &hpatchProxy{
			sessions: map[string]*hpatchHistorySession{
				"session": {calls: map[string]hpatchHistory{"call": {bytes: sessionBytes}}, bytes: sessionBytes},
			},
			historyBytes: globalBytes,
		}
	}
	messageID := "msg_hpatch_commentary_boundary"
	for name, proxy := range map[string]*hpatchProxy{
		"session": newProxy(maxHPatchHistorySessionBytes-1, 0),
		"global":  newProxy(0, maxHPatchHistoryGlobalBytes-1),
	} {
		t.Run(name, func(t *testing.T) {
			if proxy.reserveCommentaryMessageID("session", "call", messageID) {
				t.Fatal("over-capacity message ID was retained")
			}
			if got := proxy.sessions["session"].calls["call"].commentaryMessageIDs; len(got) != 0 {
				t.Fatalf("retained message IDs = %v", got)
			}
		})
	}
}

func TestCommentaryPublisherStaleCleanupCannotReplaceCurrentTimer(t *testing.T) {
	broker := newCommentaryBroker()
	t.Cleanup(broker.close)
	broker.mu.Lock()
	broker.appendDeferredLocked("old", publishedCommentary{
		callID: "old", messageID: "old", event: shellCommentaryEvent{Text: "old"},
		expiresAt: time.Now().Add(time.Millisecond),
	})
	broker.scheduleCleanupLocked()
	time.Sleep(5 * time.Millisecond)
	broker.takeDeferredLocked("old")
	broker.appendDeferredLocked("new", publishedCommentary{
		callID: "new", messageID: "new", event: shellCommentaryEvent{Text: "new"},
		expiresAt: time.Now().Add(time.Hour),
	})
	broker.scheduleCleanupLocked()
	current := broker.cleanupTimer
	broker.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.cleanupTimer != current || len(broker.deferred["new"]) != 1 {
		t.Fatal("stale cleanup callback replaced the current timer or deferred state")
	}
}

func TestCommentaryPublisherAssignsUniqueIDsAcrossDeferredBatches(t *testing.T) {
	broker := newCommentaryBroker()
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := subscription.beginForward(); ready {
		t.Fatal("empty subscription unexpectedly ready")
	}
	var identifiers []string
	for _, text := range []string{"first", "second"} {
		event := shellCommentaryEvent{Text: text}
		if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
			t.Fatal(err)
		}
		batch := broker.drain("session")
		if len(batch) != 1 {
			t.Fatalf("deferred batch = %+v", batch)
		}
		identifiers = append(identifiers, batch[0].messageID)
	}
	if identifiers[0] == "" || identifiers[0] == identifiers[1] {
		t.Fatalf("message IDs = %q", identifiers)
	}
}

func retainCommentaryTestCall(t *testing.T, transform *hpatchResponseTransform, proxy *hpatchProxy, callID string) {
	t.Helper()
	if err := proxy.rememberBatch(transform.historySessionID, map[string]hpatchHistory{
		callID: {commentaryText: "Running the requested operation."},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCommentarySSEEmitterPrecedesCompletedEvent(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	proxy.commentaryEndpoint = "http://127.0.0.1/commentary"
	retainCommentaryTestCall(t, transform, proxy, "call-shell")
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Running check"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	failure := shellCommentaryEvent{Text: "Failed: Running check", Outcome: "failure", Reason: "exit status 1"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &failure}); err != nil {
		t.Fatal(err)
	}
	if err := subscription.publish(t.Context(), commentaryPublication{Done: true}); err != nil {
		t.Fatal(err)
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	if visible, err := transform.TransformSSEWithEmitter(created, func([]byte) error { return nil }); err != nil || len(visible) != 1 {
		t.Fatalf("created = %q, error %v", visible, err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	var emitted [][]byte
	visible, err := transform.TransformSSEWithEmitter(completed, func(payload []byte) error {
		emitted = append(emitted, bytes.Clone(payload))
		return nil
	})
	if err != nil || len(emitted) != 2 || len(visible) != 1 {
		t.Fatalf("emitted = %q, visible = %q, error %v", emitted, visible, err)
	}
	var envelope struct {
		Item map[string]json.RawMessage `json:"item"`
	}
	if json.Unmarshal(emitted[0], &envelope) != nil || jsonString(envelope.Item, "phase") != "commentary" {
		t.Fatalf("commentary event = %s", emitted[0])
	}
	commentary := metrics.snapshot().Commentary
	if commentary.Default.Count != 1 || commentary.Failure.Count != 1 || commentary.Failure.FormTokens != 0 ||
		commentary.Failure.NativeTokens != metrics.countCommentaryTokens(shellCommentaryVisibleText(failure)) {
		t.Fatalf("commentary metrics = %+v", commentary)
	}
}

func TestLiveCommentaryReservesBeforeHistoryCommit(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.setMetrics(newMetricsStore(""))
	history := hpatchHistory{commentaryText: "Running the requested operation."}
	transform.recordLocal("call-live", &history)
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-live", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Running before completion", Form: "commentary Running before completion"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	if proxy.provisionalCommentaryBytes == 0 {
		t.Fatal("live commentary did not reserve provisional history")
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	if _, err := transform.TransformSSEWithEmitter(created, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	emitted := 0
	if _, err := transform.TransformSSEWithEmitter(completed, func([]byte) error {
		emitted++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retained, ok := proxy.history(transform.historySessionID, "call-live")
	if emitted != 1 || !ok || len(retained.commentaryMessageIDs) != 1 || proxy.provisionalCommentaryBytes != 0 {
		t.Fatalf("emitted = %d, retained = %+v, provisional bytes = %d", emitted, retained, proxy.provisionalCommentaryBytes)
	}
}

func TestAbandonedLiveCommentaryReleasesProvisionalReservations(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-abandoned", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Never shown"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	transform.Close()
	proxy.commentary.mu.Lock()
	_, active := proxy.commentary.routes[subscription.token]
	proxy.commentary.mu.Unlock()
	if active || proxy.provisionalCommentaryBytes != 0 || len(proxy.provisionalCommentaryIDs) != 0 {
		t.Fatalf("active = %v, provisional bytes = %d, IDs = %v", active, proxy.provisionalCommentaryBytes, proxy.provisionalCommentaryIDs)
	}
}

func TestExpiredLiveCommentaryReleasesProvisionalReservations(t *testing.T) {
	originalTTL := commentarySubscriptionTTL
	commentarySubscriptionTTL = 5 * time.Millisecond
	t.Cleanup(func() { commentarySubscriptionTTL = originalTTL })
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-expired", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Expires before display"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	provisional := func() (int, int) {
		proxy.mu.RLock()
		defer proxy.mu.RUnlock()
		return proxy.provisionalCommentaryBytes, len(proxy.provisionalCommentaryIDs)
	}
	bytes, identifiers := provisional()
	for bytes != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		bytes, identifiers = provisional()
	}
	if bytes != 0 || identifiers != 0 {
		t.Fatalf("provisional bytes = %d, ID groups = %d", bytes, identifiers)
	}
}

func TestLiveEmissionCancellationCannotReleaseVisibleMessageID(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	retainCommentaryTestCall(t, transform, proxy, "call-race")
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-race", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Visible during cancellation"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	if _, err := transform.TransformSSEWithEmitter(created, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	emitting := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := transform.TransformSSEWithEmitter(completed, func([]byte) error {
			close(emitting)
			<-release
			return nil
		})
		finished <- err
	}()
	<-emitting
	subscription.cancel()
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	retained, ok := proxy.history(transform.historySessionID, "call-race")
	if !ok || len(retained.commentaryMessageIDs) != 1 {
		t.Fatalf("retained history = %+v, exists = %v", retained, ok)
	}
}

func TestFailedLiveEmitterDoesNotRecordVisibilityOrSettlement(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	retainCommentaryTestCall(t, transform, proxy, "call-emit-failure")
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-emit-failure", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Failed: Not emitted", Outcome: "failure", Reason: "exit status 1"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	if _, err := transform.TransformSSEWithEmitter(created, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	if _, err := transform.TransformSSEWithEmitter(completed, func([]byte) error { return errors.New("downstream unavailable") }); err == nil {
		t.Fatal("emitter failure was ignored")
	}
	retained, ok := proxy.history(transform.historySessionID, "call-emit-failure")
	if !ok || retained.commentarySettled || len(retained.commentaryMessageIDs) != 0 {
		t.Fatalf("retained history after emitter failure = %+v, exists = %v", retained, ok)
	}
	if commentary := metrics.snapshot().Commentary; commentary.Failure.Count != 0 {
		t.Fatalf("failed emitter commentary metrics = %+v", commentary)
	}
}

func TestDeferredCommentaryFollowsResponseCreated(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.setMetrics(newMetricsStore(""))
	retainCommentaryTestCall(t, transform, proxy, "call-shell")
	proxy.commentary.deferEvent(transform.historySessionID, publishedCommentary{
		callID: "call-shell", event: shellCommentaryEvent{Text: "Failed: Running commands", Outcome: "failure"},
	})
	transform.deferredCommentary = proxy.commentary.drain(transform.historySessionID)
	var emitted [][]byte
	emit := func(payload []byte) error {
		emitted = append(emitted, bytes.Clone(payload))
		return nil
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	visible, err := transform.TransformSSEWithEmitter(created, emit)
	if err != nil || len(visible) != 1 || len(emitted) != 0 {
		t.Fatalf("created visible = %q, emitted = %q, error %v", visible, emitted, err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	visible, err = transform.TransformSSEWithEmitter(completed, emit)
	if err != nil || len(visible) != 1 || len(emitted) != 1 {
		t.Fatalf("completed visible = %q, emitted = %q, error %v", visible, emitted, err)
	}
}

func TestDeferredCommentaryIsDiscardedWithPrunedOwningCall(t *testing.T) {
	transform, proxy, _, workspace := newHPatchTestTransform(t, testTranslator(t, new(int)))
	retainCommentaryTestCall(t, transform, proxy, "call-pruned")
	proxy.commentary.deferEvent(transform.historySessionID, publishedCommentary{
		callID: "call-pruned", event: shellCommentaryEvent{Text: "Stale branch commentary"},
	})
	retained, ok := proxy.history(transform.historySessionID, "call-pruned")
	if !ok || len(retained.commentaryMessageIDs) != 1 {
		t.Fatalf("retained history before pruning = %+v, exists = %v", retained, ok)
	}
	transform.Close()

	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			testCodeModeAdditionalTools(testCodeModeDescription),
			map[string]any{"role": "user", "content": "new branch"},
		},
		"tools": []any{map[string]any{"type": "function", "name": "lookup"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	metadata := codexTurnMetadata{RequestKind: "turn", Directories: map[string]json.RawMessage{workspace: nil}}
	next, err := proxy.prepareRequest(t.Context(), &request, "session-1", "thread-1", metadata, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	if len(next.deferredCommentary) != 0 {
		t.Fatalf("deferred commentary = %+v", next.deferredCommentary)
	}
	if _, exists := proxy.history(next.historySessionID, "call-pruned"); exists {
		t.Fatal("pruned commentary call remains in history")
	}
	if deferred := proxy.commentary.drain(next.historySessionID); len(deferred) != 0 {
		t.Fatalf("broker retained stale commentary = %+v", deferred)
	}
}

func TestFailedJSONWriteRequeuesDeferredCommentaryWithoutVisibilityEffects(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	retainCommentaryTestCall(t, transform, proxy, "call-json-write")
	event := shellCommentaryEvent{Text: "Failed: Running commands", Outcome: "failure", Reason: "exit status 1"}
	proxy.commentary.deferEvent(transform.historySessionID, publishedCommentary{
		callID: "call-json-write", metricSessionID: transform.sessionID, event: event,
	})
	transform.deferredCommentary = proxy.commentary.drain(transform.historySessionID)
	body := strings.NewReader(`{"status":"completed","output":[]}`)
	if _, err := copyJSONTransformed(failingCommentaryWriter{}, body, transform, nil); err == nil {
		t.Fatal("JSON write failure was ignored")
	}
	retained, ok := proxy.history(transform.historySessionID, "call-json-write")
	if !ok || retained.commentarySettled || metrics.snapshot().Commentary.Failure.Count != 0 {
		t.Fatalf("retained = %+v, exists = %v, metrics = %+v", retained, ok, metrics.snapshot().Commentary)
	}
	transform.Close()
	requeued := proxy.commentary.drain(transform.historySessionID)
	if len(requeued) != 1 || requeued[0].event != event {
		t.Fatalf("requeued commentary = %+v", requeued)
	}
}

func TestFailedSSEWriteDoesNotRecordStructuredCommentaryVisibility(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	metrics := newMetricsStore("")
	proxy.setMetrics(metrics)
	transform.commentaryTools = commentaryToolCatalog{
		commentaryToolKey("functions", "exec_command"): {
			namespace: "functions", name: "exec_command", display: "exec_command", explicit: true,
		},
	}
	payload := mustTestJSON(t, map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"type": "function_call", "id": "item-exec", "call_id": "call-exec",
			"namespace": "functions", "name": "exec_command",
			"arguments": `{"cmd":"true","commentary":"Checking the command."}`,
		},
	})
	body := "event: response.output_item.done\n" + "data: " + string(payload) + "\n\n"
	if _, err := copySSETransformed(failingCommentaryWriter{}, strings.NewReader(body), transform, nil); err == nil {
		t.Fatal("SSE write failure was ignored")
	}
	if commentary := metrics.snapshot().Commentary; commentary.Explicit.Count != 0 {
		t.Fatalf("failed SSE write commentary metrics = %+v", commentary)
	}
}

func TestCommentarySSEDrainsReadyEventsWithoutWaitingForDone(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	proxy.setMetrics(newMetricsStore(""))
	retainCommentaryTestCall(t, transform, proxy, "call-shell")
	subscription, err := proxy.commentary.subscribe(transform.historySessionID, transform.sessionID, "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	transform.commentarySubscriptions = append(transform.commentarySubscriptions, subscription)
	event := shellCommentaryEvent{Text: "Running check"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &event}); err != nil {
		t.Fatal(err)
	}
	created := mustTestJSON(t, map[string]any{
		"type": "response.created", "response": map[string]any{"status": "in_progress", "output": []any{}},
	})
	if _, err := transform.TransformSSEWithEmitter(created, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{}},
	})
	emitted := 0
	visible, err := transform.TransformSSEWithEmitter(completed, func([]byte) error {
		emitted++
		return nil
	})
	if err != nil || len(visible) != 1 || emitted != 1 {
		t.Fatalf("visible = %q, emitted = %d, error %v", visible, emitted, err)
	}
	failure := shellCommentaryEvent{Text: "Failed: Running check", Outcome: "failure"}
	if err := subscription.publish(t.Context(), commentaryPublication{Event: &failure}); err != nil {
		t.Fatal(err)
	}
	if deferred := proxy.commentary.drain(transform.historySessionID); len(deferred) != 1 || deferred[0].event != failure {
		t.Fatalf("deferred = %+v", deferred)
	}
}

func TestShellWorkerPublishesPipelineFailureWithoutChangingOutput(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	broker := newCommentaryBroker()
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	subscription, err := broker.subscribe("session", "session", "call-shell", true, false)
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{
		commentaryEndpointArgument, server.URL, commentaryTokenArgument, subscription.token,
		"bash", "commentary Running pipeline\nfalse | true",
	}
	var stdout, stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(t.Context(), registry.shellRuntime, arguments, os.Stdin, &stdout, &stderr)
	if !handled || exitCode != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("handled %v, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
	}
	first, ready := subscription.beginForward()
	if !ready {
		t.Fatal("shell worker publications were deferred")
	}
	var events []publishedCommentary
	if err := subscription.forward(t.Context(), first, func(event publishedCommentary) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].event.Text != "Running pipeline" ||
		events[1].event.Text != "Failed: Running pipeline" || events[1].event.Reason != "exit status 1" {
		t.Fatalf("events = %+v", events)
	}
}

func TestShellDefaultPreservesDirectNativeCommand(t *testing.T) {
	transform, proxy, _ := newToolPluginTestTransform(t)
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{map[string]any{
			"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
			"name": "shell", "input": "rtk ok", "status": "completed",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if json.Unmarshal(response, &decoded) != nil || len(decoded.Output) != 2 {
		t.Fatalf("shell response = %s", response)
	}
	if jsonString(decoded.Output[0], "phase") != "commentary" ||
		!strings.Contains(string(decoded.Output[0]["content"]), "Running the requested commands.") {
		t.Fatalf("shell default = %s", decoded.Output[0]["content"])
	}
	carrier := jsonString(decoded.Output[1], "input")
	if !strings.Contains(carrier, `"cmd":"rtk ok"`) || strings.Contains(carrier, commentaryEndpointArgument) {
		t.Fatalf("direct shell carrier = %s", carrier)
	}
}

func TestShellCommentaryCapacitySuppressesInsteadOfDefaulting(t *testing.T) {
	transform, proxy, _ := newToolPluginTestTransform(t)
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	proxy.setMetrics(newMetricsStore(""))
	for index := range maxCommentaryRoutes {
		if _, err := proxy.commentary.subscribe(
			fmt.Sprintf("history-%d", index), fmt.Sprintf("session-%d", index), fmt.Sprintf("call-%d", index), false, true,
		); err != nil {
			t.Fatal(err)
		}
	}
	response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{map[string]any{
			"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
			"name": "shell", "input": "commentary Running check\ntrue", "status": "completed",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if json.Unmarshal(response, &decoded) != nil || len(decoded.Output) != 1 || jsonString(decoded.Output[0], "name") != "exec" {
		t.Fatalf("shell capacity response = %s", response)
	}
	history := transform.local["call-shell"]
	if !history.commentarySuppressed || history.commentaryText != "" || len(history.commentaryMessageIDs) != 0 {
		t.Fatalf("shell capacity history = %+v", history)
	}
	commentary := proxy.metrics.snapshot().Commentary
	if commentary.Suppressed.Count != 0 || commentary.Default.Count != 0 {
		t.Fatalf("shell capacity commentary metrics = %+v", commentary)
	}
	carrier := jsonString(decoded.Output[0], "input")
	if !strings.Contains(carrier, commentaryTokenArgument) {
		t.Fatalf("shell capacity carrier has no runtime suppression capability: %s", carrier)
	}
}

func TestCodeModeOneShotPublisherPreservesSilenceAndRepeatedCapability(t *testing.T) {
	registry, err := buildToolRegistry(t.Context(), t.TempDir(), testHPatchToolDescription, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	broker := newCommentaryBroker()
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)
	subscription, err := broker.subscribe("session", "session", "call-code", false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Running 1/2", "Running 2/2"} {
		arguments := []string{
			commentaryOnceArgument, server.URL, subscription.token,
			url.PathEscape("await commentary(`Running ${i}/2`);"), url.PathEscape(text),
		}
		var stdout, stderr bytes.Buffer
		handled, exitCode := RunToolPluginWorker(t.Context(), registry.shellRuntime, arguments, os.Stdin, &stdout, &stderr)
		if !handled || exitCode != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("handled %v, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
		}
	}
	events := broker.drain("session")
	if len(events) != 2 || events[0].event.Text != "Running 1/2" || events[1].event.Text != "Running 2/2" ||
		events[0].event.Form != "await commentary(`Running ${i}/2`);" {
		t.Fatalf("events = %+v", events)
	}
}
