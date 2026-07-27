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
	chargedScriptVariable  = "HPATCH_CHARGED_SCRIPT"
	accountingFileVariable = "HPATCH_ACCOUNTING_FILE"
	maxAccountingFileBytes = 16 << 20
)

// metricAccounting is host context that cannot be inferred from an evaluated
// script. The router writes it to a private bounded file so model payloads and
// tool definitions are not constrained by the process environment size.
type metricAccounting struct {
	ChargedScript      string `json:"charged_script,omitempty"`
	SessionID          string `json:"session_id,omitempty"`
	Definition         string `json:"definition,omitempty"`
	BaselineDefinition string `json:"baseline_definition,omitempty"`
	DiagnosticSuffix   string `json:"diagnostic_suffix,omitempty"`
	ReportVisible      bool   `json:"report_visible"`
}

func loadMetricAccounting() (metricAccounting, error) {
	path := os.Getenv(accountingFileVariable)
	if path == "" {
		return metricAccounting{
			ChargedScript:      os.Getenv(chargedScriptVariable),
			SessionID:          os.Getenv(sessionEnvironment),
			Definition:         os.Getenv(definitionEnvironment),
			BaselineDefinition: os.Getenv(baselineDefinitionEnvironment),
			ReportVisible:      true,
		}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return metricAccounting{}, fmt.Errorf("opening host accounting: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxAccountingFileBytes+1))
	if err != nil {
		return metricAccounting{}, fmt.Errorf("reading host accounting: %w", err)
	}
	if len(content) > maxAccountingFileBytes {
		return metricAccounting{}, fmt.Errorf("host accounting exceeds %d bytes", maxAccountingFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var accounting metricAccounting
	if err := decoder.Decode(&accounting); err != nil {
		return metricAccounting{}, fmt.Errorf("decoding host accounting: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return metricAccounting{}, fmt.Errorf("decoding host accounting: trailing data")
	}
	return accounting, nil
}

func (a metricAccounting) chargedScript(evaluated string) string {
	if a.ChargedScript == "" {
		return evaluated
	}
	return a.ChargedScript
}

func (a metricAccounting) visibleReport(report string) string {
	if !a.ReportVisible {
		return ""
	}
	return report
}
