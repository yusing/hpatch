package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func compactionTestSocket(t *testing.T, ctx context.Context, compactor *contextCompactor, upstream http.Handler) *websocket.Conn {
	t.Helper()
	provider := httptest.NewServer(upstream)
	t.Cleanup(provider.Close)
	endpoint := responsesWebSocketHandler(ctx, 5*time.Second, newProviderClient(provider.URL, provider.Client()), nil, nil, nil, nil, compactor)
	t.Cleanup(endpoint.Close)
	router := httptest.NewServer(endpoint)
	t.Cleanup(router.Close)
	conn, _, err := websocket.Dial(ctx, router.URL, &websocket.DialOptions{HTTPHeader: codexAuthHeaders()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func compactionSocketCompletion(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]json.RawMessage {
	t.Helper()
	for {
		event := socketRead(t, ctx, conn)
		if jsonString(event, "type") == "error" {
			t.Fatalf("socket error: %s", mustMarshalJSON(event))
		}
		if jsonString(event, "type") == "response.completed" {
			var response map[string]json.RawMessage
			if err := json.Unmarshal(event["response"], &response); err != nil {
				t.Fatal(err)
			}
			return response
		}
	}
}

func TestCompactionWebSocketPreservesPendingSteering(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	startSuccessor := make(chan struct{})
	conn := compactionTestSocket(t, ctx, compactor, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.CloseNow()
		if _, err := providerSocketRead(ctx, upstream); err != nil {
			t.Error(err)
			return
		}
		if err := providerSocketWrite(ctx, upstream, socketEvent("response.created", "parent")); err != nil {
			t.Error(err)
			return
		}
		if _, err := providerSocketRead(ctx, upstream); err != nil {
			t.Error(err)
			return
		}
		if err := providerSocketWrite(ctx, upstream, map[string]any{
			"type":  "response.steer.accepted",
			"steer": map[string]string{"id": "steer", "previous_response_id": "parent"},
		}); err != nil {
			t.Error(err)
			return
		}
		if err := providerSocketWrite(ctx, upstream, socketEvent("response.completed", "parent")); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-startSuccessor:
		}
		// No explicit parent: the router must use the last provider response,
		// not the intervening local compact response.
		if err := providerSocketWrite(ctx, upstream, socketEvent("response.created", "successor")); err != nil {
			t.Error(err)
			return
		}
		if err := providerSocketWrite(ctx, upstream, socketEvent("response.completed", "successor")); err != nil {
			t.Error(err)
			return
		}
		<-ctx.Done()
	}))
	socketWrite(t, ctx, conn, map[string]any{"type": "response.create", "model": "gpt-5", "input": compactHTTPHistory()})
	if event := socketRead(t, ctx, conn); jsonString(event, "type") != "response.created" {
		t.Fatalf("expected response.created: %s", mustMarshalJSON(event))
	}
	direction := "Preserve this accepted direction across the local compact."
	socketWrite(t, ctx, conn, map[string]any{"type": "response.steer", "previous_response_id": "parent", "input": direction})
	compactionSocketCompletion(t, ctx, conn)
	compact := func(parent string) map[string]json.RawMessage {
		socketWrite(t, ctx, conn, map[string]any{
			"type": "response.create", "model": "gpt-5", "previous_response_id": parent,
			"input":           []any{map[string]string{"type": "compaction_trigger"}},
			"client_metadata": map[string]string{codexTurnMetadataHeader: `{"request_kind":"compaction","compaction":{"implementation":"responses_compaction_v2"}}`},
		})
		return compactionSocketCompletion(t, ctx, conn)
	}
	compact("parent")
	close(startSuccessor)
	if response := compactionSocketCompletion(t, ctx, conn); jsonString(response, "id") != "successor" {
		t.Fatalf("automatic successor was lost: %s", mustMarshalJSON(response))
	}
	response := compact("successor")
	var output []json.RawMessage
	if err := json.Unmarshal(response["output"], &output); err != nil {
		t.Fatal(err)
	}
	restored, err := compactor.restore(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(mustMarshalJSON(restored)), direction) != 1 {
		t.Fatalf("accepted steering was lost or duplicated: %s", mustMarshalJSON(restored))
	}
}
func TestCompactionWebSocketRoundTrip(t *testing.T) {
	for _, localV2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("websocket_compaction=%t", localV2), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			history := compactHTTPHistory()
			// Provider-owned encrypted state must remain byte-for-byte opaque.
			history = append(history, json.RawMessage(`{"type":"reasoning","encrypted_content":"provider-owned"}`))
			reduced := reduceContextCompaction(history)
			var calls atomic.Int32
			received := make(chan []json.RawMessage, 2)
			conn := compactionTestSocket(t, ctx, compactor, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				upstream, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer upstream.CloseNow()
				for i := range 2 {
					create, err := providerSocketRead(ctx, upstream)
					if err != nil {
						t.Error(err)
						return
					}
					if len(create["previous_response_id"]) != 0 || bytes.Contains(create["input"], []byte(contextCompactionPrefix)) {
						t.Errorf("local state escaped to provider: %s", mustMarshalJSON(create))
					}
					var input []json.RawMessage
					if err := json.Unmarshal(create["input"], &input); err != nil {
						t.Error(err)
						return
					}
					received <- input
					id := fmt.Sprintf("provider-%d", i)
					if err := providerSocketWrite(ctx, upstream, socketEvent("response.created", id)); err != nil {
						t.Error(err)
						return
					}
					if err := providerSocketWrite(ctx, upstream, socketEvent("response.completed", id)); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			var window []json.RawMessage
			parent := ""
			if localV2 {
				socketWrite(t, ctx, conn, map[string]any{
					"type": "response.create", "model": "gpt-5",
					"input":           append(history, json.RawMessage(`{"type":"compaction_trigger"}`)),
					"client_metadata": map[string]string{codexTurnMetadataHeader: `{"request_kind":"compaction","compaction":{"implementation":"responses_compaction_v2"}}`},
				})
				response := compactionSocketCompletion(t, ctx, conn)
				parent = jsonString(response, "id")
				if !strings.Contains(string(response["output"]), contextCompactionPrefix) || calls.Load() != 0 {
					t.Fatal("V2 did not complete locally")
				}
			} else {
				// A standalone HTTP compact followed by a new socket models resume.
				request := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": history})))
				response := httptest.NewRecorder()
				compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("compact reached provider") }))(response, request)
				var result struct {
					Output []json.RawMessage `json:"output"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
					t.Fatalf("compact failed: %s", response.Body.String())
				}
				window = result.Output
				// Reopen the installation-owned key instead of relying on process state.
				*compactor = contextCompactor{keyPath: compactor.keyPath}
			}
			followup := json.RawMessage(`{"type":"message","role":"user","content":"Continue after compaction."}`)
			create := map[string]any{"type": "response.create", "model": "gpt-5", "input": append(window, followup)}
			if parent != "" {
				create["previous_response_id"] = parent
			}
			socketWrite(t, ctx, conn, create)
			response := compactionSocketCompletion(t, ctx, conn)
			want := append(reduced, followup)
			if got := <-received; contextCompactionCanonicalJSON(mustMarshalJSON(got)) != contextCompactionCanonicalJSON(mustMarshalJSON(want)) {
				t.Fatalf("restoration mismatch:\ngot %s\nwant %s", mustMarshalJSON(got), mustMarshalJSON(want))
			}
			next := json.RawMessage(`{"type":"message","role":"user","content":"One more turn."}`)
			socketWrite(t, ctx, conn, map[string]any{"type": "response.create", "model": "gpt-5", "previous_response_id": jsonString(response, "id"), "input": []json.RawMessage{next}})
			compactionSocketCompletion(t, ctx, conn)
			want = append(want, next)
			if got := <-received; contextCompactionCanonicalJSON(mustMarshalJSON(got)) != contextCompactionCanonicalJSON(mustMarshalJSON(want)) {
				t.Fatalf("incremental restoration duplicated or lost history:\ngot %s\nwant %s", mustMarshalJSON(got), mustMarshalJSON(want))
			}
		})
	}
}

func TestCompactionWebSocketFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, input, metadata string
		status                int
	}{
		{"damaged", `[{"type":"compaction","encrypted_content":"mekugi\u002ecompaction\u002ev1:broken"}]`, "", 422},
		{"unknown_version", `[{"type":"compaction","encrypted_content":"mekugi.compaction.v999:broken"}]`, "", 422},
		{"unsupported", `[{"type":"message","role":"user","content":"Keep this."}]`, `{"request_kind":"compaction","compaction":{"implementation":"unknown"}}`, 422},
		{"no_reduction", `[{"type":"message","role":"user","content":"Keep this."}]`, `{"request_kind":"compaction","compaction":{"implementation":"responses_compaction_v2"}}`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
			var calls atomic.Int32
			conn := compactionTestSocket(t, ctx, compactor, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "must not reach provider", 500)
			}))
			socketWrite(t, ctx, conn, map[string]any{
				"type": "response.create", "model": "gpt-5", "input": json.RawMessage(tc.input),
				"client_metadata": map[string]string{codexTurnMetadataHeader: tc.metadata},
			})
			event := socketRead(t, ctx, conn)
			if jsonString(event, "type") != "error" || string(event["status"]) != fmt.Sprint(tc.status) || calls.Load() != 0 {
				t.Fatalf("not fail-closed: %s, provider calls %d", mustMarshalJSON(event), calls.Load())
			}
			if _, _, err := conn.Read(ctx); err == nil {
				t.Fatal("failed compaction left the WebSocket connection open")
			}
		})
	}
}
