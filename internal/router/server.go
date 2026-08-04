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
	defaultRewriteMode     = "hpatch"
	defaultRequestTimeout  = 10 * time.Minute
	requestBodyReadTimeout = 30 * time.Second
	shutdownTimeout        = 5 * time.Second
	maxResponsesRequest    = 32 << 20
	maxModelsResponseBytes = 8 << 20
)

var errUpstreamResponseWithoutTerminal = errors.New("upstream Responses response ended without a terminal state")

func Run(ctx context.Context, args []string, stderr io.Writer) (runErr error) {
	flags := flag.NewFlagSet("hpatch-router", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", defaultListenAddress, "HTTP listen address")
	timeout := flags.Duration("timeout", defaultRequestTimeout, "upstream response-start timeout")
	mode := flags.String("mode", defaultRewriteMode, "response mode: hpatch or passthrough")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	if *mode != "hpatch" && *mode != "passthrough" {
		return errors.New("--mode must be hpatch or passthrough")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	log := newDiagnostics(stderr)
	provider := newProviderClient(codexBaseURL, nil)
	var gainDirectory string
	var hpatchCalls *hpatchProxy
	if *mode == "hpatch" {
		var err error
		gainDirectory, err = hpatchMetricsDirectory()
		if err != nil {
			return fmt.Errorf("initialize hpatch response proxy: %w", err)
		}
	}
	metrics := newMetricsStore(gainDirectory)
	metrics.mode = *mode
	if *mode == "hpatch" {
		if _, err := ensureHReadSymlink(); err != nil {
			return fmt.Errorf("initialize hread executable: %w", err)
		}
		translator := notifyingHPatchTranslator{
			inner:   newInProcessHPatchTranslator(gainDirectory),
			metrics: metrics,
		}
		hpatchCalls = newHPatchProxy(translator)
		defer func() {
			runErr = errors.Join(runErr, hpatchCalls.Close())
		}()
	}

	var requestSequence atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metrics", metrics.serveAPI)
	mux.HandleFunc("GET /", serveDashboard)
	mux.HandleFunc("GET /v1/models", modelsHandler(provider))
	mux.HandleFunc("POST /v1/responses", responsesHandler(ctx, *timeout, provider, log, hpatchCalls, metrics, &requestSequence))

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       requestBodyReadTimeout,
		IdleTimeout:       2 * time.Minute,
	}
	if err := log.log(ctx, slog.LevelInfo, "listening", "url", fmt.Sprintf("http://%s/v1/responses", *listenAddress), "mode", *mode); err != nil {
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

func modelsHandler(provider *providerClient) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		response, err := provider.forwardModels(request.Context(), request.Header, request.URL.RawQuery)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, maxModelsResponseBytes+1))
		if err != nil {
			http.Error(writer, fmt.Sprintf("read upstream models response: %v", err), http.StatusBadGateway)
			return
		}
		if len(body) > maxModelsResponseBytes {
			http.Error(writer, "upstream models response is too large", http.StatusBadGateway)
			return
		}
		for _, name := range []string{"Content-Type", "Cache-Control", "ETag"} {
			for _, value := range response.Header.Values(name) {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(body)
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
			writeRequestError(trackedWriter, err)
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

func writeRequestError(writer *trackedResponseWriter, requestErr error) {
	if !writer.committed {
		http.Error(writer, requestErr.Error(), http.StatusBadGateway)
	}
}

type requestFailurePhase string

const (
	requestFailurePrepare            requestFailurePhase = "prepare"
	requestFailureForward            requestFailurePhase = "forward"
	requestFailureInspectResponse    requestFailurePhase = "inspect_response"
	requestFailureTransform          requestFailurePhase = "transform"
	requestFailureWriteResponse      requestFailurePhase = "write_response"
	requestFailureTerminalValidation requestFailurePhase = "terminal_validation"
)

type requestFinalization struct {
	observation           requestObservation
	sessionID             string
	failurePhase          requestFailurePhase
	upstreamStatusCode    int
	upstreamTerminalState responseTerminalState
	upstreamStarted       time.Time
	upstreamBegan         bool
}

func (f *requestFinalization) finish(
	ctx context.Context,
	activeRequest *activeRequestHandle,
	requestErr error,
	output io.Writer,
	log diagnostics,
	now func() time.Time,
	totalStarted time.Time,
) error {
	finished := now()
	responseStarted := requestResponseStarted(output)
	f.observation.totalDuration = finished.Sub(totalStarted)
	if f.upstreamBegan {
		f.observation.upstreamDuration = finished.Sub(f.upstreamStarted)
	}
	if f.observation.outcome == requestOutcomeUnknown {
		switch {
		case errors.Is(requestErr, context.DeadlineExceeded):
			f.observation.outcome = requestOutcomeTimedOut
		case errors.Is(requestErr, context.Canceled) && errors.Is(ctx.Err(), context.Canceled):
			if responseStarted {
				f.observation.outcome = requestOutcomeCanceledAfterResponse
			} else {
				f.observation.outcome = requestOutcomeCanceledBeforeResponse
			}
		default:
			f.observation.outcome = requestOutcomeFailed
		}
	}
	activeRequest.finish(f.observation)
	args := []any{
		"session_id", f.sessionID,
		"outcome", f.observation.outcome.String(),
		"failure_phase", string(f.failurePhase),
		"response_started", responseStarted,
		"upstream_status_code", f.upstreamStatusCode,
		"upstream_terminal_state", f.upstreamTerminalState.String(),
		"request_duration", f.observation.totalDuration,
		"upstream_execution_duration", f.observation.upstreamDuration,
		"usage_observed", f.observation.usageObserved,
	}
	if requestErr != nil {
		args = append(args, "err", requestErr)
	}
	if err := log.log(context.WithoutCancel(ctx), slog.LevelInfo, "Responses request finished", args...); err != nil {
		return fmt.Errorf("write terminal log: %w", err)
	}
	return nil
}

func requestResponseStarted(output io.Writer) bool {
	writer, ok := output.(*trackedResponseWriter)
	return ok && writer.committed
}

func (f *requestFinalization) classifyCopyError(err error) {
	switch {
	case errors.Is(err, errResponseTransform):
		f.failurePhase = requestFailureTransform
	case errors.Is(err, errResponseWrite):
		f.failurePhase = requestFailureWriteResponse
	default:
		f.failurePhase = requestFailureInspectResponse
	}
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
) (requestErr error) {
	totalStarted := now()
	var activeRequest *activeRequestHandle
	if metrics != nil {
		activeRequest = metrics.beginRequest(sessionID, parsedRequest.model())
	}
	finalization := requestFinalization{failurePhase: requestFailurePrepare}
	if validMetricSessionID(sessionID) {
		finalization.sessionID = sessionID
	}

	defer func() {
		requestErr = errors.Join(requestErr, finalization.finish(executionCtx, activeRequest, requestErr, output, log, now, totalStarted))
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare request: %w", err)
	}
	var (
		hpatchTransform *hpatchResponseTransform
		err             error
	)
	if hpatchCalls != nil {
		metadata, metadataValid := decodeCodexTurnMetadata(headers)
		hpatchTransform, err = hpatchCalls.prepareRequest(ctx, &parsedRequest, sessionID, metadata, metadataValid)
		if err != nil {
			return fmt.Errorf("prepare hpatch response proxy: %w", err)
		}
	}
	if hpatchTransform != nil {
		defer hpatchTransform.Close()
	}
	forwardBody, err := json.Marshal(parsedRequest.fields)
	if err != nil {
		return fmt.Errorf("encode Responses request: %w", err)
	}
	finalization.failurePhase = requestFailureForward
	if err := log.log(ctx, slog.LevelInfo, "forwarding Responses request"); err != nil {
		return fmt.Errorf("write execution log: %w", err)
	}
	finalization.upstreamStarted = now()
	finalization.upstreamBegan = true
	cacheKey := parsedRequest.promptCacheKey()
	if cacheKey == "" {
		cacheKey = sessionID
	}
	response, err := provider.forwardExecution(ctx, executionCtx, forwardBody, headers, cacheKey)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	finalization.upstreamStatusCode = response.StatusCode
	finalization.failurePhase = requestFailureInspectResponse
	if hpatchTransform != nil {
		hpatchTransform.ctx = executionCtx
	}
	streamResponse, err := prepareUpstreamBody(response, parsedRequest.streamResponse)
	if err != nil {
		response.Body.Close()
		return fmt.Errorf("execute request: inspect upstream response: %w", err)
	}
	var responseTransformer *hpatchResponseTransform
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		responseTransformer = hpatchTransform
	}
	if hpatchTransform != nil && responseTransformer == nil {
		finalization.failurePhase = requestFailureTransform
		if err := hpatchTransform.Finish(false); err != nil {
			response.Body.Close()
			return fmt.Errorf("record hpatch request overhead: %w", err)
		}
	}
	observeUsage := func(counts tokenCounts) {
		finalization.observation.usageCounts = counts
		finalization.observation.usageObserved = true
	}

	var stagedBody []byte
	if !streamResponse {
		var staged bytes.Buffer
		finalization.upstreamTerminalState, err = copyUpstreamBodyTransformed(&staged, response, false, responseTransformer, observeUsage)
		if err != nil {
			finalization.classifyCopyError(err)
			return fmt.Errorf("execute request: %w", err)
		}
		stagedBody = staged.Bytes()
	}
	if !streamResponse && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && !acceptsResponseEnd(finalization.upstreamTerminalState) {
		finalization.failurePhase = requestFailureTerminalValidation
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
			finalization.failurePhase = requestFailureWriteResponse
			if err := http.NewResponseController(writer).Flush(); err != nil {
				response.Body.Close()
				return fmt.Errorf("execute request: flush response headers: %w", err)
			}
		}
	}
	if stagedBody != nil {
		finalization.failurePhase = requestFailureWriteResponse
		if _, err := output.Write(stagedBody); err != nil {
			return fmt.Errorf("execute request: copy upstream response: %w", err)
		}
	} else {
		finalization.failurePhase = requestFailureInspectResponse
		finalization.upstreamTerminalState, err = copyUpstreamBodyTransformed(output, response, streamResponse, responseTransformer, observeUsage)
		if err != nil {
			finalization.classifyCopyError(err)
			return fmt.Errorf("execute request: %w", err)
		}
	}
	if streamResponse && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && !acceptsResponseEnd(finalization.upstreamTerminalState) {
		finalization.failurePhase = requestFailureTerminalValidation
		return fmt.Errorf("execute request: %w", errUpstreamResponseWithoutTerminal)
	}
	finalization.failurePhase = ""
	switch {
	case response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices:
		finalization.observation.outcome = requestOutcomeFailed
		finalization.failurePhase = requestFailureTerminalValidation
	case finalization.upstreamTerminalState == responseTerminalCompleted:
		finalization.observation.outcome = requestOutcomeCompleted
	case finalization.upstreamTerminalState == responseTerminalFailed:
		finalization.observation.outcome = requestOutcomeFailed
		finalization.failurePhase = requestFailureTerminalValidation
	default:
		finalization.observation.outcome = requestOutcomeFailed
		finalization.failurePhase = requestFailureTerminalValidation
	}
	return nil
}

func acceptsResponseEnd(state responseTerminalState) bool {
	return state == responseTerminalCompleted || state == responseTerminalFailed
}
