package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
)

type serverForwardResult struct {
	response *http.Response
	err      error
}

type serverFakeProvider struct {
	results           []serverForwardResult
	forwarded         [][]byte
	forwardedHeaders  []http.Header
	forwardedCacheKey []string
}

func (f *serverFakeProvider) forwardExecution(_, _ context.Context, body []byte, headers http.Header, cacheKey string) (*http.Response, error) {
	f.forwardedHeaders = append(f.forwardedHeaders, headers.Clone())
	f.forwardedCacheKey = append(f.forwardedCacheKey, cacheKey)
	return f.next(body)
}

func (f *serverFakeProvider) next(body []byte) (*http.Response, error) {
	f.forwarded = append(f.forwarded, bytes.Clone(body))
	if len(f.results) == 0 {
		return nil, errors.New("unexpected upstream forward")
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.response, result.err
}

func serverHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func serverRequest(t *testing.T, mutate func(map[string]any)) parsedResponsesRequest {
	t.Helper()
	request := map[string]any{
		"model":     "gpt-test",
		"reasoning": map[string]any{"effort": "high"},
		"input": []any{
			map[string]any{
				"type": "additional_tools",
				"tools": []any{map[string]any{
					"type": "custom", "name": "exec", "description": testCodeModeDescription,
				}},
			},
			map[string]any{"role": "user", "content": "task"},
		},
		"tools":       []any{map[string]any{"type": "function", "name": "lookup"}},
		"tool_choice": "auto",
	}
	if mutate != nil {
		mutate(request)
	}
	parsed, err := parseResponsesRequest(mustTestJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func serverMetadataHeaders(t *testing.T, requestKind string, workspaces map[string]json.RawMessage) http.Header {
	t.Helper()
	encoded, err := json.Marshal(codexTurnMetadata{RequestKind: requestKind, Workspaces: workspaces})
	if err != nil {
		t.Fatal(err)
	}
	return http.Header{codexTurnMetadataHeader: []string{string(encoded)}}
}

func serverCompactionMetadataHeaders(t *testing.T) http.Header {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"request_kind": "compaction",
		"turn_id":      "turn-1",
		"compaction": map[string]any{
			"trigger": "auto", "reason": "context_limit", "implementation": "responses",
			"phase": "standalone_turn", "strategy": "memento",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return http.Header{codexTurnMetadataHeader: []string{string(encoded)}}
}

func TestExecuteRequestFailsClosedBeforeUpstreamWhenRewriteIsIneligible(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	validHeaders := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
	tests := []struct {
		name      string
		sessionID string
		headers   http.Header
		mutate    func(map[string]any)
		want      string
	}{
		{name: "missing session", headers: validHeaders, want: "valid session ID"},
		{name: "invalid metadata", sessionID: "session", headers: http.Header{}, want: "valid turn metadata"},
		{name: "unknown request kind", sessionID: "session", headers: serverMetadataHeaders(t, "other", nil), want: "valid turn metadata"},
		{name: "legacy compact request kind", sessionID: "session", headers: serverMetadataHeaders(t, "compact", nil), want: "valid turn metadata"},
		{name: "compaction metadata on ordinary turn", sessionID: "session", headers: serverCompactionMetadataHeaders(t), mutate: func(request map[string]any) {
			delete(request, "tools")
			request["stream"] = true
			request["parallel_tool_calls"] = false
		}, want: "compaction request cannot expose tools"},
		{name: "multiple usable workspaces", sessionID: "session", headers: serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil, otherWorkspace: nil}), want: "exactly one usable workspace"},
		{name: "no usable workspace", sessionID: "session", headers: serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir() + "/missing": nil}), want: "usable workspace"},
		{name: "missing apply_patch", sessionID: "session", headers: validHeaders, mutate: func(request map[string]any) {
			input := request["input"].([]any)
			additional := input[0].(map[string]any)
			tools := additional["tools"].([]any)
			tools[0].(map[string]any)["description"] = "Run JavaScript without apply_patch."
		}, want: "required hpatch rewrite"},
		{name: "restricted Code Mode tool", sessionID: "session", headers: validHeaders, mutate: func(request map[string]any) {
			request["tool_choice"] = map[string]any{"type": "custom", "name": "exec"}
		}, want: "required hpatch rewrite"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &serverFakeProvider{}
			err := executeRequest(t.Context(), t.Context(), serverRequest(t, test.mutate), test.headers, test.sessionID, provider, io.Discard, newDiagnostics(io.Discard), time.Now, newManagedHPatchProxy(t, testTranslator(t, new(int))), newMetricsStore(""))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if len(provider.forwarded) != 0 {
				t.Fatalf("ineligible request reached upstream: %s", provider.forwarded[0])
			}
		})
	}
}

func TestExecuteRequestForwardsCompactionWithoutHPatchRewrite(t *testing.T) {
	parsed, err := parseResponsesRequest(mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}},
			map[string]any{"type": "message", "role": "developer", "content": "instructions"},
			map[string]any{"type": "message", "role": "user", "content": "summarize the conversation"},
		},
		"tool_choice":         "auto",
		"parallel_tool_calls": false,
		"stream":              true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	originalBody, err := json.Marshal(parsed.fields)
	if err != nil {
		t.Fatal(err)
	}
	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": []any{}},
	})
	responseBody := "event: response.completed\ndata: " + string(completed) + "\n\n"
	response := serverHTTPResponse(responseBody)
	response.Header.Set("Content-Type", "text/event-stream")
	provider := &serverFakeProvider{results: []serverForwardResult{{response: response}}}
	proxy := newManagedHPatchProxy(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		t.Fatal("compaction reached the hpatch translator")
		return nil, nil
	}))
	var output bytes.Buffer
	err = executeRequest(t.Context(), t.Context(), parsed, serverCompactionMetadataHeaders(t), "session", provider, &output, newDiagnostics(io.Discard), time.Now, proxy, newMetricsStore(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(provider.forwarded))
	}
	if !bytes.Equal(provider.forwarded[0], originalBody) {
		t.Fatalf("compaction request was rewritten:\n got %s\nwant %s", provider.forwarded[0], originalBody)
	}
	if output.String() != responseBody {
		t.Fatalf("visible response = %s, want %s", output.String(), responseBody)
	}
}

func TestExecuteRequestPassesThroughOriginalRequestAndRecordsUsage(t *testing.T) {
	parsed := serverRequest(t, func(request map[string]any) {
		request["prompt_cache_key"] = "control-cache"
	})
	originalBody, err := json.Marshal(parsed.fields)
	if err != nil {
		t.Fatal(err)
	}
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage": map[string]any{
			"input_tokens": 12, "input_tokens_details": map[string]any{"cached_tokens": 5},
			"output_tokens": 7, "output_tokens_details": map[string]any{"reasoning_tokens": 3},
		},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(responseBody)}}}
	store := newMetricsStore("")
	var output bytes.Buffer
	if err := executeRequest(t.Context(), t.Context(), parsed, http.Header{}, "session", provider, &output, newDiagnostics(io.Discard), time.Now, nil, store); err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 1 || !bytes.Equal(provider.forwarded[0], originalBody) {
		t.Fatalf("forwarded request = %q, want original %q", provider.forwarded, originalBody)
	}
	if got := provider.forwardedCacheKey[0]; got != "control-cache" {
		t.Fatalf("upstream cache key = %q, want control-cache", got)
	}
	if output.String() != responseBody {
		t.Fatalf("visible response = %q, want %q", output.String(), responseBody)
	}
	want := tokenCounts{InputTokens: 12, UncachedInputTokens: 7, OutputTokens: 7, ReasoningTokens: 3}
	if got := store.snapshot().Total; got != want {
		t.Fatalf("usage = %#v, want %#v", got, want)
	}
}

//nolint:canonicalheader // Exact lowercase names match Codex's observed wire headers.
func TestExecuteRequestForwardsRewrittenRequestAndRecordsUsage(t *testing.T) {
	workspace := t.TempDir()
	parsed := serverRequest(t, func(request map[string]any) {
		request["prompt_cache_key"] = "prompt-cache"
	})

	headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
	headers.Add(sessionIDHeader, "session-primary")
	headers.Add(sessionIDHeader, "session-secondary")
	headers.Set(threadIDHeader, "thread")
	headers.Set(clientRequestIDHeader, "client-request")
	headers.Set(codexWindowIDHeader, "window:0")
	headers.Set(codexBetaFeaturesHeader, "feature")
	headers.Set(codexResponsesLiteHeader, "true")
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage": map[string]any{
			"input_tokens": 10, "input_tokens_details": map[string]any{"cached_tokens": 4},
			"output_tokens": 6, "output_tokens_details": map[string]any{"reasoning_tokens": 2},
		},
		"future": map[string]any{"kept": true},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(responseBody)}}}
	proxy := newManagedHPatchProxy(t, hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		t.Fatal("response without an hpatch call reached the translator")
		return nil, nil
	}))
	store := newMetricsStore("")
	var output bytes.Buffer
	err := executeRequest(t.Context(), t.Context(), parsed, headers, "session", provider, &output, newDiagnostics(io.Discard), time.Now, proxy, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(provider.forwarded))
	}
	if got := provider.forwardedCacheKey[0]; got != "prompt-cache" {
		t.Fatalf("upstream cache key = %q, want prompt_cache_key", got)
	}
	for _, name := range []string{sessionIDHeader, threadIDHeader, clientRequestIDHeader, codexWindowIDHeader, codexBetaFeaturesHeader, codexResponsesLiteHeader, codexTurnMetadataHeader} {
		if got, want := provider.forwardedHeaders[0].Values(name), headers.Values(name); !slices.Equal(got, want) {
			t.Fatalf("forwarded header %s = %q, want %q", name, got, want)
		}
	}
	forwarded, err := parseResponsesRequest(provider.forwarded[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(forwarded.fields["tools"]), "\"name\":\"hpatch\"") || strings.Contains(string(forwarded.fields["tools"]), workspace) {
		t.Fatalf("unstable rewritten tools = %s", forwarded.fields["tools"])
	}
	if strings.Contains(string(forwarded.fields["input"]), workspace) {
		t.Fatalf("single-workspace input contains redundant metadata: %s", forwarded.fields["input"])
	}
	if string(forwarded.fields["reasoning"]) != "{\"effort\":\"high\"}" {
		t.Fatalf("reasoning request changed: %s", forwarded.fields["reasoning"])
	}
	if strings.Contains(output.String(), "selected_reasoning_effort") || !strings.Contains(output.String(), "\"future\":{\"kept\":true}") {
		t.Fatalf("visible response changed unexpectedly: %s", output.String())
	}
	want := tokenCounts{InputTokens: 10, UncachedInputTokens: 6, OutputTokens: 6, ReasoningTokens: 2}
	if got := store.snapshot().Total; got != want {
		t.Fatalf("usage = %#v, want %#v", got, want)
	}
}

func TestExecuteRequestRejectsDirectAdditionalApplyPatchWithoutExecCarrier(t *testing.T) {
	workspace := t.TempDir()
	parsed := serverRequest(t, func(request map[string]any) {
		additional := request["input"].([]any)[0].(map[string]any)
		additional["tools"] = []any{
			map[string]any{"type": "custom", "name": "shell"},
			map[string]any{"type": "custom", "name": applyPatchToolName, "description": "Apply a patch."},
		}
	})
	responseBody := string(mustTestJSON(t, map[string]any{"status": "completed", "output": []any{}}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(responseBody)}}}
	var output bytes.Buffer
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	err := executeRequest(
		t.Context(),
		t.Context(),
		parsed,
		serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}),
		"session-direct",
		provider,
		&output,
		newDiagnostics(io.Discard),
		time.Now,
		proxy,
		newMetricsStore(""),
	)
	if err == nil || !strings.Contains(err.Error(), "no Code Mode exec carrier") {
		t.Fatalf("direct request error = %v", err)
	}
	if len(provider.forwarded) != 0 || output.Len() != 0 {
		t.Fatalf("direct request was forwarded %d time(s), output %q", len(provider.forwarded), output.String())
	}
}

type serverErrorWriter struct{ err error }

func (w serverErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExecuteRequestRecordsUsageAndFailureWhenDeliveryFails(t *testing.T) {
	workspace := t.TempDir()
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage":  map[string]any{"input_tokens": 10},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(responseBody)}}}
	store := newMetricsStore("")
	var logOutput bytes.Buffer
	err := executeRequest(t.Context(), t.Context(), serverRequest(t, nil), serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}), "session", provider, serverErrorWriter{err: io.ErrClosedPipe}, newDiagnostics(&logOutput), serverLifecycleClock(0, 10*time.Millisecond, 30*time.Millisecond), newManagedHPatchProxy(t, testTranslator(t, new(int))), store)
	if err == nil {
		t.Fatal("delivery failure returned no error")
	}
	snapshot := store.snapshot()
	if got := snapshot.Total.InputTokens; got != 10 {
		t.Fatalf("observed usage after delivery failure = %d, want 10", got)
	}
	assertServerLifecycleFinished(t, snapshot.Requests)
	if snapshot.Requests.Failed != 1 || snapshot.Requests.UsageObserved != 1 {
		t.Fatalf("delivery lifecycle = %#v", snapshot.Requests)
	}
	if logs := logOutput.String(); strings.Count(logs, "Responses request finished") != 1 || !strings.Contains(logs, "failure_phase=write_response") {
		t.Fatalf("delivery terminal log = %q", logs)
	}
}

type serverRepeatingReader struct{}

func (serverRepeatingReader) Read(content []byte) (int, error) {
	for index := range content {
		content[index] = 'x'
	}
	return len(content), nil
}

func TestResponsesHandlerRejectsBackgroundBeforeUpstream(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-test","input":"task","background":true}`),
	)
	recorder := httptest.NewRecorder()
	provider := &serverFakeProvider{}
	responsesHandler(t.Context(), time.Minute, provider, newDiagnostics(io.Discard), nil, nil, new(atomic.Uint64))(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "background Responses requests are not supported") {
		t.Fatalf("response = %q", recorder.Body.String())
	}
	if len(provider.forwarded) != 0 {
		t.Fatal("background request reached upstream")
	}
}

func TestResponsesHandlerRejectsOversizedBodyBeforeUpstream(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", io.LimitReader(serverRepeatingReader{}, maxResponsesRequest+1))
	recorder := httptest.NewRecorder()
	provider := &serverFakeProvider{}
	responsesHandler(t.Context(), time.Minute, provider, newDiagnostics(io.Discard), nil, nil, new(atomic.Uint64))(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if len(provider.forwarded) != 0 {
		t.Fatal("oversized request reached upstream")
	}
}

type serverProviderFunc func(context.Context, context.Context, []byte, http.Header, string) (*http.Response, error)

func (f serverProviderFunc) forwardExecution(startCtx, responseCtx context.Context, body []byte, headers http.Header, cacheKey string) (*http.Response, error) {
	return f(startCtx, responseCtx, body, headers, cacheKey)
}

type serverCancelableSSEBody struct {
	ctx      context.Context
	content  *strings.Reader
	canceled chan struct{}
	once     sync.Once
}

func (body *serverCancelableSSEBody) Read(content []byte) (int, error) {
	if body.content.Len() > 0 {
		return body.content.Read(content)
	}
	<-body.ctx.Done()
	body.once.Do(func() { close(body.canceled) })
	return 0, body.ctx.Err()
}

func (*serverCancelableSSEBody) Close() error { return nil }

//nolint:canonicalheader // Exact lowercase names match Codex's observed wire headers.
func TestResponsesHandlerDoesNotLogClientCancellationAsOperationalEvent(t *testing.T) {
	upstreamEvent := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	upstreamCanceled := make(chan struct{})
	provider := serverProviderFunc(func(_, responseCtx context.Context, _ []byte, _ http.Header, _ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: &serverCancelableSSEBody{
				ctx: responseCtx, content: strings.NewReader(upstreamEvent), canceled: upstreamCanceled,
			},
		}, nil
	})
	var logOutput bytes.Buffer
	handler := responsesHandler(t.Context(), time.Minute, provider, newDiagnostics(&logOutput), newManagedHPatchProxy(t, nil), newMetricsStore(""), new(atomic.Uint64))
	handled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler(writer, request)
		close(handled)
	}))
	defer server.Close()

	body := mustTestJSON(t, map[string]any{
		"model": "gpt-test",
		"input": []any{
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}},
			map[string]any{"type": "message", "role": "developer", "content": "instructions"},
			map[string]any{"type": "message", "role": "user", "content": "summarize the conversation"},
		},
		"tool_choice": "auto", "parallel_tool_calls": false, "stream": true,
	})
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = serverCompactionMetadataHeaders(t)
	request.Header.Set(sessionIDHeader, "session")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	visible := make([]byte, len(upstreamEvent))
	if _, err := io.ReadFull(response.Body, visible); err != nil {
		t.Fatalf("read initial upstream event: %v", err)
	}
	if string(visible) != upstreamEvent {
		t.Fatalf("visible event = %q, want %q", visible, upstreamEvent)
	}
	cancelRequest()
	response.Body.Close()

	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not stop after the client canceled its request")
	}
	select {
	case <-upstreamCanceled:
	default:
		t.Fatal("client cancellation did not reach the upstream response body")
	}
	if logs := logOutput.String(); strings.Contains(logs, "canceled after response started") {
		t.Fatalf("client cancellation was logged as an operational event:\n%s", logs)
	}
}

func serverLifecycleClock(offsets ...time.Duration) func() time.Time {
	base := time.Unix(1_000, 0)
	var index atomic.Uint64
	return func() time.Time {
		return base.Add(offsets[index.Add(1)-1])
	}
}

func assertServerLifecycleFinished(t *testing.T, requests requestLifecycleMetrics) {
	t.Helper()
	terminal := requests.Completed +
		requests.Failed +
		requests.CanceledBeforeResponse +
		requests.CanceledAfterResponse +
		requests.TimedOut +
		requests.BackgroundPending
	if requests.Active != 0 || terminal != requests.Started {
		t.Fatalf("lifecycle invariant failed: %#v", requests)
	}
}

func serverSessionLifecycle(t *testing.T, store *metricsStore, sessionID string) requestLifecycleMetrics {
	t.Helper()
	for _, session := range store.snapshot().Sessions {
		if session.SessionID == sessionID {
			return session.Requests
		}
	}
	t.Fatalf("session %q not retained", sessionID)
	return requestLifecycleMetrics{}
}

func TestExecuteRequestSuccessfulStreamLifecycle(t *testing.T) {
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
			"usage": map[string]any{
				"input_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 4},
				"output_tokens": 5, "output_tokens_details": map[string]any{"reasoning_tokens": 2},
			},
		},
	})
	response := serverHTTPResponse("event: response.completed\ndata: " + string(completed) + "\n\n")
	response.Header.Set("Content-Type", "text/event-stream")
	provider := &serverFakeProvider{results: []serverForwardResult{{response: response}}}
	store := newMetricsStore("")
	recorder := httptest.NewRecorder()
	writer := &trackedResponseWriter{ResponseWriter: recorder}
	var logOutput bytes.Buffer
	err := executeRequest(
		t.Context(), t.Context(),
		serverRequest(t, func(request map[string]any) { request["stream"] = true }),
		http.Header{}, "stream-session", provider, writer, newDiagnostics(&logOutput),
		serverLifecycleClock(0, 10*time.Millisecond, 40*time.Millisecond), nil, store,
	)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := store.snapshot()
	assertServerLifecycleFinished(t, snapshot.Requests)
	if snapshot.Requests.Completed != 1 ||
		snapshot.Requests.TotalDurationMilliseconds != 40 ||
		snapshot.Requests.UpstreamDurationMilliseconds != 30 ||
		snapshot.Requests.UsageObserved != 1 {
		t.Fatalf("stream lifecycle = %#v", snapshot.Requests)
	}
	wantUsage := tokenCounts{InputTokens: 11, UncachedInputTokens: 7, OutputTokens: 5, ReasoningTokens: 2}
	if snapshot.Total != wantUsage {
		t.Fatalf("stream usage = %#v, want %#v", snapshot.Total, wantUsage)
	}
	session := serverSessionLifecycle(t, store, "stream-session")
	if session != snapshot.Requests {
		t.Fatalf("session lifecycle = %#v, global %#v", session, snapshot.Requests)
	}
	if logs := logOutput.String(); strings.Count(logs, "Responses request finished") != 1 ||
		!strings.Contains(logs, "outcome=completed") ||
		!strings.Contains(logs, "response_started=true") {
		t.Fatalf("stream terminal log = %q", logs)
	}
}

func TestExecuteRequestTerminalOutcomes(t *testing.T) {
	failed := mustTestJSON(t, map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"status": "failed",
			"usage":  map[string]any{"input_tokens": 9},
		},
	})
	failedResponse := serverHTTPResponse("event: response.failed\ndata: " + string(failed) + "\n\n")
	failedResponse.Header.Set("Content-Type", "text/event-stream")
	tests := []struct {
		name             string
		response         *http.Response
		mutate           func(map[string]any)
		wantOutcome      requestOutcome
		wantUsage        uint64
		wantFailurePhase requestFailurePhase
	}{
		{
			name:             "upstream terminal failure",
			response:         failedResponse,
			mutate:           func(request map[string]any) { request["stream"] = true },
			wantOutcome:      requestOutcomeFailed,
			wantUsage:        9,
			wantFailurePhase: requestFailureTerminalValidation,
		},
		{
			name: "non-2xx upstream response",
			response: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Status:     "503 Service Unavailable",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`)),
			},
			wantOutcome:      requestOutcomeFailed,
			wantFailurePhase: requestFailureTerminalValidation,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &serverFakeProvider{results: []serverForwardResult{{response: test.response}}}
			store := newMetricsStore("")
			var logOutput bytes.Buffer
			err := executeRequest(
				t.Context(), t.Context(), serverRequest(t, test.mutate), http.Header{}, "session",
				provider, io.Discard, newDiagnostics(&logOutput),
				serverLifecycleClock(0, 5*time.Millisecond, 25*time.Millisecond), nil, store,
			)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := store.snapshot()
			assertServerLifecycleFinished(t, snapshot.Requests)
			if got := snapshot.Total.InputTokens; got != test.wantUsage {
				t.Fatalf("usage = %d, want %d", got, test.wantUsage)
			}
			switch test.wantOutcome {
			case requestOutcomeFailed:
				if snapshot.Requests.Failed != 1 {
					t.Fatalf("lifecycle = %#v", snapshot.Requests)
				}
			}
			logs := logOutput.String()
			if strings.Count(logs, "Responses request finished") != 1 ||
				!strings.Contains(logs, "outcome="+test.wantOutcome.String()) ||
				!strings.Contains(logs, "failure_phase="+string(test.wantFailurePhase)) {
				t.Fatalf("terminal log = %q", logs)
			}
		})
	}
}

func TestExecuteRequestCancellationBeforeResponseLifecycle(t *testing.T) {
	started := make(chan struct{})
	provider := serverProviderFunc(func(startCtx, _ context.Context, _ []byte, _ http.Header, _ string) (*http.Response, error) {
		close(started)
		<-startCtx.Done()
		return nil, startCtx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	store := newMetricsStore("")
	var logOutput bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- executeRequest(
			ctx, ctx, serverRequest(t, nil), http.Header{}, "session", provider, io.Discard,
			newDiagnostics(&logOutput), serverLifecycleClock(0, 10*time.Millisecond, 30*time.Millisecond), nil, store,
		)
	}()
	<-started
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}

	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.CanceledBeforeResponse != 1 ||
		requests.TotalDurationMilliseconds != 30 ||
		requests.UpstreamDurationMilliseconds != 20 {
		t.Fatalf("cancellation lifecycle = %#v", requests)
	}
	if logs := logOutput.String(); strings.Count(logs, "Responses request finished") != 1 ||
		!strings.Contains(logs, "outcome=canceled_before_response") ||
		!strings.Contains(logs, "response_started=false") {
		t.Fatalf("terminal log = %q", logs)
	}
}

type serverBlockingSSEBody struct {
	ctx     context.Context
	content *strings.Reader
	blocked chan struct{}
	once    sync.Once
}

func (body *serverBlockingSSEBody) Read(content []byte) (int, error) {
	if body.content.Len() > 0 {
		return body.content.Read(content)
	}
	body.once.Do(func() { close(body.blocked) })
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*serverBlockingSSEBody) Close() error { return nil }

func TestCopySSETransformedKeepsMalformedStateInvalid(t *testing.T) {
	completed := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed"},
	})
	body := "data: {not-json}\n\n" +
		"event: response.completed\n" +
		"data: " + string(completed) + "\n\n"
	state, err := copySSETransformed(io.Discard, strings.NewReader(body), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state != responseTerminalInvalid {
		t.Fatalf("terminal state = %v, want invalid", state)
	}

	for _, test := range []struct {
		name              string
		current, observed responseTerminalState
	}{
		{name: "invalid then completed", current: responseTerminalInvalid, observed: responseTerminalCompleted},
		{name: "invalid then failed", current: responseTerminalInvalid, observed: responseTerminalFailed},
		{name: "completed then invalid", current: responseTerminalCompleted, observed: responseTerminalInvalid},
		{name: "failed then invalid", current: responseTerminalFailed, observed: responseTerminalInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mergeResponseTerminalState(test.current, test.observed); got != responseTerminalInvalid {
				t.Fatalf("mergeResponseTerminalState() = %v, want invalid", got)
			}
		})
	}
}

func TestExecuteRequestCancellationAfterResponseLifecycle(t *testing.T) {
	blocked := make(chan struct{})
	provider := serverProviderFunc(func(_, executionCtx context.Context, _ []byte, _ http.Header, _ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: &serverBlockingSSEBody{
				ctx: executionCtx, content: strings.NewReader("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"), blocked: blocked,
			},
		}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	store := newMetricsStore("")
	recorder := httptest.NewRecorder()
	writer := &trackedResponseWriter{ResponseWriter: recorder}
	var logOutput bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- executeRequest(
			ctx, ctx,
			serverRequest(t, func(request map[string]any) { request["stream"] = true }),
			http.Header{}, "session", provider, writer, newDiagnostics(&logOutput),
			serverLifecycleClock(0, 5*time.Millisecond, 35*time.Millisecond), nil, store,
		)
	}()
	<-blocked
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}

	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.CanceledAfterResponse != 1 ||
		requests.TotalDurationMilliseconds != 35 ||
		requests.UpstreamDurationMilliseconds != 30 {
		t.Fatalf("cancellation lifecycle = %#v", requests)
	}
	if logs := logOutput.String(); strings.Count(logs, "Responses request finished") != 1 ||
		!strings.Contains(logs, "outcome=canceled_after_response") ||
		!strings.Contains(logs, "response_started=true") {
		t.Fatalf("terminal log = %q", logs)
	}
}

type serverCancelAfterTerminalResponseWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
	once   sync.Once
}

func (writer *serverCancelAfterTerminalResponseWriter) Write(content []byte) (int, error) {
	written, err := writer.ResponseWriter.Write(content)
	if bytes.Contains(content, []byte(`"type":"response.completed"`)) {
		writer.once.Do(writer.cancel)
	}
	return written, err
}

func (writer *serverCancelAfterTerminalResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func TestExecuteRequestCompletesAtTerminalEventBeforeStreamEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	blocked := make(chan struct{})
	completed := mustTestJSON(t, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
			"usage":  map[string]any{"input_tokens": 7},
		},
	})
	provider := serverProviderFunc(func(_, executionCtx context.Context, _ []byte, _ http.Header, _ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: &serverBlockingSSEBody{
				ctx: executionCtx, content: strings.NewReader("event: response.completed\ndata: " + string(completed) + "\n\n"), blocked: blocked,
			},
		}, nil
	})
	recorder := httptest.NewRecorder()
	downstream := &serverCancelAfterTerminalResponseWriter{ResponseWriter: recorder, cancel: cancel}
	writer := &trackedResponseWriter{ResponseWriter: downstream}
	store := newMetricsStore("")
	var logOutput bytes.Buffer

	err := executeRequest(
		ctx, ctx,
		serverRequest(t, func(request map[string]any) { request["stream"] = true }),
		http.Header{}, "session", provider, writer, newDiagnostics(&logOutput),
		serverLifecycleClock(0, 5*time.Millisecond, 35*time.Millisecond), nil, store,
	)
	if err != nil {
		t.Fatalf("terminal response returned error: %v", err)
	}
	select {
	case <-blocked:
		t.Fatal("router read upstream after the terminal response event")
	default:
	}

	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.Completed != 1 || requests.CanceledAfterResponse != 0 || requests.UsageObserved != 1 {
		t.Fatalf("terminal response lifecycle = %#v", requests)
	}
	if logs := logOutput.String(); !strings.Contains(logs, "outcome=completed") ||
		!strings.Contains(logs, "upstream_terminal_state=completed") {
		t.Fatalf("terminal response log = %q", logs)
	}
}

func TestExecuteRequestResponseStartDeadlineLifecycle(t *testing.T) {
	started := make(chan struct{})
	provider := serverProviderFunc(func(startCtx, _ context.Context, _ []byte, _ http.Header, _ string) (*http.Response, error) {
		close(started)
		<-startCtx.Done()
		return nil, startCtx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	store := newMetricsStore("")
	result := make(chan error, 1)
	go func() {
		result <- executeRequest(
			ctx, t.Context(), serverRequest(t, nil), http.Header{}, "session", provider, io.Discard,
			newDiagnostics(io.Discard), serverLifecycleClock(0, 5*time.Millisecond, 25*time.Millisecond), nil, store,
		)
	}()
	<-started
	err := <-result
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline", err)
	}
	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.TimedOut != 1 || requests.Failed != 0 ||
		requests.CanceledBeforeResponse != 0 || requests.CanceledAfterResponse != 0 {
		t.Fatalf("deadline lifecycle = %#v", requests)
	}
}

func TestExecuteRequestIndependentUpstreamCancellationIsFailure(t *testing.T) {
	provider := serverProviderFunc(func(context.Context, context.Context, []byte, http.Header, string) (*http.Response, error) {
		return nil, context.Canceled
	})
	store := newMetricsStore("")
	var logOutput bytes.Buffer
	err := executeRequest(
		t.Context(), t.Context(), serverRequest(t, nil), http.Header{}, "session",
		provider, io.Discard, newDiagnostics(&logOutput),
		serverLifecycleClock(0, 5*time.Millisecond, 25*time.Millisecond), nil, store,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want wrapped upstream cancellation", err)
	}
	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.Failed != 1 || requests.CanceledBeforeResponse != 0 || requests.CanceledAfterResponse != 0 {
		t.Fatalf("independent upstream cancellation lifecycle = %#v", requests)
	}
	if logs := logOutput.String(); !strings.Contains(logs, "outcome=failed") {
		t.Fatalf("terminal log = %q", logs)
	}
}

type serverFailTerminalLogWriter struct {
	writes int
}

func (writer *serverFailTerminalLogWriter) Write(content []byte) (int, error) {
	writer.writes++
	if writer.writes == 2 {
		return 0, io.ErrClosedPipe
	}
	return len(content), nil
}

func TestExecuteRequestReturnsTerminalLogFailure(t *testing.T) {
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(`{"status":"completed"}`)}}}
	store := newMetricsStore("")
	logOutput := new(serverFailTerminalLogWriter)
	err := executeRequest(
		t.Context(), t.Context(), serverRequest(t, nil), http.Header{}, "session",
		provider, io.Discard, newDiagnostics(logOutput),
		serverLifecycleClock(0, 5*time.Millisecond, 25*time.Millisecond), nil, store,
	)
	if !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), "write terminal log") {
		t.Fatalf("error = %v, want terminal log failure", err)
	}
	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.Completed != 1 {
		t.Fatalf("request outcome changed by logging failure: %#v", requests)
	}
}

func TestExecuteRequestTransformFailureLifecycle(t *testing.T) {
	workspace := t.TempDir()
	item := testHPatchItem()
	item["type"] = "message"
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"output": []any{item},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(responseBody)}}}
	store := newMetricsStore("")
	var logOutput bytes.Buffer
	err := executeRequest(
		t.Context(), t.Context(), serverRequest(t, nil),
		serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}),
		"session", provider, io.Discard, newDiagnostics(&logOutput),
		serverLifecycleClock(0, 5*time.Millisecond, 25*time.Millisecond),
		newManagedHPatchProxy(t, testTranslator(t, new(int))), store,
	)
	if err == nil {
		t.Fatal("transform failure returned no error")
	}
	requests := store.snapshot().Requests
	assertServerLifecycleFinished(t, requests)
	if requests.Failed != 1 || requests.Active != 0 {
		t.Fatalf("transform lifecycle = %#v", requests)
	}
	if logs := logOutput.String(); strings.Count(logs, "Responses request finished") != 1 ||
		!strings.Contains(logs, "failure_phase=transform") {
		t.Fatalf("terminal log = %q", logs)
	}
}

const testProviderBaseURL = "https://provider.example"

type serverRoundTripper func(*http.Request) (*http.Response, error)

func (f serverRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestModelsHandlerForwardsCodexAuthenticationQueryAndResponse(t *testing.T) {
	httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != testProviderBaseURL+"/models?client_version=0.146.0" {
			t.Errorf("request = %s %s", request.Method, request.URL)
		}
		for name, want := range map[string]string{
			"Authorization":      "Bearer caller-token",
			"ChatGPT-Account-ID": "caller-account",
			"Accept":             "application/json",
			"Originator":         codexClientIdentity,
			"User-Agent":         codexClientIdentity,
		} {
			if got := request.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}
		responseHeaders := make(http.Header)
		responseHeaders.Set("Content-Type", "application/json")
		responseHeaders.Set("Cache-Control", "max-age=60")
		responseHeaders.Set("ETag", "catalog-version")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     responseHeaders,
			Body:       io.NopCloser(strings.NewReader(`{"models":[]}`)),
		}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.146.0", nil)
	request.Header = codexAuthHeaders()
	recorder := httptest.NewRecorder()

	modelsHandler(newProviderClient(testProviderBaseURL, httpClient))(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"models":[]}` {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	for name, want := range map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "max-age=60",
		"ETag":          "catalog-version",
	} {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("response header %s = %q, want %q", name, got, want)
		}
	}
}

func TestModelsHandlerRejectsUpstreamBodyReadFailure(t *testing.T) {
	httpClient := &http.Client{Transport: serverRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": []string{"max-age=60"}},
			Body: io.NopCloser(io.MultiReader(
				strings.NewReader(`{"models":[`),
				iotest.ErrReader(errors.New("upstream read failed")),
			)),
		}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header = codexAuthHeaders()
	recorder := httptest.NewRecorder()

	modelsHandler(newProviderClient(testProviderBaseURL, httpClient))(recorder, request)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "upstream read failed") {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `{"models":[`) {
		t.Fatalf("response exposes partial upstream body: %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Errorf("response Cache-Control = %q, want empty", got)
	}
}

func TestModelsHandlerRejectsOversizedUpstreamBody(t *testing.T) {
	httpClient := &http.Client{Transport: serverRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(io.LimitReader(serverRepeatingReader{}, maxModelsResponseBytes+1)),
		}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header = codexAuthHeaders()
	recorder := httptest.NewRecorder()

	modelsHandler(newProviderClient(testProviderBaseURL, httpClient))(recorder, request)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "too large") {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestModelsHandlerRejectsMissingAuthentication(t *testing.T) {
	var forwarded bool
	httpClient := &http.Client{Transport: serverRoundTripper(func(*http.Request) (*http.Response, error) {
		forwarded = true
		return serverHTTPResponse("{}"), nil
	})}
	recorder := httptest.NewRecorder()

	modelsHandler(newProviderClient(testProviderBaseURL, httpClient))(
		recorder,
		httptest.NewRequest(http.MethodGet, "/v1/models", nil),
	)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "Authorization") {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if forwarded {
		t.Fatal("unauthenticated models request reached upstream")
	}
}

//nolint:canonicalheader // Exact lowercase names match Codex's observed wire headers.
func TestProviderClientForwardsCodexAuthenticationAndRequestHeaders(t *testing.T) {
	headers := codexAuthHeaders()
	headers.Set("Originator", "caller-originator")
	headers.Set("User-Agent", "caller-agent")
	headers.Set("X-Unrelated-Caller-Data", "not-forwarded")

	headers.Add(sessionIDHeader, "session-primary")
	headers.Add(sessionIDHeader, "session-secondary")
	headers.Set(threadIDHeader, "thread")
	headers.Set(clientRequestIDHeader, "client-request")
	headers.Set(codexWindowIDHeader, "window:0")
	headers.Set(codexBetaFeaturesHeader, "feature")
	headers.Set(codexResponsesLiteHeader, "true")
	headers.Set(codexTurnMetadataHeader, "metadata")
	httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
		trusted := map[string]string{
			"Authorization":      "Bearer caller-token",
			"ChatGPT-Account-ID": "caller-account",
			"Originator":         codexClientIdentity,
			"User-Agent":         codexClientIdentity,
		}
		for name, value := range trusted {
			if got := request.Header.Get(name); got != value {
				t.Errorf("header %s = %q, want %q", name, got, value)
			}
		}
		for _, name := range []string{threadIDHeader, clientRequestIDHeader, codexWindowIDHeader, codexBetaFeaturesHeader, codexResponsesLiteHeader, codexTurnMetadataHeader} {
			if got, want := request.Header.Values(name), headers.Values(name); !slices.Equal(got, want) {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}
		if got := request.Header[codexSessionIDHeader]; !slices.Equal(got, []string{"cache-key"}) {
			t.Errorf("%s = %q, want cache key", codexSessionIDHeader, got)
		}
		if got := request.Header.Get(sessionIDHeader); got != "" {
			t.Errorf("%s = %q, want empty", sessionIDHeader, got)
		}
		if got := request.Header.Values("X-Unrelated-Caller-Data"); len(got) != 0 {
			t.Errorf("unrelated caller header was forwarded: %q", got)
		}
		return serverHTTPResponse("{}"), nil
	})}
	client := newProviderClient(testProviderBaseURL, httpClient)
	response, err := client.forwardExecution(t.Context(), t.Context(), []byte("{}"), headers, "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func codexAuthHeaders() http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer caller-token")
	headers.Set(chatGPTAccountIDHeader, "caller-account")
	return headers
}

func TestProviderClientRejectsMissingOrMalformedCodexAuthentication(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{"missing authorization", http.Header{chatGPTAccountIDHeader: []string{"account"}}, "Authorization"},
		{"multiple authorizations", http.Header{"Authorization": []string{"Bearer one", "Bearer two"}, chatGPTAccountIDHeader: []string{"account"}}, "Authorization"},
		{"unsupported scheme", http.Header{"Authorization": []string{"Basic token"}, chatGPTAccountIDHeader: []string{"account"}}, "invalid bearer"},
		{"empty token", http.Header{"Authorization": []string{"Bearer "}, chatGPTAccountIDHeader: []string{"account"}}, "invalid bearer"},
		{"missing account", http.Header{"Authorization": []string{"Bearer token"}}, "ChatGPT-Account-ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var forwarded bool
			httpClient := &http.Client{Transport: serverRoundTripper(func(*http.Request) (*http.Response, error) {
				forwarded = true
				return serverHTTPResponse("{}"), nil
			})}
			client := newProviderClient(testProviderBaseURL, httpClient)
			_, err := client.forwardExecution(t.Context(), t.Context(), []byte("{}"), test.headers, "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if forwarded {
				t.Fatal("request with invalid authentication reached upstream")
			}
		})
	}
}

func TestProviderClientOmitsUnsafeCodexCacheKey(t *testing.T) {
	for name, cacheKey := range map[string]string{
		"control byte": "cache\nkey",
		"too long":     strings.Repeat("x", maxSessionIDBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			var forwarded bool
			httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
				forwarded = true
				if values, ok := request.Header[codexSessionIDHeader]; ok {
					t.Errorf("%s = %q, want omitted", codexSessionIDHeader, values)
				}
				return serverHTTPResponse("{}"), nil
			})}
			client := newProviderClient(testProviderBaseURL, httpClient)
			response, err := client.forwardExecution(t.Context(), t.Context(), []byte("{}"), codexAuthHeaders(), cacheKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if !forwarded {
				t.Fatal("request did not reach upstream")
			}
		})
	}
}

//nolint:canonicalheader // Exact lowercase names match Codex's observed wire headers.
func TestProviderClientPreservesCodexRequestHeadersAcrossRetries(t *testing.T) {
	headers := codexAuthHeaders()
	headers.Set(sessionIDHeader, "session")
	headers.Set(threadIDHeader, "thread")
	headers.Set(clientRequestIDHeader, "client-request")
	headers.Set(codexWindowIDHeader, "window:0")
	headers.Set(codexBetaFeaturesHeader, "feature")
	headers.Set(codexResponsesLiteHeader, "true")
	headers.Set(codexTurnMetadataHeader, "metadata")
	var attempts []http.Header
	httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
		attempts = append(attempts, request.Header.Clone())
		if len(attempts) == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Retry-After": []string{"0"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"selected model is at capacity"}}`)),
			}, nil
		}
		return serverHTTPResponse("{}"), nil
	})}
	client := newProviderClient(testProviderBaseURL, httpClient)
	response, err := client.forwardExecution(t.Context(), t.Context(), []byte("{}"), headers, "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("upstream attempts = %d, want 2", len(attempts))
	}
	for attempt, forwarded := range attempts {
		if got := forwarded[codexSessionIDHeader]; !slices.Equal(got, []string{"cache-key"}) {
			t.Errorf("attempt %d %s = %q, want cache key", attempt+1, codexSessionIDHeader, got)
		}
		if got := forwarded.Get(sessionIDHeader); got != "" {
			t.Errorf("attempt %d %s = %q, want empty", attempt+1, sessionIDHeader, got)
		}
		for _, name := range []string{threadIDHeader, clientRequestIDHeader, codexWindowIDHeader, codexBetaFeaturesHeader, codexResponsesLiteHeader, codexTurnMetadataHeader} {
			if got, want := forwarded.Values(name), headers.Values(name); !slices.Equal(got, want) {
				t.Errorf("attempt %d header %s = %q, want %q", attempt+1, name, got, want)
			}
		}
	}
}

//nolint:canonicalheader // Exact lowercase names match Codex's observed wire headers.
func TestProviderClientCancelsRequestWithCodexRequestHeaders(t *testing.T) {
	headers := codexAuthHeaders()
	headers.Set(sessionIDHeader, "session")
	started := make(chan http.Header, 1)
	httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
		started <- request.Header.Clone()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	client := newProviderClient(testProviderBaseURL, httpClient)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := client.forwardExecution(ctx, ctx, []byte("{}"), headers, "cache-key")
		result <- err
	}()

	var forwarded http.Header
	select {
	case forwarded = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request did not start")
	}
	if got := forwarded[codexSessionIDHeader]; !slices.Equal(got, []string{"cache-key"}) {
		t.Fatalf("forwarded %s = %q, want cache key", codexSessionIDHeader, got)
	}
	if got := forwarded.Get(sessionIDHeader); got != "" {
		t.Fatalf("forwarded %s = %q, want empty", sessionIDHeader, got)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request error = %v, want context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request did not stop after cancellation")
	}
}

func TestCopyJSONTransformedRejectsOversizedResponse(t *testing.T) {
	_, err := copyJSONTransformed(io.Discard, io.LimitReader(serverRepeatingReader{}, maxUpstreamJSONBytes+1), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want response size rejection", err)
	}
}

func TestRoutingWorkspaceDetectsPathReplacement(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "workspace")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, ok := usableRoutingWorkspace(map[string]json.RawMessage{path: nil})
	if !ok {
		t.Fatal("usable workspace was rejected")
	}
	defer workspace.close()
	if !workspace.unchanged() {
		t.Fatal("fresh workspace is not unchanged")
	}
	if err := os.Rename(path, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if workspace.unchanged() {
		t.Fatal("replacement workspace retained the original identity")
	}
}

type cancelOnWrite struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (writer *cancelOnWrite) Write(content []byte) (int, error) {
	writer.once.Do(writer.cancel)
	return len(content), nil
}

func TestRunRejectsUnknownModeBeforeListening(t *testing.T) {
	err := Run(t.Context(), []string{"--mode", "unknown"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--mode must be hpatch or passthrough") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunReturnsCancellationAfterGracefulShutdown(t *testing.T) {
	if _, err := exec.LookPath(hpatchToolName); err != nil {
		t.Skipf("installed hpatch unavailable: %v", err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	writer := &cancelOnWrite{cancel: cancel}
	err := Run(ctx, []string{"--listen", "127.0.0.1:0"}, writer)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
}
