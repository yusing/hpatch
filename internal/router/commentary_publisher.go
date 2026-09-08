package router

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	commentaryPublisherPath       = "/internal/commentary"
	commentaryOnceArgument        = "--commentary-once"
	maxThreadCommentaryIDs        = 16384
	maxCommentaryRoutes           = 256
	maxCommentaryEvents           = 1024
	maxCommentaryEventsPerRoute   = 64
	maxCommentaryPublicationBytes = 16 << 10
)

var (
	commentaryRouteTTL   = time.Hour
	commentaryHTTPClient = &http.Client{Timeout: 2 * time.Second}
)

type publishedCommentary struct {
	callID    string
	messageID string
	text      string
}

type commentaryRoute struct {
	threadID  string
	sessionID string
	callID    string
	expires   time.Time
	nextID    uint64
	events    []publishedCommentary
	complete  bool
}

// Replay provenance has its own non-evicting budget. A thread can outlive its
// route and change history sessions without losing user-only message identity.
// Exhaustion suppresses new commentary, never essential tool replay history.
type threadCommentaryProvenance struct {
	sessionID string
	ids       map[string]struct{}
}

type commentaryBroker struct {
	threads       map[string]*threadCommentaryProvenance
	threadIDCount int
	mu            sync.Mutex
	routes        map[string]*commentaryRoute
	eventCount    int
	closed        bool
}

func newCommentaryBroker() *commentaryBroker {
	return &commentaryBroker{routes: make(map[string]*commentaryRoute), threads: make(map[string]*threadCommentaryProvenance)}
}

func (b *commentaryBroker) subscribe(sessionID, callID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	if b.closed || len(b.routes) >= maxCommentaryRoutes {
		return ""
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	b.routes[token] = &commentaryRoute{
		sessionID: sessionID,
		callID:    callID,
		expires:   time.Now().Add(commentaryRouteTTL),
	}
	return token
}

// subscribeThread reuses the thread capability while refreshing its current replay session.
// The broker never calls back into the proxy: proxy locks may precede this lock.
func (b *commentaryBroker) subscribeThread(sessionID, threadID string) string {
	if sessionID == "" || threadID == "" {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.cleanupExpiredLocked(now)
	if b.closed {
		return ""
	}
	provenance := b.threads[threadID]
	if provenance == nil {
		if len(b.threads) >= maxCommentaryRoutes {
			return ""
		}
		provenance = &threadCommentaryProvenance{ids: make(map[string]struct{})}
		b.threads[threadID] = provenance
	}
	provenance.sessionID = sessionID
	for token, route := range b.routes {
		if route.threadID == threadID {
			route.sessionID = sessionID
			route.expires = now.Add(commentaryRouteTTL)
			return token
		}
	}
	if len(b.routes) >= maxCommentaryRoutes {
		return ""
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	b.routes[token] = &commentaryRoute{sessionID: sessionID, threadID: threadID, expires: now.Add(commentaryRouteTTL)}
	return token
}

func (b *commentaryBroker) publish(token, text string, complete bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.cleanupExpiredLocked(now)
	route := b.routes[token]
	if route == nil {
		return false
	}
	route.expires = now.Add(commentaryRouteTTL)
	withinRouteCapacity := route.nextID < maxCommentaryEventsPerRoute
	if route.threadID != "" {
		complete = false // Concurrent shell workers share this route; no worker owns its lifetime.
		withinRouteCapacity = len(route.events) < maxCommentaryEventsPerRoute && b.threadIDCount < maxThreadCommentaryIDs
	}
	if strings.TrimSpace(text) != "" && withinRouteCapacity && b.eventCount < maxCommentaryEvents {
		route.nextID++
		event := publishedCommentary{
			callID:    route.callID,
			messageID: commentaryMessageID(token + ":" + fmt.Sprint(route.nextID)),
			text:      text,
		}
		if route.threadID != "" {
			b.threads[route.threadID].ids[event.messageID] = struct{}{}
			b.threadIDCount++
		}
		route.events = append(route.events, event)
		b.eventCount++
	}
	if complete && len(route.events) == 0 {
		delete(b.routes, token)
		return true
	}
	route.complete = route.complete || complete
	return true
}

func (b *commentaryBroker) drainSession(sessionID string) []publishedCommentary {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	var events []publishedCommentary
	for token, route := range b.routes {
		if route.sessionID != sessionID {
			continue
		}
		events = append(events, b.drainLocked(token)...)
	}
	return events
}

func (b *commentaryBroker) drainThreadSession(sessionID string) []publishedCommentary {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	var events []publishedCommentary
	for token, route := range b.routes {
		if route.threadID != "" && route.sessionID == sessionID {
			events = append(events, b.drainLocked(token)...)
		}
	}
	return events
}

func (b *commentaryBroker) hasThreadMessageID(sessionID, messageID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, thread := range b.threads {
		if thread.sessionID == sessionID {
			if _, exists := thread.ids[messageID]; exists {
				return true
			}
		}
	}
	return false
}

func (b *commentaryBroker) threadMessageIDs(sessionID string) map[string]struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make(map[string]struct{})
	for _, thread := range b.threads {
		if thread.sessionID == sessionID {
			maps.Copy(ids, thread.ids)
		}
	}
	return ids
}

func (b *commentaryBroker) cleanupExpiredLocked(now time.Time) {
	for token, route := range b.routes {
		if !now.Before(route.expires) {
			b.eventCount -= len(route.events)
			delete(b.routes, token)
		}
	}
}

func (b *commentaryBroker) close() {
	b.mu.Lock()
	b.closed = true
	clear(b.routes)
	clear(b.threads)
	b.threadIDCount = 0
	b.eventCount = 0
	b.mu.Unlock()
}

func (b *commentaryBroker) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	token, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxCommentaryPublicationBytes)
	var publication struct {
		Text     string `json:"text,omitempty"`
		Complete bool   `json:"complete,omitempty"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&publication); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(writer, "invalid commentary publication", http.StatusBadRequest)
		return
	}
	if !b.publish(token, publication.Text, publication.Complete) {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func commentaryPublisherURL(listenAddress string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: commentaryPublisherPath}).String(), nil
}

func (b *commentaryBroker) drain(token string) []publishedCommentary {
	if token == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	return b.drainLocked(token)
}

// drainLocked consumes publications without retiring a still-running publisher.
// Both live and deferred delivery share the same completion-sensitive lifetime.
func (b *commentaryBroker) drainLocked(token string) []publishedCommentary {
	route := b.routes[token]
	if route == nil {
		return nil
	}
	events := route.events
	b.eventCount -= len(events)
	route.events = nil
	if route.complete {
		delete(b.routes, token)
	}
	return events
}

func (b *commentaryBroker) cancel(token string) {
	if token == "" {
		return
	}
	b.mu.Lock()
	if route := b.routes[token]; route != nil {
		b.eventCount -= len(route.events)
		delete(b.routes, token)
	}
	b.mu.Unlock()
}

type shellCommentarySink interface {
	Publish(context.Context, string) error
	Complete(context.Context) error
}

type httpShellCommentarySink struct {
	endpoint string
	token    string
	client   *http.Client
}

func publishCommentaryOnce(ctx context.Context, arguments []string) (bool, error) {
	if len(arguments) != 4 || arguments[0] != commentaryOnceArgument {
		return false, nil
	}
	text, err := url.PathUnescape(arguments[3])
	if err != nil {
		return true, err
	}
	sink := &httpShellCommentarySink{endpoint: arguments[1], token: arguments[2], client: commentaryHTTPClient}
	_ = sink.Publish(ctx, text)
	return true, nil
}

func (s *httpShellCommentarySink) Publish(ctx context.Context, text string) error {
	return s.send(ctx, map[string]any{"text": text})
}

func (s *httpShellCommentarySink) Complete(ctx context.Context) error {
	return s.send(ctx, map[string]any{"complete": true})
}

func (s *httpShellCommentarySink) send(ctx context.Context, publication map[string]any) error {
	body, err := json.Marshal(publication)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("commentary publisher returned %s", response.Status)
	}
	return nil
}
