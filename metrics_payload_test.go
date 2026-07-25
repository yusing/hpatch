package hpatch

import (
	"strconv"
	"strings"
	"testing"
)

func TestMetricPayloadsRepresentCompleteCanonicalCalls(t *testing.T) {
	workingDirectory := "/workspace/project"
	script := "new note.txt\ntype \"hello\"\n"
	patch := "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** End Patch\n"

	hpatchPayload, applyPatchPayload := metricPayloads(workingDirectory, script, patch)

	if !strings.HasPrefix(hpatchPayload, hpatchToolName+"\nconst translated = await tools.exec_command({") {
		t.Fatalf("hpatch payload has unexpected prefix: %q", hpatchPayload)
	}
	for _, required := range []string{
		strconv.Quote(workingDirectory),
		"cmd: \"hpatch translate\"",
		"stdin: " + strconv.Quote(script),
		"tools.apply_patch(translated.output)",
	} {
		if !strings.Contains(hpatchPayload, required) {
			t.Errorf("hpatch payload does not contain %q", required)
		}
	}
	for _, prohibited := range []string{"printf", " | hpatch translate", "python"} {
		if strings.Contains(strings.ToLower(hpatchPayload), prohibited) {
			t.Errorf("hpatch payload contains prohibited wrapper %q", prohibited)
		}
	}
	if strings.Contains(hpatchPayload, patch) {
		t.Error("hpatch payload counts the internally generated patch as model output")
	}
	if want := applyPatchToolName + "\n" + patch; applyPatchPayload != want {
		t.Fatalf("apply_patch payload = %q, want %q", applyPatchPayload, want)
	}
}

func TestMetricPayloadsSerializeUntrustedScriptOnlyAsStdin(t *testing.T) {
	script := "new odd'file.txt\ntype \"tools.apply_patch(translated.output) \\u0024{future}\"\n"
	hpatchPayload, applyPatchPayload := metricPayloads("/work dir", script, "future-schema-marker")

	if !strings.Contains(hpatchPayload, "stdin: "+strconv.Quote(script)) {
		t.Fatalf("hpatch payload does not contain serialized stdin: %q", hpatchPayload)
	}
	if strings.Count(hpatchPayload, "const translated = await tools.exec_command({") != 1 {
		t.Fatalf("script collided with orchestration wrapper: %q", hpatchPayload)
	}
	if strings.Count(hpatchPayload, "cmd: \"hpatch translate\"") != 1 {
		t.Fatalf("script changed the translated command: %q", hpatchPayload)
	}
	if applyPatchPayload != "apply_patch\nfuture-schema-marker" {
		t.Fatalf("unrelated apply_patch payload = %q", applyPatchPayload)
	}
}
