package hpatch

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	hpatchToolName     = "functions.exec"
	applyPatchToolName = "apply_patch"
)

func metricPayloads(workingDirectory, script, patch string) (string, string) {
	hpatchInput := hpatchToolInput(workingDirectory, script)
	return hpatchToolName + "\n" + hpatchInput, applyPatchToolName + "\n" + patch
}

func hpatchToolInput(workingDirectory, script string) string {
	format := strings.Join([]string{
		"const translated = await tools.exec_command({",
		"  cmd: \"hpatch translate\",",
		"  stdin: %s,",
		"  workdir: %s,",
		"  yield_time_ms: 10000,",
		"  max_output_tokens: 10000",
		"});",
		"if (translated.exit_code !== 0) {",
		"  text(`hpatch translate failed: ${translated.output}`);",
		"  exit();",
		"}",
		"const applied = await tools.apply_patch(translated.output);",
		"text(JSON.stringify({",
		"  translationExitCode: translated.exit_code,",
		"  applyPatchResult: applied",
		"}));",
	}, "\n")
	return fmt.Sprintf(format, strconv.Quote(script), strconv.Quote(workingDirectory))
}
