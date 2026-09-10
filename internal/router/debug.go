package router

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Debug output is separate from sanitized capture. Only the instruction dump
// contains prompt text; diagnostics never serialize arbitrary errors or headers.
type debugOutput struct {
	mu    sync.Mutex
	log   *os.File
	dump  *os.File
	paths []string
	err   error
}

type debugContextKey struct{}

func openDebugOutput(flags routerFlags) (*debugOutput, error) {
	if !*flags.debug {
		return nil, nil
	}
	directory, err := os.MkdirTemp("", "hpatch-debug-")
	if err != nil {
		return nil, fmt.Errorf("create debug directory: %w", err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if *flags.captureOutput == "" {
		*flags.captureOutput = filepath.Join(directory, "capture.jsonl")
	}
	if *flags.metricsOutput == "" {
		*flags.metricsOutput = filepath.Join(directory, "metrics.json")
	}
	capture, err := filepath.Abs(*flags.captureOutput)
	if err != nil {
		return nil, err
	}
	metrics, err := filepath.Abs(*flags.metricsOutput)
	if err != nil {
		return nil, err
	}
	d := &debugOutput{paths: []string{filepath.Join(directory, "router.jsonl"), capture, metrics, filepath.Join(directory, "instructions.jsonl")}}
	d.log, err = os.OpenFile(d.paths[0], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		d.dump, err = os.OpenFile(d.paths[3], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize debug artifacts in %s: %w", directory, errors.Join(err, d.close()))
	}
	d.event(map[string]any{"event": "router_start"})
	return d, nil
}

func (d *debugOutput) handler(next http.Handler) http.Handler {
	if d == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), debugContextKey{}, d)))
	})
}

func (d *debugOutput) write(file *os.File, value any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		d.err = json.NewEncoder(file).Encode(value)
	}
}

func (d *debugOutput) event(fields map[string]any) {
	if d == nil {
		return
	}
	fields["timestamp"] = time.Now().UTC()
	d.write(d.log, fields)
}

func (d *debugOutput) instructions(body []byte, headers http.Header, sessionID, requestID string, cachedInput int) {
	if d == nil {
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		d.mu.Lock()
		d.err = errors.Join(d.err, err)
		d.mu.Unlock()
		return
	}
	// Export the effective Responses input before cached-prefix elision. This
	// keeps inherited developer messages available on incremental continuations.
	var input []json.RawMessage
	_ = json.Unmarshal(fields["input"], &input)
	developers := []json.RawMessage{}
	additional := []json.RawMessage{}
	for _, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if jsonString(item, "role") == "developer" {
			developers = append(developers, raw)
		}
		if jsonString(item, "type") == "additional_tools" {
			additional = append(additional, raw)
		}
	}
	d.write(d.dump, map[string]any{
		"timestamp": time.Now().UTC(), "request_id": requestID,
		"client_request_id": headers.Get("x-client-request-id"),
		"thread_id":         codexThreadID(headers), "session_id": sessionID,
		"scope": "effective_responses_request", "cached_input_items": cachedInput,
		"model": fields["model"], "previous_response_id": fields["previous_response_id"],
		"instructions": fields["instructions"], "developer_messages": developers,
		"tools": fields["tools"], "additional_tools": additional,
	})
}

func debugRequest(ctx context.Context) (*debugOutput, string) {
	d, _ := ctx.Value(debugContextKey{}).(*debugOutput)
	if d == nil {
		return nil, ""
	}
	return d, rand.Text()
}

func (d *debugOutput) close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var closeErr error
	for _, file := range []*os.File{d.log, d.dump} {
		if file != nil {
			closeErr = errors.Join(closeErr, file.Close())
		}
	}
	return errors.Join(d.err, closeErr)
}
