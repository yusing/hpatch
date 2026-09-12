package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

// Each runtime evaluation publishes immutable evidence before the carrier can
// apply it. Continuations share the original change, not conversation ancestry.
func (state hpatchResumeState) translateTracked(ctx context.Context, source string) (hpatchTranslation, error) {
	result, files, err := translateHpatchSegment(ctx, state.Root, source)
	if err != nil || state.ChangeID == "" {
		return result, err
	}
	store, err := openMekugiReplayStore(state.ReplayDirectory)
	if err != nil {
		return result, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return result, err
	}
	callID := state.Handle + "-" + hex.EncodeToString(nonce[:])
	history := mekugiHistory{
		toolName: mekugiToolName, script: source, root: state.Root,
		changeID: state.ChangeID, correlationID: state.CorrelationID, attempt: 1,
		patch: result.Patch, report: changeNotice(state.ChangeID) + result.Report,
		translationError: result.Diagnostic, reviewFiles: files,
		alreadySatisfied: result.Patch == "" && result.Diagnostic == "",
	}
	if err := store.put(ctx, state.Root, map[string]mekugiHistory{callID: history}); err != nil {
		return result, err
	}
	result.AttemptID = callID
	if result.Diagnostic != "" {
		result.Diagnostic = changeNotice(state.ChangeID) + result.Diagnostic
	} else {
		result.Report = history.report
	}
	return result, nil
}

func (state hpatchResumeState) confirmTracked(ctx context.Context, callID string) error {
	if state.ChangeID == "" || !strings.HasPrefix(callID, state.Handle+"-") {
		return errors.New("change confirmation does not belong to this mixed script")
	}
	store, err := openMekugiReplayStore(state.ReplayDirectory)
	if err != nil {
		return err
	}
	history, found, err := store.lookup(ctx, state.Root, callID)
	if err != nil {
		return err
	}
	if !found || history.changeID != state.ChangeID || history.correlationID != state.CorrelationID || history.translationError != "" {
		return errors.New("change confirmation has no successful evaluation")
	}
	history.confirmed = true
	return store.confirmChanges(ctx, state.Root, map[string]mekugiHistory{callID: history})
}
