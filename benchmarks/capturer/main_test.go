package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCaptureProxyPreservesStreamingAndWritesSanitizedRecord(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5.6-sol","input":"private prompt"}`)
	firstEvent := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\",\"arguments\":\"private arguments\"}}\n\n"
	lastEvent := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"item-1\",\"call_id\":\"call-1\"}],\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":80},\"output_tokens\":20,\"output_tokens_details\":{\"reasoning_tokens\":7}}}}\n\n"
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-hpatch-capture-id") != "" {
			t.Error("private capture header reached the provider")
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if !bytes.Equal(payload, requestBody) {
			t.Errorf("request body = %q", payload)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, firstEvent)
		writer.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(writer, lastEvent)
	}))
	defer upstream.Close()

	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	recorder, err := newCaptureRecorder(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	recorder.beginRequest("capture-1")
	targetURL := mustParseTestURL(t, upstream.URL)
	proxy := httptest.NewServer(newCaptureProxy(targetURL, "provider", recorder))
	defer proxy.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, proxy.URL+"/responses", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-client-request-id", "request-1")
	request.Header.Set("thread-id", "thread-1")
	request.Header.Set("x-openai-subagent", "collab_spawn")
	request.Header.Set("x-hpatch-capture-id", "capture-1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if first != strings.TrimSuffix(firstEvent, "\n") {
		t.Fatalf("first streamed line = %q", first)
	}
	close(release)
	visibleRemainder, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := first + string(visibleRemainder); got != firstEvent+lastEvent {
		t.Fatalf("visible response changed:\n%s", got)
	}

	payload, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("private")) {
		t.Fatalf("capture retained private content: %s", payload)
	}
	var record captureRecord
	if err := json.Unmarshal(bytes.TrimSpace(payload), &record); err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != 2 || record.Boundary != "provider" || record.CaptureID != "capture-1" ||
		record.RequestSequence != 1 || record.ProviderAttempt != 1 ||
		record.RequestID != "request-1" || record.ThreadID != "thread-1" ||
		record.Subagent != "collab_spawn" || record.RequestModel != "gpt-5.6-sol" || !record.ResponseComplete ||
		record.ResponseStatus != "completed" || record.DurationMillis > 30_000 ||
		!slices.Equal(record.ToolCallIDs, []string{"item-1", "call-1"}) {
		t.Fatalf("capture record = %#v", record)
	}
	wantUsage := tokenUsage{InputTokens: 100, CachedTokens: 80, OutputTokens: 20, ReasoningTokens: 7}
	if record.Usage == nil || *record.Usage != wantUsage {
		t.Fatalf("usage = %#v, want %#v", record.Usage, wantUsage)
	}
}

func TestCaptureProxyCorrelatesBoundariesAndSequencesRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-hpatch-capture-id") != "" {
			t.Error("private capture header reached the provider")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"status":"completed","usage":{"input_tokens":1}}`)
	}))
	defer upstream.Close()

	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	recorder, err := newCaptureRecorder(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	back := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, upstream.URL), "provider", recorder))
	defer back.Close()
	front := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, back.URL), "codex", recorder))
	defer front.Close()

	for range 2 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, front.URL+"/responses", strings.NewReader(`{"model":"model"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("thread-id", "thread")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(response.Body); err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}

	file, err := os.Open(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	bySequence := map[uint64]map[string]captureRecord{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record captureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if bySequence[record.RequestSequence] == nil {
			bySequence[record.RequestSequence] = map[string]captureRecord{}
		}
		bySequence[record.RequestSequence][record.Boundary] = record
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(bySequence) != 2 {
		t.Fatalf("captured sequences = %#v", bySequence)
	}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		pair := bySequence[sequence]
		if len(pair) != 2 || pair["codex"].CaptureID == "" || pair["codex"].CaptureID != pair["provider"].CaptureID ||
			pair["provider"].ProviderAttempt != 1 {
			t.Fatalf("capture pair %d = %#v", sequence, pair)
		}
	}
}

func TestCaptureProxyRetainsProviderRetryAttempts(t *testing.T) {
	var providerRequests int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerRequests++
		writer.Header().Set("Content-Type", "application/json")
		if providerRequests == 1 {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"error":{"message":"model is at capacity"}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"status":"completed","usage":{"input_tokens":2}}`)
	}))
	defer upstream.Close()

	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	recorder, err := newCaptureRecorder(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	back := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, upstream.URL), "provider", recorder))
	defer back.Close()
	router := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		payload, err := io.ReadAll(incoming.Body)
		if err != nil {
			t.Error(err)
			http.Error(writer, "read request", http.StatusInternalServerError)
			return
		}
		for attempt := range 2 {
			request, err := http.NewRequestWithContext(incoming.Context(), http.MethodPost, back.URL+"/responses", bytes.NewReader(payload))
			if err != nil {
				t.Error(err)
				http.Error(writer, "build request", http.StatusInternalServerError)
				return
			}
			request.Header = incoming.Header.Clone()
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Error(err)
				http.Error(writer, "send request", http.StatusInternalServerError)
				return
			}
			responsePayload, err := io.ReadAll(response.Body)
			if err != nil {
				t.Error(err)
				_ = response.Body.Close()
				http.Error(writer, "read response", http.StatusInternalServerError)
				return
			}
			if err := response.Body.Close(); err != nil {
				t.Error(err)
				http.Error(writer, "close response", http.StatusInternalServerError)
				return
			}
			if attempt == 0 {
				continue
			}
			writer.Header().Set("Content-Type", response.Header.Get("Content-Type"))
			writer.WriteHeader(response.StatusCode)
			_, _ = writer.Write(responsePayload)
		}
	}))
	defer router.Close()
	front := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, router.URL), "codex", recorder))
	defer front.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, front.URL+"/responses", strings.NewReader(`{"model":"model"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("thread-id", "thread")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var providerRecords []captureRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record captureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record.Boundary == "provider" {
			providerRecords = append(providerRecords, record)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(providerRecords) != 2 || providerRecords[0].ProviderAttempt != 1 ||
		providerRecords[0].ResponseStatus != "http_error" || !providerRecords[0].ResponseComplete ||
		providerRecords[0].Usage != nil || providerRecords[1].ProviderAttempt != 2 ||
		providerRecords[1].Usage == nil || providerRecords[1].Usage.InputTokens != 2 {
		t.Fatalf("provider retry captures = %#v", providerRecords)
	}
}

func TestDecodedCapturePayloadReadsGzipWithoutChangingForwardedBytes(t *testing.T) {
	plain := []byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodedCapturePayload(compressed.Bytes(), "gzip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("decoded payload = %q", decoded)
	}
}

func TestObserveResponseJoinsMultilineSSEData(t *testing.T) {
	payload := []byte("event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":3}}}\n\n")
	var record captureRecord
	observeResponse(payload, "application/octet-stream", &record)
	if record.CaptureError != "" || record.ResponseStatus != "completed" ||
		record.Usage == nil || record.Usage.InputTokens != 3 {
		t.Fatalf("multiline SSE record = %#v", record)
	}
}

func TestCaptureProxyKeepsNonterminalResponseIncomplete(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.in_progress\",\"response\":{\"status\":\"in_progress\"}}\n\n")
	}))
	defer upstream.Close()
	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	recorder, err := newCaptureRecorder(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	recorder.beginRequest("capture-1")
	proxy := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, upstream.URL), "provider", recorder))
	defer proxy.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, proxy.URL+"/responses", strings.NewReader(`{"model":"gpt-5.6-sol"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-hpatch-capture-id", "capture-1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var record captureRecord
	if err := json.Unmarshal(bytes.TrimSpace(payload), &record); err != nil {
		t.Fatal(err)
	}
	if record.ResponseStatus != "in_progress" || record.ResponseComplete {
		t.Fatalf("nonterminal capture record = %#v", record)
	}
}

func TestCaptureProxyPassesModelsWithoutRecording(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"models":[]}`)
	}))
	defer upstream.Close()
	capturePath := filepath.Join(t.TempDir(), "capture.jsonl")
	recorder, err := newCaptureRecorder(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	proxy := httptest.NewServer(newCaptureProxy(mustParseTestURL(t, upstream.URL), "codex", recorder))
	defer proxy.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxy.URL+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if string(payload) != `{"models":[]}` {
		t.Fatalf("models response = %q", payload)
	}
	info, err := os.Stat(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("models request wrote %d capture bytes", info.Size())
	}
}

func mustParseTestURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
