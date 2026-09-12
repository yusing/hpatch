package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func compactCodeModeOutput(id, text string) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": "custom_tool_call_output", "call_id": id,
		"output": []any{
			map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			map[string]any{"type": "input_text", "text": string(mustMarshalJSON(map[string]any{
				"exit_code": 0, "output": text, "script_ref": "@shell/retained",
			}))},
		},
	})
}

func TestCompactionRepeatedCodeModeSource(t *testing.T) {
	var excerpt strings.Builder
	for row := 1; row <= 12; row++ {
		fmt.Fprintf(&excerpt, "%d:abcd source declaration with enough exact text to distinguish this retained source row\n", row)
	}
	call := func(id string) json.RawMessage {
		return mustMarshalJSON(map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": id, "input": "opaque script"})
	}
	items := []json.RawMessage{
		call("old"), compactCodeModeOutput("old", "old read header\n"+excerpt.String()+"unique old diagnostic\n"),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Keep the earlier constraint."}),
		call("middle"), compactCodeModeOutput("middle", excerpt.String()),
		call("new"), compactCodeModeOutput("new", "new read header\n"+excerpt.String()),
	}
	ambiguous := append([]json.RawMessage{call("old")}, items...)
	if got := reduceContextCompaction(ambiguous); string(got[2]) != string(ambiguous[2]) {
		t.Fatal("ambiguous call identity was used for source replacement")
	}
	before := string(mustMarshalJSON(items))
	got := reduceContextCompaction(items)
	for index := range items {
		if index != 1 && index != 4 && string(got[index]) != string(items[index]) {
			t.Fatalf("protected item %d changed", index)
		}
	}
	if !strings.Contains(string(got[1]), "unique old diagnostic") || !strings.Contains(string(got[1]), "retained verbatim") ||
		!strings.Contains(string(got[4]), "retained verbatim") {
		t.Fatal("duplicate excerpt was not reduced with unique evidence retained")
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("input was mutated")
	}
	if string(mustMarshalJSON(reduceContextCompaction(got))) != string(mustMarshalJSON(got)) {
		t.Fatal("repeated reduction changed the retained references")
	}
	extended := append(append([]json.RawMessage(nil), got...), call("newest"), compactCodeModeOutput("newest", excerpt.String()))
	if again := reduceContextCompaction(extended); string(again[6]) != string(got[6]) {
		t.Fatal("a subsequent compaction pruned already-referenced evidence")
	}
	// The search reducer and source-row reducer must not invalidate one
	// another's references, including across repeated compactions.
	search := []json.RawMessage{
		compactTestCall("search", "rg rows file"), compactTestOutput("search", excerpt.String(), 0),
		compactTestCall("read", "cat file"), compactTestOutput("read", excerpt.String(), 0),
		call("last"), compactCodeModeOutput("last", excerpt.String()),
	}
	searched := reduceContextCompaction(search)
	if !strings.Contains(string(searched[1]), "matching search listing") || string(searched[3]) != string(search[3]) {
		t.Fatal("search replacement no longer points to verbatim retained evidence")
	}
	if again := reduceContextCompaction(searched); string(again[3]) != string(search[3]) {
		t.Fatal("repeated compaction invalidated a search reference")
	}
	longID := strings.Repeat("long-id", 10000)
	separated := strings.Repeat(excerpt.String()+"non-row separator\n", 100)
	oversized := []json.RawMessage{call("first"), compactCodeModeOutput("first", separated),
		call(longID), compactCodeModeOutput(longID, separated)}
	if reduced := reduceContextCompaction(oversized); string(reduced[1]) != string(oversized[1]) {
		t.Fatal("oversized replacement reference expanded the output")
	}
	for _, replacement := range []struct{ old, new string }{
		{"Script completed", "Script running"},
		{`\"exit_code\":0`, `\"exit_code\":1`},
		{"1:abcd source", "1:abce source"},
	} {
		altered := append([]json.RawMessage(nil), items...)
		altered[1] = json.RawMessage(strings.ReplaceAll(string(items[1]), replacement.old, replacement.new))
		// For changed text, require that changed evidence survives; unchanged
		// trailing rows may still have an exact retained replacement.
		reduced := reduceContextCompaction(altered)
		if replacement.old == "1:abcd source" {
			if !strings.Contains(string(reduced[1]), "1:abce source") {
				t.Fatal("changed source evidence was removed")
			}
		} else if string(reduced[1]) != string(altered[1]) {
			t.Fatal("unfinished or failed output changed")
		}
	}
}

func TestCompactionReferencedResultsScansOnlySpecialResultNotes(t *testing.T) {
	note := fmt.Sprintf(
		"[mekugi compaction: 3 source rows (1:0001 through 3:0003) retained verbatim in later tool result %q]\n",
		"later-source",
	)
	failed := compactTestOutput("failed-consumer", note, 1)
	if !contextCompactionReferencedResults([]json.RawMessage{failed})["later-source"] {
		t.Fatal("surviving failed result note did not protect its referenced evidence")
	}
	retired := mustMarshalJSON(map[string]any{
		"type": "message", "role": "assistant", "content": note,
	})
	if !contextCompactionReferencedResults([]json.RawMessage{retired})["later-source"] {
		t.Fatal("surviving factual assistant note did not protect its referenced evidence")
	}

	decoys := []json.RawMessage{
		compactTestOutput("plain-id", "later-source", 1),
		compactTestOutput("prefixed-note", "ordinary output: "+note, 1),
	}
	if protected := contextCompactionReferencedResults(decoys); len(protected) != 0 {
		t.Fatal("non-special or non-result text was treated as a replacement note")
	}
}

func compactionReplayAllowsOnlyMetadataCleanup(before, after json.RawMessage) bool {
	var left, right map[string]json.RawMessage
	if json.Unmarshal(before, &left) != nil || json.Unmarshal(after, &right) != nil || left == nil || right == nil {
		return false
	}
	var leftMetadata, rightMetadata map[string]json.RawMessage
	if json.Unmarshal(left["internal_chat_message_metadata_passthrough"], &leftMetadata) != nil || leftMetadata == nil {
		return false
	}
	if raw, exists := right["internal_chat_message_metadata_passthrough"]; exists {
		if json.Unmarshal(raw, &rightMetadata) != nil || rightMetadata == nil {
			return false
		}
	} else {
		rightMetadata = map[string]json.RawMessage{}
	}

	removed := false
	for key, leftValue := range leftMetadata {
		rightValue, exists := rightMetadata[key]
		if key == "turn_id" || key == "create_time" {
			if !exists {
				removed = true
				continue
			}
		} else if !exists {
			return false
		}
		if contextCompactionCanonicalJSON(leftValue) != contextCompactionCanonicalJSON(rightValue) {
			return false
		}
	}
	for key := range rightMetadata {
		if _, exists := leftMetadata[key]; !exists {
			return false
		}
	}
	delete(left, "internal_chat_message_metadata_passthrough")
	delete(right, "internal_chat_message_metadata_passthrough")
	return removed && contextCompactionCanonicalJSON(mustMarshalJSON(left)) == contextCompactionCanonicalJSON(mustMarshalJSON(right))
}

// Opt-in private-history replay. Only aggregate sizes are reported; no
// conversation text is copied into fixtures or printed on failure.
func TestCompactionRolloutReplay(t *testing.T) {
	path := os.Getenv("MEKUGI_COMPACTION_ROLLOUT")
	if path == "" {
		t.Skip("set MEKUGI_COMPACTION_ROLLOUT to check a local rollout")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var input []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), responsesRequestBufferBytes)
	for scanner.Scan() {
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal("invalid rollout record")
		}
		// Reproduce the first compaction boundary, not a concatenation of
		// pre-compaction history and later continuation records.
		if record.Type == "compacted" {
			break
		}
		if record.Type == "response_item" {
			input = append(input, record.Payload)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	reduced := reduceContextCompaction(input)
	changed := len(input) - len(reduced)
	if changed == 0 {
		for index := range input {
			if string(input[index]) != string(reduced[index]) {
				changed++
			}
		}
	}
	retainedCursor := 0
	for index := range input {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(input[index], &fields)
		kind := jsonString(fields, "type")
		if kind == "function_call_output" || kind == "custom_tool_call_output" || kind == "function_call" || kind == "custom_tool_call" || kind == "reasoning" {
			continue
		}
		if kind == "agent_message" || kind == "message" && jsonString(fields, "role") == "assistant" {
			continue
		}
		found := false
		for retainedCursor < len(reduced) {
			candidate := reduced[retainedCursor]
			retainedCursor++
			if string(input[index]) == string(candidate) || compactionReplayAllowsOnlyMetadataCleanup(input[index], candidate) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("protected item %d changed or moved out of order", index)
		}
	}
	if changed == 0 {
		t.Fatal("rollout has no supported reduction")
	}
	compactor := &contextCompactor{keyPath: filepath.Join(t.TempDir(), "compaction.key")}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(mustMarshalJSON(map[string]any{
		"model": "loopback", "input": input, "stream": true,
	}))))
	request.Header.Set(codexTurnMetadataHeader, string(mustMarshalJSON(map[string]any{
		"request_kind": "compaction", "compaction": map[string]any{"implementation": "responses_compaction_v2", "trigger": "manual", "reason": "user_request", "phase": "mid_turn", "strategy": "memento"},
	})))
	compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("compaction reached provider") }))(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("local compaction status %d", response.Code)
	}
	var capsule json.RawMessage
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type string          `json:"type"`
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "response.output_item.done" {
			capsule = event.Item
		}
	}
	if len(capsule) == 0 {
		t.Fatal("stream omitted completed compaction item")
	}
	restored, err := compactor.restore(t.Context(), []json.RawMessage{capsule})

	if err != nil || contextCompactionCanonicalJSON(mustMarshalJSON(restored)) != contextCompactionCanonicalJSON(mustMarshalJSON(reduced)) {
		t.Fatalf("native history round trip failed: error=%v; item counts=%d -> %d", err, len(reduced), len(restored))
	}
	// Check the ordinary continuation boundary too, not only envelope opening.
	suffix := mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Continue the current task."})
	nextInput := []json.RawMessage{capsule, suffix}
	continued := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(mustMarshalJSON(map[string]any{
		"model": "loopback", "input": nextInput, "stream": true,
	}))))
	seen := false
	compactor.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []json.RawMessage `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid restored continuation")
		}
		want := append(append([]json.RawMessage(nil), reduced...), suffix)
		if contextCompactionCanonicalJSON(mustMarshalJSON(body.Input)) != contextCompactionCanonicalJSON(mustMarshalJSON(want)) {
			t.Fatal("retired material reappeared or continuation was lost")
		}
		seen = true
	}))(httptest.NewRecorder(), continued)
	if !seen {
		t.Fatal("continuation did not reach the model boundary")
	}
	legacy := httptest.NewRecorder()
	compactor.handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("legacy compaction reached provider")
	}))(legacy, httptest.NewRequest(http.MethodPost, "/v1/responses/compact",
		strings.NewReader(string(mustMarshalJSON(map[string]any{"model": "loopback", "input": input})))))
	var legacyResponse struct {
		Output []json.RawMessage `json:"output"`
	}
	if legacy.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &legacyResponse) != nil {
		t.Fatalf("legacy compaction failed: status=%d", legacy.Code)
	}
	legacyRestored, err := compactor.restore(t.Context(), legacyResponse.Output)
	if err != nil || contextCompactionCanonicalJSON(mustMarshalJSON(legacyRestored)) != contextCompactionCanonicalJSON(mustMarshalJSON(reduced)) {
		t.Fatalf("legacy replay duplicated or lost selected context: %v", err)
	}
	// Report only aggregate structural reasons, never private transcript text.
	calls := make(map[string]map[string]json.RawMessage)
	originalResults := make(map[string]json.RawMessage)
	for _, raw := range input {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		kind := jsonString(fields, "type")
		if kind == "function_call" || kind == "custom_tool_call" {
			calls[jsonString(fields, "call_id")] = fields
		} else if kind == "function_call_output" || kind == "custom_tool_call_output" {
			originalResults[jsonString(fields, "call_id")] = raw
		}
	}
	buckets := make(map[string][]json.RawMessage)
	for _, raw := range reduced {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		kind := jsonString(fields, "type")
		if kind != "function_call_output" && kind != "custom_tool_call_output" {
			continue
		}
		call := calls[jsonString(fields, "call_id")]
		operation, known := compactionOperationCall(call)
		reason := "protected or recent"
		if !known {
			reason = "unsupported carrier"
		} else if _, ok := compactionRetiredOutput(fields["output"], operation); !ok {
			reason = "unknown or unsuccessful completion"
		}
		if string(raw) != string(originalResults[jsonString(fields, "call_id")]) {
			reason = "evidence reduced, native"
		}
		buckets[reason] = append(buckets[reason], raw)
	}
	for _, reason := range []string{"protected or recent", "unsupported carrier", "unknown or unsuccessful completion", "evidence reduced, native"} {
		t.Logf("retained result classification: %s (%d items)", reason, len(buckets[reason]))
		logCompactionTokenProfile(t, nil, buckets[reason])
	}
	retainedTokens := logCompactionTokenProfile(t, input, reduced)
	if requested := os.Getenv("MEKUGI_COMPACTION_MAX_TOKENS"); requested != "" {
		limit, err := strconv.Atoi(requested)
		if err != nil || limit <= 0 {
			t.Fatal("MEKUGI_COMPACTION_MAX_TOKENS must be a positive integer")
		}
		if retainedTokens > limit {
			t.Errorf("retained visible context exceeds target: %d > %d tokens", retainedTokens, limit)
		}
	}
	t.Logf("changed history items=%d; native history bytes=%d -> %d", changed, len(mustMarshalJSON(input)), len(mustMarshalJSON(reduced)))
}

// Local diagnostics only: string-token estimates exclude opaque ciphertext and
// do not pretend to measure provider-hidden reasoning or request/tool framing.
func logCompactionTokenProfile(t *testing.T, before, after []json.RawMessage) int {
	t.Helper()
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal("compaction profile tokenizer unavailable")
	}
	type measurement struct {
		tokens      int
		opaqueBytes int
	}
	profile := func(items []json.RawMessage) map[string]measurement {
		result := make(map[string]measurement)
		for _, raw := range items {
			var fields map[string]any
			if json.Unmarshal(raw, &fields) != nil {
				t.Fatal("invalid profile item")
			}
			kind, _ := fields["type"].(string)
			label := "other"
			switch kind {
			case "message":
				label = "messages"
			case "reasoning":
				label = "reasoning"
			case "custom_tool_call", "function_call":
				label = "calls"
			case "custom_tool_call_output", "function_call_output":
				label = "results"
			}
			m := result[label]
			var visit func(any)
			visit = func(value any) {
				switch value := value.(type) {
				case string:
					count, err := codec.Count(value)
					if err != nil {
						t.Fatal("unable to tokenize profile text")
					}
					m.tokens += count
				case []any:
					for _, part := range value {
						visit(part)
					}
				case map[string]any:
					for key, part := range value {
						if key == "encrypted_content" {
							if text, ok := part.(string); ok {
								m.opaqueBytes += len(text)
							}
							continue
						}
						visit(part)
					}
				}
			}
			visit(fields)
			result[label] = m
		}
		return result
	}
	beforeTotal, afterTotal := 0, 0
	left, right := profile(before), profile(after)
	for _, category := range []string{"messages", "calls", "results", "reasoning", "other"} {
		beforeTotal += left[category].tokens
		afterTotal += right[category].tokens
		t.Logf("visible string token estimate (%s), %s: %d -> %d; opaque bytes excluded: %d -> %d",
			codec.GetName(), category, left[category].tokens, right[category].tokens, left[category].opaqueBytes, right[category].opaqueBytes)
	}
	t.Logf("total visible string token estimate (%s): %d -> %d", codec.GetName(), beforeTotal, afterTotal)
	return afterTotal
}
