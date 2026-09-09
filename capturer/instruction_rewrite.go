package capturer

import "context"

// InstructionRewrite records router decisions, never prompt text or file paths.
// Missing evidence means unobserved, including captures from older versions.
type InstructionRewrite struct {
	Carrier          string `json:"carrier"`
	Strategy         string `json:"strategy"`
	Workflow         string `json:"workflow"`
	CustomConfigured bool   `json:"custom_configured"`
}

// ObserveInstructionRewrite is auxiliary. Only fixed categories may enter capture.
func ObserveInstructionRewrite(ctx context.Context, evidence InstructionRewrite) {
	state, ok := ctx.Value(captureKey{}).(*requestState)
	if !ok {
		return
	}
	switch evidence.Carrier {
	case "instructions", "developer", "none":
	default:
		return
	}
	switch evidence.Strategy {
	case "marked", "stock-gpt5", "stock-astra", "custom-append", "unchanged", "rejected":
	default:
		return
	}
	switch evidence.Workflow {
	case "astra", "default":
	default:
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.instructionRewrite = &evidence
}
