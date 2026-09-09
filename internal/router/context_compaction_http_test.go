package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func compactHTTPHistory() []json.RawMessage {
	return []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Fix only the router. Do not commit."}}}),
		compactTestCall("tests", "go test -v ./internal/router"),
		compactTestOutput("tests", strings.Repeat("=== RUN   TestRoute\n--- PASS: TestRoute (0.01s)\n", 50)+"PASS\nok  example/router 0.1s\n", 0),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Keep the failed test diagnostics too."}}}),
		compactTestCall("last", "go test ./..."),
		compactTestOutput("last", "failure: keep this evidence", 1),
	}
}

func TestCompactionHTTPDoesNotInterpretTextAndBlocksEscapedEnvelopes(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	calls := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })
	for _, body := range []string{
		`{"model":"gpt-5","input":"Explain hpatch.compaction.v1: please."}`,
		`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"cmp_hpatch_example"}]}`,
	} {
		response := httptest.NewRecorder()
		compactor.handler(next)(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatal("ordinary text was interpreted as a capsule")
		}
	}
	body := `{"model":"gpt-5","input":[{"type":"compaction","encrypted_content":"hpatch\u002ecompaction\u002ev1:broken"}]}`
	response := httptest.NewRecorder()
	compactor.handler(next)(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if response.Code < 400 || calls != 2 {
		t.Fatal("escaped local envelope was forwarded")
	}
}
func TestCompactionHTTPProviderFreeRoundTrip(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	calls := 0
	var delivered []json.RawMessage
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(fields["input"], &delivered)
		if strings.Contains(string(body), contextCompactionPrefix) {
			t.Fatal("local envelope escaped to provider")
		}
		if jsonString(fields, "instructions") != "current instructions" {
			t.Fatal("current instructions changed")
		}
	})
	input := compactHTTPHistory()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input}))))
	response := httptest.NewRecorder()
	compactor.handler(next)(response, request)
	if response.Code != http.StatusOK || calls != 0 {
		t.Fatalf("compaction = %d, provider calls = %d: %s", response.Code, calls, response.Body.String())
	}
	var compacted struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &compacted); err != nil {
		t.Fatal(err)
	}
	// Model the legacy client retaining user items, injecting fresh context,
	// and appending a later user request. Restoration must keep each once.
	fresh := mustMarshalJSON(map[string]any{"type": "message", "role": "developer", "content": []any{map[string]string{"type": "input_text", "text": "Fresh session context."}}})
	followup := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Continue."}}})
	window := slices.Insert(compacted.Output, 1, fresh)
	window = append(window, followup)
	request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(mustMarshalJSON(map[string]any{"model": "gpt-5", "instructions": "current instructions", "input": window}))))
	response = httptest.NewRecorder()
	(&contextCompactor{keyPath: compactor.keyPath}).handler(next)(response, request)
	want := slices.Insert(reduceContextCompaction(input), 3, fresh)
	want = append(want, followup)
	if response.Code != http.StatusOK || calls != 1 || string(mustMarshalJSON(delivered)) != string(mustMarshalJSON(want)) {
		t.Fatalf("native restoration failed: status=%d, provider calls=%d", response.Code, calls)
	}
}

func TestCompactionLegacyDoesNotReexportCanonicalContext(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	message := func(text string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": text}}})
	}
	contextItems := []json.RawMessage{
		message("# AGENTS.md instructions\n<INSTRUCTIONS>Preserve the workspace.</INSTRUCTIONS>"),
		message("<environment_context>\n<cwd>/old</cwd>\n</environment_context>"),
	}
	input := append(slices.Clone(contextItems), compactHTTPHistory()...)
	response := httptest.NewRecorder()
	compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("compaction reached provider")
	}))(response, httptest.NewRequest(http.MethodPost, "/v1/responses/compact",
		strings.NewReader(string(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": input})))))
	var compacted struct {
		Output []json.RawMessage `json:"output"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &compacted) != nil {
		t.Fatalf("local compaction failed: %d", response.Code)
	}
	// Canonical context belongs in the authenticated history, not in the legacy
	// real-user carry list where restoration would mistake it for a fresh event.
	for _, item := range compacted.Output {
		if contextCompactionFreshContext(item) {
			t.Fatal("historical canonical context reexported as fresh context")
		}
	}
	got, err := compactor.restore(t.Context(), compacted.Output)
	if err != nil || contextCompactionCanonicalJSON(mustMarshalJSON(got)) != contextCompactionCanonicalJSON(mustMarshalJSON(reduceContextCompaction(input))) {
		t.Fatalf("historical context duplicated or lost: %v", err)
	}
}

func TestCompactionHTTPStreamingV2AndFailures(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("compaction reached provider") })
	input := append(compactHTTPHistory(), mustMarshalJSON(map[string]any{"type": "compaction_trigger"}))
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(mustMarshalJSON(map[string]any{
		"model": "gpt-5", "input": input, "stream": true, "tools": []any{}, "parallel_tool_calls": true,
	}))))
	request.Header.Set(codexTurnMetadataHeader, string(mustMarshalJSON(map[string]any{
		"request_kind": "compaction", "compaction": map[string]any{"implementation": "responses_compaction_v2", "trigger": "auto", "reason": "context_limit", "phase": "mid_turn", "strategy": "memento"},
	})))
	response := httptest.NewRecorder()
	compactor.handler(next)(response, request)
	if response.Code != http.StatusOK || strings.Count(response.Body.String(), "event: response.output_item.done\n") != 1 || !strings.Contains(response.Body.String(), "event: response.completed\n") {
		t.Fatalf("V2 result = %d: %s", response.Code, response.Body.String())
	}
	for _, body := range []string{
		`{"model":"gpt-5","input":[]}`,
		`{"model":"gpt-5","input":[null]}`,
		`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"protected"}]}`,
		`{"model":"gpt-5","input":[{"type":"compaction","id":"cmp_hpatch_broken","encrypted_content":"broken"}]}`,
	} {
		response := httptest.NewRecorder()
		compactor.handler(next)(response, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body)))
		if response.Code < 400 {
			t.Fatalf("unsafe compaction accepted: %s", body)
		}
	}
}

func TestCompactionRestoreRepeatedAndTruncatedCarriedItems(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	first := mustMarshalJSON(map[string]any{"type": "message", "id": "msg_user", "role": "user", "content": "full original request"})
	items := append([]json.RawMessage{first}, compactHTTPHistory()...)
	capsule, err := compactor.seal(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	truncated := mustMarshalJSON(map[string]any{"type": "message", "id": "msg_user", "role": "user", "content": "full..."})
	got, err := compactor.restore(t.Context(), []json.RawMessage{truncated, capsule})
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(items)) {
		t.Fatalf("truncated carried message not restored: %v", err)
	}
	second, err := compactor.seal(t.Context(), reduceContextCompaction(got))
	if err != nil {
		t.Fatal(err)
	}
	got, err = compactor.restore(t.Context(), []json.RawMessage{capsule, second})
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(reduceContextCompaction(items))) {
		t.Fatalf("repeated envelope duplicated or lost history: %v", err)
	}
}

func TestCompactionRestoreFreshAuthorityAndRepeatedUserAnchors(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	message := func(role, text string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "message", "role": role, "content": []any{map[string]string{"type": "input_text", "text": text}}})
	}
	a, b, yes := message("developer", "Current rule A"), message("developer", "Intermediate conflicting rule B"), message("user", "yes")
	items := []json.RawMessage{a, yes, b, yes, compactTestCall("last", "pwd")}
	capsule, err := compactor.seal(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	got, err := compactor.restore(t.Context(), []json.RawMessage{a, yes, capsule})
	want := slices.Insert(slices.Clone(items), 3, a)
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(want)) {
		t.Fatalf("fresh canonical authority was lost or placed at an old user anchor: %v", err)
	}
}

func TestCompactionRestoreNoIDTruncation(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	message := func(text string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": text}}})
	}
	old, correction := message("middle1234"), message("Actually, do not make the old change.")
	items := []json.RawMessage{old, correction, compactTestCall("last", "pwd")}
	capsule, err := compactor.seal(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	var carried map[string]json.RawMessage
	_ = json.Unmarshal(message("midd…1 tokens truncated…1234"), &carried)
	carried["internal_chat_message_metadata_passthrough"] = mustMarshalJSON(map[string]any{"content_item_kinds": []string{"unknown"}})
	for _, prefix := range [][]json.RawMessage{{mustMarshalJSON(carried)}, {mustMarshalJSON(carried), correction}} {
		got, err := compactor.restore(t.Context(), append(prefix, capsule))
		if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(items)) {
			t.Fatalf("no-ID truncated request duplicated or moved past its correction: %v", err)
		}
	}
	ambiguous, err := compactor.seal(t.Context(), []json.RawMessage{message("middle1234"), message("middXX1234")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compactor.restore(t.Context(), []json.RawMessage{mustMarshalJSON(carried), ambiguous}); err == nil {
		t.Fatal("ambiguous truncation silently selected an original request")
	}
}

func TestCompactionRestoreClassifiedUsersAndSnapshotProvenance(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	classified := func(text string) json.RawMessage {
		return mustMarshalJSON(map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]string{"type": "input_text", "text": text}},
			"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{"user.text"}},
		})
	}
	developer := mustMarshalJSON(map[string]any{"type": "message", "role": "developer", "content": "Current policy"})
	user := classified("middle1234")
	items := []json.RawMessage{developer, user}
	capsule, err := compactor.seal(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}
	got, err := compactor.restore(t.Context(), []json.RawMessage{classified("midd…1 tokens truncated…1234"), capsule})
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(items)) {
		t.Fatalf("classified user was not reconciled: %v", err)
	}
	got, err = compactor.restore(t.Context(), []json.RawMessage{capsule, capsule, capsule})
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(items)) {
		t.Fatalf("historical canonical context was duplicated across envelopes: %v", err)
	}
	fresh := mustMarshalJSON(map[string]any{"type": "message", "role": "developer", "content": "New policy"})
	got, err = compactor.restore(t.Context(), []json.RawMessage{fresh, user, capsule})
	want := []json.RawMessage{developer, fresh, user}
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(want)) {
		t.Fatalf("classified user lost the fresh-instruction anchor: %v", err)
	}
}

func TestCompactionRestoreRejectsUnreconciledPartialUserContent(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	original := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]string{"type": "input_text", "text": "before"},
		map[string]string{"type": "input_image", "image_url": "data:image/png;base64,fixture"},
		map[string]string{"type": "input_text", "text": "after"},
	}})
	capsule, err := compactor.seal(t.Context(), []json.RawMessage{original})
	if err != nil {
		t.Fatal(err)
	}
	partial := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]string{"type": "input_text", "text": "before"},
		map[string]string{"type": "input_text", "text": "after"},
	}})
	if _, err := compactor.restore(t.Context(), []json.RawMessage{partial, capsule}); err == nil {
		t.Fatal("unreconciled multimodal user content was silently duplicated")
	}
}

func TestCompactionRestoreLegacyCanonicalUserContext(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	message := func(text string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": text}}})
	}
	old := message("<environment_context>\n<cwd>/old</cwd>\n</environment_context>")
	current := message("<environment_context>\n<cwd>/current</cwd>\n</environment_context>")
	user := message("Continue carefully.")
	capsule, err := compactor.seal(t.Context(), []json.RawMessage{old, user})
	if err != nil {
		t.Fatal(err)
	}
	got, err := compactor.restore(t.Context(), []json.RawMessage{current, user, capsule})
	want := []json.RawMessage{old, current, user}
	if err != nil || string(mustMarshalJSON(got)) != string(mustMarshalJSON(want)) {
		t.Fatalf("legacy canonical user context lost or misplaced: %v", err)
	}
}

func TestCompactionResponsesHaveDistinctIdentities(t *testing.T) {
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("provider called") })
	body := string(mustMarshalJSON(map[string]any{"model": "gpt-5", "input": compactHTTPHistory()}))
	previous := ""
	for range 2 {
		response := httptest.NewRecorder()
		compactor.handler(next)(response, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body)))
		var result map[string]json.RawMessage
		_ = json.Unmarshal(response.Body.Bytes(), &result)
		id := jsonString(result, "id")
		if response.Code != http.StatusOK || id == "" || id == previous {
			t.Fatal("separate compactions reused a response identity")
		}
		previous = id
	}
}
