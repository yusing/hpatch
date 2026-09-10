package router

import (
	"context"
	"errors"
	"maps"
	"strings"
)

// Helpers inspect the bounded cache in focused retention tests. Production recovery
// and confirmation consume only the isolated visible map on each transform.
func (p *mekugiProxy) reconcileInputPrefix(request *parsedResponsesRequest, sessionID string) error {
	workspace, _, _ := strings.Cut(sessionID, "\x00")
	_, err := p.reconcileVisibleInput(context.Background(), request, workspace, sessionID)
	return err
}

func (p *mekugiProxy) recoverableHistory(sessionID string) (mekugiHistory, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return mekugiHistory{}, errors.New("no rejected HPATCH script to recover; send a complete script")
	}
	return recoveryHistoryOf(maps.Values(session.calls))
}

func (p *mekugiProxy) latestRecoveryAttempt(sessionID, correlationID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.sessions[sessionID]
	if session == nil {
		return 0
	}
	return latestRecoveryAttempt(maps.Values(session.calls), correlationID)
}
