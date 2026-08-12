package router

import (
	"strconv"

	"github.com/yusing/hpatch"
)

const failedApplyPatch = "*** Begin Patch\n*** End Patch\n"

type hpatchMetricRecord struct {
	hpatch.HostMetricRecord
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
		call = &hpatch.HostToolCall{
			PluginID:          "builtin.hpatch",
			ToolName:          toolName,
			EmittedName:       "functions." + toolName,
			EmittedInput:      inputs.emittedScript,
			TranslatedName:    "functions.exec",
			TranslatedPayload: applyPatchMetricProgram(patch),
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
	return hpatchMetricRecord{HostMetricRecord: classified}, nil
}
