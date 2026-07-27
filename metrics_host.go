package hpatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	hostMetricsCommand        = "record-metrics"
	metricsOutputFileVariable = "HPATCH_METRICS_OUTPUT_FILE"
	maxHostMetricsBytes       = 16 << 20
)

// hostMetricRecord is the complete accounting entry calculated by the host.
// Invocation counters originate in hpatch; every token count and the session
// attribution originate at the host seam where the visible payload is known.
type hostMetricRecord struct {
	Invocation                   *invocationMetrics `json:"invocation"`
	SessionID                    string             `json:"session_id,omitempty"`
	HPatchTokens                 uint64             `json:"hpatch_tokens,omitempty"`
	ApplyPatchTokens             uint64             `json:"apply_patch_tokens,omitempty"`
	IneffectiveHPatchTokens      uint64             `json:"ineffective_hpatch_tokens,omitempty"`
	FailedApplyPatchTokens       uint64             `json:"failed_apply_patch_tokens,omitempty"`
	ReportInputTokens            uint64             `json:"report_input_tokens,omitempty"`
	DiagnosticInputTokens        uint64             `json:"diagnostic_input_tokens,omitempty"`
	MetadataInputTokens          uint64             `json:"metadata_input_tokens,omitempty"`
	DefinitionRequests           uint64             `json:"definition_requests,omitempty"`
	DefinitionInputTokens        uint64             `json:"definition_input_tokens,omitempty"`
	RemovedDefinitionInputTokens uint64             `json:"removed_definition_input_tokens,omitempty"`
}

func (r hostMetricRecord) entry() metrics {
	return metrics{
		invocationMetrics:            *r.Invocation,
		HPatchTokens:                 r.HPatchTokens,
		ApplyPatchTokens:             r.ApplyPatchTokens,
		IneffectiveHPatchTokens:      r.IneffectiveHPatchTokens,
		FailedApplyPatchTokens:       r.FailedApplyPatchTokens,
		ReportInputTokens:            r.ReportInputTokens,
		DiagnosticInputTokens:        r.DiagnosticInputTokens,
		MetadataInputTokens:          r.MetadataInputTokens,
		DefinitionRequests:           r.DefinitionRequests,
		DefinitionInputTokens:        r.DefinitionInputTokens,
		RemovedDefinitionInputTokens: r.RemovedDefinitionInputTokens,
	}
}

// writeInvocationMetrics exports evaluator-owned counters without persisting
// them. The host later returns them with its exact payload accounting so one
// durable update represents one logical call.
func writeInvocationMetrics(value invocationMetrics) (bool, error) {
	path := os.Getenv(metricsOutputFileVariable)
	if path == "" {
		return false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return true, fmt.Errorf("encoding invocation metrics: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return true, fmt.Errorf("writing invocation metrics: %w", err)
	}
	return true, nil
}

func recordHostMetrics(stdin io.Reader, stderr io.Writer, dataDirectory string) int {
	content, err := io.ReadAll(io.LimitReader(stdin, maxHostMetricsBytes+1))
	if err != nil {
		return fail(stderr, fmt.Sprintf("reading host metrics: %v", err))
	}
	if len(content) > maxHostMetricsBytes {
		return fail(stderr, fmt.Sprintf("host metrics exceed %d bytes", maxHostMetricsBytes))
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var record hostMetricRecord
	if err := decoder.Decode(&record); err != nil {
		return fail(stderr, fmt.Sprintf("decoding host metrics: %v", err))
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fail(stderr, "decoding host metrics: trailing data")
	}
	if record.Invocation == nil {
		return fail(stderr, "host metrics require invocation counters")
	}
	if record.DefinitionRequests > 1 {
		return fail(stderr, "host metrics contain more than one definition request")
	}
	if record.DefinitionRequests == 0 && (record.DefinitionInputTokens != 0 || record.RemovedDefinitionInputTokens != 0) {
		return fail(stderr, "host definition token counts require a definition request")
	}
	if record.SessionID == "" && record.DefinitionRequests != 0 {
		return fail(stderr, "host definition metrics require a session")
	}
	if err := updateMetricsForSession(dataDirectory, record.entry(), record.SessionID); err != nil {
		return fail(stderr, err.Error())
	}
	return 0
}
