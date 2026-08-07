package hpatch

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"slices"

	"github.com/tiktoken-go/tokenizer"
)

const (
	maxMetricTools         = 128
	maxMetricPluginIDBytes = 128
	maxMetricToolNameBytes = 64
)

type toolMetric struct {
	PluginID               string
	ToolName               string
	DefinitionInputTokens  uint64
	Calls                  uint64
	EmittedTokens          uint64
	TranslatedTokens       uint64
	FailedTranslations     uint64
	FailedEmittedTokens    uint64
	FailedTranslatedTokens uint64
	Executions             uint64
	CurrentInputTokens     uint64
	StockInputTokens       uint64
}

// HostToolDefinition identifies one installed model-visible tool object.
type HostToolDefinition struct {
	PluginID   string
	ToolName   string
	Definition string
}

// HostToolCall is one terminal router translation classification.
// Names and payloads are the exact model-visible and validated carrier shapes.
type HostToolCall struct {
	PluginID          string
	ToolName          string
	EmittedName       string
	EmittedInput      string
	TranslatedName    string
	TranslatedPayload string
	FailedTranslation bool
}

// HostToolResult is one completed executor result classification.
// CurrentOutput and StockOutput are the canonical stdout-then-stderr content shapes.
type HostToolResult struct {
	PluginID      string
	ToolName      string
	CurrentOutput string
	StockOutput   string
}

// HostMetricInput is the structured host evidence consumed by the metrics classifier.
type HostMetricInput struct {
	Invocation InvocationMetrics
	Rejections []HostRejection
	Attempt    AttemptMetadata
	SessionID  string

	InstalledDefinition string
	ToolDefinitions     []HostToolDefinition
	RemovedDefinition   string
	ToolCall            *HostToolCall
	ToolResult          *HostToolResult
	StateReport         string
	Diagnostic          string
	AuxiliaryTexts      []string
}

// ToolMetricRecord is one classified plugin-and-tool increment.
type ToolMetricRecord struct {
	PluginID               string
	ToolName               string
	DefinitionInputTokens  uint64
	Calls                  uint64
	EmittedTokens          uint64
	TranslatedTokens       uint64
	FailedTranslations     uint64
	FailedEmittedTokens    uint64
	FailedTranslatedTokens uint64
	Executions             uint64
	CurrentInputTokens     uint64
	StockInputTokens       uint64
}

// ToolGainMetric is one stable output-token row in structured gain data.
type ToolGainMetric struct {
	PluginID         string `json:"plugin_id"`
	ToolName         string `json:"tool_name"`
	Failed           bool   `json:"failed"`
	Calls            uint64 `json:"calls"`
	EmittedTokens    uint64 `json:"emitted_tokens"`
	TranslatedTokens uint64 `json:"translated_tokens"`
	Reduction        string `json:"reduction_percent"`
}

// ToolInputGainMetric is one stable input-token row in structured gain data.
type ToolInputGainMetric struct {
	PluginID      string `json:"plugin_id"`
	ToolName      string `json:"tool_name"`
	Executions    uint64 `json:"executions"`
	CurrentTokens uint64 `json:"current_tokens"`
	StockTokens   uint64 `json:"stock_tokens"`
	Reduction     string `json:"reduction_percent"`
}

// ToolDefinitionGainMetric is one descriptive child of the installed-definition total.
type ToolDefinitionGainMetric struct {
	PluginID string `json:"plugin_id"`
	ToolName string `json:"tool_name"`
	Tokens   uint64 `json:"tokens"`
}

// ClassifyHostMetrics tokenizes structured registry and carrier evidence with the GPT-5
// mapping. Plugin code supplies shapes, never counts.
func ClassifyHostMetrics(input HostMetricInput) (HostMetricRecord, error) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return HostMetricRecord{}, fmt.Errorf("load GPT-5 tokenizer: %w", err)
	}
	record := HostMetricRecord{
		Invocation: input.Invocation,
		Rejections: slices.Clone(input.Rejections),
		Attempt:    input.Attempt,
		SessionID:  input.SessionID,
	}
	for _, text := range input.AuxiliaryTexts {
		count, countErr := countMetricText(codec, text, "auxiliary input")
		if countErr != nil {
			return HostMetricRecord{}, countErr
		}
		if !addCounter(&record.AuxiliaryTokens, count) {
			return HostMetricRecord{}, fmt.Errorf("classifying metrics: auxiliary token count overflow")
		}
	}
	if record.ReportInputTokens, err = countMetricText(codec, input.StateReport, "state report input"); err != nil {
		return HostMetricRecord{}, err
	}
	if record.DiagnosticInputTokens, err = countMetricText(codec, input.Diagnostic, "diagnostic input"); err != nil {
		return HostMetricRecord{}, err
	}
	if input.SessionID != "" && (input.InstalledDefinition != "" || input.RemovedDefinition != "") {
		record.DefinitionRequests = 1
		if record.DefinitionInputTokens, err = countMetricText(codec, input.InstalledDefinition, "installed tool definitions"); err != nil {
			return HostMetricRecord{}, err
		}
		if record.RemovedDefinitionInputTokens, err = countMetricText(codec, input.RemovedDefinition, "removed tool definition"); err != nil {
			return HostMetricRecord{}, err
		}
		var breakdown uint64
		for _, definition := range input.ToolDefinitions {
			if err := validateMetricToolKey(definition.PluginID, definition.ToolName); err != nil {
				return HostMetricRecord{}, err
			}
			count, countErr := countMetricText(codec, definition.Definition, "installed tool definition")
			if countErr != nil {
				return HostMetricRecord{}, countErr
			}
			if !addCounter(&breakdown, count) {
				return HostMetricRecord{}, fmt.Errorf("classifying metrics: definition breakdown overflow")
			}
			if err := mergeToolMetricRecord(&record.ToolMetrics, ToolMetricRecord{
				PluginID: definition.PluginID, ToolName: definition.ToolName, DefinitionInputTokens: count,
			}); err != nil {
				return HostMetricRecord{}, err
			}
		}
		switch {
		case record.DefinitionInputTokens >= breakdown && record.DefinitionInputTokens-breakdown <= math.MaxInt64:
			record.SharedDefinitionInputTokens = int64(record.DefinitionInputTokens - breakdown)
		case breakdown > record.DefinitionInputTokens && breakdown-record.DefinitionInputTokens <= math.MaxInt64:
			record.SharedDefinitionInputTokens = -int64(breakdown - record.DefinitionInputTokens)
		default:
			return HostMetricRecord{}, fmt.Errorf("classifying metrics: shared definition framing overflow")
		}
	}
	if input.ToolCall != nil {
		call := input.ToolCall
		if err := validateMetricToolKey(call.PluginID, call.ToolName); err != nil {
			return HostMetricRecord{}, err
		}
		emitted, countErr := countMetricText(codec, metricCallShape(call.EmittedName, call.EmittedInput), "emitted tool call")
		if countErr != nil {
			return HostMetricRecord{}, countErr
		}
		var translated uint64
		if call.TranslatedName != "" {
			translated, countErr = countMetricText(codec, metricCallShape(call.TranslatedName, call.TranslatedPayload), "translated tool call")
			if countErr != nil {
				return HostMetricRecord{}, countErr
			}
		}
		metric := ToolMetricRecord{PluginID: call.PluginID, ToolName: call.ToolName}
		if call.FailedTranslation {
			metric.FailedTranslations = 1
			metric.FailedEmittedTokens = emitted
			metric.FailedTranslatedTokens = translated
		} else {
			metric.Calls = 1
			metric.EmittedTokens = emitted
			metric.TranslatedTokens = translated
		}
		if err := mergeToolMetricRecord(&record.ToolMetrics, metric); err != nil {
			return HostMetricRecord{}, err
		}
		if call.PluginID == "builtin.hpatch" && call.ToolName == "hpatch" {
			if call.FailedTranslation {
				record.IneffectiveHPatchTokens = emitted
				record.FailedApplyPatchTokens = translated
			} else {
				record.HPatchTokens = emitted
				record.ApplyPatchTokens = translated
			}
		}
	}
	if input.ToolResult != nil {
		result := input.ToolResult
		if err := validateMetricToolKey(result.PluginID, result.ToolName); err != nil {
			return HostMetricRecord{}, err
		}
		current, countErr := countMetricText(codec, result.CurrentOutput, "current tool result")
		if countErr != nil {
			return HostMetricRecord{}, countErr
		}
		stock, countErr := countMetricText(codec, result.StockOutput, "stock tool result")
		if countErr != nil {
			return HostMetricRecord{}, countErr
		}
		if err := mergeToolMetricRecord(&record.ToolMetrics, ToolMetricRecord{
			PluginID: result.PluginID, ToolName: result.ToolName,
			Executions: 1, CurrentInputTokens: current, StockInputTokens: stock,
		}); err != nil {
			return HostMetricRecord{}, err
		}
	}
	slices.SortFunc(record.ToolMetrics, compareToolMetricRecords)
	return record, nil
}

func metricCallShape(name, payload string) string {
	return name + "\n" + payload
}

func countMetricText(codec tokenizer.Codec, value, description string) (uint64, error) {
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

func validateMetricToolKey(pluginID, toolName string) error {
	if !validMetricIdentifier(pluginID, maxMetricPluginIDBytes, false, true) ||
		!validMetricIdentifier(toolName, maxMetricToolNameBytes, true, false) {
		return fmt.Errorf("metrics tool identity %q/%q is invalid", pluginID, toolName)
	}
	return nil
}

func validMetricIdentifier(value string, maxBytes int, allowLeadingUnderscore, allowDot bool) bool {
	if len(value) == 0 || len(value) > maxBytes {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
		if index == 0 {
			if !letter && (!allowLeadingUnderscore || character != '_') {
				return false
			}
			continue
		}
		if letter || character >= '0' && character <= '9' || character == '_' || character == '-' || allowDot && character == '.' {
			continue
		}
		return false
	}
	return true
}

func compareToolMetricRecords(a, b ToolMetricRecord) int {
	return cmp.Or(cmp.Compare(a.PluginID, b.PluginID), cmp.Compare(a.ToolName, b.ToolName))
}

func mergeToolMetricRecord(records *[]ToolMetricRecord, increment ToolMetricRecord) error {
	for index := range *records {
		current := &(*records)[index]
		if current.PluginID != increment.PluginID || current.ToolName != increment.ToolName {
			continue
		}
		if !addToolMetricRecord(current, increment) {
			return fmt.Errorf("classifying metrics: tool counter overflow")
		}
		return nil
	}
	if len(*records) >= maxMetricTools {
		return fmt.Errorf("classifying metrics: tool collection exceeds %d entries", maxMetricTools)
	}
	*records = append(*records, increment)
	return nil
}

func addToolMetricRecord(destination *ToolMetricRecord, increment ToolMetricRecord) bool {
	return addCounter(&destination.DefinitionInputTokens, increment.DefinitionInputTokens) &&
		addCounter(&destination.Calls, increment.Calls) &&
		addCounter(&destination.EmittedTokens, increment.EmittedTokens) &&
		addCounter(&destination.TranslatedTokens, increment.TranslatedTokens) &&
		addCounter(&destination.FailedTranslations, increment.FailedTranslations) &&
		addCounter(&destination.FailedEmittedTokens, increment.FailedEmittedTokens) &&
		addCounter(&destination.FailedTranslatedTokens, increment.FailedTranslatedTokens) &&
		addCounter(&destination.Executions, increment.Executions) &&
		addCounter(&destination.CurrentInputTokens, increment.CurrentInputTokens) &&
		addCounter(&destination.StockInputTokens, increment.StockInputTokens)
}

func (m *metrics) addTool(increment toolMetric) error {
	for index := range int(m.ToolCount) {
		current := &m.Tools[index]
		order := cmp.Or(cmp.Compare(current.PluginID, increment.PluginID), cmp.Compare(current.ToolName, increment.ToolName))
		if order == 0 {
			record := ToolMetricRecord(*current)
			if !addToolMetricRecord(&record, ToolMetricRecord(increment)) {
				return fmt.Errorf("updating metrics: tool counter overflow")
			}
			*current = toolMetric(record)
			return nil
		}
		if order > 0 {
			if int(m.ToolCount) >= maxMetricTools {
				return fmt.Errorf("updating metrics: tool collection exceeds %d entries", maxMetricTools)
			}
			copy(m.Tools[index+1:int(m.ToolCount)+1], m.Tools[index:int(m.ToolCount)])
			m.Tools[index] = increment
			m.ToolCount++
			return nil
		}
	}
	if int(m.ToolCount) >= maxMetricTools {
		return fmt.Errorf("updating metrics: tool collection exceeds %d entries", maxMetricTools)
	}
	m.Tools[m.ToolCount] = increment
	m.ToolCount++
	return nil
}

func (m *metrics) clearDefinitionMetrics() {
	m.DefinitionInputTokens = 0
	m.RemovedDefinitionInputTokens = 0
	m.SharedDefinitionInputTokens = 0
	for index := range int(m.ToolCount) {
		m.Tools[index].DefinitionInputTokens = 0
	}
}

func addSignedCounter(destination *int64, increment int64) bool {
	if increment > 0 && *destination > math.MaxInt64-increment ||
		increment < 0 && *destination < math.MinInt64-increment {
		return false
	}
	*destination += increment
	return true
}

func validToolMetrics(m metrics) bool {
	if int(m.ToolCount) > maxMetricTools {
		return false
	}
	var prior toolMetric
	var definitions, calls, emitted, translated, failed, failedEmitted, failedTranslated uint64
	var executions, currentInput, stockInput uint64
	for index := range int(m.ToolCount) {
		entry := m.Tools[index]
		if validateMetricToolKey(entry.PluginID, entry.ToolName) != nil {
			return false
		}
		if index > 0 && (prior.PluginID > entry.PluginID || prior.PluginID == entry.PluginID && prior.ToolName >= entry.ToolName) {
			return false
		}
		prior = entry
		for _, pair := range []struct {
			total *uint64
			value uint64
		}{
			{&definitions, entry.DefinitionInputTokens}, {&calls, entry.Calls},
			{&emitted, entry.EmittedTokens}, {&translated, entry.TranslatedTokens},
			{&failed, entry.FailedTranslations}, {&failedEmitted, entry.FailedEmittedTokens},
			{&failedTranslated, entry.FailedTranslatedTokens},
			{&executions, entry.Executions}, {&currentInput, entry.CurrentInputTokens}, {&stockInput, entry.StockInputTokens},
		} {
			if !addCounter(pair.total, pair.value) {
				return false
			}
		}
	}
	if !addCounter(&calls, failed) || !addCounter(&emitted, failedEmitted) || !addCounter(&translated, failedTranslated) {
		return false
	}
	reconciled := new(big.Int).SetUint64(definitions)
	reconciled.Add(reconciled, big.NewInt(m.SharedDefinitionInputTokens))
	return reconciled.Sign() >= 0 && reconciled.Cmp(new(big.Int).SetUint64(m.DefinitionInputTokens)) == 0
}

func metricReduction(emitted, translated uint64) string {
	if translated == 0 {
		return "n/a"
	}
	difference := new(big.Int).Sub(new(big.Int).SetUint64(translated), new(big.Int).SetUint64(emitted))
	return percentage(difference, new(big.Int).SetUint64(translated))
}

func (m metrics) gainToolRows() ([]ToolGainMetric, ToolGainMetric, []ToolDefinitionGainMetric) {
	entries := slices.Clone(m.Tools[:m.ToolCount])
	hasHPatch := slices.ContainsFunc(entries, func(entry toolMetric) bool {
		return entry.PluginID == "builtin.hpatch" && entry.ToolName == "hpatch"
	})
	if !hasHPatch && (m.HPatchTokens != 0 || m.ApplyPatchTokens != 0 || m.IneffectiveHPatchTokens != 0 || m.FailedApplyPatchTokens != 0) {
		entries = append(entries, toolMetric{
			PluginID: "builtin.hpatch", ToolName: "hpatch",
			Calls:         boolCount(m.HPatchTokens != 0 || m.ApplyPatchTokens != 0),
			EmittedTokens: m.HPatchTokens, TranslatedTokens: m.ApplyPatchTokens,
			FailedTranslations:  boolCount(m.IneffectiveHPatchTokens != 0 || m.FailedApplyPatchTokens != 0),
			FailedEmittedTokens: m.IneffectiveHPatchTokens, FailedTranslatedTokens: m.FailedApplyPatchTokens,
		})
		slices.SortFunc(entries, func(a, b toolMetric) int {
			return cmp.Or(cmp.Compare(a.PluginID, b.PluginID), cmp.Compare(a.ToolName, b.ToolName))
		})
	}

	tools := make([]ToolGainMetric, 0, len(entries)*2)
	definitions := make([]ToolDefinitionGainMetric, 0, len(entries))
	all := ToolGainMetric{ToolName: "all-tools"}
	for _, entry := range entries {
		row := ToolGainMetric{
			PluginID: entry.PluginID, ToolName: entry.ToolName, Calls: entry.Calls,
			EmittedTokens: entry.EmittedTokens, TranslatedTokens: entry.TranslatedTokens,
			Reduction: metricReduction(entry.EmittedTokens, entry.TranslatedTokens),
		}
		tools = append(tools, row)
		all.Calls += row.Calls
		all.EmittedTokens += row.EmittedTokens
		all.TranslatedTokens += row.TranslatedTokens
		if entry.FailedTranslations != 0 {
			row := ToolGainMetric{
				PluginID: entry.PluginID, ToolName: entry.ToolName, Failed: true, Calls: entry.FailedTranslations,
				EmittedTokens: entry.FailedEmittedTokens, TranslatedTokens: entry.FailedTranslatedTokens,
				Reduction: "n/a",
			}
			tools = append(tools, row)
			all.Calls += row.Calls
			all.EmittedTokens += row.EmittedTokens
			all.TranslatedTokens += row.TranslatedTokens
		}
		if entry.DefinitionInputTokens != 0 || m.DefinitionRequests != 0 {
			definitions = append(definitions, ToolDefinitionGainMetric{
				PluginID: entry.PluginID, ToolName: entry.ToolName, Tokens: entry.DefinitionInputTokens,
			})
		}
	}
	all.Reduction = metricReduction(all.EmittedTokens, all.TranslatedTokens)
	return tools, all, definitions
}

func (m metrics) gainToolInputRows() ([]ToolInputGainMetric, ToolInputGainMetric) {
	rows := make([]ToolInputGainMetric, 0, m.ToolCount)
	all := ToolInputGainMetric{ToolName: "all-tools"}
	for _, entry := range m.Tools[:m.ToolCount] {
		if entry.Executions == 0 {
			continue
		}
		row := ToolInputGainMetric{
			PluginID:      entry.PluginID,
			ToolName:      entry.ToolName,
			Executions:    entry.Executions,
			CurrentTokens: entry.CurrentInputTokens,
			StockTokens:   entry.StockInputTokens,
			Reduction:     metricReduction(entry.CurrentInputTokens, entry.StockInputTokens),
		}
		rows = append(rows, row)
		all.Executions += row.Executions
		all.CurrentTokens += row.CurrentTokens
		all.StockTokens += row.StockTokens
	}
	all.Reduction = metricReduction(all.CurrentTokens, all.StockTokens)
	return rows, all
}

func boolCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func toolMetricLabel(pluginID, toolName string, failed bool) string {
	label := pluginID + "/" + toolName
	if failed {
		return label + " failed"
	}
	return label
}

func encodeToolMetrics(encoded []byte, value metrics) {
	binary.LittleEndian.PutUint16(encoded[metricsToolCountOffset:metricsToolCountOffset+2], value.ToolCount)
	binary.LittleEndian.PutUint64(encoded[metricsSharedOffset:metricsSharedOffset+8], uint64(value.SharedDefinitionInputTokens))
	for index, entry := range value.Tools[:value.ToolCount] {
		base := metricsToolOffset + index*metricsToolEntrySize
		binary.LittleEndian.PutUint16(encoded[base:base+2], uint16(len(entry.PluginID)))
		binary.LittleEndian.PutUint16(encoded[base+2:base+4], uint16(len(entry.ToolName)))
		copy(encoded[base+4:base+4+maxMetricPluginIDBytes], entry.PluginID)
		copy(encoded[base+4+maxMetricPluginIDBytes:base+4+maxMetricPluginIDBytes+maxMetricToolNameBytes], entry.ToolName)
		for offset, count := range []uint64{
			entry.DefinitionInputTokens, entry.Calls, entry.EmittedTokens, entry.TranslatedTokens,
			entry.FailedTranslations, entry.FailedEmittedTokens, entry.FailedTranslatedTokens,
			entry.Executions, entry.CurrentInputTokens, entry.StockInputTokens,
		} {
			position := base + 200 + offset*8
			binary.LittleEndian.PutUint64(encoded[position:position+8], count)
		}
	}
}

func decodeToolMetrics(encoded []byte, value *metrics) bool {
	count := binary.LittleEndian.Uint16(encoded[metricsToolCountOffset : metricsToolCountOffset+2])
	if count > maxMetricTools {
		return false
	}
	value.ToolCount = count
	value.SharedDefinitionInputTokens = int64(binary.LittleEndian.Uint64(encoded[metricsSharedOffset : metricsSharedOffset+8]))
	for index := range int(count) {
		base := metricsToolOffset + index*metricsToolEntrySize
		pluginLength := int(binary.LittleEndian.Uint16(encoded[base : base+2]))
		nameLength := int(binary.LittleEndian.Uint16(encoded[base+2 : base+4]))
		if pluginLength < 1 || pluginLength > maxMetricPluginIDBytes || nameLength < 1 || nameLength > maxMetricToolNameBytes {
			return false
		}
		entry := toolMetric{
			PluginID: string(encoded[base+4 : base+4+pluginLength]),
			ToolName: string(encoded[base+4+maxMetricPluginIDBytes : base+4+maxMetricPluginIDBytes+nameLength]),
		}
		counts := []*uint64{
			&entry.DefinitionInputTokens, &entry.Calls, &entry.EmittedTokens, &entry.TranslatedTokens,
			&entry.FailedTranslations, &entry.FailedEmittedTokens, &entry.FailedTranslatedTokens,
			&entry.Executions, &entry.CurrentInputTokens, &entry.StockInputTokens,
		}
		for offset, destination := range counts {
			position := base + 200 + offset*8
			*destination = binary.LittleEndian.Uint64(encoded[position : position+8])
		}
		value.Tools[index] = entry
	}
	return true
}
