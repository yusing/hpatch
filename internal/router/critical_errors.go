package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// CriticalErrors retains only bounded, actionable session notices, never raw
// request data or an operational event history. It outlives router shutdown so
// the launcher can report notices that could not reach Codex.
type CriticalErrors struct {
	mu       sync.Mutex
	entries  []*criticalNotice
	overflow uint64
}

type criticalNotice struct {
	session, category, message, id string
	count, delivered               uint64
	inFlight                       bool
}

func NewCriticalErrors() *CriticalErrors { return &CriticalErrors{} }

func (c *CriticalErrors) record(f *requestFinalization, err error) {
	if c == nil || f.observation.outcome == requestOutcomeCompleted ||
		f.observation.outcome == requestOutcomeCanceledBeforeResponse || f.observation.outcome == requestOutcomeCanceledAfterResponse {
		return
	}
	category := string(f.failurePhase)
	message := "Hpatch could not complete the request. Retry the turn; if it persists, restart the session."
	if compatibility, ok := errors.AsType[*requestCompatibilityError](err); ok {
		category, message = compatibility.code, compatibility.Error()
	} else {
		switch {
		case f.upstreamStatusCode == 401 || f.upstreamStatusCode == 403:
			category, message = "authentication", "Hpatch upstream authentication was rejected. Check your Codex or Grok credentials before retrying."
		case f.upstreamStatusCode == 429:
			category, message = "rate_limit", "The upstream service rate-limited this turn. Wait before retrying."
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, errUpstreamStreamIdleTimeout):
			category, message = "timeout", "The upstream response timed out. Retry the turn."
		case f.failurePhase == requestFailurePrepare:
			message = "Hpatch could not prepare this request. Check the request error for the incompatible configuration or tool definition."
		case f.failurePhase == requestFailureTransform:
			message = "Hpatch could not safely translate the response. No unsupported tool call was released; check the request error before retrying."
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, notice := range c.entries {
		if notice.session == f.sessionID && notice.category == category {
			notice.count++
			return
		}
	}
	if len(c.entries) >= 256 {
		c.overflow++
		return
	}
	c.entries = append(c.entries, &criticalNotice{session: f.sessionID, category: category, message: message,
		id: commentaryMessageID("critical:" + f.sessionID + ":" + category), count: 1})
}

func noticeText(n *criticalNotice) string {
	if n.count > 1 {
		return fmt.Sprintf("%s (occurred %d times)", n.message, n.count)
	}
	return n.message
}

// Pending is called after the router has stopped, outside Codex's terminal UI.
func (c *CriticalErrors) Pending() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []string
	for _, n := range c.entries {
		if n.count > n.delivered {
			result = append(result, "hpatch: "+noticeText(n))
		}
	}
	if c.overflow != 0 {
		result = append(result, fmt.Sprintf("hpatch: %d additional failures could not be retained; check the failed turns.", c.overflow))
	}
	return result
}

// stripInput removes exact router-owned IDs for this session, including notices
// replayed after a client disconnected before delivery could be confirmed.
func (c *CriticalErrors) stripInput(request *parsedResponsesRequest, session string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var items []map[string]json.RawMessage
	if json.Unmarshal(request.fields["input"], &items) != nil {
		return
	}
	before := len(items)
	items = slices.DeleteFunc(items, func(item map[string]json.RawMessage) bool {
		if jsonString(item, "type") != "message" {
			return false
		}
		for _, n := range c.entries {
			if n.session == session && n.id == jsonString(item, "id") {
				return true
			}
		}
		return false
	})
	if len(items) != before {
		request.fields["input"] = mustMarshalJSON(items)
	}
}

type criticalErrorTransform struct {
	owner    *CriticalErrors
	notices  []*criticalNotice
	counts   []uint64
	messages []map[string]json.RawMessage
	subagent bool
	emitted  bool
}

func (c *CriticalErrors) transform(session string, subagent bool) *criticalErrorTransform {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &criticalErrorTransform{owner: c, subagent: subagent}
	for _, n := range c.entries {
		// Repeats after the first visible notice are summarized only at shutdown.
		if n.session != session || n.delivered != 0 || n.inFlight {
			continue
		}
		n.inFlight = true
		t.notices = append(t.notices, n)
		t.counts = append(t.counts, n.count)
		t.messages = append(t.messages, assistantCommentaryMessage(n.id, noticeText(n)))
	}
	return t
}

func (t *criticalErrorTransform) finish(success bool) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	for i, n := range t.notices {
		n.inFlight = false
		if success && t.emitted {
			n.delivered = t.counts[i]
		}
	}
}

func (t *criticalErrorTransform) TransformJSON(body []byte) ([]byte, error) {
	if t == nil || len(t.messages) == 0 {
		return body, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return body, nil
	}
	var output []map[string]json.RawMessage
	if json.Unmarshal(object["output"], &output) != nil {
		return body, nil
	}
	object["output"] = mustMarshalJSON(append(slices.Clone(t.messages), output...))
	result, err := marshalProtocolJSON(object)
	if err == nil {
		t.emitted = true
	}
	return result, err
}

func (t *criticalErrorTransform) TransformSSE(body []byte) ([][]byte, error) {
	if t == nil || len(t.messages) == 0 {
		return [][]byte{body}, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return [][]byte{body}, nil
	}
	switch jsonString(object, "type") {
	case "response.created":
		// A child's last standalone assistant item must remain its substantive result.
		if !t.subagent {
			result := [][]byte{body}
			for _, message := range t.messages {
				result = append(result, assistantCommentaryDoneEvent(message))
			}
			t.emitted = true
			return result, nil
		}
	case "response.completed", "response.failed", "response.incomplete":
		response, err := t.TransformJSON(object["response"])
		if err != nil {
			return nil, err
		}
		result, err := replaceRawField(body, "response", response)
		return [][]byte{result}, err
	}
	return [][]byte{body}, nil
}
func (*criticalErrorTransform) Finish(bool) error { return nil }

// Permanent local incompatibilities must not masquerade as retryable upstream 502s.
type requestCompatibilityError struct{ code, message string }

func (e *requestCompatibilityError) Error() string { return "Hpatch " + e.code + ": " + e.message }
func incompatibleRequest(code, message string) error {
	return &requestCompatibilityError{code: code, message: message}
}
