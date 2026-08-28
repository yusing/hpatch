package main

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const shutdownTimeout = 5 * time.Second

type captureKey struct{}

type requestCapture struct {
	CaptureID string
	RequestID string
	SessionID string
	ThreadID  string
	Subagent  string
	Model     string
	Error     string
}

type tokenUsage struct {
	InputTokens     uint64 `json:"input_tokens"`
	CachedTokens    uint64 `json:"cached_input_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	ReasoningTokens uint64 `json:"reasoning_tokens"`
}

type captureRecord struct {
	SchemaVersion    int         `json:"schema_version"`
	Boundary         string      `json:"boundary"`
	CaptureID        string      `json:"capture_id"`
	RequestID        string      `json:"request_id,omitempty"`
	SessionID        string      `json:"session_id,omitempty"`
	ThreadID         string      `json:"thread_id,omitempty"`
	Subagent         string      `json:"subagent,omitempty"`
	RequestModel     string      `json:"request_model,omitempty"`
	StatusCode       int         `json:"status_code"`
	ResponseComplete bool        `json:"response_complete"`
	ResponseStatus   string      `json:"response_status,omitempty"`
	Usage            *tokenUsage `json:"usage,omitempty"`
	ToolCallIDs      []string    `json:"tool_call_ids,omitempty"`
	CaptureError     string      `json:"capture_error,omitempty"`
	CapturedAt       time.Time   `json:"captured_at"`
}

type captureRecorder struct {
	mu   sync.Mutex
	file *os.File
}

func newCaptureRecorder(path string) (*captureRecorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &captureRecorder{file: file}, nil
}

func (r *captureRecorder) Close() error {
	return r.file.Close()
}

func (r *captureRecorder) write(record captureRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.file.Write(payload)
	return err
}

type captureBody struct {
	io.ReadCloser
	content  bytes.Buffer
	record   func([]byte, bool)
	recorded bool
}

func (body *captureBody) Read(destination []byte) (int, error) {
	count, err := body.ReadCloser.Read(destination)
	if count != 0 {
		_, _ = body.content.Write(destination[:count])
	}
	if errors.Is(err, io.EOF) {
		body.finish(true)
	}
	return count, err
}

func (body *captureBody) Close() error {
	body.finish(false)
	return body.ReadCloser.Close()
}

func (body *captureBody) finish(complete bool) {
	if body.recorded {
		return
	}
	body.recorded = true
	body.record(body.content.Bytes(), complete)
}

func newCaptureProxy(target *url.URL, boundary string, recorder *captureRecorder) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		capture, ok := response.Request.Context().Value(captureKey{}).(requestCapture)
		if !ok {
			return nil
		}
		contentType := response.Header.Get("Content-Type")
		contentEncoding := response.Header.Get("Content-Encoding")
		statusCode := response.StatusCode
		response.Body = &captureBody{
			ReadCloser: response.Body,
			record: func(payload []byte, complete bool) {
				record := captureRecord{
					SchemaVersion:    1,
					Boundary:         boundary,
					CaptureID:        capture.CaptureID,
					RequestID:        capture.RequestID,
					SessionID:        capture.SessionID,
					ThreadID:         capture.ThreadID,
					Subagent:         capture.Subagent,
					RequestModel:     capture.Model,
					StatusCode:       statusCode,
					ResponseComplete: complete,
					CaptureError:     capture.Error,
					CapturedAt:       time.Now().UTC(),
				}
				observedPayload, err := decodedCapturePayload(payload, contentEncoding)
				if err != nil {
					record.CaptureError = "unsupported or invalid response content encoding"
				} else {
					observeResponse(observedPayload, contentType, &record)
				}
				if record.ResponseStatus != "" {
					record.ResponseComplete = true
				}
				if err := recorder.write(record); err != nil {
					fmt.Fprintf(os.Stderr, "capturer: write %s record: %v\n", boundary, err)
				}
			},
		}
		return nil
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/responses") {
			proxy.ServeHTTP(writer, request)
			return
		}
		capture := requestCapture{
			CaptureID: request.Header.Get("x-hpatch-capture-id"),
			RequestID: request.Header.Get("x-client-request-id"),
			SessionID: cmp.Or(request.Header.Get("session-id"), request.Header.Get("Session_id")),
			ThreadID:  request.Header.Get("thread-id"),
			Subagent:  request.Header.Get("x-openai-subagent"),
		}
		if boundary == "codex" {
			captureID, err := randomCaptureID()
			if err != nil {
				http.Error(writer, "create capture identity", http.StatusInternalServerError)
				return
			}
			capture.CaptureID = captureID
			request.Header.Set("x-hpatch-capture-id", capture.CaptureID)
		} else {
			request.Header.Del("x-hpatch-capture-id")
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "read request body", http.StatusBadRequest)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(payload))
		var envelope struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			capture.Error = "invalid request JSON"
		} else if strings.TrimSpace(envelope.Model) == "" {
			capture.Error = "missing request model"
		} else {
			capture.Model = envelope.Model
		}
		request = request.WithContext(context.WithValue(request.Context(), captureKey{}, capture))
		proxy.ServeHTTP(writer, request)
	})
}

func randomCaptureID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func decodedCapturePayload(payload []byte, contentEncoding string) ([]byte, error) {
	switch strings.TrimSpace(strings.ToLower(contentEncoding)) {
	case "":
		return payload, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		decompressed, readErr := io.ReadAll(reader)
		return decompressed, errors.Join(readErr, reader.Close())
	default:
		return nil, errors.New("unsupported content encoding")
	}
}

func observeResponse(payload []byte, contentType string, record *captureRecord) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || capturedPayloadLooksLikeSSE(payload) {
		var dataParts [][]byte
		observeEvent := func() {
			if len(dataParts) != 0 {
				observeResponseJSON(bytes.Join(dataParts, []byte{'\n'}), record)
				dataParts = dataParts[:0]
			}
		}
		for line := range bytes.SplitSeq(payload, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				observeEvent()
				continue
			}
			if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				dataParts = append(dataParts, bytes.TrimSpace(data))
			}
		}
		observeEvent()
		return
	}
	observeResponseJSON(payload, record)
}

func capturedPayloadLooksLikeSSE(payload []byte) bool {
	payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
	for line := range bytes.SplitSeq(payload, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == ':' {
			return true
		}
		field, _, _ := bytes.Cut(line, []byte{':'})
		return bytes.Equal(field, []byte("data")) || bytes.Equal(field, []byte("event")) ||
			bytes.Equal(field, []byte("id")) || bytes.Equal(field, []byte("retry"))
	}
	return false
}

func observeResponseJSON(payload []byte, record *captureRecord) {
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	var event struct {
		Type     string          `json:"type"`
		Item     json.RawMessage `json:"item"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		if record.CaptureError == "" {
			record.CaptureError = "invalid response JSON"
		}
		return
	}
	if len(event.Item) != 0 {
		observeOutputItem(event.Item, record)
	}
	if len(event.Response) != 0 {
		observeResponseEnvelope(event.Response, record)
		return
	}
	observeResponseEnvelope(payload, record)
}

func observeResponseEnvelope(payload []byte, record *captureRecord) {
	var response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  *struct {
			InputTokens  uint64 `json:"input_tokens"`
			OutputTokens uint64 `json:"output_tokens"`
			InputDetails struct {
				CachedTokens uint64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens uint64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return
	}
	if response.Status != "" {
		record.ResponseStatus = response.Status
	}
	for _, item := range response.Output {
		observeOutputItem(item, record)
	}
	if response.Usage != nil {
		record.Usage = &tokenUsage{
			InputTokens:     response.Usage.InputTokens,
			CachedTokens:    response.Usage.InputDetails.CachedTokens,
			OutputTokens:    response.Usage.OutputTokens,
			ReasoningTokens: response.Usage.OutputDetails.ReasoningTokens,
		}
	}
}

func observeOutputItem(payload []byte, record *captureRecord) {
	var item struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		CallID string `json:"call_id"`
	}
	if json.Unmarshal(payload, &item) != nil || (item.Type != "custom_tool_call" && item.Type != "function_call") {
		return
	}
	for _, candidate := range []string{item.ID, item.CallID} {
		if candidate != "" && !slices.Contains(record.ToolCallIDs, candidate) {
			record.ToolCallIDs = append(record.ToolCallIDs, candidate)
		}
	}
}

func parseURL(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
	}
	return parsed, nil
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("hpatch-benchmark-capturer", flag.ContinueOnError)
	frontListen := flags.String("front-listen", "", "Codex-facing listen address")
	frontTarget := flags.String("front-target", "", "Hpatch router base URL")
	backListen := flags.String("back-listen", "", "Hpatch-facing listen address")
	backTarget := flags.String("back-target", "", "provider base URL")
	output := flags.String("output", "", "sanitized JSONL capture path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *frontListen == "" || *backListen == "" || *output == "" {
		return errors.New("--front-listen, --back-listen, and --output are required")
	}
	frontURL, err := parseURL("--front-target", *frontTarget)
	if err != nil {
		return err
	}
	backURL, err := parseURL("--back-target", *backTarget)
	if err != nil {
		return err
	}
	recorder, err := newCaptureRecorder(*output)
	if err != nil {
		return fmt.Errorf("open capture: %w", err)
	}
	defer recorder.Close()

	servers := []*http.Server{
		{Addr: *frontListen, Handler: newCaptureProxy(frontURL, "codex", recorder), ReadHeaderTimeout: 5 * time.Second},
		{Addr: *backListen, Handler: newCaptureProxy(backURL, "provider", recorder), ReadHeaderTimeout: 5 * time.Second},
	}
	serverErrors := make(chan error, len(servers))
	var wait sync.WaitGroup
	for _, server := range servers {
		wait.Go(func() {
			serverErrors <- server.ListenAndServe()
		})
	}
	var runErr error
	select {
	case <-ctx.Done():
		runErr = context.Cause(ctx)
	case runErr = <-serverErrors:
		if errors.Is(runErr, http.ErrServerClosed) {
			runErr = nil
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	for _, server := range servers {
		runErr = errors.Join(runErr, server.Shutdown(shutdownCtx))
	}
	wait.Wait()
	return runErr
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "capturer: %v\n", err)
		os.Exit(1)
	}
}
