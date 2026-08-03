package hpatch

import (
	"context"
	"fmt"
)

// InvocationMetrics is evaluator-owned accounting returned by TranslateForHost.
// Its representation remains private so hosts cannot construct inconsistent
// command, selector, outcome, and reason totals.
type InvocationMetrics struct {
	value invocationMetrics
}

// HostMetricRecord is the complete accounting entry calculated by the host.
// Invocation counters originate in hpatch; every token count and the session
// attribution originate at the host seam where the visible payload is known.
// Rejections are transient host telemetry and are not written to metrics.bin.
type HostMetricRecord struct {
	Invocation              InvocationMetrics
	Rejections              []HostRejection
	SessionID               string
	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
	FailedApplyPatchTokens  uint64
	ReportInputTokens       uint64
	DiagnosticInputTokens   uint64

	DefinitionRequests           uint64
	DefinitionInputTokens        uint64
	RemovedDefinitionInputTokens uint64
}

func (r HostMetricRecord) entry() metrics {
	return metrics{
		invocationMetrics:       r.Invocation.value,
		HPatchTokens:            r.HPatchTokens,
		ApplyPatchTokens:        r.ApplyPatchTokens,
		IneffectiveHPatchTokens: r.IneffectiveHPatchTokens,
		FailedApplyPatchTokens:  r.FailedApplyPatchTokens,
		ReportInputTokens:       r.ReportInputTokens,
		DiagnosticInputTokens:   r.DiagnosticInputTokens,

		DefinitionRequests:           r.DefinitionRequests,
		DefinitionInputTokens:        r.DefinitionInputTokens,
		RemovedDefinitionInputTokens: r.RemovedDefinitionInputTokens,
	}
}

// RecordHostMetrics persists one complete host-accounted call.
func RecordHostMetrics(ctx context.Context, dataDirectory string, record HostMetricRecord) error {
	if ctx == nil {
		return fmt.Errorf("host metrics context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.DefinitionRequests > 1 {
		return fmt.Errorf("host metrics contain more than one definition request")
	}
	if record.DefinitionRequests == 0 && (record.DefinitionInputTokens != 0 || record.RemovedDefinitionInputTokens != 0) {
		return fmt.Errorf("host definition token counts require a definition request")
	}
	if record.SessionID == "" && record.DefinitionRequests != 0 {
		return fmt.Errorf("host definition metrics require a session")
	}
	return updateMetricsForSessionContext(ctx, dataDirectory, record.entry(), record.SessionID)
}
