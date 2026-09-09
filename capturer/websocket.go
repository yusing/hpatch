package capturer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"time"
)

const webSocketContentType = "application/x-openai-websocket-json"

// WebSocketAttempt observes one response.create exchange on a possibly reused
// connection. Message records contain actual JSON payload bytes, not WebSocket
// frame headers, control frames, or the router's downstream SSE serialization.
// Like Transport, this is a transport-boundary observer, not a metrics callback.
// Its owner calls Finish after terminal usage observation and body consumption.
type WebSocketAttempt struct {
	state    *requestState
	attempt  uint64
	started  time.Time
	request  []byte
	response boundedObservation
	evidence providerResponseEvidence
	finished bool
}

// BeginWebSocketAttempt is a no-op outside a Recorder handler. Headers describe
// this exchange's routing, not a prior exchange's connection handshake.
func BeginWebSocketAttempt(ctx context.Context, payload []byte, headers, responseHeaders http.Header) *WebSocketAttempt {
	state, ok := ctx.Value(captureKey{}).(*requestState)
	if !ok {
		return nil
	}
	attempt := state.beginProviderAttempt()
	var request struct {
		Metadata map[string]json.RawMessage `json:"client_metadata"`
	}
	var turnState string
	if json.Unmarshal(payload, &request) == nil && json.Unmarshal(request.Metadata["x-codex-turn-state"], &turnState) == nil {
		headers = headers.Clone()
		if headers == nil {
			headers = make(http.Header)
		}
		headers.Set("x-codex-turn-state", turnState)
	}
	state.observeProviderRouting(attempt, headers)
	return &WebSocketAttempt{state: state, attempt: attempt, started: time.Now(), request: bytes.Clone(payload), evidence: providerHeaderEvidence(responseHeaders)}
}

func (state *requestState) observeProviderRouting(attempt uint64, headers http.Header) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.providerRouting == nil {
		state.providerRouting = make(map[uint64]requestRouting)
	}
	routing := requestRouting{turnState: state.recorder.turnStateFingerprint(headers.Get("x-codex-turn-state"))}
	if key := headers.Get("Session_id"); key != "" {
		routing.sessionKey = state.recorder.fingerprint("cache-key", key)
	}
	state.providerRouting[attempt] = routing
}

// Message measures the bytes read from one provider text message. Observation
// remains bounded even when the complete exchange exceeds the parsing budget.
func (attempt *WebSocketAttempt) Message(payload []byte) {
	if attempt == nil || attempt.finished {
		return
	}
	_, _ = attempt.response.Write(payload)
}

func (attempt *WebSocketAttempt) Finish(err error) {
	if attempt == nil || attempt.finished {
		return
	}
	attempt.finished = true
	attempt.state.recorder.recordExchange(attempt.state, "provider", attempt.attempt, attempt.started, attempt.request, attempt.response.snapshot(), http.StatusSwitchingProtocols, webSocketContentType, "", err, attempt.evidence)
	attempt.request = nil
	attempt.response = boundedObservation{}
}

// HTTPErrorBody observes a rejected upgrade's actual HTTP body. No inference
// payload was sent, so its request measurement is empty rather than fabricated.
func (attempt *WebSocketAttempt) HTTPErrorBody(body io.ReadCloser, status int, contentType, contentEncoding string) io.ReadCloser {
	if attempt == nil {
		return body
	}
	return &observedResponseBody{ReadCloser: body, finish: func(response observedPayload, err error) {
		if attempt.finished {
			return
		}
		attempt.finished = true
		attempt.state.recorder.recordExchange(attempt.state, "provider", attempt.attempt, attempt.started, nil, response, status, contentType, contentEncoding, err, attempt.evidence)
	}}
}

func webSocketMessages(payload []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		for {
			var message json.RawMessage
			if err := decoder.Decode(&message); err != nil {
				return
			}
			if !yield(message) {
				return
			}
		}
	}
}
