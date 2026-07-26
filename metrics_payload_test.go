package hpatch

import (
	"strconv"
	"strings"
	"testing"
)

func TestMetricPayloadsRepresentCompleteToolCalls(t *testing.T) {
	script := "new note.txt\ntype \"hello\"\n"
	patch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"

	hpatchPayload, applyPatchPayload := metricPayloads(script, patch)

	if want := hpatchToolName + "\n" + script; hpatchPayload != want {
		t.Fatalf("hpatch payload = %q, want %q", hpatchPayload, want)
	}
	wantApplyPatch := applyPatchToolName +
		"\nconst result = await tools.apply_patch(" + strconv.Quote(patch) + ");\ntext(result);"
	if applyPatchPayload != wantApplyPatch {
		t.Fatalf("apply_patch payload = %q, want %q", applyPatchPayload, wantApplyPatch)
	}
}

func TestMetricPayloadsSerializeUntrustedPatchAsApplyPatchInput(t *testing.T) {
	script := "new odd'file.txt\ntype \"tools.apply_patch(fake) \\u0024{future}\"\n"
	patch := "*** Begin Patch\n*** Add File: odd'file.txt\n+`); text(\"collision\")\n*** End Patch\n"

	hpatchPayload, applyPatchPayload := metricPayloads(script, patch)

	if hpatchPayload != "functions.hpatch\n"+script {
		t.Fatalf("hpatch payload changed the free-form script: %q", hpatchPayload)
	}
	if !strings.Contains(applyPatchPayload, "tools.apply_patch("+strconv.Quote(patch)+")") {
		t.Fatalf("apply_patch payload does not contain serialized patch input: %q", applyPatchPayload)
	}
	if strings.Count(applyPatchPayload, "tools.apply_patch(") != 1 {
		t.Fatalf("patch collided with orchestration program: %q", applyPatchPayload)
	}
}
