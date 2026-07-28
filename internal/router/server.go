package router

// Source: main.go:20:551 HTTP lifecycle and Responses proxy execution.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	defaultListenAddress   = "127.0.0.1:8080"
	defaultRequestTimeout  = 10 * time.Minute
	requestBodyReadTimeout = 30 * time.Second
	shutdownTimeout        = 5 * time.Second
	maxResponsesRequest    = 32 << 20
)

var errUpstreamResponseWithoutTerminal = errors.New("upstream Responses response ended without a terminal or resumable background state")

func Run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("hpatch-router", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", defaultListenAddress, "HTTP listen address")
	timeout := flags.Duration("timeout", defaultRequestTimeout, "upstream response-start timeout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	auth, err := loadCodexAuth()
	if err != nil {
		return err
	}
	log := newDiagnostics(stderr)
	provider := newProviderClient(auth, nil)
	translator, err := newInProcessHPatchTranslator()
	if err != nil {
		return fmt.Errorf("initialize hpatch response proxy: %w", err)
	}
	hpatchCalls := newHPatchProxy(translator)
	metrics := newMetricsStore()
	var requestSequence atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metrics", metrics.serveAPI)
	mux.HandleFunc("GET /", serveDashboard)
	mux.HandleFunc("POST /v1/responses", responsesHandler(ctx, *timeout, provider, log, hpatchCalls, metrics, &requestSequence))

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       requestBodyReadTimeout,
		IdleTimeout:       2 * time.Minute,
	}
	if err := log.log(ctx, slog.LevelInfo, "listening", "url", fmt.Sprintf("http://%s/v1/responses", *listenAddress)); err != nil {
		return fmt.Errorf("write listening log: %w", err)
	}
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.ListenAndServe()
	}()
	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-serverError
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(ctx.Err(), shutdownErr, serveErr)
	}
}

func responsesHandler(
	lifecycle context.Context,
	responseStartTimeout time.Duration,
	provider responseProvider,
	log diagnostics,
	hpatchCalls *hpatchProxy,
	metrics *metricsStore,
	requestSequence *atomic.Uint64,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		trackedWriter := &trackedResponseWriter{ResponseWriter: writer}
		requestLog := log.with("request_id", requestSequence.Add(1))
		request.Body = http.MaxBytesReader(trackedWriter, request.Body, maxResponsesRequest)
		body, err := readResponsesRequest(request.Body)
		if err != nil {
			if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
				http.Error(trackedWriter, "Responses request body is too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(trackedWriter, err.Error(), http.StatusBadRequest)
			return
		}
		parsedRequest, err := parseResponsesRequest(body)
		if err != nil {
			http.Error(trackedWriter, err.Error(), http.StatusBadRequest)
			return
		}
		startCtx, executionCtx, cancelRequest := requestContexts(request.Context(), lifecycle, responseStartTimeout)
		defer cancelRequest()
		sessionID := routingSessionID(request.Header, parsedRequest)
		if err := executeRequest(startCtx, executionCtx, parsedRequest, request.Header, sessionID, provider, trackedWriter, requestLog, time.Now, hpatchCalls, metrics); err != nil {
			writeRequestError(executionCtx, trackedWriter, err, requestLog)
		}
	}
}

func requestContexts(requestCtx, serverCtx context.Context, responseStartTimeout time.Duration) (context.Context, context.Context, func()) {
	executionCtx, cancelExecution := context.WithCancel(requestCtx)
	stopServerCancellation := context.AfterFunc(serverCtx, cancelExecution)
	startCtx, cancelStart := context.WithTimeout(executionCtx, responseStartTimeout)
	return startCtx, executionCtx, func() {
		stopServerCancellation()
		cancelStart()
		cancelExecution()
	}
}

type trackedResponseWriter struct {
	http.ResponseWriter

	committed bool
}

func (w *trackedResponseWriter) WriteHeader(statusCode int) {
	if w.committed {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *trackedResponseWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *trackedResponseWriter) FlushError() error {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *trackedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func writeRequestError(ctx context.Context, writer *trackedResponseWriter, requestErr error, log diagnostics) {
	if !writer.committed {
		http.Error(writer, requestErr.Error(), http.StatusBadGateway)
		return
	}
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(requestErr, context.Canceled) {
		_ = log.log(ctx, slog.LevelDebug, "Responses request canceled after response started", "err", requestErr)
		return
	}
	_ = log.log(ctx, slog.LevelError, "Responses request failed after response started", "err", requestErr)
}

func executeRequest(
	ctx context.Context,
	executionCtx context.Context,
	parsedRequest parsedResponsesRequest,
	headers http.Header,
	sessionID string,
	provider responseProvider,
	output io.Writer,
	log diagnostics,
	now func() time.Time,
	hpatchCalls *hpatchProxy,
	metrics *metricsStore,
) error {
	activeRequest := metrics.beginRequest(sessionID, parsedRequest.model())
	defer activeRequest.end()
	totalStarted := now()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare request: %w", err)
	}
	metadata, metadataValid := decodeCodexTurnMetadata(headers)
	hpatchTransform, err := hpatchCalls.prepareRequest(ctx, &parsedRequest, sessionID, metadata, metadataValid)
	if err != nil {
		return fmt.Errorf("prepare hpatch response proxy: %w", err)
	}
	if hpatchTransform != nil {
		defer hpatchTransform.Close()
	}
	forwardBody, err := json.Marshal(parsedRequest.fields)
	if err != nil {
		return fmt.Errorf("encode Responses request: %w", err)
	}
	if err := log.log(ctx, slog.LevelInfo, "forwarding Responses request"); err != nil {
		return fmt.Errorf("write execution log: %w", err)
	}
	started := now()
	response, err := provider.forwardExecution(ctx, executionCtx, forwardBody)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	if hpatchTransform != nil {
		hpatchTransform.ctx = executionCtx
	}
	streamResponse, err := prepareUpstreamBody(response, parsedRequest.streamResponse)
	if err != nil {
		response.Body.Close()
		return fmt.Errorf("execute request: inspect upstream response: %w", err)
	}
	var (
		usageCounts   tokenCounts
		usageObserved bool
		terminalState responseTerminalState
	)
	var responseTransformer *hpatchResponseTransform
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		responseTransformer = hpatchTransform
	}
	if hpatchTransform != nil && responseTransformer == nil {
		if err := hpatchTransform.Finish(false); err != nil {
			response.Body.Close()
			return fmt.Errorf("record hpatch request overhead: %w", err)
		}
	}
	observeUsage := func(counts tokenCounts) {
		usageCounts = counts
		usageObserved = true
	}

	var stagedBody []byte
	if !streamResponse {
		var staged bytes.Buffer
		terminalState, err = copyUpstreamBodyTransformed(&staged, response, false, responseTransformer, observeUsage)
		if err != nil {
			return fmt.Errorf("execute request: %w", err)
		}
		stagedBody = staged.Bytes()
	}
	if !streamResponse && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && !acceptsResponseEnd(parsedRequest, terminalState) {
		return fmt.Errorf("execute request: %w", errUpstreamResponseWithoutTerminal)
	}
	if writer, ok := output.(http.ResponseWriter); ok {
		for name, values := range response.Header {
			if http.CanonicalHeaderKey(name) != "Content-Length" {
				writer.Header()[name] = append([]string(nil), values...)
			}
		}
		writer.WriteHeader(response.StatusCode)
	}
	if streamResponse {
		if writer, ok := output.(http.ResponseWriter); ok {
			if err := http.NewResponseController(writer).Flush(); err != nil {
				response.Body.Close()
				return fmt.Errorf("execute request: flush response headers: %w", err)
			}
		}
	}
	if stagedBody != nil {
		if _, err := output.Write(stagedBody); err != nil {
			return fmt.Errorf("execute request: copy upstream response: %w", err)
		}
	} else {
		terminalState, err = copyUpstreamBodyTransformed(output, response, streamResponse, responseTransformer, observeUsage)
		if err != nil {
			return fmt.Errorf("execute request: %w", err)
		}
	}
	if streamResponse && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && !acceptsResponseEnd(parsedRequest, terminalState) {
		return fmt.Errorf("execute request: %w", errUpstreamResponseWithoutTerminal)
	}
	responseCompleted := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && terminalState == responseTerminalCompleted
	if responseCompleted && usageObserved {
		metrics.record(sessionID, parsedRequest.model(), usageCounts)
	}
	finished := now()
	if err := log.log(executionCtx, slog.LevelInfo, "Responses request completed",
		"upstream_status_code", response.StatusCode,
		"upstream_terminal_state", terminalState.String(),
		"request_duration", finished.Sub(totalStarted),
		"upstream_execution_duration", finished.Sub(started),
	); err != nil {
		return fmt.Errorf("write completion log: %w", err)
	}
	return nil
}

func acceptsResponseEnd(request parsedResponsesRequest, state responseTerminalState) bool {
	if state == responseTerminalCompleted || state == responseTerminalFailed {
		return true
	}
	return request.backgroundResponse && state == responseTerminalPending
}
