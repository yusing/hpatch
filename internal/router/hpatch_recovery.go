package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/yusing/hpatch"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryToolName = "hpatch_recover"

func hpatchRecoveryGuidance(
	script string,
	rejections []hpatch.HostRejection,
	refreshed bool,
) string {
	references, eligible := hpatchRecoveryReferences(script, rejections, refreshed)
	if !eligible {
		return "\nThis rejection requires one complete corrected HPATCH/2 script through functions.hpatch; functions.hpatch_recover changes stale targets only.\n"
	}
	return codexinstructions.RecoveryGuidance(references)
}

func hpatchRecoveryReferences(
	script string,
	rejections []hpatch.HostRejection,
	refreshed bool,
) (string, bool) {
	commands := recoveryCommands(script)
	relevant := make(map[int]struct{})
	for _, rejection := range rejections {
		if rejection.Reason != "row-stale" || rejection.Command < 1 || rejection.Command > len(commands) {
			return "", false
		}
		command := commands[rejection.Command-1]
		if !command.parts.parsed || command.parts.target == "" {
			return "", false
		}
		relevant[rejection.Command] = struct{}{}
	}
	if len(relevant) == 0 {
		return "", false
	}

	var output strings.Builder
	if refreshed {
		output.WriteString("This re-rejection changed no workspace file. Earlier C... handles are stale; use only the current handles below.\n\n")
	}
	output.WriteString("Rejected target commands:\n")
	indices := make([]int, 0, len(relevant))
	for index := range relevant {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	for _, index := range indices {
		command := commands[index-1]
		fmt.Fprintf(&output, "    %s %s\n", command.handle, hpatchRecoveryCommandSummary(command))
	}
	output.WriteString("\nSend one line per listed command as C... CURRENT_TARGET. Put all corrections in one functions.hpatch_recover payload; the router preserves every operation and value and reevaluates the complete script.\n")
	return output.String(), true
}

func hpatchRecoveryCommandSummary(command recoveryCommandReference) string {
	summary := command.parts.operation
	if command.parts.target != "" {
		summary += " " + command.parts.target
	}
	if command.parts.multiline {
		return summary + " [heredoc value]"
	}
	return summary + " [inline value]"
}

func hpatchLogicalRowsByPhysicalLine(script string, lines []hpatchsyntax.PhysicalLine) [][]int {
	mapped := make([][]int, len(lines))
	offset := 0
	logicalRow := 1
	for index, line := range lines {
		next := offset + len(line.Text) + len(line.Terminator)
		count := hpatch.TextLineCount(script[offset:next])
		for range count {
			mapped[index] = append(mapped[index], logicalRow)
			logicalRow++
		}
		offset = next
	}
	return mapped
}

type hpatchOutcomeReporter interface {
	ReportOutcome(ctx context.Context, stage, outcome string) error
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

// recoveryHistoryOf picks the newest call that hpatch actually evaluated.
// Proxy-rejected calls are skipped because they changed nothing. A successful
// newest call blocks recovery of an older rejection.
func recoveryHistoryOf(histories iter.Seq[hpatchHistory]) (hpatchHistory, error) {
	var latest hpatchHistory
	found := false
	for history := range histories {
		if history.unevaluated || (history.toolName != hpatchToolName && history.toolName != hpatchRecoveryToolName) {
			continue
		}
		if !found || history.sequence > latest.sequence {
			latest = history
			found = true
		}
	}
	if !found {
		return hpatchHistory{}, errors.New("no rejected hpatch script to recover; send a complete script")
	}
	if latest.translationError == "" {
		return hpatchHistory{}, errors.New("the most recent hpatch call succeeded; recovery edits require a rejected script, so send a complete script")
	}
	if !latest.evaluatorRejected {
		return hpatchHistory{}, errors.New("the most recent hpatch call did not produce an evaluator rejection; send a complete script")
	}
	return latest, nil
}

func latestRecoveryAttempt(histories iter.Seq[hpatchHistory], correlationID string) int {
	latest := 0
	for history := range histories {
		if (history.toolName == hpatchToolName || history.toolName == hpatchRecoveryToolName) && history.correlationID == correlationID {
			latest = max(latest, history.attempt)
		}
	}
	return latest
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

// recoveryBaseline is the complete rejected script a following recovery edits.
func (h hpatchHistory) recoveryBaseline() string {
	if h.evaluated != "" {
		return h.evaluated
	}
	return h.script
}

func (t *hpatchResponseTransform) translateRecovery(
	callID, input string,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	if history, ok := t.local[callID]; ok {
		if history.toolName != hpatchRecoveryToolName || history.pluginID != "" || history.script != input {
			return hpatchHistory{}, fmt.Errorf("hpatch recovery call %q changed input", callID)
		}
		if len(upstreamItem) != 0 {
			history.upstreamItem = maps.Clone(upstreamItem)
			t.local[callID] = history
		}
		return history, nil
	}
	if len(input) > maxHPatchScriptBytes {
		return hpatchHistory{}, fmt.Errorf("hpatch recovery call %q payload exceeds %d bytes", callID, maxHPatchScriptBytes)
	}
	base, baseErr := t.recoveryHistory()
	attemptMetadata := hpatch.AttemptMetadata{
		SessionID:      t.sessionID,
		Title:          t.proxy.titles.title(t.sessionID),
		CorrelationID:  callID,
		CallID:         callID,
		Attempt:        1,
		Correction:     true,
		Model:          t.model,
		ToolName:       hpatchRecoveryToolName,
		EmittedPayload: input,
	}
	if baseErr != nil {
		return t.rejectUnevaluated(hpatchRecoveryToolName, callID, input, baseErr, attemptMetadata, "", nil, upstreamItem)
	}
	attemptMetadata.CorrelationID = base.correlationID
	if attemptMetadata.CorrelationID == "" {
		attemptMetadata.CorrelationID = callID
	}
	attemptMetadata.Attempt = t.nextRecoveryAttempt(attemptMetadata.CorrelationID, base.attempt)
	if base.root != t.directory {
		return t.rejectUnevaluated(
			hpatchRecoveryToolName,
			callID,
			input,
			errors.New("the rejected script belongs to a different worktree; send a complete script"),
			attemptMetadata,
			"",
			nil,
			upstreamItem,
		)
	}
	baseline := base.recoveryBaseline()
	recovered, err := recoverScriptDetailed(t.ctx, baseline, input)
	if err != nil {
		return t.rejectUnevaluated(
			hpatchRecoveryToolName,
			callID,
			input,
			err,
			attemptMetadata,
			baseline,
			base.rejections,
			upstreamItem,
		)
	}
	attemptMetadata.EvaluatedScript = recovered.script
	attemptMetadata.RecoveryDelta = recovered.delta
	history, err := t.translateRecovered(callID, input, recovered.script, attemptMetadata, upstreamItem)
	if err == nil {
		history.toolName = hpatchRecoveryToolName
		t.local[callID] = history
	}
	return history, err
}

func (t *hpatchResponseTransform) translateRecovered(
	callID, emitted, evaluated string,
	attemptMetadata hpatch.AttemptMetadata,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	attemptContext := hpatch.WithAttemptMetadata(t.ctx, attemptMetadata)
	translated, err := t.proxy.translator.Translate(attemptContext, t.directory, evaluated)
	if err != nil {
		if contextErr := t.ctx.Err(); contextErr != nil {
			return hpatchHistory{}, contextErr
		}
		if errors.Is(err, errHPatchCapacity) {
			return hpatchHistory{}, err
		}
		evaluatorRejected := len(translated.rejections) != 0
		diagnostic := translated.diagnostic
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		if evaluatorRejected {
			diagnostic += hpatchRecoveryGuidance(evaluated, translated.rejections, true)
		}
		history := hpatchHistory{
			toolName:          hpatchRecoveryToolName,
			script:            emitted,
			root:              t.directory,
			evaluated:         evaluated,
			carrierName:       t.codeModeToolName,
			translationError:  diagnostic,
			evaluatorRejected: evaluatorRejected,
			rejections:        slices.Clone(translated.rejections),
			upstreamItem:      maps.Clone(upstreamItem),
			correlationID:     attemptMetadata.CorrelationID,
			attempt:           attemptMetadata.Attempt,
		}
		t.recordLocal(callID, &history)
		return history, nil
	}
	patchText := string(translated.patch)
	alreadySatisfied := translated.change.AlreadySatisfied
	history := hpatchHistory{
		toolName:         hpatchRecoveryToolName,
		script:           emitted,
		root:             t.directory,
		evaluated:        evaluated,
		patch:            patchText,
		alreadySatisfied: alreadySatisfied,
		aliases:          slices.Clone(translated.aliases),
		carrierName:      t.codeModeToolName,
		report:           hpatchReport(translated.report, translated.diagnostic),
		upstreamItem:     maps.Clone(upstreamItem),
		correlationID:    attemptMetadata.CorrelationID,
		attempt:          attemptMetadata.Attempt,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

func (t *hpatchResponseTransform) rejectUnevaluated(
	toolName, callID, input string,
	rejection error,
	attempt hpatch.AttemptMetadata,
	referenceScript string,
	rejections []hpatch.HostRejection,
	upstreamItem map[string]json.RawMessage,
) (hpatchHistory, error) {
	diagnostic := rejection.Error()
	if referenceScript != "" {
		diagnostic += hpatchRecoveryGuidance(referenceScript, rejections, false)
	}
	if reporter, ok := t.proxy.translator.(hpatchOutcomeReporter); ok {
		attemptContext := hpatch.WithAttemptMetadata(t.ctx, attempt)
		if hookErr := reporter.ReportOutcome(attemptContext, "unevaluated", "rejected"); hookErr != nil {
			diagnostic += "\nhpatch: warning: " + strings.TrimSpace(hookErr.Error()) + "\n"
		}
	}
	history := hpatchHistory{
		toolName: toolName,
		script:   input,

		carrierName:      t.codeModeToolName,
		translationError: diagnostic,
		correlationID:    attempt.CorrelationID,
		attempt:          attempt.Attempt,
		upstreamItem:     maps.Clone(upstreamItem),
		unevaluated:      true,
	}
	t.recordLocal(callID, &history)
	return history, nil
}

// recoveryHistory is the rejected call a recovery in this turn edits. A
// rejection this turn is newer than retained history, which commits only after
// the response completes.
func (t *hpatchResponseTransform) recoveryHistory() (hpatchHistory, error) {
	for _, history := range t.local {
		if !history.unevaluated &&
			(history.toolName == hpatchToolName || history.toolName == hpatchRecoveryToolName) {
			return recoveryHistoryOf(maps.Values(t.local))
		}
	}
	return t.proxy.recoverableHistory(t.historySessionID)
}

func (t *hpatchResponseTransform) nextRecoveryAttempt(correlationID string, baseAttempt int) int {
	latest := max(baseAttempt, t.proxy.latestRecoveryAttempt(t.historySessionID, correlationID))
	latest = max(latest, latestRecoveryAttempt(maps.Values(t.local), correlationID))
	return max(latest+1, 2)
}
