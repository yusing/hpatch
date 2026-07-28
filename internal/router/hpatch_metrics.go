package router

import (
	"fmt"
	"hpatch"
	"strconv"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

const failedApplyPatch = "*** Begin Patch\n*** End Patch\n"

type hpatchMetricRecord = hpatch.HostMetricRecord

type hpatchMetricInputs struct {
	invocation         hpatch.InvocationMetrics
	emittedScript      string
	report             string
	patch              string
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

func applyPatchMetricPayload(patch string) string {
	return "functions.exec\nconst result = await tools.apply_patch(" + strconv.Quote(patch) + ");\ntext(result);"
}

func calculateHPatchMetricRecord(inputs hpatchMetricInputs) (hpatchMetricRecord, error) {
	codec, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		return hpatchMetricRecord{}, fmt.Errorf("load GPT-5 tokenizer: %w", err)
	}
	record := hpatchMetricRecord{Invocation: inputs.invocation}
	if !inputs.overheadOnly {
		if inputs.successful {
			if record.HPatchTokens, err = countHPatchMetricText(codec, hpatchMetricPayload(inputs.emittedScript), "hpatch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.ApplyPatchTokens, err = countHPatchMetricText(codec, applyPatchMetricPayload(inputs.patch), "apply_patch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.ReportInputTokens, err = countHPatchMetricText(codec, inputs.report, "state report input"); err != nil {
				return hpatchMetricRecord{}, err
			}
		} else {
			if record.IneffectiveHPatchTokens, err = countHPatchMetricText(codec, hpatchMetricPayload(inputs.emittedScript), "ineffective hpatch output"); err != nil {
				return hpatchMetricRecord{}, err
			}
			if record.FailedApplyPatchTokens, err = countHPatchMetricText(codec, applyPatchMetricPayload(failedApplyPatch), "failed apply_patch output"); err != nil {
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
