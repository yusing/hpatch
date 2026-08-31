package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCommentaryPublisherAuthenticatesAndDrainsLiveOrDeferred(t *testing.T) {
	broker := newCommentaryBroker()
	t.Cleanup(broker.close)
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)

	live := broker.subscribe("session", "call-live")
	if live == "" {
		t.Fatal("live subscription was rejected")
	}
	sink := &httpShellCommentarySink{endpoint: server.URL, token: live, client: server.Client()}
	if err := sink.Publish(t.Context(), "Running live work."); err != nil {
		t.Fatal(err)
	}
	if err := sink.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	events := broker.drain(live)
	if len(events) != 1 || events[0].callID != "call-live" || events[0].text != "Running live work." {
		t.Fatalf("live events = %+v", events)
	}
	empty := broker.subscribe("session", "call-empty")
	if empty == "" {
		t.Fatal("empty subscription was rejected")
	}
	sink.token = empty
	if err := sink.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	broker.mu.Lock()
	_, emptyRetained := broker.routes[empty]
	broker.mu.Unlock()
	if emptyRetained {
		t.Fatal("completed route without publications was retained")
	}

	deferred := broker.subscribe("session", "call-deferred")
	if deferred == "" {
		t.Fatal("deferred subscription was rejected")
	}
	sink.token = deferred
	if err := sink.Publish(t.Context(), "Running deferred work."); err != nil {
		t.Fatal(err)
	}
	events = broker.drainSession("session")
	if len(events) != 1 || events[0].callID != "call-deferred" || events[0].text != "Running deferred work." {
		t.Fatalf("deferred events = %+v", events)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.StatusCode)
	}
}

func TestPublishCommentaryOnceIgnoresPublicationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	handled, err := publishCommentaryOnce(t.Context(), []string{
		commentaryOnceArgument, server.URL, "token", "Running%20work.",
	})
	if !handled || err != nil {
		t.Fatalf("handled = %v, error = %v", handled, err)
	}
	if handled, err := publishCommentaryOnce(t.Context(), []string{
		commentaryOnceArgument, server.URL, "token", "%zz",
	}); !handled || err == nil {
		t.Fatalf("invalid escape handled = %v, error = %v", handled, err)
	}
	if handled, err := publishCommentaryOnce(t.Context(), []string{"other"}); handled || err != nil {
		t.Fatalf("unrelated invocation handled = %v, error = %v", handled, err)
	}
}

func TestConcurrentSessionDoesNotDrainCommentary(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	const sessionID = "session"
	if err := proxy.activateSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if err := proxy.activateSession(sessionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		proxy.deactivateSession(sessionID)
	})

	subscription := proxy.commentary.subscribe(sessionID, "call")
	if subscription == "" || !proxy.commentary.publish(subscription, "Still running.", false) {
		t.Fatal("commentary was not published")
	}
	if events := proxy.drainCommentarySession(sessionID); len(events) != 0 {
		t.Fatalf("concurrent drain = %+v", events)
	}
	proxy.deactivateSession(sessionID)
	if events := proxy.drainCommentarySession(sessionID); len(events) != 1 || events[0].text != "Still running." {
		t.Fatalf("completed-turn drain = %+v", events)
	}
}

func TestShellRouteInstallsPublisherWithoutAddingDefaultCommentary(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         string
		wantPublisher bool
	}{
		{name: "ordinary shell", input: "printf ok", wantPublisher: true},
		{name: "commentary", input: "commentary Running check\nprintf ok", wantPublisher: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transform, proxy, _ := newToolPluginTestTransform(t)
			proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
			response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed", "output": []any{map[string]any{
					"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
					"name": "shell", "input": test.input, "status": "completed",
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Output []map[string]json.RawMessage `json:"output"`
			}
			if json.Unmarshal(response, &decoded) != nil || len(decoded.Output) != 1 {
				t.Fatalf("shell response = %s", response)
			}
			carrier := jsonString(decoded.Output[0], "input")
			if strings.Contains(carrier, commentaryEndpointArgument) != test.wantPublisher {
				t.Fatalf("shell carrier = %s", carrier)
			}
		})
	}
}

func TestShellWorkerPublishesWithoutChangingCommandResult(t *testing.T) {
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
	subscription := broker.subscribe("session", "call-shell")
	if subscription == "" {
		t.Fatal("shell subscription was rejected")
	}
	arguments := []string{
		commentaryEndpointArgument, server.URL, commentaryTokenArgument, subscription,
		"bash", "cmd=commentary\n\"$cmd\" Running check\nfalse\necho continued",
	}
	var stdout, stderr bytes.Buffer
	handled, exitCode := RunToolPluginWorker(t.Context(), registry.shellRuntime, arguments, os.Stdin, &stdout, &stderr)
	if !handled || exitCode != 0 || stdout.String() != "continued\n" || stderr.Len() != 0 {
		t.Fatalf("handled %v, exit %d, stdout %q, stderr %q", handled, exitCode, stdout.String(), stderr.String())
	}
	events := broker.drain(subscription)
	if len(events) != 1 || events[0].text != "Running check" {
		t.Fatalf("shell events = %+v", events)
	}
}

func TestReadyRuntimeCommentaryPrecedesStreamCompletion(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch(transform.historySessionID, map[string]hpatchHistory{
		"call-runtime": {
			toolName: "shell", carrierName: "exec", carrierKind: codeModeCarrierCustom,
			carrierPayload: "payload", upstreamItem: map[string]json.RawMessage{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	subscription := proxy.commentary.subscribe(transform.historySessionID, "call-runtime")
	if subscription == "" || !proxy.commentary.publish(subscription, "Running streamed work.", false) {
		t.Fatal("runtime commentary was not published")
	}
	transform.commentaryTokens = []string{subscription}
	beforeBytes := proxy.historyBytes
	payload := mustTestJSON(t, map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"status": "completed", "output": []any{}},
	})
	events, err := transform.TransformSSE(payload)
	if err != nil || len(events) != 2 || !bytes.Contains(events[0], []byte("Running streamed work.")) ||
		!bytes.Contains(events[1], []byte(`"type":"response.completed"`)) {
		t.Fatalf("stream events = %q, error %v", events, err)
	}
	history, exists := proxy.history(transform.historySessionID, "call-runtime")
	if !exists || len(history.commentaryMessageIDs) != 1 ||
		proxy.historyBytes-beforeBytes != len(history.commentaryMessageIDs[0]) {
		t.Fatalf("history = %+v, bytes before = %d, after = %d", history, beforeBytes, proxy.historyBytes)
	}
}

func TestUnhandedRuntimeCommentaryRouteIsCancelled(t *testing.T) {
	transform, proxy, _, _ := newHPatchTestTransform(t, testTranslator(t, new(int)))
	token := proxy.commentary.subscribe(transform.historySessionID, "call-runtime")
	if token == "" {
		t.Fatal("runtime commentary route was rejected")
	}
	transform.commentaryTokens = []string{token}
	transform.Close()
	proxy.commentary.mu.Lock()
	_, retained := proxy.commentary.routes[token]
	proxy.commentary.mu.Unlock()
	if retained {
		t.Fatal("unhanded commentary route was retained")
	}
}
