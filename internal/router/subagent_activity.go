package router

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

// Presentation state is independent of executable-call recovery. Identities and
// delivered IDs are never evicted to admit new traffic: exhaustion drops only
// auxiliary updates, without making old commentary eligible for provider replay.
type subagentActivity struct {
	mu      sync.Mutex
	threads map[string]*activityThread
	copies  map[string]struct{}
	events  []activityEvent
	sources int
	closed  bool
}

type activityThread struct {
	parent, name      string
	child, conflicted bool
	seen              map[string]struct{}
}

type activityEvent struct {
	thread, source, kind, text string
	observed                   time.Time
	diagnostic                 bool
}

func newSubagentActivity() *subagentActivity {
	return &subagentActivity{threads: make(map[string]*activityThread), copies: make(map[string]struct{})}
}

func (a *subagentActivity) observe(thread, parent, name string, child bool) bool {
	if a == nil || thread == "" {
		return false
	}
	if len(thread)+len(parent)+len(name) > maxCommentaryPublicationBytes || strings.ContainsAny(name, "\r\n\x00") ||
		child && (parent == "" || parent == thread || !strings.HasPrefix(name, "/root/")) ||
		!child && (parent != "" || name != "/root") {
		a.invalidate(thread)
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	if old := a.threads[thread]; old != nil {
		if old.parent != parent || old.name != name || old.child != child {
			old.conflicted = true
		}
		return !old.conflicted
	}
	if len(a.threads) >= maxCommentaryRoutes {
		return false
	}
	a.threads[thread] = &activityThread{parent: parent, name: name, child: child, seen: make(map[string]struct{})}
	return true
}

func (a *subagentActivity) rootLocked(thread string) string {
	for range len(a.threads) {
		node := a.threads[thread]
		if node == nil || node.conflicted {
			return ""
		}
		if !node.child {
			return thread
		}
		thread = node.parent
	}
	return "" // Cycles and unknown ancestry never broadcast.
}

// Shell workers share stable thread capabilities across requests. Once an
// accepted request makes that thread's identity ambiguous, later publications
// cannot safely use its earlier ancestry, even after another valid turn.
// Keep local runtime authors and replay provenance intact; suppress root copies.
func (a *subagentActivity) invalidate(thread string) {
	if a == nil || thread == "" || len(thread) > maxCommentaryPublicationBytes {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	if node := a.threads[thread]; node != nil {
		node.conflicted = true
	} else if len(a.threads) < maxCommentaryRoutes {
		// An initially ambiguous capability must not acquire ancestry later.
		a.threads[thread] = &activityThread{conflicted: true}
	}
}

func (a *subagentActivity) collect(thread, source, kind, text string) {
	if a == nil || source == "" || len(source) > maxCommentaryPublicationBytes || strings.TrimSpace(text) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	node := a.threads[thread]
	if a.closed || node == nil || !node.child || node.conflicted || a.sources >= maxThreadCommentaryIDs {
		return
	}
	diagnostic := strings.HasPrefix(source, diagnosticCallPrefix)
	source = commentaryMessageID(source)
	if _, exists := node.seen[source]; exists {
		return
	}
	if kind == "operation" || !strings.HasPrefix(text, "["+commentaryCode(node.name)+" -> ") && !strings.HasPrefix(text, "["+commentaryCode(node.name)+" <- ") {
		text = attributedCommentary(node.name, text)
	}
	if len(text) > maxCommentaryPublicationBytes {
		return
	}
	now := time.Now()
	a.expireLocked(now)
	// Keep the latest ordinary operation, but preserve distinct notices. Appending
	// the replacement after notices preserves each child's observation order.
	if kind == "operation" {
		a.events = slices.DeleteFunc(a.events, func(e activityEvent) bool { return e.thread == thread && e.kind == kind })
	}
	count := 0
	for _, event := range a.events {
		if event.thread == thread {
			count++
		}
	}
	if len(a.events) >= maxCommentaryEvents || count >= maxCommentaryEventsPerRoute {
		return
	}
	node.seen[source] = struct{}{}
	a.sources++
	a.events = append(a.events, activityEvent{thread: thread, source: source, kind: kind, text: text, observed: now, diagnostic: diagnostic})
}

func (a *subagentActivity) expireLocked(now time.Time) {
	a.events = slices.DeleteFunc(a.events, func(e activityEvent) bool { return now.Sub(e.observed) >= commentaryRouteTTL })
}

func (a *subagentActivity) drain(root string, started time.Time, budget int) []map[string]json.RawMessage {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	node := a.threads[root]
	if node == nil || node.child || a.rootLocked(root) != root {
		return nil
	}
	a.expireLocked(time.Now())
	var messages []map[string]json.RawMessage
	kept := a.events[:0]
	blocked := make(map[string]bool)
	for _, event := range a.events {
		if a.rootLocked(event.thread) != root {
			kept = append(kept, event)
			continue
		}
		text := event.text
		if event.observed.Before(started) {
			text = "Subagent activity since the last update:\n" + text
		}
		// Labels can make an admitted event permanently too large. Omit it
		// rather than letting it block later activity until expiry.
		if len(text) > maxCommentaryPublicationBytes {
			continue
		}
		if blocked[event.thread] || len(text) > budget {
			blocked[event.thread] = true
			kept = append(kept, event)
			continue
		}
		id := commentaryMessageID("root-copy\x00" + root + "\x00" + event.thread + "\x00" + event.source)
		a.copies[id] = struct{}{}
		messages = append(messages, assistantCommentaryMessage(id, text))
		budget -= len(text)
	}
	clear(a.events[len(kept):])
	a.events = kept
	return messages
}

// Root copies can be inherited by a newly forked child before its first request
// establishes ancestry. Exact generated IDs are user-only in every replay, even
// though delivery itself always requires an observed root relationship.
func (a *subagentActivity) stripInput(fields map[string]json.RawMessage) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.copies) == 0 {
		return
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["input"], &items) != nil {
		return
	}
	original := len(items)
	items = slices.DeleteFunc(items, func(item map[string]json.RawMessage) bool {
		_, owned := a.copies[jsonString(item, "id")]
		return owned && jsonString(item, "type") == "message" && jsonString(item, "role") == "assistant"
	})
	if len(items) != original {
		fields["input"] = mustMarshalJSON(items)
	}
}

func (t *hpatchResponseTransform) drainActivity() []map[string]json.RawMessage {
	messages := t.proxy.activity.drain(t.threadID, t.activityStarted, maxCommentaryPublicationBytes-t.activityBytes)
	for _, message := range messages {
		var content []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(message["content"], &content) == nil && len(content) == 1 {
			t.activityBytes += len(content[0].Text)
		}
	}
	return messages
}

func (a *subagentActivity) close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	clear(a.threads)
	clear(a.copies)
	a.events = nil
	a.sources = 0
}

func (a *subagentActivity) discardDiagnostics(root string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = slices.DeleteFunc(a.events, func(event activityEvent) bool { return event.diagnostic && a.rootLocked(event.thread) == root })
}
