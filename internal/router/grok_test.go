package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func grokTestRequest(t *testing.T, stream bool) []byte {
	t.Helper()
	return mustTestJSON(t, map[string]any{"model": grokModel, "stream": stream, "instructions": "test instructions", "reasoning": map[string]string{"effort": "low"}, "tools": []any{map[string]any{"type": "custom", "name": "exec", "description": "Run exact input"}}, "input": []any{map[string]any{"type": "agent_message", "author": "/root", "recipient": "/root/probe", "content": []any{map[string]string{"type": "input_text", "text": "Run the probe"}}}}})
}
func grokTestHeaders() http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer codex-secret")
	headers.Set(chatGPTAccountIDHeader, "codex-account")
	headers.Set(openAISubagentHeader, "collab_spawn")
	headers.Set(threadIDHeader, "child-thread")
	headers.Set(codexTurnMetadataHeader, `{"subagent_kind":"thread_spawn","request_kind":"regular"}`)
	return headers
}
func grokTestSSE(chunks ...any) string {
	var out strings.Builder
	for _, chunk := range chunks {
		fmt.Fprintf(&out, "data: %s\n\n", mustMarshalJSON(chunk))
	}
	out.WriteString("data: [DONE]\n\n")
	return out.String()
}
func grokTextStream() string {
	return grokTestSSE(map[string]any{"model": "grok-4.6-build", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "GROK_OK"}, "finish_reason": "stop"}}}, map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 4, "prompt_tokens_details": map[string]int{"cached_tokens": 3}, "completion_tokens_details": map[string]int{"reasoning_tokens": 2}}})
}

func TestGrokTranslationPreservesToolsHistoryAndImages(t *testing.T) {
	body := grokTestRequest(t, true)
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	request["input"] = []any{
		map[string]any{"type": "message", "role": "developer", "content": "rules"},
		map[string]any{"type": "agent_message", "content": []any{map[string]string{"type": "input_text", "text": "task"}}},
		map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "exec", "input": "text(1)"},
		map[string]any{"type": "function_call", "call_id": "c2", "namespace": "other", "name": "lookup", "arguments": "{}"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": []any{map[string]string{"type": "input_text", "text": "image"}, map[string]string{"type": "input_image", "image_url": "data:image/png;base64,AA=="}}},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": "result"},
	}
	tr, err := translateGrokRequest(mustTestJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	messages := tr.body["messages"].([]map[string]any)
	if messages[1]["role"] != "system" || len(messages[3]["tool_calls"].([]any)) != 2 {
		t.Fatalf("messages=%v", messages)
	}
	if messages[4]["role"] != "tool" || messages[4]["tool_call_id"] != "c1" || len(messages[4]["content"].([]any)) != 2 {
		t.Fatalf("tool image=%v", messages[4])
	}
	if tr.body["reasoning_effort"] != "low" {
		t.Fatal("reasoning lost")
	}
	for _, kind := range []string{"encrypted_content", "input_file"} {
		request["input"] = []any{map[string]any{"type": "agent_message", "content": []any{map[string]string{"type": kind, "encrypted_content": "opaque"}}}}
		if _, err := translateGrokRequest(mustTestJSON(t, request)); err == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
	request["input"] = []any{map[string]string{"type": "reasoning", "encrypted_content": "opaque"}}
	if _, err := translateGrokRequest(mustTestJSON(t, request)); err == nil {
		t.Fatal("accepted encrypted reasoning")
	}
}

func TestGrokStreamingCustomCallsAndUsage(t *testing.T) {
	tr, err := translateGrokRequest(grokTestRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	wire := grokToolName("", "exec")
	call := func(args string) any {
		return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c1", "function": map[string]string{"name": wire, "arguments": args}}}}}}}
	}
	stream := grokTestSSE(call(`{"input":"text(1)"}`), map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}})
	var events []map[string]any
	result, err := tr.readGrokStream(strings.NewReader(stream), func(e map[string]any) error { events = append(events, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	output := result["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || item["input"] != "text(1)" || item["call_id"] != "c1" {
		t.Fatalf("item=%v", item)
	}
	if events[1]["type"] != "response.in_progress" {
		t.Fatal("no progress while buffering arguments")
	}
	for _, bad := range []string{strings.TrimSuffix(stream, "data: [DONE]\n\n"), grokTestSSE(call(`{"bad":1}`), map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls"}}})} {
		var executed bool
		_, err := tr.readGrokStream(strings.NewReader(bad), func(e map[string]any) error {
			if e["type"] == "response.output_item.done" {
				executed = true
			}
			return nil
		})
		if err == nil || executed {
			t.Fatalf("bad stream exposed tool: err=%v executed=%v", err, executed)
		}
	}
	result, err = tr.readGrokStream(strings.NewReader(grokTextStream()), func(map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	usage := result["usage"].(map[string]any)
	if usage["input_tokens"] != int64(10) || usage["output_tokens"] != int64(6) {
		t.Fatalf("usage=%v", usage)
	}
}

func TestGrokReasoningUsage(t *testing.T) {
	for _, fields := range []string{
		`"completion_tokens_details":{"reasoning_tokens":94}`,
		`"reasoning_tokens":94`,
		`"reasoning_tokens":94,"completion_tokens_details":{"reasoning_tokens":94}`,
	} {
		tr, err := translateGrokRequest(grokTestRequest(t, true))
		if err != nil {
			t.Fatal(err)
		}
		stream := `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,` + fields + "}}\n\ndata: [DONE]\n\n"
		result, err := tr.readGrokStream(strings.NewReader(stream), func(map[string]any) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		usage := result["usage"].(map[string]any)
		if usage["output_tokens"] != int64(103) || usage["total_tokens"] != int64(135) || usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != int64(94) {
			t.Fatalf("usage=%v", usage)
		}
	}
}

func TestGrokClientIsolationStreamingJSONAndCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer xai-test" || r.Header.Get(chatGPTAccountIDHeader) != "" || r.Header.Get(openAISubagentHeader) != "" {
					t.Errorf("credential boundary violated")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, grokTextStream())
			}))
			defer server.Close()
			client := &grokClient{httpClient: grokTestHTTPClient(t, server), auth: newGrokAuth("", "xai-test"), streamIdleTimeout: time.Second}
			response, err := client.forwardExecution(t.Context(), t.Context(), grokTestRequest(t, stream), grokTestHeaders())
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("GROK_OK")) || !bytes.Contains(data, []byte("cached_tokens")) {
				t.Fatalf("body=%s", data)
			}
			headers := grokTestHeaders()
			headers.Del(openAISubagentHeader)
			if _, err := client.forwardExecution(t.Context(), t.Context(), grokTestRequest(t, true), headers); err == nil {
				t.Fatal("accepted root")
			}
			if calls.Load() != 1 {
				t.Fatal("forwarded rejected request")
			}
		})
	}
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	client := &grokClient{httpClient: grokTestHTTPClient(t, server), auth: newGrokAuth("", "xai-test")}
	ctx, cancel := context.WithCancel(t.Context())
	response, err := client.forwardExecution(ctx, ctx, grokTestRequest(t, true), grokTestHeaders())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach provider")
	}
}

type grokTestTransport func(*http.Request) (*http.Response, error)

func (f grokTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func grokWriteTestAuth(t *testing.T, path string, expiry time.Time) {
	t.Helper()
	entry := map[string]any{"auth_mode": "oidc", "oidc_issuer": grokIssuer, "oidc_client_id": grokOAuthClientID, "key": "old-token", "refresh_token": "old-refresh", "expires_at": expiry.Format(time.RFC3339Nano), "unrelated": "keep"}
	if err := os.WriteFile(path, mustTestJSON(t, map[string]any{grokIssuer + "::" + grokOAuthClientID: entry, "other-account": map[string]string{"key": "unrelated"}}), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestGrokOAuthRefreshSerializesAndPreservesStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	now := time.Now().UTC()
	grokWriteTestAuth(t, path, now.Add(-time.Minute))
	var exchanges atomic.Int32
	transport := grokTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Error("inference authorization leaked into OAuth")
		}
		if r.Method == "GET" {
			return serverHTTPResponse(`{"issuer":"https://auth.x.ai","token_endpoint":"https://auth.x.ai/token"}`), nil
		}
		exchanges.Add(1)
		r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_id") != grokOAuthClientID {
			t.Error("bad refresh form")
		}
		return serverHTTPResponse(`{"access_token":"fresh-token","refresh_token":"rotated-refresh","expires_in":3600}`), nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			a := newGrokAuth(path, "")
			a.httpClient.Transport = transport
			c, err := a.credentials(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if c.headers.Get("Authorization") != "Bearer fresh-token" {
				t.Error("stale token")
			}
		})
	}
	wg.Wait()
	if exchanges.Load() != 1 {
		t.Fatalf("refreshes=%d", exchanges.Load())
	}
	a := newGrokAuth(path, "")
	store, entry, err := a.read()
	if err != nil {
		t.Fatal(err)
	}
	if jsonString(entry, "refresh_token") != "rotated-refresh" || jsonString(entry, "unrelated") != "keep" || store["other-account"] == nil {
		t.Fatal("store fields lost")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe credential permissions")
	}
}

func TestGrokOAuthRejectsUntrustedDiscovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	grokWriteTestAuth(t, path, time.Now().Add(-time.Hour))
	a := newGrokAuth(path, "")
	var calls int
	a.httpClient.Transport = grokTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return serverHTTPResponse(`{"issuer":"https://auth.x.ai","token_endpoint":"https://attacker.invalid/token"}`), nil
	})
	if _, err := a.credentials(t.Context()); err == nil {
		t.Fatal("accepted credential exfiltration endpoint")
	}
	if calls != 1 {
		t.Fatal("sent refresh token to untrusted endpoint")
	}
}

func TestGrokUnauthorizedRefreshRetriesOnceWithoutCredentialLeak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	grokWriteTestAuth(t, path, time.Now().Add(time.Hour))
	auth := newGrokAuth(path, "")
	auth.httpClient.Transport = grokTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return serverHTTPResponse(`{"issuer":"https://auth.x.ai","token_endpoint":"https://auth.x.ai/token"}`), nil
		}
		return serverHTTPResponse(`{"access_token":"new-token","refresh_token":"new-refresh","expires_in":3600}`), nil
	})
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get(chatGPTAccountIDHeader) != "" {
			t.Error("leaked Codex account")
		}
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, "sensitive upstream body")
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-token" {
			t.Error("did not refresh access token")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, grokTextStream())
	}))
	defer server.Close()
	client := &grokClient{httpClient: grokTestHTTPClient(t, server), auth: auth}
	response, err := client.forwardExecution(t.Context(), t.Context(), grokTestRequest(t, false), grokTestHeaders())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Contains(data, []byte("GROK_OK")) || calls != 2 {
		t.Fatalf("retry calls=%d err=%v", calls, err)
	}
}

func TestGrokDisabledAndUnknownModelFailBeforeProvider(t *testing.T) {
	provider := newProviderClient("http://127.0.0.1:1", nil)
	_, err := provider.forwardExecution(t.Context(), t.Context(), grokTestRequest(t, true), grokTestHeaders(), "")
	if err == nil || !strings.Contains(err.Error(), "--grok") {
		t.Fatalf("disabled route=%v", err)
	}
	body := bytes.Replace(grokTestRequest(t, true), []byte(grokModel), []byte("grok:unknown"), 1)
	if _, err := translateGrokRequest(body); err == nil {
		t.Fatal("accepted unknown model")
	}
}

func grokTestHTTPClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	transport := client.Transport
	client.Transport = grokTestTransport(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme = endpoint.Scheme
		request.URL.Host = endpoint.Host
		request.Host = endpoint.Host
		return transport.RoundTrip(request)
	})
	return client
}

func TestGrokNullCustomInputIsNotExecutable(t *testing.T) {
	tr, err := translateGrokRequest(grokTestRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	stream := grokTestSSE(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c", "function": map[string]string{"name": "exec", "arguments": `{"input":null}`}}}}, "finish_reason": "tool_calls"}}})
	executable := false
	_, err = tr.readGrokStream(strings.NewReader(stream), func(event map[string]any) error {
		if event["type"] == "response.output_item.done" {
			executable = true
		}
		return nil
	})
	if err == nil || executable {
		t.Fatalf("null input accepted: err=%v executable=%v", err, executable)
	}
}
