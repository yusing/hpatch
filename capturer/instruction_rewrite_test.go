package capturer

import (
	"context"
	"testing"
)

func TestInstructionRewriteEvidenceAllowlist(t *testing.T) {
	valid := InstructionRewrite{Carrier: "developer", Strategy: "stock-astra", Workflow: "default", CustomConfigured: true}
	for _, field := range []string{"carrier", "strategy", "workflow"} {
		t.Run(field, func(t *testing.T) {
			state := &requestState{}
			ctx := context.WithValue(t.Context(), captureKey{}, state)
			evidence := valid
			switch field {
			case "carrier":
				evidence.Carrier = "private prompt"
			case "strategy":
				evidence.Strategy = "/private/config/path"
			case "workflow":
				evidence.Workflow = "private content"
			}
			ObserveInstructionRewrite(ctx, evidence)
			if state.instructionRewrite != nil {
				t.Fatal("unsafe evidence retained")
			}
			ObserveInstructionRewrite(ctx, valid)
			if state.instructionRewrite == nil || *state.instructionRewrite != valid {
				t.Fatal("safe evidence lost")
			}
		})
	}
	ObserveInstructionRewrite(t.Context(), valid) // No recorder is a no-op.
}
