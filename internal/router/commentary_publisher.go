package router

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yusing/hpatch/internal/router/toolplugin"
)

const (
	commentaryPublisherPath      = "/_hpatch/commentary"
	commentaryPublicationBytes   = maxHPatchScriptBytes + (64 << 10)
	commentaryPublicationBuffer  = 128
	commentaryPublisherTimeout   = 5 * time.Second
	maxCommentaryRoutes          = maxSessionHistories
	maxCommentarySessionRoutes   = maxSessionTurns
	maxDeferredCommentary        = maxSessionHistories
	maxDeferredSessionCommentary = maxSessionTurns
	commentaryEndpointArgument   = "--hpatch-commentary-endpoint"
	commentaryTokenArgument      = "--hpatch-commentary-token"
	commentaryOnceArgument       = "--hpatch-commentary-once"
)

var commentarySubscriptionTTL = time.Hour

var errCommentaryCapacity = errors.New("commentary broker capacity exceeded")

type publishedCommentary struct {
	callID          string
	metricSessionID string
	messageID       string
	event           shellCommentaryEvent
	expiresAt       time.Time
}

type commentaryPublication struct {
	Event       *shellCommentaryEvent `json:"event,omitempty"`
	Done        bool                  `json:"done,omitempty"`
	BudgetBytes *int                  `json:"budget_bytes,omitempty"`
	messageID   string
}

type commentarySubscription struct {
	broker             *commentaryBroker
	token              string
	sessionID          string
	metricSessionID    string
	callID             string
	live               bool
	persistent         bool
	events             chan commentaryPublication
	publishedBytes     int
	publishedCount     int
	warnedSuppression  bool
	reservedMessageIDs map[string]bool
	expiry             *time.Timer
}

type commentarySuppressionCapability struct {
	MetricSessionID string `json:"metric_session_id"`
	Nonce           string `json:"nonce"`
	ExpiresAt       int64  `json:"expires_at"`
}

type commentaryBroker struct {
	mu                   sync.Mutex
	routes               map[string]*commentarySubscription
	deferred             map[string][]publishedCommentary
	deferredCount        int
	deferredBytes        int
	deferredSessionBytes map[string]int
	cleanupTimer         *time.Timer
	reserveMessageID     func(string, string, string) bool
	reserveLiveMessageID func(string, string, string) bool
	releaseMessageID     func(string, string, string)
	recordSuppressed     func(string, shellCommentaryEvent, string)
	nextMessageID        uint64
	suppressionKey       []byte
	suppressionKeyErr    error
}

func newCommentaryBroker() *commentaryBroker {
	key := make([]byte, 32)
	_, keyErr := rand.Read(key)
	return &commentaryBroker{
		routes:               make(map[string]*commentarySubscription),
		deferred:             make(map[string][]publishedCommentary),
		deferredSessionBytes: make(map[string]int),
		suppressionKey:       key,
		suppressionKeyErr:    keyErr,
	}
}

func (b *commentaryBroker) suppressionCapability(metricSessionID string) (string, error) {
	if b.suppressionKeyErr != nil {
		return "", fmt.Errorf("create commentary suppression capability: %w", b.suppressionKeyErr)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create commentary suppression nonce: %w", err)
	}
	payload, err := json.Marshal(commentarySuppressionCapability{
		MetricSessionID: metricSessionID,
		Nonce:           base64.RawURLEncoding.EncodeToString(nonce),
		ExpiresAt:       time.Now().Add(commentarySubscriptionTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode commentary suppression capability: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, b.suppressionKey)
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "s1." + encodedPayload + "." + signature, nil
}

func (b *commentaryBroker) suppressionMetricSession(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "s1" || b.suppressionKeyErr != nil {
		return "", false
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, b.suppressionKey)
	_, _ = mac.Write([]byte(parts[1]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var capability commentarySuppressionCapability
	if json.Unmarshal(payload, &capability) != nil || capability.Nonce == "" || capability.ExpiresAt <= time.Now().Unix() {
		return "", false
	}
	return capability.MetricSessionID, true
}

func commentaryPublisherURL(listenAddress string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", fmt.Errorf("parse commentary publisher listen address: %w", err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: commentaryPublisherPath}).String(), nil
}

func (b *commentaryBroker) subscribe(sessionID, metricSessionID, callID string, live, persistent bool) (*commentarySubscription, error) {
	capability := make([]byte, 32)
	if _, err := rand.Read(capability); err != nil {
		return nil, fmt.Errorf("create commentary publisher capability: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(capability)
	subscription := &commentarySubscription{
		broker: b, token: token, sessionID: sessionID, metricSessionID: metricSessionID, callID: callID,
		live: live, persistent: persistent,
		events:             make(chan commentaryPublication, commentaryPublicationBuffer+1),
		reservedMessageIDs: make(map[string]bool),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.routes) >= maxCommentaryRoutes {
		return nil, errCommentaryCapacity
	}
	sessionRoutes := 0
	for _, existing := range b.routes {
		if existing.sessionID == sessionID {
			sessionRoutes++
		}
	}
	if sessionRoutes >= maxCommentarySessionRoutes {
		return nil, errCommentaryCapacity
	}
	if _, exists := b.routes[token]; exists {
		return nil, errors.New("commentary publisher capability collision")
	}
	b.routes[token] = subscription
	subscription.expiry = time.AfterFunc(commentarySubscriptionTTL, subscription.cancel)
	return subscription, nil
}

func (b *commentaryBroker) drain(sessionID string) []publishedCommentary {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	events := b.takeDeferredLocked(sessionID)
	for token, subscription := range b.routes {
		if subscription.sessionID == sessionID && subscription.persistent {
			b.removeLocked(token, subscription)
		}
	}
	return events
}

func (b *commentaryBroker) hasDeferredTerminal(sessionID, callID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupExpiredLocked(time.Now())
	for _, event := range b.deferred[sessionID] {
		if event.callID == callID && event.event.Outcome != "" && event.event.Outcome != "suppressed" {
			return true
		}
	}
	return false
}

func (b *commentaryBroker) requeue(sessionID string, events []publishedCommentary) {
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	existing := b.takeDeferredLocked(sessionID)
	for _, event := range append(events, existing...) {
		if !b.appendDeferredLocked(sessionID, event) {
			if b.releaseMessageID != nil {
				b.releaseMessageID(sessionID, event.callID, event.messageID)
			}
			if b.recordSuppressed != nil {
				b.recordSuppressed(event.metricSessionID, event.event, "")
			}
		}
	}
	b.scheduleCleanupLocked()
}

func (b *commentaryBroker) deferEvent(sessionID string, event publishedCommentary) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assignMessageIDLocked(&event)
	if b.reserveMessageID != nil && !b.reserveMessageID(sessionID, event.callID, event.messageID) {
		if b.recordSuppressed != nil {
			b.recordSuppressed(event.metricSessionID, event.event, "")
		}
		return
	}
	if !b.appendDeferredLocked(sessionID, event) {
		if b.releaseMessageID != nil {
			b.releaseMessageID(sessionID, event.callID, event.messageID)
		}
		if b.recordSuppressed != nil {
			b.recordSuppressed(event.metricSessionID, event.event, "")
		}
	}
	b.scheduleCleanupLocked()
}

func (b *commentaryBroker) appendDeferredLocked(sessionID string, event publishedCommentary) bool {
	now := time.Now()
	b.cleanupExpiredLocked(now)
	if !event.expiresAt.IsZero() && !event.expiresAt.After(now) {
		return false
	}
	size := len(event.callID) + len(event.messageID) + len(shellCommentaryVisibleText(event.event)) + len(event.event.Form)
	if len(b.deferred[sessionID]) >= maxDeferredSessionCommentary || b.deferredCount >= maxDeferredCommentary ||
		b.deferredSessionBytes[sessionID]+size > maxHPatchHistorySessionBytes ||
		b.deferredBytes+size > maxHPatchHistoryGlobalBytes {
		return false
	}
	if event.expiresAt.IsZero() {
		event.expiresAt = now.Add(commentarySubscriptionTTL)
	}
	b.deferred[sessionID] = append(b.deferred[sessionID], event)
	b.deferredCount++
	b.deferredBytes += size
	b.deferredSessionBytes[sessionID] += size
	return true
}

func (b *commentaryBroker) takeDeferredLocked(sessionID string) []publishedCommentary {
	events := b.deferred[sessionID]
	if len(events) == 0 {
		return nil
	}
	delete(b.deferred, sessionID)
	b.deferredCount -= len(events)
	b.deferredBytes -= b.deferredSessionBytes[sessionID]
	delete(b.deferredSessionBytes, sessionID)
	b.scheduleCleanupLocked()
	return events
}

func (b *commentaryBroker) cleanupExpiredLocked(now time.Time) {
	for sessionID, events := range b.deferred {
		kept := events[:0]
		for _, event := range events {
			if !event.expiresAt.IsZero() && !event.expiresAt.After(now) {
				if b.releaseMessageID != nil {
					b.releaseMessageID(sessionID, event.callID, event.messageID)
				}
				size := len(event.callID) + len(event.messageID) + len(shellCommentaryVisibleText(event.event)) + len(event.event.Form)
				b.deferredCount--
				b.deferredBytes -= size
				b.deferredSessionBytes[sessionID] -= size
				continue
			}
			kept = append(kept, event)
		}
		if len(kept) == 0 {
			delete(b.deferred, sessionID)
			delete(b.deferredSessionBytes, sessionID)
		} else {
			b.deferred[sessionID] = kept
		}
	}
}

func (b *commentaryBroker) scheduleCleanupLocked() {
	if b.cleanupTimer != nil {
		b.cleanupTimer.Stop()
		b.cleanupTimer = nil
	}
	var next time.Time
	for _, events := range b.deferred {
		for _, event := range events {
			if next.IsZero() || event.expiresAt.Before(next) {
				next = event.expiresAt
			}
		}
	}
	if next.IsZero() {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(max(time.Until(next), time.Nanosecond), func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.cleanupTimer != timer {
			return
		}
		b.cleanupTimer = nil
		b.cleanupExpiredLocked(time.Now())
		b.scheduleCleanupLocked()
	})
	b.cleanupTimer = timer
}

func (b *commentaryBroker) cancelCall(sessionID, callID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for token, subscription := range b.routes {
		if subscription.sessionID == sessionID && subscription.callID == callID {
			b.cancelLocked(token, subscription)
		}
	}
}

func (b *commentaryBroker) assignMessageIDLocked(event *publishedCommentary) {
	if event.messageID != "" {
		return
	}
	b.nextMessageID++
	event.messageID = commentaryMessageID(fmt.Sprintf("runtime:%d:%s", b.nextMessageID, event.callID))
}

func (b *commentaryBroker) removeLocked(token string, subscription *commentarySubscription) {
	if b.routes[token] != subscription {
		return
	}
	delete(b.routes, token)
	if subscription.expiry != nil {
		subscription.expiry.Stop()
	}
}

func (b *commentaryBroker) cancelLocked(token string, subscription *commentarySubscription) {
	if b.routes[token] != subscription {
		return
	}
	b.abandonSubscriptionLocked(subscription)
	b.removeLocked(token, subscription)
}

func (b *commentaryBroker) abandonSubscriptionLocked(subscription *commentarySubscription) {
	for messageID, inFlight := range subscription.reservedMessageIDs {
		if inFlight {
			continue
		}
		if b.releaseMessageID != nil {
			b.releaseMessageID(subscription.sessionID, subscription.callID, messageID)
		}
		delete(subscription.reservedMessageIDs, messageID)
	}
	for {
		select {
		case <-subscription.events:
		default:
			return
		}
	}
}

func (b *commentaryBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for token, subscription := range b.routes {
		b.removeLocked(token, subscription)
	}
	if b.cleanupTimer != nil {
		b.cleanupTimer.Stop()
		b.cleanupTimer = nil
	}
	clear(b.routes)
	clear(b.deferred)
	clear(b.deferredSessionBytes)
	b.deferredCount = 0
	b.deferredBytes = 0
}

func (b *commentaryBroker) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	authorizations := request.Header.Values("Authorization")
	if len(authorizations) != 1 {
		http.Error(writer, "invalid commentary publisher capability", http.StatusUnauthorized)
		return
	}
	token, ok := strings.CutPrefix(authorizations[0], "Bearer ")
	if !ok || token == "" || token != strings.TrimSpace(token) {
		http.Error(writer, "invalid commentary publisher capability", http.StatusUnauthorized)
		return
	}
	var publication commentaryPublication
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, commentaryPublicationBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&publication); err != nil {
		http.Error(writer, "invalid commentary publication", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(writer, "invalid commentary publication", http.StatusBadRequest)
		return
	}
	if publication.Done == (publication.Event != nil) || publication.Done && publication.BudgetBytes != nil {
		http.Error(writer, "commentary publication must contain exactly one event or completion", http.StatusBadRequest)
		return
	}
	if publication.BudgetBytes != nil && (*publication.BudgetBytes < 0 || *publication.BudgetBytes > toolplugin.ExecutionOutputBudgetBytes) {
		http.Error(writer, "commentary publication budget is invalid", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	subscription := b.routes[token]
	b.mu.Unlock()
	if subscription == nil {
		metricSessionID, suppressed := b.suppressionMetricSession(token)
		if !suppressed {
			http.Error(writer, "invalid commentary publisher capability", http.StatusUnauthorized)
			return
		}
		if publication.Event != nil && b.recordSuppressed != nil {
			b.recordSuppressed(metricSessionID, *publication.Event, "")
		}
		writer.Header().Set("X-HPatch-Commentary-Visible-Bytes", "0")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	visibleBytes, err := subscription.publishVisible(request.Context(), publication)
	if err != nil {
		http.Error(writer, "commentary publication is unavailable", http.StatusGone)
		return
	}
	writer.Header().Set("X-HPatch-Commentary-Visible-Bytes", strconv.Itoa(visibleBytes))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *commentarySubscription) publish(ctx context.Context, publication commentaryPublication) error {
	_, err := s.publishVisible(ctx, publication)
	return err
}

func (s *commentarySubscription) publishVisible(_ context.Context, publication commentaryPublication) (int, error) {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	if s.broker.routes[s.token] != s {
		return 0, errors.New("commentary publisher capability is no longer active")
	}
	visibleBytes := 0
	warningText := ""
	messageReserved := false
	var original shellCommentaryEvent
	if publication.Event != nil {
		original = *publication.Event
		visibleBytes = len(shellCommentaryVisibleText(*publication.Event))
		availableBytes := toolplugin.ExecutionOutputBudgetBytes - s.publishedBytes
		if publication.BudgetBytes != nil {
			availableBytes = min(availableBytes, *publication.BudgetBytes)
		}
		if s.publishedCount >= commentaryPublicationBuffer-1 || visibleBytes > availableBytes {
			if s.warnedSuppression {
				if s.broker.recordSuppressed != nil {
					s.broker.recordSuppressed(s.metricSessionID, original, "")
				}
				return 0, nil
			}
			s.warnedSuppression = true
			warning := shellCommentaryEvent{
				Text:    "Further commentary was suppressed because the tool output budget was exhausted.",
				Outcome: "suppressed",
			}
			if len(warning.Text) > availableBytes {
				if s.broker.recordSuppressed != nil {
					s.broker.recordSuppressed(s.metricSessionID, original, "")
				}
				return 0, nil
			}
			warningText = warning.Text
			publication.Event = &warning
			visibleBytes = len(warning.Text)
		}
		if visibleBytes != 0 {
			publication.messageID = s.newMessageIDLocked()
			reserveMessageID := s.broker.reserveMessageID
			if s.live && s.broker.reserveLiveMessageID != nil {
				reserveMessageID = s.broker.reserveLiveMessageID
			}
			if reserveMessageID != nil && !reserveMessageID(s.sessionID, s.callID, publication.messageID) {
				if s.broker.recordSuppressed != nil {
					s.broker.recordSuppressed(s.metricSessionID, original, "")
				}
				return 0, nil
			}
			messageReserved = reserveMessageID != nil
		}
	}
	if !s.live {
		if publication.Event != nil {
			event := publication.published(s.callID, s.metricSessionID)
			if !s.broker.appendDeferredLocked(s.sessionID, event) {
				if messageReserved && s.broker.releaseMessageID != nil {
					s.broker.releaseMessageID(s.sessionID, s.callID, publication.messageID)
				}
				if s.broker.recordSuppressed != nil {
					s.broker.recordSuppressed(s.metricSessionID, original, "")
				}
				return 0, nil
			}
			s.publishedBytes += visibleBytes
			s.publishedCount++
			if warningText != "" && s.broker.recordSuppressed != nil {
				s.broker.recordSuppressed(s.metricSessionID, original, warningText)
			}
			s.broker.scheduleCleanupLocked()
		}
		if publication.Done {
			s.broker.removeLocked(s.token, s)
		}
		return visibleBytes, nil
	}
	select {
	case s.events <- publication:
		if publication.Event != nil {
			if messageReserved {
				s.reservedMessageIDs[publication.messageID] = false
			}
			s.publishedBytes += visibleBytes
			s.publishedCount++
			if warningText != "" && s.broker.recordSuppressed != nil {
				s.broker.recordSuppressed(s.metricSessionID, original, warningText)
			}
		}
		if publication.Done {
			s.broker.removeLocked(s.token, s)
		}
		return visibleBytes, nil
	default:
		if messageReserved && s.broker.releaseMessageID != nil {
			s.broker.releaseMessageID(s.sessionID, s.callID, publication.messageID)
		}
		if publication.Event != nil && s.broker.recordSuppressed != nil {
			s.broker.recordSuppressed(s.metricSessionID, original, "")
		}
		return 0, errors.New("commentary publication buffer is full")
	}
}

func (s *commentarySubscription) newMessageIDLocked() string {
	s.broker.nextMessageID++
	return commentaryMessageID(fmt.Sprintf("runtime:%d:%s", s.broker.nextMessageID, s.callID))
}

func (p commentaryPublication) published(callID, metricSessionID string) publishedCommentary {
	return publishedCommentary{callID: callID, metricSessionID: metricSessionID, messageID: p.messageID, event: *p.Event}
}

func (s *commentarySubscription) beginForward() (commentaryPublication, bool) {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	select {
	case publication := <-s.events:
		if publication.messageID != "" {
			if _, reserved := s.reservedMessageIDs[publication.messageID]; reserved {
				s.reservedMessageIDs[publication.messageID] = true
			}
		}
		return publication, true
	default:
		s.live = false
		return commentaryPublication{}, false
	}
}

func (s *commentarySubscription) forward(_ context.Context, first commentaryPublication, emit func(publishedCommentary) error) error {
	publication := first
	for {
		if publication.Done {
			return nil
		}
		if publication.Event != nil {
			if err := emit(publication.published(s.callID, s.metricSessionID)); err != nil {
				s.finishReservation(publication.messageID, false)
				return err
			}
			s.finishReservation(publication.messageID, true)
		}
		var ready bool
		publication, ready = s.nextForward()
		if !ready {
			return nil
		}
	}
}

func (s *commentarySubscription) finishReservation(messageID string, emitted bool) {
	if messageID == "" {
		return
	}
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	if _, reserved := s.reservedMessageIDs[messageID]; !reserved {
		return
	}
	delete(s.reservedMessageIDs, messageID)
	if !emitted && s.broker.releaseMessageID != nil {
		s.broker.releaseMessageID(s.sessionID, s.callID, messageID)
	}
}

func (s *commentarySubscription) nextForward() (commentaryPublication, bool) {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	select {
	case publication := <-s.events:
		if publication.messageID != "" {
			if _, reserved := s.reservedMessageIDs[publication.messageID]; reserved {
				s.reservedMessageIDs[publication.messageID] = true
			}
		}
		return publication, true
	default:
		if s.broker.routes[s.token] == s {
			s.live = false
		}
		return commentaryPublication{}, false
	}
}

func (s *commentarySubscription) cancel() {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	s.broker.cancelLocked(s.token, s)
}

func (s *commentarySubscription) abandon() {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	s.broker.abandonSubscriptionLocked(s)
	s.broker.removeLocked(s.token, s)
}

type httpShellCommentarySink struct {
	client   *http.Client
	endpoint string
	token    string
	budget   *shellOutputBudget
}

func shellCommentaryPublisher(arguments []string) (shellCommentarySink, []string, error) {
	if len(arguments) == 0 || arguments[0] != commentaryEndpointArgument {
		return discardShellCommentarySink{}, arguments, nil
	}
	if len(arguments) < 4 || arguments[2] != commentaryTokenArgument || arguments[1] == "" || arguments[3] == "" {
		return nil, nil, errors.New("shell commentary publisher arguments are malformed")
	}
	return &httpShellCommentarySink{
		client: &http.Client{Timeout: commentaryPublisherTimeout}, endpoint: arguments[1], token: arguments[3],
		budget: newShellOutputBudget(),
	}, arguments[4:], nil
}

func publishShellCommentaryOnce(ctx context.Context, arguments []string) (bool, error) {
	if len(arguments) == 0 || arguments[0] != commentaryOnceArgument {
		return false, nil
	}
	if len(arguments) != 5 || arguments[1] == "" || arguments[2] == "" {
		return true, errors.New("one-shot commentary publisher arguments are malformed")
	}
	form, err := url.PathUnescape(arguments[3])
	if err != nil {
		return true, errors.New("one-shot commentary form is malformed")
	}
	message, err := url.PathUnescape(arguments[4])
	if err != nil {
		return true, errors.New("one-shot commentary text is malformed")
	}
	sink := &httpShellCommentarySink{
		client: &http.Client{Timeout: commentaryPublisherTimeout}, endpoint: arguments[1], token: arguments[2],
	}
	// Publication is auxiliary to the Code Mode program. A closed client or
	// router must not turn commentary into a failed user operation.
	_ = sink.Publish(ctx, shellCommentaryEvent{Text: message, Form: form})
	return true, nil
}

func (s *httpShellCommentarySink) Publish(ctx context.Context, event shellCommentaryEvent) error {
	if s.budget == nil {
		_, err := s.send(ctx, commentaryPublication{Event: &event})
		return err
	}
	return s.budget.charge(func(available int) (int, error) {
		return s.send(ctx, commentaryPublication{Event: &event, BudgetBytes: &available})
	})
}

func (s *httpShellCommentarySink) Complete(ctx context.Context) error {
	_, err := s.send(ctx, commentaryPublication{Done: true})
	return err
}

func (s *httpShellCommentarySink) send(ctx context.Context, publication commentaryPublication) (int, error) {
	body, err := json.Marshal(publication)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("commentary publisher returned %s", response.Status)
	}
	visibleBytes, err := strconv.Atoi(response.Header.Get("X-HPatch-Commentary-Visible-Bytes"))
	if err != nil || visibleBytes < 0 {
		return 0, errors.New("commentary publisher returned an invalid visible byte count")
	}
	return visibleBytes, nil
}
