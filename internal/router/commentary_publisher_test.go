package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestCommentaryPublisherAuthenticatesAndDrainsLiveOrDeferred(t *testing.T) {
	broker := newCommentaryBroker()
	t.Cleanup(broker.close)
	server := httptest.NewServer(http.HandlerFunc(broker.serveHTTP))
	t.Cleanup(server.Close)

	live := broker.subscribe("session", "call-live", "")
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
	empty := broker.subscribe("session", "call-empty", "")
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

	deferred := broker.subscribe("session", "call-deferred", "")
	if deferred == "" {
		t.Fatal("deferred subscription was rejected")
	}
	sink.token = deferred
	if err := sink.Publish(t.Context(), "Running deferred work."); err != nil {
		t.Fatal(err)
	}
	events = broker.drainSession("session", "")
	if len(events) != 1 || events[0].callID != "call-deferred" || events[0].text != "Running deferred work." {
		t.Fatalf("deferred events = %+v", events)
	}

	if err := sink.Publish(t.Context(), "Still running deferred work."); err != nil {
		t.Fatal(err)
	}
	if err := sink.Complete(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := broker.drainSession("session", "")
	if len(next) != 1 || next[0].text != "Still running deferred work." || next[0].messageID == events[0].messageID {
		t.Fatalf("later deferred events = %+v", next)
	}
	if len(broker.drainSession("session", "")) != 0 || broker.publish(deferred, "after completion", false) {
		t.Fatal("completed route retained events or authorization")
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

func TestCommentaryDrainRetainsActiveCapacityUntilCompletionOrExpiry(t *testing.T) {
	for _, mode := range []string{"token", "session"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				broker := newCommentaryBroker()
				t.Cleanup(broker.close)
				drain := func(token string) []publishedCommentary {
					if mode == "token" {
						return broker.drain(token)
					}
					return broker.drainSession("session", "")
				}
				tokens := make([]string, maxCommentaryRoutes)
				for i := range tokens {
					tokens[i] = broker.subscribe("session", "call", "")
					if tokens[i] == "" || !broker.publish(tokens[i], "first", false) {
						t.Fatal("route capacity was not available")
					}
				}
				seen := make(map[string]bool)
				for _, token := range tokens {
					for _, event := range drain(token) {
						if seen[event.messageID] {
							t.Fatal("publication delivered twice")
						}
						seen[event.messageID] = true
					}
				}
				if len(seen) != maxCommentaryRoutes || broker.eventCount != 0 || broker.subscribe("session", "overflow", "") != "" {
					t.Fatalf("active drain: delivered=%d pending=%d routes=%d", len(seen), broker.eventCount, len(broker.routes))
				}
				if !broker.publish(tokens[0], "second", true) {
					t.Fatal("drain retired an active publisher")
				}
				events := drain(tokens[0])
				if len(events) != 1 || seen[events[0].messageID] || events[0].text != "second" || broker.publish(tokens[0], "late", false) {
					t.Fatalf("completed drain = %+v", events)
				}
				replacement := broker.subscribe("session", "replacement", "")
				if replacement == "" || !broker.publish(replacement, "pending expiry", false) {
					t.Fatal("completion did not release capacity")
				}
				time.Sleep(commentaryRouteTTL)
				if events := drain(replacement); len(events) != 0 || broker.eventCount != 0 || len(broker.routes) != 0 {
					t.Fatalf("expiry: events=%+v pending=%d routes=%d", events, broker.eventCount, len(broker.routes))
				}
				if broker.subscribe("session", "after-expiry", "") == "" {
					t.Fatal("expiry did not release route capacity")
				}
			})
		})
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

func TestConcurrentSessionDrainsOnlyOriginatingShellCommentary(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "terminal"}[terminal], func(t *testing.T) {
			proxy := newManagedMekugiProxy(t, testTranslator(t, new(int)))
			const sessionID = "session"
			for range 2 {
				if err := proxy.activateSession(sessionID); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { proxy.deactivateSession(sessionID) })
			}
			shell := proxy.commentary.subscribeThread(sessionID, "root", "")
			child := proxy.commentary.subscribeThread(sessionID, "child", "/root/child")
			call := proxy.commentary.subscribe(sessionID, "code-mode-call", "")
			for token, text := range map[string]string{shell: "root progress", child: "child progress", call: "call progress"} {
				if token == "" || !proxy.commentary.publish(token, text, false) {
					t.Fatal("commentary was not published")
				}
			}
			drain := proxy.drainCommentarySession
			if terminal {
				drain = proxy.drainThreadCommentarySession
			}
			// Concurrent responses for the same thread compete atomically. Other
			// threads and call-scoped deferred publications cannot be consumed.
			results := make(chan []publishedCommentary, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() { results <- drain(sessionID, "root") })
			}
			workers.Wait()
			close(results)
			var events []publishedCommentary
			for result := range results {
				events = append(events, result...)
			}
			if len(events) != 1 || events[0].text != "root progress" {
				t.Fatalf("concurrent root delivery = %+v", events)
			}
			if events := drain(sessionID, "child"); len(events) != 1 || !strings.Contains(events[0].text, "child progress") {
				t.Fatalf("child delivery = %+v", events)
			}
			if !proxy.commentary.publish(shell, "later root progress", false) {
				t.Fatal("drain retired the shell publisher")
			}
			if events := drain(sessionID, "root"); len(events) != 1 || events[0].text != "later root progress" {
				t.Fatalf("later delivery = %+v", events)
			}
			proxy.deactivateSession(sessionID)
			if events := proxy.drainCommentarySession(sessionID, "root"); len(events) != 1 || events[0].text != "call progress" {
				t.Fatalf("non-concurrent call delivery = %+v", events)
			}
		})
	}
}

func TestShellRouteKeepsCleanCommandWithoutDefaultCommentary(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "ordinary shell", input: "printf ok"},
		{name: "commentary", input: "commentary Running check\nprintf ok"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transform, proxy, _, _ := newMekugiTestTransform(t, testTranslator(t, new(int)))
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
			var args struct {
				Command string `json:"cmd"`
			}
			decodeExecCarrierArguments(t, carrier, &args)
			want := "shell bash " + shellQuoteArgument(test.input)
			if args.Command != want {
				t.Fatalf("command = %q, want %q", args.Command, want)
			}

			if strings.Contains(carrier, "--commentary-") || strings.Contains(carrier, proxy.commentaryEndpoint) || len(transform.commentarySubscriptions) != 0 {
				t.Fatalf("shell carrier = %s", carrier)
			}
		})
	}
}

func TestShellCommentaryPreservesDirectCommands(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "code mode"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			var transform *mekugiResponseTransform
			var proxy *mekugiProxy
			if native {
				proxy = newManagedMekugiProxy(t, testTranslator(t, new(int)))
				transform, _ = newNativeMekugiTestTransformWithProxy(t, proxy)
			} else {
				transform, proxy, _, _ = newMekugiTestTransform(t, testTranslator(t, new(int)))
			}
			proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
			const command = "mktemp -d -t mekugi-shell.XXXXXXXXXX"
			response, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": "completed", "output": []any{map[string]any{
					"type": "custom_tool_call", "id": "item-shell", "call_id": "call-shell",
					"name": "shell", "input": command, "status": "completed",
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Output []map[string]json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Output) != 1 {
				t.Fatalf("output count = %d, want 1", len(decoded.Output))
			}
			var arguments struct {
				Command string `json:"cmd"`
			}
			if native {
				if err := json.Unmarshal([]byte(jsonString(decoded.Output[0], "arguments")), &arguments); err != nil {
					t.Fatal(err)
				}
			} else {
				decodeExecCarrierArguments(t, jsonString(decoded.Output[0], "input"), &arguments)
			}
			visibleCommand := arguments.Command
			if native {
				// Native carriers append the existing script-retention result metadata.
				visibleCommand, _, _ = strings.Cut(visibleCommand, "\n")
			}
			if visibleCommand != command {
				t.Fatal("commentary changed the direct command")
			}
			if len(transform.commentarySubscriptions) != 0 {
				t.Fatal("direct command allocated a commentary subscription")
			}
		})
	}
}

func shellCommentaryTestItem() map[string]any {
	return map[string]any{
		"type": "custom_tool_call", "id": "item-runtime", "call_id": "call-runtime",
		"name": "exec", "input": `await commentary("Working");`, "status": "completed",
	}
}

func newRuntimeCommentaryTransform(t *testing.T) (*mekugiResponseTransform, *mekugiProxy) {
	t.Helper()
	transform, proxy, _, _ := newMekugiTestTransform(t, testTranslator(t, new(int)))
	proxy.commentaryEndpoint = "http://127.0.0.1:8080" + commentaryPublisherPath
	return transform, proxy
}

func runtimeCommentaryToken(t *testing.T, transform *mekugiResponseTransform) string {
	t.Helper()
	if len(transform.commentarySubscriptions) != 1 {
		t.Fatalf("commentary subscription count = %d", len(transform.commentarySubscriptions))
	}
	return transform.commentarySubscriptions[0].token
}

func TestReadyRuntimeCommentaryPrecedesEveryStreamTerminal(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			transform, proxy := newRuntimeCommentaryTransform(t)
			carrier, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
				"type": "response.output_item.done", "item": shellCommentaryTestItem(),
			}))
			if err != nil || len(carrier) != 1 || !bytes.Contains(carrier[0], []byte(`"name":"exec"`)) {
				t.Fatalf("shell carrier = %s, %v", carrier, err)
			}
			token := runtimeCommentaryToken(t, transform)
			if !proxy.commentary.publish(token, "Running streamed work.", false) {
				t.Fatal("runtime commentary was not published")
			}
			beforeBytes := proxy.historyBytes
			payload := mustTestJSON(t, map[string]any{
				"type":     "response." + status,
				"response": map[string]any{"status": status, "output": []any{}},
			})
			events, err := transform.TransformSSE(payload)
			if err != nil || len(events) != 2 || !bytes.Contains(events[0], []byte("Running streamed work.")) ||
				!bytes.Contains(events[1], []byte(`"type":"response.`+status+`"`)) {
				t.Fatalf("terminal events = %s, %v", events, err)
			}
			history, exists := proxy.history(transform.historySessionID, "call-runtime")
			if !exists || len(history.commentaryMessageIDs) != 1 || proxy.historyBytes-beforeBytes != len(history.commentaryMessageIDs[0]) {
				t.Fatalf("history = %+v, bytes before = %d, after = %d", history, beforeBytes, proxy.historyBytes)
			}
			transform.Close()
			if !proxy.commentary.publish(token, "Later work.", false) {
				t.Fatal("terminal response retired an active publisher")
			}
			deferred := proxy.drainCommentarySession(transform.historySessionID, transform.shellThreadID)
			if len(deferred) != 1 || deferred[0].text != "Later work." || deferred[0].messageID == history.commentaryMessageIDs[0] {
				t.Fatalf("deferred events = %+v", deferred)
			}
			if len(proxy.drainCommentarySession(transform.historySessionID, transform.shellThreadID)) != 0 {
				t.Fatal("publication delivered twice")
			}
			if !proxy.commentary.publish(token, "", true) || proxy.commentary.publish(token, "after completion", false) {
				t.Fatal("publisher completion did not retire drained route")
			}
		})
	}
}

func TestJSONTerminalHandsOffRuntimePublisher(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			transform, proxy := newRuntimeCommentaryTransform(t)
			payload, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
				"status": status, "output": []any{shellCommentaryTestItem()},
			}))
			if err != nil || !bytes.Contains(payload, []byte(`"name":"exec"`)) {
				t.Fatalf("JSON carrier = %s, %v", payload, err)
			}
			token := runtimeCommentaryToken(t, transform)
			transform.Close()
			if !proxy.commentary.publish(token, "Deferred JSON work.", true) {
				t.Fatal("JSON terminal cancelled handed-off publisher")
			}
			if events := proxy.drainCommentarySession(transform.historySessionID, transform.shellThreadID); len(events) != 1 || events[0].text != "Deferred JSON work." {
				t.Fatalf("deferred JSON events = %+v", events)
			}
		})
	}
}

func TestEarlyStreamReleasePreservesHandedOffPublishers(t *testing.T) {
	for _, name := range []string{"exec"} {
		t.Run(name, func(t *testing.T) {
			transform, proxy := newRuntimeCommentaryTransform(t)
			item := shellCommentaryTestItem()
			item["name"], item["input"], item["status"] = name, "", "in_progress"
			if _, err := transform.TransformSSE(mustTestJSON(t, map[string]any{"type": "response.output_item.added", "item": item})); err != nil {
				t.Fatal(err)
			}
			input := "printf ok"
			if name == "exec" {
				input = `await commentary("Working");`
			}
			events, err := transform.TransformSSE(mustTestJSON(t, map[string]any{
				"type": "response.custom_tool_call_input.done", "item_id": "item-runtime", "input": input,
			}))
			if err != nil || len(events) == 0 || !bytes.Contains(bytes.Join(events, nil), []byte("response.custom_tool_call_input.done")) {
				t.Fatalf("carrier input handoff = %s, %v", events, err)
			}
			token := runtimeCommentaryToken(t, transform)
			transform.Close() // A disconnect can occur before item.done or a terminal.
			if !proxy.commentary.publish(token, "Work after disconnect.", true) {
				t.Fatal("disconnect cancelled an emitted carrier publisher")
			}
			if events := proxy.drainCommentarySession(transform.historySessionID, transform.shellThreadID); len(events) != 1 || events[0].text != "Work after disconnect." {
				t.Fatalf("deferred disconnected events = %+v", events)
			}
			if _, exists := proxy.history(transform.historySessionID, "call-runtime"); !exists {
				t.Fatal("emitted carrier has no replay history for deferred commentary")
			}
		})
	}
}

func TestUnhandedRuntimeCommentaryRouteIsCancelled(t *testing.T) {
	transform, proxy := newRuntimeCommentaryTransform(t)
	_, err := transform.TransformJSON(mustTestJSON(t, map[string]any{
		"status": "completed", "output": []any{
			shellCommentaryTestItem(),
			map[string]any{"type": "custom_tool_call", "name": mekugiToolName, "input": testMekugiScript},
		},
	}))
	if err == nil {
		t.Fatal("malformed later call did not prevent JSON handoff")
	}
	token := runtimeCommentaryToken(t, transform)
	if !proxy.commentary.publish(token, "Unhanded work.", false) {
		t.Fatal("prepared route was not registered")
	}
	transform.Close()
	if proxy.commentary.publish(token, "later", false) || len(proxy.drainCommentarySession(transform.historySessionID, transform.shellThreadID)) != 0 {
		t.Fatal("unhanded route or its queued publication was retained")
	}
}
