package router

import (
	"strconv"

	"github.com/yusing/hpatch"
)

const failedApplyPatch = "*** Begin Patch\n*** End Patch\n"

type hpatchMetricRecord struct {
	hpatch.HostMetricRecord

	correctionScope    string
	valueRowOperations uint64
	baseValueRows      uint64
	baseCommandTokens  uint64
}

type hpatchMetricInputs struct {
	invocation    hpatch.InvocationMetrics
	rejections    []hpatch.HostRejection
	attempt       hpatch.AttemptMetadata
	emittedScript string
	report        string
	patch         string
	diagnostic    string
	sessionID     string
	correction    hpatchCorrectionStats

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
		call = &hpatch.HostToolCall{
			PluginID:          "builtin.hpatch",
			ToolName:          hpatchToolName,
			EmittedName:       "functions.hpatch",
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
		AuxiliaryTexts:                inputs.correction.baseCommands,
	})
	if err != nil {
		return hpatchMetricRecord{}, err
	}
	return hpatchMetricRecord{
		HostMetricRecord:   classified,
		correctionScope:    inputs.correction.scope,
		valueRowOperations: inputs.correction.valueRowOperations,
		baseValueRows:      inputs.correction.baseValueRows,
		baseCommandTokens:  classified.AuxiliaryTokens,
	}, nil
}
