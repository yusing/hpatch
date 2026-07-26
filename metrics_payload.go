package hpatch

import "strconv"

const (
	hpatchToolName     = "functions.hpatch"
	applyPatchToolName = "functions.exec"
)

func metricPayloads(script, patch string) (string, string) {
	return hpatchMetricPayload(script), applyPatchMetricPayload(patch)
}

func hpatchMetricPayload(script string) string {
	return hpatchToolName + "\n" + script
}

func applyPatchMetricPayload(patch string) string {
	return applyPatchToolName + "\nconst result = await tools.apply_patch(" + strconv.Quote(patch) + ");\ntext(result);"
}
