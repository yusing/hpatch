package router

import (
	"context"
	"errors"
	"maps"
	"strings"
)

// Helpers inspect the bounded cache in focused retention tests. Production recovery
// and confirmation consume only the isolated visible map on each transform.
func (p *hpatchProxy) reconcileInputPrefix(request *parsedResponsesRequest, sessionID string) error {
	workspace, _, _ := strings.Cut(sessionID, "\x00")
	_, err := p.reconcileVisibleInput(context.Background(), request, workspace, sessionID)
	return err
}

func (p *hpatchProxy) recoverableHistory(sessionID string) (hpatchHistory, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return hpatchHistory{}, errors.New("no rejected hpatch script to recover; send a complete script")
	}
	return recoveryHistoryOf(maps.Values(session.calls))
}

func (p *hpatchProxy) latestRecoveryAttempt(sessionID, correlationID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return 0
	}
	return latestRecoveryAttempt(maps.Values(session.calls), correlationID)
}
