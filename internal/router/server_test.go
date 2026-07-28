package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type serverForwardResult struct {
	response *http.Response
	err      error
}

type serverFakeProvider struct {
	results   []serverForwardResult
	forwarded [][]byte
}

func (f *serverFakeProvider) forwardExecution(_, _ context.Context, body []byte) (*http.Response, error) {
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

func serverHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
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
			err := executeRequest(t.Context(), t.Context(), serverRequest(t, test.mutate), test.headers, test.sessionID, provider, io.Discard, newDiagnostics(io.Discard), time.Now, newHPatchProxy(testTranslator(t, new(int))), newMetricsStore())
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
	response := serverHTTPResponse(http.StatusOK, responseBody)
	response.Header.Set("Content-Type", "text/event-stream")
	provider := &serverFakeProvider{results: []serverForwardResult{{response: response}}}
	proxy := newHPatchProxy(hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		t.Fatal("compaction reached the hpatch translator")
		return nil, nil
	}))
	var output bytes.Buffer
	err = executeRequest(t.Context(), t.Context(), parsed, serverCompactionMetadataHeaders(t), "session", provider, &output, newDiagnostics(io.Discard), time.Now, proxy, newMetricsStore())
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

func TestExecuteRequestForwardsRewrittenRequestAndRecordsUsage(t *testing.T) {
	workspace := t.TempDir()
	parsed := serverRequest(t, nil)
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage": map[string]any{
			"input_tokens": 10, "input_tokens_details": map[string]any{"cached_tokens": 4},
			"output_tokens": 6, "output_tokens_details": map[string]any{"reasoning_tokens": 2},
		},
		"future": map[string]any{"kept": true},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(http.StatusOK, responseBody)}}}
	proxy := newHPatchProxy(hpatchTranslatorFunc(func(context.Context, routingWorkspace, string) ([]byte, error) {
		t.Fatal("response without an hpatch call reached the translator")
		return nil, nil
	}))
	store := newMetricsStore()
	var output bytes.Buffer
	err := executeRequest(t.Context(), t.Context(), parsed, serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}), "session", provider, &output, newDiagnostics(io.Discard), time.Now, proxy, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(provider.forwarded))
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

type serverErrorWriter struct{ err error }

func (w serverErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExecuteRequestDoesNotRecordUsageWhenDeliveryFails(t *testing.T) {
	workspace := t.TempDir()
	responseBody := string(mustTestJSON(t, map[string]any{
		"status": "completed",
		"usage":  map[string]any{"input_tokens": 10},
	}))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(http.StatusOK, responseBody)}}}
	store := newMetricsStore()
	err := executeRequest(t.Context(), t.Context(), serverRequest(t, nil), serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil}), "session", provider, serverErrorWriter{err: io.ErrClosedPipe}, newDiagnostics(io.Discard), time.Now, newHPatchProxy(testTranslator(t, new(int))), store)
	if err == nil {
		t.Fatal("delivery failure returned no error")
	}
	if got := store.snapshot().Total; got != (tokenCounts{}) {
		t.Fatalf("failed delivery recorded metrics: %#v", got)
	}
}

type serverRepeatingReader struct{}

func (serverRepeatingReader) Read(content []byte) (int, error) {
	for index := range content {
		content[index] = 'x'
	}
	return len(content), nil
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

type serverProviderFunc func(context.Context, context.Context, []byte) (*http.Response, error)

func (f serverProviderFunc) forwardExecution(startCtx, responseCtx context.Context, body []byte) (*http.Response, error) {
	return f(startCtx, responseCtx, body)
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

func TestResponsesHandlerDoesNotLogClientCancellationAsOperationalEvent(t *testing.T) {
	upstreamEvent := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	upstreamCanceled := make(chan struct{})
	provider := serverProviderFunc(func(_, responseCtx context.Context, _ []byte) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: &serverCancelableSSEBody{
				ctx: responseCtx, content: strings.NewReader(upstreamEvent), canceled: upstreamCanceled,
			},
		}, nil
	})
	var logOutput bytes.Buffer
	handler := responsesHandler(t.Context(), time.Minute, provider, newDiagnostics(&logOutput), newHPatchProxy(nil), newMetricsStore(), new(atomic.Uint64))
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

type serverRoundTripper func(*http.Request) (*http.Response, error)

func (f serverRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestProviderClientDoesNotAcceptCallerCredentialOverrides(t *testing.T) {
	auth := authConfig{Token: "trusted-token", AccountID: "trusted-account", BaseURL: "https://provider.example"}
	httpClient := &http.Client{Transport: serverRoundTripper(func(request *http.Request) (*http.Response, error) {
		want := map[string]string{
			"Authorization":      "Bearer trusted-token",
			"ChatGPT-Account-ID": "trusted-account",
			"Originator":         codexClientIdentity,
			"User-Agent":         codexClientIdentity,
		}
		for name, value := range want {
			if got := request.Header.Get(name); got != value {
				t.Errorf("header %s = %q, want %q", name, got, value)
			}
		}
		return serverHTTPResponse(http.StatusOK, "{}"), nil
	})}
	client := newProviderClient(auth, httpClient)
	response, err := client.forwardExecution(t.Context(), t.Context(), []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
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

func TestRunReturnsCancellationAfterGracefulShutdown(t *testing.T) {
	if _, err := exec.LookPath(hpatchToolName); err != nil {
		t.Skipf("installed hpatch unavailable: %v", err)
	}
	codexHome := t.TempDir()
	auth := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"token","account_id":"account"}}`)
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	ctx, cancel := context.WithCancel(t.Context())
	writer := &cancelOnWrite{cancel: cancel}
	err := Run(ctx, []string{"--listen", "127.0.0.1:0"}, writer)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
}
