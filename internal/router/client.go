package router

// Source: client.go:1:679 upstream forwarding, response transformation, and usage observation.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	codexBaseURL           = "https://chatgpt.com/backend-api/codex"
	codexClientIdentity    = "codex_cli_rs"
	chatGPTAccountIDHeader = "Chatgpt-Account-Id"

	defaultDialTimeout          = 5 * time.Second
	maxUpstreamSniffBytes       = 64 << 10
	maxUpstreamErrorDetailBytes = 8 << 10
	maxUpstreamRetries          = 5
	initialUpstreamRetryBackoff = 100 * time.Millisecond
	maxUpstreamRetryBackoff     = 30 * time.Second
	selectedModelAtCapacity     = "selected model is at capacity"
	codexBetaFeaturesHeader     = "x-codex-beta-features"
	codexResponsesLiteHeader    = "x-openai-internal-codex-responses-lite"
	codexSessionIDHeader        = "Session_id"
	maxUpstreamJSONBytes        = 64 << 20
)

var (
	utf8BOM              = []byte{0xef, 0xbb, 0xbf}
	errResponseTransform = errors.New("transform upstream response")
	errResponseWrite     = errors.New("write downstream response")
)

type responseProvider interface {
	forwardExecution(startCtx, responseCtx context.Context, body []byte, headers http.Header, cacheKey string) (*http.Response, error)
}

type providerClient struct {
	httpClient *http.Client
	baseURL    string
}

func newProviderClient(baseURL string, supplied *http.Client) *providerClient {
	return &providerClient{httpClient: withDialTimeout(supplied), baseURL: baseURL}
}

// requiredCodexAuthHeaders enforces the router's authentication boundary.
// Codex owns login and token refresh; a custom provider configured with
// requires_openai_auth = true attaches its managed credentials to every request.
// The router validates those headers and relays them to the ChatGPT Codex backend.
func requiredCodexAuthHeaders(headers http.Header) (string, string, error) {
	authorizationValues := headers.Values("Authorization")
	if len(authorizationValues) != 1 {
		return "", "", errors.New("codex request must provide exactly one Authorization header; configure the provider with requires_openai_auth = true")
	}
	scheme, token, ok := strings.Cut(authorizationValues[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || token != strings.TrimSpace(token) {
		return "", "", errors.New("codex request has an invalid bearer Authorization header")
	}
	accountValues := headers.Values(chatGPTAccountIDHeader)
	if len(accountValues) != 1 || accountValues[0] == "" || accountValues[0] != strings.TrimSpace(accountValues[0]) {
		return "", "", errors.New("codex request must provide exactly one non-empty ChatGPT-Account-ID header")
	}
	return authorizationValues[0], accountValues[0], nil
}

func (c *providerClient) forwardExecution(startCtx, responseCtx context.Context, body []byte, headers http.Header, cacheKey string) (*http.Response, error) {
	authorization, accountID, err := requiredCodexAuthHeaders(headers)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/responses"
	for attempt := 0; ; attempt++ {
		requestCtx, cancelRequest := context.WithCancel(responseCtx)
		stopStartCancellation := context.AfterFunc(startCtx, cancelRequest)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			stopStartCancellation()
			cancelRequest()
			return nil, fmt.Errorf("build upstream request: %w", err)
		}
		defaults := []struct {
			name  string
			value string
		}{
			{"Authorization", authorization},
			{"Content-Type", "application/json"},
			{"Accept", "application/json, text/event-stream"},
			{chatGPTAccountIDHeader, accountID},
			{"Originator", codexClientIdentity},
			{"User-Agent", codexClientIdentity},
		}
		for _, header := range defaults {
			request.Header.Set(header.name, header.value)
		}
		forwardCodexRequestHeaders(request.Header, headers)
		if validCodexCacheKey(cacheKey) {
			request.Header[codexSessionIDHeader] = []string{cacheKey}
		}
		response, err := c.httpClient.Do(request)
		if err != nil {
			stopStartCancellation()
			cancelRequest()
			return nil, fmt.Errorf("forward upstream request: %w", err)
		}
		finishResponseStart := func() error {
			if !stopStartCancellation() {
				cancelRequest()
				_ = response.Body.Close()
				if err := startCtx.Err(); err != nil {
					return err
				}
				return context.Canceled
			}
			response.Body = &cancelOnCloseReadCloser{body: response.Body, cancel: cancelRequest}
			return nil
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			if err := finishResponseStart(); err != nil {
				return nil, fmt.Errorf("forward upstream request: %w", err)
			}
			return response, nil
		}
		recoverable := responseHasRecoverableUpstreamError(response)
		if err := finishResponseStart(); err != nil {
			return nil, fmt.Errorf("forward upstream request: %w", err)
		}
		if !recoverable || attempt == maxUpstreamRetries {
			return response, nil
		}
		delay := upstreamRetryDelay(response, attempt)
		_ = response.Body.Close()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-startCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait to retry upstream request: %w", startCtx.Err())
		}
	}
}

func forwardCodexRequestHeaders(destination, source http.Header) {
	for _, name := range []string{
		threadIDHeader,
		clientRequestIDHeader,
		codexWindowIDHeader,
		codexBetaFeaturesHeader,
		codexResponsesLiteHeader,
		codexTurnMetadataHeader,
	} {
		for _, value := range source.Values(name) {
			destination.Add(name, value)
		}
	}
}

func validCodexCacheKey(value string) bool {
	if len(value) == 0 || len(value) > maxSessionIDBytes || strings.TrimSpace(value) != value {
		return false
	}
	for index := range len(value) {
		char := value[index]
		if (char < 0x20 && char != '\t') || char == 0x7f {
			return false
		}
	}
	return true
}

type cancelOnCloseReadCloser struct {
	body   io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Read(body []byte) (int, error) {
	return r.body.Read(body)
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.body.Close()
	r.cancel()
	return err
}

func upstreamRetryDelay(response *http.Response, attempt int) time.Duration {
	if value := strings.TrimSpace(response.Header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
			if seconds >= uint64(maxUpstreamRetryBackoff/time.Second) {
				return maxUpstreamRetryBackoff
			}
			return min(time.Duration(seconds)*time.Second, maxUpstreamRetryBackoff)
		}
		if retryAt, err := http.ParseTime(value); err == nil {
			return min(max(time.Until(retryAt), 0), maxUpstreamRetryBackoff)
		}
	}
	delay := min(initialUpstreamRetryBackoff<<attempt, maxUpstreamRetryBackoff)
	half := delay / 2
	return half + rand.N(delay-half)
}

func responseHasRecoverableUpstreamError(response *http.Response) bool {
	if response == nil || response.Body == nil || response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return false
	}
	detail, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamErrorDetailBytes+1))
	replayResponseBody(response, detail, response.Body)
	if err != nil || len(detail) > maxUpstreamErrorDetailBytes {
		return false
	}
	message := strings.ToLower(upstreamErrorMessage(detail))
	return strings.Contains(message, selectedModelAtCapacity)
}

func upstreamErrorMessage(body []byte) string {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Error) == 0 {
		return ""
	}
	var providerError struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(envelope.Error, &providerError) == nil {
		return strings.TrimSpace(providerError.Message)
	}
	var message string
	if json.Unmarshal(envelope.Error, &message) == nil {
		return strings.TrimSpace(message)
	}
	return ""
}

func prepareUpstreamBody(response *http.Response, streamRequested bool) (bool, error) {
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return true, nil
	}
	if !streamRequested {
		return false, nil
	}

	probe := bufio.NewReader(io.LimitReader(response.Body, maxUpstreamSniffBytes+1))
	var prefix bytes.Buffer
	defer func() {
		replayResponseBody(response, prefix.Bytes(), io.MultiReader(probe, response.Body))
	}()
	firstLine := true
	for {
		line, err := probe.ReadString('\n')
		prefix.WriteString(line)
		if prefix.Len() > maxUpstreamSniffBytes {
			return false, nil
		}
		content, _ := trimSSELineEnding(line)
		if firstLine {
			content = strings.TrimPrefix(content, "\uFEFF")
			firstLine = false
		}
		if content != "" {
			if isSSELine(content) {
				response.Header.Set("Content-Type", "text/event-stream")
				return true, nil
			}
			jsonPrefix := strings.TrimSpace(content)
			if strings.HasPrefix(jsonPrefix, "{") || strings.HasPrefix(jsonPrefix, "[") {
				return false, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
	}
}

func replayResponseBody(response *http.Response, prefix []byte, remainder io.Reader) {
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), remainder), response.Body}
}

func isSSELine(line string) bool {
	if strings.HasPrefix(line, ":") {
		return true
	}
	field, _, _ := strings.Cut(line, ":")
	switch field {
	case "data", "event", "id", "retry":
		return true
	default:
		return false
	}
}

type responseTerminalState uint8

const (
	responseTerminalUnknown responseTerminalState = iota
	responseTerminalInvalid
	responseTerminalPending
	responseTerminalCompleted
	responseTerminalFailed
)

func (state responseTerminalState) String() string {
	switch state {
	case responseTerminalInvalid:
		return "invalid"
	case responseTerminalPending:
		return "pending"
	case responseTerminalCompleted:
		return "completed"
	case responseTerminalFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func copyUpstreamBodyTransformed(writer io.Writer, response *http.Response, streamResponse bool, transformer *hpatchResponseTransform, observeUsage func(tokenCounts)) (responseTerminalState, error) {
	defer response.Body.Close()
	var (
		terminalState responseTerminalState
		err           error
	)
	if streamResponse {
		terminalState, err = copySSETransformed(writer, response.Body, transformer, observeUsage)
	} else {
		terminalState, err = copyJSONTransformed(writer, response.Body, transformer, observeUsage)
	}
	if err != nil {
		return responseTerminalUnknown, fmt.Errorf("copy upstream response: %w", err)
	}
	return terminalState, nil
}

func copyJSONTransformed(writer io.Writer, reader io.Reader, transformer *hpatchResponseTransform, observeUsage func(tokenCounts)) (responseTerminalState, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxUpstreamJSONBytes+1))
	if err != nil {
		return responseTerminalUnknown, err
	}
	if len(body) > maxUpstreamJSONBytes {
		return responseTerminalUnknown, fmt.Errorf("upstream JSON response exceeds %d bytes", maxUpstreamJSONBytes)
	}
	recordObservedUsage(body, false, observeUsage)
	visible := body
	if transformer != nil {
		visible, err = transformer.TransformJSON(body)
		if err != nil {
			return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseTransform, err)
		}
		if err := transformer.Finish(false); err != nil {
			return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseTransform, err)
		}
	}
	terminalState := observeResponseTerminal(visible, false)
	if _, err = writer.Write(visible); err != nil {
		return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
	}
	return terminalState, nil
}

func copySSETransformed(writer io.Writer, reader io.Reader, transformer *hpatchResponseTransform, observeUsage func(tokenCounts)) (responseTerminalState, error) {
	buffered := bufio.NewReader(reader)
	if err := consumeOptionalUTF8BOM(buffered); err != nil {
		return responseTerminalUnknown, err
	}
	event := []string{}
	terminalState := responseTerminalUnknown
	for {
		line, err := buffered.ReadString('\n')
		if len(line) > 0 {
			if line == "\n" || line == "\r\n" {
				eventTerminalState, writeErr := writeSSEEvent(writer, event, line, transformer, observeUsage)
				terminalState = mergeResponseTerminalState(terminalState, eventTerminalState)
				if writeErr != nil {
					return responseTerminalUnknown, writeErr
				}
				event = event[:0]
			} else {
				event = append(event, line)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				eventTerminalState, writeErr := writeSSEEvent(writer, event, "", transformer, observeUsage)
				terminalState = mergeResponseTerminalState(terminalState, eventTerminalState)
				if writeErr != nil {
					return responseTerminalUnknown, writeErr
				}
				if transformer != nil {
					if finishErr := transformer.Finish(true); finishErr != nil {
						return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseTransform, finishErr)
					}
				}
				return terminalState, nil
			}
			return responseTerminalUnknown, err
		}
	}
}

func consumeOptionalUTF8BOM(reader *bufio.Reader) error {
	prefix, err := reader.Peek(len(utf8BOM))
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if bytes.Equal(prefix, utf8BOM) {
		_, err = reader.Discard(len(utf8BOM))
		return err
	}
	return nil
}

func writeSSEEvent(writer io.Writer, lines []string, separator string, transformer *hpatchResponseTransform, observeUsage func(tokenCounts)) (responseTerminalState, error) {
	if len(lines) == 0 {
		if separator != "" {
			if _, err := io.WriteString(writer, separator); err != nil {
				return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
			}
		}
		if responseWriter, ok := writer.(http.ResponseWriter); ok {
			if err := http.NewResponseController(responseWriter).Flush(); err != nil {
				return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
			}
		}
		return responseTerminalUnknown, nil
	}
	payload := ssePayload(lines)
	recordObservedUsage(payload, true, observeUsage)
	visible := [][]byte{payload}
	if transformer != nil && len(payload) > 0 {
		var err error
		visible, err = transformer.TransformSSE(payload)
		if err != nil {
			return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseTransform, err)
		}
	}
	if len(visible) == 0 {
		return responseTerminalUnknown, nil
	}

	terminalState := responseTerminalUnknown
	var originalEnvelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(payload, &originalEnvelope)
	for index, eventPayload := range visible {
		eventLines := lines
		eventSeparator := separator
		var transformedEnvelope struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(eventPayload, &transformedEnvelope)
		if len(visible) > 1 || transformedEnvelope.Type != originalEnvelope.Type {
			lineEnding := "\n"
			if separator == "\r\n" {
				lineEnding = "\r\n"
			}
			eventLines = []string{"data: " + string(eventPayload) + lineEnding}
			if transformedEnvelope.Type != "" {
				eventLines = append([]string{"event: " + transformedEnvelope.Type + lineEnding}, eventLines...)
			}
			if index < len(visible)-1 && eventSeparator == "" {
				eventSeparator = lineEnding
			}
		}
		augmented := encodeSSEEventPayload(eventLines, eventPayload)
		terminalState = mergeResponseTerminalState(terminalState, observeResponseTerminal(eventPayload, true))
		if _, err := io.WriteString(writer, augmented); err != nil {
			return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
		}
		if eventSeparator != "" {
			if _, err := io.WriteString(writer, eventSeparator); err != nil {
				return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
			}
		}
	}
	if responseWriter, ok := writer.(http.ResponseWriter); ok {
		if err := http.NewResponseController(responseWriter).Flush(); err != nil {
			return responseTerminalUnknown, fmt.Errorf("%w: %w", errResponseWrite, err)
		}
	}
	return terminalState, nil
}

func ssePayload(lines []string) []byte {
	parts := []string{}
	for _, line := range lines {
		content, _ := trimSSELineEnding(line)
		if part, ok := strings.CutPrefix(content, "data:"); ok {
			parts = append(parts, strings.TrimPrefix(part, " "))
		}
	}
	return []byte(strings.Join(parts, "\n"))
}

func recordObservedUsage(payload []byte, streamEvent bool, observe func(tokenCounts)) {
	if observe == nil {
		return
	}
	counts, ok := usageFromResponsePayload(payload, streamEvent)
	if !ok {
		return
	}
	observe(counts)
}

func encodeSSEEventPayload(lines []string, payload []byte) string {
	dataIndexes := []int{}
	for index, line := range lines {
		content, _ := trimSSELineEnding(line)
		if !strings.HasPrefix(content, "data:") {
			continue
		}
		dataIndexes = append(dataIndexes, index)
	}
	if len(dataIndexes) == 0 {
		return strings.Join(lines, "")
	}
	if strings.TrimSpace(string(payload)) == "" || strings.TrimSpace(string(payload)) == "[DONE]" {
		return strings.Join(lines, "")
	}

	firstData := dataIndexes[0]
	dataSet := map[int]bool{}
	for _, index := range dataIndexes {
		dataSet[index] = true
	}
	var result strings.Builder
	for index, line := range lines {
		if index == firstData {
			_, ending := trimSSELineEnding(line)
			result.WriteString("data: ")
			result.Write(payload)
			result.WriteString(ending)
			continue
		}
		if !dataSet[index] {
			result.WriteString(line)
		}
	}
	return result.String()
}

func observeResponseTerminal(body []byte, streamEvent bool) responseTerminalState {
	if streamEvent {
		payload := strings.TrimSpace(string(body))
		if payload == "" || payload == "[DONE]" {
			return responseTerminalUnknown
		}
	}
	var envelope struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		if streamEvent {
			return responseTerminalInvalid
		}
		return responseTerminalUnknown
	}
	status := envelope.Status
	if streamEvent {
		if !strings.HasPrefix(envelope.Type, "response.") || envelope.Type == "response." {
			return responseTerminalInvalid
		}
		status = strings.TrimPrefix(envelope.Type, "response.")
	}
	switch status {
	case "completed":
		return responseTerminalCompleted
	case "failed", "incomplete":
		return responseTerminalFailed
	case "queued", "in_progress":
		return responseTerminalPending
	default:
		if streamEvent {
			return responseTerminalPending
		}
		return responseTerminalUnknown
	}
}

func mergeResponseTerminalState(current, observed responseTerminalState) responseTerminalState {
	if observed == responseTerminalUnknown {
		return current
	}
	if observed == responseTerminalInvalid {
		return responseTerminalInvalid
	}
	if current == responseTerminalInvalid {
		return observed
	}
	if current == responseTerminalFailed || observed == responseTerminalFailed {
		return responseTerminalFailed
	}
	if current == responseTerminalCompleted || observed == responseTerminalCompleted {
		return responseTerminalCompleted
	}
	return responseTerminalPending
}

func trimSSELineEnding(line string) (string, string) {
	if before, ok := strings.CutSuffix(line, "\r\n"); ok {
		return before, "\r\n"
	}
	if before, ok := strings.CutSuffix(line, "\n"); ok {
		return before, "\n"
	}
	return line, ""
}

func withDialTimeout(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	cloned := *client
	if cloned.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
		cloned.Transport = transport
	}
	return &cloned
}
