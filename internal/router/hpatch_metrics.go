package router

import (
	"strconv"

	"github.com/yusing/hpatch"
)

const failedApplyPatch = "*** Begin Patch\n*** End Patch\n"

type hpatchMetricRecord struct {
	hpatch.HostMetricRecord
	attemptHPatchTokens     uint64
	attemptApplyPatchTokens uint64
}

type hpatchChainSettlement struct {
	compensation hpatch.HostMetricCompensation
	hpatchTokens uint64
	toolMetrics  [2]hpatch.ToolMetricRecord
	toolCount    uint8
}

type hpatchMetricInputs struct {
	invocation    hpatch.InvocationMetrics
	rejections    []hpatch.HostRejection
	attempt       hpatch.AttemptMetadata
	emittedScript string
	emittedTool   string
	report        string
	patch         string
	diagnostic    string
	misuseWarning string
	sessionID     string

	definition             string
	baselineDefinition     string
	execCommandDefinitions []string
	definitions            []hpatch.HostToolDefinition
	toolCall               *hpatch.HostToolCall
	successful             bool
	overheadOnly           bool
	priorAttempts          []hpatchHistory
	priorSettlement        hpatchChainSettlement
}

func applyPatchMetricProgram(patch string) string {
	return "const result = await tools.apply_patch(" + strconv.Quote(patch) + ");\ntext(result);"
}

func calculateHPatchMetricRecord(inputs hpatchMetricInputs) (hpatchMetricRecord, error) {
	call := inputs.toolCall
	if !inputs.overheadOnly && call == nil {
		patch := inputs.patch
		if !inputs.successful {
			patch = failedApplyPatch
		}
		toolName := inputs.emittedTool
		if toolName == "" {
			toolName = hpatchToolName
		}
		translatedName := "functions.exec"
		translatedPayload := applyPatchMetricProgram(patch)
		if !inputs.successful && toolName == hpatchRecoveryToolName {
			translatedName = ""
			translatedPayload = ""
		}
		call = &hpatch.HostToolCall{
			PluginID:          "builtin.hpatch",
			ToolName:          toolName,
			EmittedName:       "functions." + toolName,
			EmittedInput:      inputs.emittedScript,
			TranslatedName:    translatedName,
			TranslatedPayload: translatedPayload,
			FailedTranslation: !inputs.successful,
		}
	}
	classified, err := hpatch.ClassifyHostMetrics(hpatch.HostMetricInput{
		Invocation:                    inputs.invocation,
		Rejections:                    inputs.rejections,
		Attempt:                       inputs.attempt,
		SessionID:                     inputs.sessionID,
		InstalledDefinition:           inputs.definition,
		ToolDefinitions:               inputs.definitions,
		RemovedDefinition:             inputs.baselineDefinition,
		RemovedExecCommandDefinitions: inputs.execCommandDefinitions,
		ToolCall:                      call,
		StateReport:                   inputs.report,
		Diagnostic:                    inputs.diagnostic,
		MisuseWarning:                 inputs.misuseWarning,
	})
	if err != nil {
		return hpatchMetricRecord{}, err
	}
	if inputs.emittedTool == hpatchRecoveryToolName && call != nil {
		for _, metric := range classified.ToolMetrics {
			if metric.PluginID != call.PluginID || metric.ToolName != call.ToolName {
				continue
			}
			if call.FailedTranslation {
				classified.IneffectiveHPatchTokens = metric.FailedEmittedTokens
				classified.FailedApplyPatchTokens = metric.FailedTranslatedTokens
			} else {
				classified.HPatchTokens = metric.EmittedTokens
				classified.ApplyPatchTokens = metric.TranslatedTokens
			}
			break
		}
	}
	attemptHPatchTokens := classified.HPatchTokens
	if attemptHPatchTokens == 0 {
		attemptHPatchTokens = classified.IneffectiveHPatchTokens
	}
	attemptApplyPatchTokens := classified.ApplyPatchTokens
	if attemptApplyPatchTokens == 0 {
		attemptApplyPatchTokens = classified.FailedApplyPatchTokens
	}
	if inputs.successful && inputs.emittedTool == hpatchRecoveryToolName {
		if err := settleRecoveredChain(&classified, inputs.priorSettlement, inputs.priorAttempts); err != nil {
			return hpatchMetricRecord{}, err
		}
	}
	return hpatchMetricRecord{
		HostMetricRecord:        classified,
		attemptHPatchTokens:     attemptHPatchTokens,
		attemptApplyPatchTokens: attemptApplyPatchTokens,
	}, nil
}

func settleRecoveredChain(record *hpatch.HostMetricRecord, settled hpatchChainSettlement, attempts []hpatchHistory) error {
	record.Compensation = settled.compensation
	record.HPatchTokens += settled.hpatchTokens
	record.ToolMetrics = append(record.ToolMetrics, settled.toolMetrics[:settled.toolCount]...)
	for _, attempt := range attempts {
		if attempt.toolName != hpatchToolName && attempt.toolName != hpatchRecoveryToolName {
			continue
		}
		prior, err := calculateHPatchMetricRecord(hpatchMetricInputs{
			attempt:       hpatch.AttemptMetadata{Correction: attempt.toolName == hpatchRecoveryToolName},
			emittedScript: attempt.script,
			emittedTool:   attempt.toolName,
		})
		if err != nil {
			return err
		}
		record.Compensation.IneffectiveHPatchTokens += prior.IneffectiveHPatchTokens
		record.Compensation.FailedApplyPatchTokens += prior.FailedApplyPatchTokens
		mergeCompensatedToolMetrics(&record.Compensation, prior.ToolMetrics)
		record.HPatchTokens += prior.IneffectiveHPatchTokens
		for _, metric := range prior.ToolMetrics {
			if metric.FailedTranslations == 0 {
				continue
			}
			record.ToolMetrics = append(record.ToolMetrics, hpatch.ToolMetricRecord{
				PluginID:      metric.PluginID,
				ToolName:      metric.ToolName,
				Calls:         metric.FailedTranslations,
				EmittedTokens: metric.FailedEmittedTokens,
			})
		}
	}
	return nil
}

func (settlement *hpatchChainSettlement) add(attempt hpatchHistory) error {
	if attempt.toolName != hpatchToolName && attempt.toolName != hpatchRecoveryToolName {
		return nil
	}
	prior, err := calculateHPatchMetricRecord(hpatchMetricInputs{
		attempt:       hpatch.AttemptMetadata{Correction: attempt.toolName == hpatchRecoveryToolName},
		emittedScript: attempt.script,
		emittedTool:   attempt.toolName,
	})
	if err != nil {
		return err
	}
	settlement.compensation.IneffectiveHPatchTokens += prior.IneffectiveHPatchTokens
	settlement.compensation.FailedApplyPatchTokens += prior.FailedApplyPatchTokens
	mergeCompensatedToolMetrics(&settlement.compensation, prior.ToolMetrics)
	settlement.hpatchTokens += prior.IneffectiveHPatchTokens
	for _, metric := range prior.ToolMetrics {
		if metric.FailedTranslations == 0 {
			continue
		}
		mergeSettledToolMetric(settlement, hpatch.ToolMetricRecord{
			PluginID: metric.PluginID, ToolName: metric.ToolName,
			Calls: metric.FailedTranslations, EmittedTokens: metric.FailedEmittedTokens,
		})
	}
	return nil
}

func mergeSettledToolMetric(settlement *hpatchChainSettlement, metric hpatch.ToolMetricRecord) {
	for index := range settlement.toolCount {
		current := &settlement.toolMetrics[index]
		if current.PluginID == metric.PluginID && current.ToolName == metric.ToolName {
			current.Calls += metric.Calls
			current.EmittedTokens += metric.EmittedTokens
			return
		}
	}
	if int(settlement.toolCount) == len(settlement.toolMetrics) {
		return
	}
	settlement.toolMetrics[settlement.toolCount] = metric
	settlement.toolCount++
}

func mergeCompensatedToolMetrics(compensation *hpatch.HostMetricCompensation, metrics []hpatch.ToolMetricRecord) {
	for _, metric := range metrics {
		merged := false
		for index := range compensation.ToolCount {
			current := &compensation.ToolMetrics[index]
			if current.PluginID != metric.PluginID || current.ToolName != metric.ToolName {
				continue
			}
			current.DefinitionInputTokens += metric.DefinitionInputTokens
			current.Calls += metric.Calls
			current.EmittedTokens += metric.EmittedTokens
			current.TranslatedTokens += metric.TranslatedTokens
			current.FailedTranslations += metric.FailedTranslations
			current.FailedEmittedTokens += metric.FailedEmittedTokens
			current.FailedTranslatedTokens += metric.FailedTranslatedTokens
			merged = true
			break
		}
		if merged || int(compensation.ToolCount) == len(compensation.ToolMetrics) {
			continue
		}
		compensation.ToolMetrics[compensation.ToolCount] = metric
		compensation.ToolCount++
	}
}
