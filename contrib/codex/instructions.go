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

// Instructions returns the authoritative Codex file-editing instructions.
func Instructions() string {
	return instructions
}

// RecoveryGuidance renders dynamic rejected-script guidance.
func RecoveryGuidance(references string) string {
	var rendered strings.Builder
	if err := recoveryTemplate.Execute(&rendered, struct{ References string }{references}); err != nil {
		panic(err)
	}
	return rendered.String()
}
