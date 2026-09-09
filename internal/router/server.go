package router

// Source: main.go:20:551 HTTP lifecycle and Responses proxy execution.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yusing/hpatch/capturer"
)

const (
	defaultListenAddress        = "127.0.0.1:0"
	defaultRewriteMode          = "hpatch"
	defaultModelProtocol        = "ctp2"
	defaultRequestTimeout       = 10 * time.Minute
	defaultStreamIdleTimeout    = 4 * time.Minute
	requestBodyReadTimeout      = 30 * time.Second
	shutdownTimeout             = 5 * time.Second
	responsesRequestBufferBytes = 32 << 20
	modelsResponseBufferBytes   = 8 << 20
)

var errUpstreamResponseWithoutTerminal = errors.New("upstream Responses response ended without a terminal state")

// Session is available only after initialization and listener binding succeed.
type Session struct {
	BaseURL           string
	FrontendDirectory string
}

// RunSession owns the private router for one wrapped Codex process.
func RunSession(ctx context.Context, args []string, issues *CriticalErrors, ready func(Session)) (runErr error) {
	flags := newRouterFlags(io.Discard)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	if *flags.mode != "hpatch" && *flags.mode != "passthrough" {
		return errors.New("--mode must be hpatch or passthrough")
	}
	if *flags.modelProtocol != "native" && *flags.modelProtocol != "ctp2" {
		return errors.New("--model-protocol must be native or ctp2")
	}
	protocolSet := false
	mentorSet := false
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "model-protocol":
			protocolSet = true
		case "mentor-handoff":
			mentorSet = true
		}
	})
	if *flags.mode == "passthrough" {
		if protocolSet && *flags.modelProtocol != "native" {
			return errors.New("--model-protocol ctp2 requires --mode hpatch")
		}
		if mentorSet && *flags.mentorHandoffEnabled {
			return errors.New("--mentor-handoff requires --mode hpatch")
		}
		*flags.modelProtocol = "native"
		*flags.mentorHandoffEnabled = false
	}
	if *flags.grokEnabled && *flags.mode != "hpatch" {
		return errors.New("--grok requires --mode hpatch")
	}
	if !*flags.grokEnabled && *flags.grokAuthFile != "" {
		return errors.New("--grok-auth-file requires --grok")
	}
	if *flags.timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	if *flags.streamIdleTimeout <= 0 {
		return errors.New("--stream-idle-timeout must be positive")
	}
	capture, err := capturer.New(capturer.Config{Output: *flags.captureOutput, Mode: *flags.mode, ModelProtocol: *flags.modelProtocol})
	if err != nil {
		return fmt.Errorf("initialize capture: %w", err)
	}
	var metricsFile *os.File
	if *flags.metricsOutput != "" {
		metricsPath, err := filepath.Abs(*flags.metricsOutput)
		if err != nil {
			return errors.Join(err, capture.Close())
		}
		capturePath, _ := filepath.Abs(*flags.captureOutput)
		if *flags.captureOutput != "" && metricsPath == capturePath {
			return errors.Join(errors.New("capture-output and metrics-output must use different files"), capture.Close())
		}
		if *flags.captureOutput != "" {
			captureInfo, captureErr := os.Stat(capturePath)
			metricsInfo, metricsErr := os.Stat(metricsPath)
			if captureErr == nil && metricsErr == nil && os.SameFile(captureInfo, metricsInfo) {
				return errors.Join(errors.New("capture-output and metrics-output must use different files"), capture.Close())
			}
		}
		metricsFile, err = os.OpenFile(metricsPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return errors.Join(fmt.Errorf("open metrics output: %w", err), capture.Close())
		}
	}
	defer func() {
		if metricsFile != nil {
			runErr = errors.Join(runErr, capture.WriteMetrics(metricsFile), metricsFile.Close())
		}
		runErr = errors.Join(runErr, capture.Close())
	}()
	provider := newProviderClient(codexBaseURL, nil)
	provider.httpClient.Transport = capture.Transport(provider.httpClient.Transport)
	provider.streamIdleTimeout = *flags.streamIdleTimeout
	provider.enableWebSockets(ctx)
	// Cover early setup returns before ctx is canceled; close is idempotent
	// with enableWebSockets' normal context-shutdown callback.
	defer provider.websockets.close()
	if *flags.grokEnabled {
		apiKey := strings.TrimSpace(os.Getenv("XAI_API_KEY"))
		path := *flags.grokAuthFile
		if path == "" && apiKey == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locate Grok credentials: %w", err)
			}
			path = filepath.Join(home, ".grok", "auth.json")
		}
		auth := newGrokAuth(path, apiKey)
		client := withDialTimeout(nil)
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client.Transport = capture.Transport(client.Transport)
		provider.grok = &grokClient{httpClient: client, auth: auth, streamIdleTimeout: *flags.streamIdleTimeout}
	}
	var frontendDirectory string
	var dataDirectory string
	var hpatchCalls *hpatchProxy
	var compactTokens *ctp2Codec
	var mentor *mentorHandoff
	if *flags.mode == "hpatch" {
		var err error
		dataDirectory, err = hpatchDataDirectory()
		if err != nil {
			return fmt.Errorf("initialize hpatch response proxy: %w", err)
		}
		if *flags.modelProtocol == "ctp2" {
			compactTokens, err = newCTP2Codec()
			if err != nil {
				return fmt.Errorf("initialize compact token protocol: %w", err)
			}
		}
	}
	if *flags.mentorHandoffEnabled {
		mentor = newMentorHandoff()
	}
	titles := newSessionTitleCache()
	if *flags.mode == "hpatch" {
		translator := newInProcessHPatchTranslator(dataDirectory)
		customizedInstructions, err := codexModelInstructionFileConfigured()
		if err != nil {
			return fmt.Errorf("initialize model instruction rewriting: %w", err)
		}
		registry, err := buildToolRegistry(ctx, dataDirectory, translator.ToolDescription(), os.Getenv("HPATCH_DIAGNOSE") == "1")
		if err != nil {
			return fmt.Errorf("initialize tool registry: %w", err)
		}
		if err := registry.installFrontends(); err != nil {
			return errors.Join(
				fmt.Errorf("initialize configured tool frontends: %w", err),
				registry.Close(),
			)
		}
		defer func() {
			runErr = errors.Join(runErr, registry.Close())
		}()
		frontendDirectory = registry.frontendDirectory
		replayDirectory, err := defaultHPatchReplayDirectory()
		if err != nil {
			return fmt.Errorf("initialize replay storage: %w", err)
		}
		replayStore, err := openHPatchReplayStore(replayDirectory)
		if err != nil {
			return fmt.Errorf("initialize replay storage: %w", err)
		}
		hpatchCalls = newHPatchProxy(translator, registry, customizedInstructions, compactTokens != nil, titles)
		hpatchCalls.replayStore = replayStore
		defer func() {
			runErr = errors.Join(runErr, hpatchCalls.Close())
		}()
	}

	listener, err := net.Listen("tcp", defaultListenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()
	address := listener.Addr().String()
	if hpatchCalls != nil {
		hpatchCalls.commentaryEndpoint, err = commentaryPublisherURL(address)
		if err != nil {
			return fmt.Errorf("initialize commentary publisher: %w", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveDashboard)
	mux.HandleFunc("GET /api/metrics", capture.ServeHTTP)
	mux.HandleFunc("GET /v1/models", modelsHandler(provider, issues))
	if hpatchCalls != nil {
		mux.HandleFunc("POST "+commentaryPublisherPath, hpatchCalls.commentary.serveHTTP)
	}
	mux.HandleFunc("POST /v1/responses", responsesHandler(ctx, *flags.timeout, provider, issues, hpatchCalls, compactTokens, mentor))

	server := &http.Server{
		ErrorLog:          log.New(io.Discard, "", 0), // Disable net/http terminal diagnostics while Codex owns it.
		Addr:              defaultListenAddress,
		Handler:           capture.Handler(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       requestBodyReadTimeout,
		IdleTimeout:       2 * time.Minute,
	}
	baseURL := "http://" + address + "/v1"
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.Serve(listener)
	}()
	if ready != nil && ctx.Err() == nil {
		ready(Session{BaseURL: baseURL, FrontendDirectory: frontendDirectory})
	}
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
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		serveErr := <-serverError
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}

func modelsHandler(provider *providerClient, issues *CriticalErrors) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		tracked := &trackedResponseWriter{ResponseWriter: writer}
		writer = tracked
		defer func() {
			if tracked.statusCode >= 400 && request.Context().Err() == nil {
				issues.record(&requestFinalization{sessionID: request.Header.Get(sessionIDHeader), failurePhase: requestFailureForward,
					upstreamStatusCode: tracked.statusCode, observation: requestObservation{outcome: requestOutcomeFailed}}, nil)
			}
		}()

		response, err := provider.forwardModels(request.Context(), request.Header, request.URL.RawQuery)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, modelsResponseBufferBytes+1))
		if err != nil {
			http.Error(writer, fmt.Sprintf("read upstream models response: %v", err), http.StatusBadGateway)
			return
		}
		if len(body) > modelsResponseBufferBytes {
			http.Error(writer, "upstream models response exceeds the router buffer budget", http.StatusBadGateway)
			return
		}
		if provider.grok != nil && response.StatusCode == http.StatusOK {
			body, err = appendGrokModel(body)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadGateway)
				return
			}
			sum := sha256.Sum256(body)
			response.Header.Set("ETag", fmt.Sprintf(`"hpatch-%x"`, sum))
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
	issues *CriticalErrors,
	hpatchCalls *hpatchProxy,
	compactTokens *ctp2Codec,
	mentor *mentorHandoff,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		trackedWriter := &trackedResponseWriter{ResponseWriter: writer}
		body, err := readResponsesRequest(io.LimitReader(request.Body, responsesRequestBufferBytes+1))
		if err != nil {
			http.Error(trackedWriter, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) > responsesRequestBufferBytes {
			http.Error(trackedWriter, "Responses request exceeds the router buffer budget", http.StatusRequestEntityTooLarge)
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
		if err := executeRequest(startCtx, executionCtx, parsedRequest, request.Header, sessionID, provider, trackedWriter, issues, hpatchCalls, compactTokens, mentor); err != nil {
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

	committed  bool
	statusCode int
}

func (w *trackedResponseWriter) WriteHeader(statusCode int) {
	if w.committed {
		return
	}
	w.committed = true
	w.statusCode = statusCode
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
		status := http.StatusBadGateway
		if _, ok := errors.AsType[*requestCompatibilityError](requestErr); ok {
			status = http.StatusBadRequest
		}
		http.Error(writer, requestErr.Error(), status)
	}
}

type requestFailurePhase string

const (
	requestFailurePrepare            requestFailurePhase = "prepare"
	requestFailureForward            requestFailurePhase = "forward"
	requestFailureInspectResponse    requestFailurePhase = "inspect_response"
	requestFailureStreamIdleTimeout  requestFailurePhase = "stream_idle_timeout"
	requestFailureTransform          requestFailurePhase = "transform"
	requestFailureWriteResponse      requestFailurePhase = "write_response"
	requestFailureTerminalValidation requestFailurePhase = "terminal_validation"
)

type requestFinalization struct {
	observeCriticalNotice func(source, text string)
	observation           requestObservation
	sessionID             string
	failurePhase          requestFailurePhase
	upstreamStatusCode    int
	upstreamTerminalState responseTerminalState
}

func (f *requestFinalization) finish(
	ctx context.Context,
	requestErr error,
	output io.Writer,
	issues *CriticalErrors,
) error {
	responseStarted := requestResponseStarted(output)
	if f.observation.outcome == requestOutcomeUnknown {
		switch {
		case errors.Is(requestErr, context.DeadlineExceeded):
			f.observation.outcome = requestOutcomeTimedOut
		case errors.Is(requestErr, context.Canceled) && errors.Is(ctx.Err(), context.Canceled):
			if errors.Is(requestErr, errUpstreamStreamIdleTimeout) {
				f.failurePhase = requestFailureInspectResponse
			}
			if responseStarted {
				f.observation.outcome = requestOutcomeCanceledAfterResponse
			} else {
				f.observation.outcome = requestOutcomeCanceledBeforeResponse
			}
		case errors.Is(requestErr, errUpstreamStreamIdleTimeout):
			f.observation.outcome = requestOutcomeStreamIdleTimedOut
		default:
			f.observation.outcome = requestOutcomeFailed
		}
	}
	issues.record(f, requestErr)

	return nil
}

func requestResponseStarted(output io.Writer) bool {
	writer, ok := output.(*trackedResponseWriter)
	return ok && writer.committed
}

func (f *requestFinalization) classifyCopyError(err error) {
	switch {
	case errors.Is(err, errUpstreamStreamIdleTimeout):
		f.failurePhase = requestFailureStreamIdleTimeout
	case errors.Is(err, errResponseTransform):
		f.failurePhase = requestFailureTransform
	case errors.Is(err, errResponseWrite):
		f.failurePhase = requestFailureWriteResponse
	default:
		f.failurePhase = requestFailureInspectResponse
	}
}

// executeRequest executes a parsed Responses request through the router pipeline.
func executeRequest(
	ctx context.Context,
	executionCtx context.Context,
	parsedRequest parsedResponsesRequest,
	headers http.Header,
	sessionID string,
	provider responseProvider,
	output io.Writer,
	issues *CriticalErrors,
	hpatchCalls *hpatchProxy,
	compactTokens *ctp2Codec,
	mentor *mentorHandoff,
) (requestErr error) {
	finalization := requestFinalization{failurePhase: requestFailurePrepare}
	if sessionID != "" {
		finalization.sessionID = sessionID
	}

	defer func() {
		requestErr = errors.Join(requestErr, finalization.finish(executionCtx, requestErr, output, issues))
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare request: %w", err)
	}
	metadata, metadataValid := decodeCodexTurnMetadata(headers)
	threadID := codexThreadID(headers)
	if hpatchCalls != nil && metadataValid && !metadata.activityIdentityInvalid && (metadata.ThreadID == "" || metadata.ThreadID == threadID) {
		finalization.observeCriticalNotice = func(source, text string) { hpatchCalls.activity.collect(threadID, source, "error", text) }
	}
	issues.stripInput(&parsedRequest, sessionID)
	notices := issues.transform(sessionID, metadata.SubagentKind != "")
	defer func() { notices.finish(requestErr == nil) }()
	handoffRequest, err := mentor.prepare(headers, metadata, metadataValid, &parsedRequest)
	if err != nil {
		return fmt.Errorf("prepare Mentor Handoff: %w", err)
	}
	handoffRecorded := false
	recordHandoff := func(includeCompletedOutput bool) {
		if handoffRequest == nil || handoffRecorded {
			return
		}
		handoffRequest.record(finalization.observation.usageCounts.InputTokens, includeCompletedOutput)
		handoffRecorded = true
	}
	defer func() {
		if finalization.observation.usageObserved {
			recordHandoff(false)
		}
	}()
	var hpatchTransform *hpatchResponseTransform
	if hpatchCalls != nil {
		hpatchTransform, err = hpatchCalls.prepareRequest(
			ctx,
			&parsedRequest,
			sessionID,
			codexThreadID(headers),
			metadata,
			metadataValid,
		)
		if err != nil {
			return fmt.Errorf("prepare hpatch response proxy: %w", err)
		}
		if metadataValid && metadata.RequestKind == "compaction" {
			compactTokens = nil
		}
	}
	if hpatchCalls != nil {
		workspace, usable := usableRoutingDirectory(metadata.Directories)
		if hpatchTransform != nil {
			workspace, usable = hpatchTransform.directory, true
		}
		if usable {
			notices.retain(ctx, hpatchCalls.replayStore, workspace)
		} else {
			// Without a canonical namespace the notice cannot be stripped from a
			// later ordinary turn, so keep it queued instead of emitting it.
			notices.suppress()
		}
	}
	if hpatchTransform != nil {
		defer hpatchTransform.Close()
	}
	var bridge *subagentBridge
	var compactTransform *ctp2ResponseTransform
	if client, ok := provider.(*providerClient); ok && client.grok != nil {
		bridge, err = prepareSubagentBridge(&parsedRequest)
		if err != nil {
			return fmt.Errorf("prepare Grok collaboration bridge: %w", err)
		}
	}
	nativeBody, err := parsedRequest.wireBody(parsedRequest.fields)
	if err != nil {
		return fmt.Errorf("encode native Responses request: %w", err)
	}
	capturer.ObserveNativeRequest(ctx, nativeBody)
	var forwardBody []byte
	compactTransform, forwardBody, err = compactTokens.prepareRequest(&parsedRequest, nativeBody)
	if err != nil {
		return fmt.Errorf("prepare compact token protocol: %w", err)
	}
	if forwardBody == nil {
		forwardBody = nativeBody
	}
	finalization.failurePhase = requestFailureForward
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
	var responseTransform responseTransformer
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if handoffRequest != nil {
			responseTransform = &handoffRequest.observation
		}
		if compactTransform != nil {
			responseTransform = composeResponseTransformers(responseTransform, compactTransform)
		}
		if bridge != nil {
			responseTransform = composeResponseTransformers(responseTransform, bridge)
		}
		if hpatchTransform != nil {
			responseTransform = composeResponseTransformers(responseTransform, hpatchTransform)
		}
		if notices != nil {
			responseTransform = composeResponseTransformers(responseTransform, notices)
		}
	}
	if hpatchTransform != nil && responseTransform == nil {
		finalization.failurePhase = requestFailureTransform
		if err := hpatchTransform.Finish(false); err != nil {
			response.Body.Close()
			return fmt.Errorf("record hpatch request overhead: %w", err)
		}
	}
	var untransformedUsage *threadUsageObservation
	if hpatchCalls != nil && metadataValid {
		untransformedUsage = hpatchCalls.usage.observation(threadID, metadata.ThreadID)
	}
	observeUsage := func(counts tokenCounts) {
		finalization.observation.usageCounts = counts
		finalization.observation.usageObserved = true
		if hpatchTransform != nil {
			hpatchTransform.observeResponseUsage(counts)
		} else {
			// Compaction has no hpatch response transform, but still consumes
			// provider tokens belonging to the same stable thread.
			untransformedUsage.observe(counts)
		}

		capturer.ObserveProviderUsage(executionCtx, capturer.ProviderUsage{
			InputTokens:     counts.InputTokens,
			CachedTokens:    counts.InputTokens - counts.UncachedInputTokens,
			OutputTokens:    counts.OutputTokens,
			ReasoningTokens: counts.ReasoningTokens,
		})
	}

	var stagedBody []byte
	if !streamResponse {
		var staged bytes.Buffer
		finalization.upstreamTerminalState, err = copyUpstreamBodyTransformed(&staged, response, false, responseTransform, observeUsage)
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
		finalization.upstreamTerminalState, err = copyUpstreamBodyTransformed(output, response, streamResponse, responseTransform, observeUsage)
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
		recordHandoff(true)
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
