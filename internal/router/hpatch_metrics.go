package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

const (
	hpatchMetricsOutputFileVariable = "HPATCH_METRICS_OUTPUT_FILE"
	maxHPatchMetricsBytes           = 16 << 20
)

var errHPatchMetricsProtocol = errors.New("hpatch metrics protocol failure")

var hpatchMetricEnvironmentVariables = map[string]bool{
	"HPATCH_ACCOUNTING_FILE":          true,
	"HPATCH_BASELINE_TOOL_DEFINITION": true,
	"HPATCH_CHARGED_SCRIPT":           true,
	"HPATCH_SESSION_ID":               true,
	"HPATCH_TOOL_DEFINITION":          true,
	hpatchMetricsOutputFileVariable:   true,
}

type hpatchMetricRecord struct {
	Invocation                   json.RawMessage `json:"invocation"`
	SessionID                    string          `json:"session_id,omitempty"`
	HPatchTokens                 uint64          `json:"hpatch_tokens,omitempty"`
	ApplyPatchTokens             uint64          `json:"apply_patch_tokens,omitempty"`
	IneffectiveHPatchTokens      uint64          `json:"ineffective_hpatch_tokens,omitempty"`
	FailedApplyPatchTokens       uint64          `json:"failed_apply_patch_tokens,omitempty"`
	ReportInputTokens            uint64          `json:"report_input_tokens,omitempty"`
	DiagnosticInputTokens        uint64          `json:"diagnostic_input_tokens,omitempty"`
	DefinitionRequests           uint64          `json:"definition_requests,omitempty"`
	DefinitionInputTokens        uint64          `json:"definition_input_tokens,omitempty"`
	RemovedDefinitionInputTokens uint64          `json:"removed_definition_input_tokens,omitempty"`
}

type hpatchMetricInputs struct {
	invocation         json.RawMessage
	emittedScript      string
	report             string
	carrier            string
	diagnostic         string
	sessionID          string
	definition         string
	baselineDefinition string
	successful         bool
	overheadOnly       bool
}

func hpatchMetricPayload(script string) string {
	return "functions.hpatch\n" + script
}

func applyPatchMetricPayload(carrier string) string {
	return "functions.exec\n" + carrier
}

func calculateHPatchMetricRecord(inputs hpatchMetricInputs) (hpatchMetricRecord, error) {
	invocation, err := normalizeHPatchInvocationMetrics(inputs.invocation)
	if err != nil {
		return hpatchMetricRecord{}, err
	}
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return hpatchMetricRecord{}, fmt.Errorf("load GPT-5 tokenizer: %w", err)
	}
	record := hpatchMetricRecord{Invocation: invocation}
	if !inputs.overheadOnly {
		if inputs.successful {
			if record.HPatchTokens, err = countHPatchMetricText(codec, hpatchMetricPayload(inputs.emittedScript), "hpatch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.ApplyPatchTokens, err = countHPatchMetricText(codec, applyPatchMetricPayload(inputs.carrier), "apply_patch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.ReportInputTokens, err = countHPatchMetricText(codec, inputs.report, "state report input"); err != nil {
				return hpatchMetricRecord{}, err
			}
		} else {
			if record.IneffectiveHPatchTokens, err = countHPatchMetricText(codec, hpatchMetricPayload(inputs.emittedScript), "ineffective hpatch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.FailedApplyPatchTokens, err = countHPatchMetricText(codec, applyPatchMetricPayload(inputs.carrier), "rejected-call carrier output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.DiagnosticInputTokens, err = countHPatchMetricText(codec, inputs.diagnostic, "diagnostic input"); err != nil {
				return hpatchMetricRecord{}, err
			}
		}
	}
	definition := strings.TrimRight(inputs.definition, "\n")
	baseline := strings.TrimRight(inputs.baselineDefinition, "\n")
	if inputs.sessionID == "" || (definition == "" && baseline == "") {
		return record, nil
	}
	record.SessionID = inputs.sessionID
	record.DefinitionRequests = 1
	if record.DefinitionInputTokens, err = countHPatchMetricText(codec, definition, "hpatch definition input"); err != nil {
		return hpatchMetricRecord{}, err
	}
	if record.RemovedDefinitionInputTokens, err = countHPatchMetricText(codec, baseline, "removed apply_patch definition input"); err != nil {
		return hpatchMetricRecord{}, err
	}
	return record, nil
}

func countHPatchMetricText(codec tokenizer.Codec, value, description string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	count, err := codec.Count(value)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", description, err)
	}
	if count < 0 {
		return 0, fmt.Errorf("count %s: negative token count", description)
	}
	return uint64(count), nil
}

func normalizeHPatchInvocationMetrics(content json.RawMessage) (json.RawMessage, error) {
	if len(content) == 0 {
		return json.RawMessage("{}"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("%w: decode invocation metrics: %w", errHPatchMetricsProtocol, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%w: invocation metrics must be an object", errHPatchMetricsProtocol)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: invocation metrics contain trailing data", errHPatchMetricsProtocol)
	}
	return bytes.Clone(content), nil
}

func readHPatchInvocationMetrics(path string) (json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open invocation metrics: %w", errHPatchMetricsProtocol, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxHPatchMetricsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read invocation metrics: %w", errHPatchMetricsProtocol, err)
	}
	if len(content) > maxHPatchMetricsBytes {
		return nil, fmt.Errorf("%w: invocation metrics exceed %d bytes", errHPatchMetricsProtocol, maxHPatchMetricsBytes)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: invocation metrics are empty", errHPatchMetricsProtocol)
	}
	return normalizeHPatchInvocationMetrics(content)
}

func encodeHPatchMetricRecord(record hpatchMetricRecord) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode hpatch metrics: %w", err)
	}
	if len(encoded) > maxHPatchMetricsBytes {
		return nil, fmt.Errorf("%w: hpatch metrics exceed %d bytes", errHPatchCapacity, maxHPatchMetricsBytes)
	}
	return encoded, nil
}

func hpatchCommandEnvironment(metricsOutputPath string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if hpatchMetricEnvironmentVariables[name] {
			continue
		}
		environment = append(environment, entry)
	}
	if metricsOutputPath != "" {
		environment = append(environment, hpatchMetricsOutputFileVariable+"="+metricsOutputPath)
	}
	return environment
}
