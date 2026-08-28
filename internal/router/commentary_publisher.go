package router

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	commentaryPublisherPath       = "/internal/commentary"
	commentaryEndpointArgument    = "--commentary-endpoint"
	commentaryTokenArgument       = "--commentary-token"
	commentaryOnceArgument        = "--commentary-once"
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
	sessionID string
	callID    string
	expires   time.Time
	nextID    uint64
	published uint64
	events    []publishedCommentary
	complete  bool
}

type commentaryBroker struct {
	mu         sync.Mutex
	routes     map[string]*commentaryRoute
	eventCount int
	closed     bool
}

func newCommentaryBroker() *commentaryBroker {
	return &commentaryBroker{routes: make(map[string]*commentaryRoute)}
}

func (b *commentaryBroker) subscribe(sessionID, callID string) *commentarySubscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	if b.closed || len(b.routes) >= maxCommentaryRoutes {
		return nil
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	b.routes[token] = &commentaryRoute{
		sessionID: sessionID,
		callID:    callID,
		expires:   time.Now().Add(commentaryRouteTTL),
	}
	return &commentarySubscription{broker: b, token: token}
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
	if strings.TrimSpace(text) != "" && route.published < maxCommentaryEventsPerRoute && b.eventCount < maxCommentaryEvents {
		route.nextID++
		route.published++
		event := publishedCommentary{
			callID:    route.callID,
			messageID: commentaryMessageID(token + ":" + fmt.Sprint(route.nextID)),
			text:      text,
		}
		route.events = append(route.events, event)
		b.eventCount++
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
		events = append(events, route.events...)
		b.eventCount -= len(route.events)
		route.events = nil
		delete(b.routes, token)
	}
	return events
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

type commentarySubscription struct {
	broker *commentaryBroker
	token  string
}

func (s *commentarySubscription) drain() []publishedCommentary {
	if s == nil {
		return nil
	}
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	route := s.broker.routes[s.token]
	if route == nil {
		return nil
	}
	events := route.events
	s.broker.eventCount -= len(events)
	route.events = nil
	if route.complete {
		delete(s.broker.routes, s.token)
	}
	return events
}

func (s *commentarySubscription) cancel() {
	if s == nil {
		return
	}
	s.broker.mu.Lock()
	if route := s.broker.routes[s.token]; route != nil {
		s.broker.eventCount -= len(route.events)
		delete(s.broker.routes, s.token)
	}
	s.broker.mu.Unlock()
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

func shellCommentaryPublisher(arguments []string) (shellCommentarySink, []string, error) {
	if len(arguments) < 4 || arguments[0] != commentaryEndpointArgument || arguments[2] != commentaryTokenArgument {
		return nil, arguments, nil
	}
	if _, err := url.ParseRequestURI(arguments[1]); err != nil || arguments[3] == "" {
		return nil, nil, errors.New("shell commentary publisher arguments are invalid")
	}
	return &httpShellCommentarySink{
		endpoint: arguments[1],
		token:    arguments[3],
		client:   commentaryHTTPClient,
	}, arguments[4:], nil
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
	return true, sink.Publish(ctx, text)
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
