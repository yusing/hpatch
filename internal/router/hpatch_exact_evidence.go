package router

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	hpatchExactEvidenceEnvironment = "HPATCH_BENCH_EXACT_EVIDENCE_DIR"
	hpatchExactEvidenceSchema      = "hpatch.benchmark.exact-attempt.v1"
)

type hpatchExactEvidenceRecorder struct {
	directory string
}

type hpatchExactAttemptEvidence struct {
	Schema                   string `json:"schema"`
	SessionID                string `json:"session_id"`
	CorrelationID            string `json:"correlation_id"`
	CallID                   string `json:"call_id"`
	Attempt                  int    `json:"attempt"`
	Correction               bool   `json:"correction"`
	Model                    string `json:"model"`
	ToolName                 string `json:"tool_name"`
	Outcome                  string `json:"outcome"`
	EmittedPayload           string `json:"emitted_payload"`
	EmittedPayloadBytes      int    `json:"emitted_payload_bytes"`
	EmittedPayloadSHA256     string `json:"emitted_payload_sha256"`
	RenderedDiagnostic       string `json:"rendered_diagnostic"`
	RenderedDiagnosticBytes  int    `json:"rendered_diagnostic_bytes"`
	RenderedDiagnosticSHA256 string `json:"rendered_diagnostic_sha256"`
	RenderedReport           string `json:"rendered_report"`
	RenderedReportBytes      int    `json:"rendered_report_bytes"`
	RenderedReportSHA256     string `json:"rendered_report_sha256"`
}

func newHPatchExactEvidenceRecorder() *hpatchExactEvidenceRecorder {
	directory := os.Getenv(hpatchExactEvidenceEnvironment)
	if directory == "" || !filepath.IsAbs(directory) {
		return nil
	}
	return &hpatchExactEvidenceRecorder{directory: filepath.Clean(directory)}
}

func (r *hpatchExactEvidenceRecorder) record(inputs hpatchMetricInputs) error {
	if r == nil || inputs.attempt.Attempt < 1 {
		return nil
	}
	toolName := inputs.emittedTool
	if toolName == "" {
		toolName = inputs.attempt.ToolName
	}
	if toolName != hpatchToolName && toolName != hpatchRecoveryToolName {
		return nil
	}
	outcome := "router_rejected"
	if inputs.successful {
		outcome = "successful"
	} else if len(inputs.rejections) != 0 {
		outcome = "evaluator_rejected"
	}
	diagnostic := inputs.diagnostic
	report := ""
	if inputs.successful {
		// Successful calls have a report carrier, not a rendered rejection
		// diagnostic. Preserve that exact model-visible report separately.
		diagnostic = ""
		report = inputs.report
	}
	evidence := hpatchExactAttemptEvidence{
		Schema:                   hpatchExactEvidenceSchema,
		SessionID:                inputs.attempt.SessionID,
		CorrelationID:            inputs.attempt.CorrelationID,
		CallID:                   inputs.attempt.CallID,
		Attempt:                  inputs.attempt.Attempt,
		Correction:               inputs.attempt.Correction,
		Model:                    inputs.attempt.Model,
		ToolName:                 toolName,
		Outcome:                  outcome,
		EmittedPayload:           inputs.emittedScript,
		EmittedPayloadBytes:      len(inputs.emittedScript),
		EmittedPayloadSHA256:     hpatchExactEvidenceSHA256(inputs.emittedScript),
		RenderedDiagnostic:       diagnostic,
		RenderedDiagnosticBytes:  len(diagnostic),
		RenderedDiagnosticSHA256: hpatchExactEvidenceSHA256(diagnostic),
		RenderedReport:           report,
		RenderedReportBytes:      len(report),
		RenderedReportSHA256:     hpatchExactEvidenceSHA256(report),
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(r.directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(r.directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(r.directory, ".exact-attempt-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	completedName := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(temporaryName), ".tmp"), ".") + ".json"
	if err := os.Rename(temporaryName, filepath.Join(r.directory, completedName)); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func hpatchExactEvidenceSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
