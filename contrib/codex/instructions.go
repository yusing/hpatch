package codexinstructions

import (
	_ "embed"
	"strings"
	"text/template"
)

const HPatchToolDescription = "Atomic HPATCH/2 edit-script application. Rejection or cancellation leaves the workspace unchanged."

//go:embed file-editing-instructions.md
var instructions string

//go:embed hpatch-recovery.tmpl
var recoverySource string

var recoveryTemplate = template.Must(template.New("hpatch-recovery").Parse(recoverySource))

// Instructions returns the authoritative persistent Codex model instructions.
func Instructions() string {
	return instructions
}

// NativeInstructions returns the central guidance without the CTP section. Deriving it from the
// active source keeps every non-CTP workflow byte-identical across the two model protocols.
func NativeInstructions() string {
	const (
		ctpHeading         = "## CTP/1 transport\n"
		fileEditingHeading = "## File editing\n"
	)
	start := strings.Index(instructions, ctpHeading)
	if start < 0 {
		panic("central model instructions omit the CTP heading")
	}
	remainder := instructions[start:]
	end := strings.Index(remainder, fileEditingHeading)
	if end < 0 {
		panic("central model instructions omit the file-editing heading after CTP")
	}
	return instructions[:start] + remainder[end:]
}

// RecoveryGuidance renders dynamic rejected-script guidance.
func RecoveryGuidance(references string) string {
	var rendered strings.Builder
	if err := recoveryTemplate.Execute(&rendered, struct{ References string }{references}); err != nil {
		panic(err)
	}
	return rendered.String()
}
