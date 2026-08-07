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

// EvaluatedCommandCount returns the number of recognized commands evaluated
// during this translation attempt.
func (m InvocationMetrics) EvaluatedCommandCount() uint64 {
	total, ok := m.value.Commands.total()
	if !ok {
		return 0
	}
	return total.Invocations
}

// HostMetricRecord is the complete accounting entry calculated by the host.
// Invocation counters originate in hpatch; every token count and the session
// attribution originate at the host seam where the visible payload is known.
// Rejections and attempt identity are transient host telemetry and are not
// written to metrics.bin.
type HostMetricRecord struct {
	Invocation              InvocationMetrics
	Rejections              []HostRejection
	Attempt                 AttemptMetadata
	SessionID               string
	HPatchTokens            uint64
	ApplyPatchTokens        uint64
	IneffectiveHPatchTokens uint64
	FailedApplyPatchTokens  uint64
	ReportInputTokens       uint64
	DiagnosticInputTokens   uint64

	DefinitionRequests                      uint64
	DefinitionInputTokens                   uint64
	RemovedDefinitionInputTokens            uint64
	RemovedExecCommandDefinitionInputTokens uint64
	ToolMetrics                             []ToolMetricRecord
	SharedDefinitionInputTokens             int64
	AuxiliaryTokens                         uint64
}

func (r HostMetricRecord) entry() (metrics, error) {
	entry := metrics{
		invocationMetrics:       r.Invocation.value,
		HPatchTokens:            r.HPatchTokens,
		ApplyPatchTokens:        r.ApplyPatchTokens,
		IneffectiveHPatchTokens: r.IneffectiveHPatchTokens,
		FailedApplyPatchTokens:  r.FailedApplyPatchTokens,
		ReportInputTokens:       r.ReportInputTokens,
		DiagnosticInputTokens:   r.DiagnosticInputTokens,

		DefinitionRequests:                      r.DefinitionRequests,
		DefinitionInputTokens:                   r.DefinitionInputTokens,
		RemovedDefinitionInputTokens:            r.RemovedDefinitionInputTokens,
		RemovedExecCommandDefinitionInputTokens: r.RemovedExecCommandDefinitionInputTokens,
		SharedDefinitionInputTokens:             r.SharedDefinitionInputTokens,
	}
	for _, record := range r.ToolMetrics {
		if err := validateMetricToolKey(record.PluginID, record.ToolName); err != nil {
			return metrics{}, err
		}
		if err := entry.addTool(toolMetric(record)); err != nil {
			return metrics{}, err
		}
	}
	if !validToolMetrics(entry) {
		return metrics{}, fmt.Errorf("host tool metrics are inconsistent")
	}
	return entry, nil
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
	if record.DefinitionRequests == 0 {
		if record.DefinitionInputTokens != 0 || record.RemovedDefinitionInputTokens != 0 ||
			record.RemovedExecCommandDefinitionInputTokens != 0 || record.SharedDefinitionInputTokens != 0 {
			return fmt.Errorf("host definition token counts require a definition request")
		}
		for _, tool := range record.ToolMetrics {
			if tool.DefinitionInputTokens != 0 {
				return fmt.Errorf("host definition token counts require a definition request")
			}
		}
	}
	if record.SessionID == "" && record.DefinitionRequests != 0 {
		return fmt.Errorf("host definition metrics require a session")
	}
	entry, err := record.entry()
	if err != nil {
		return err
	}
	return updateMetricsForSessionContext(ctx, dataDirectory, entry, record.SessionID)
}
