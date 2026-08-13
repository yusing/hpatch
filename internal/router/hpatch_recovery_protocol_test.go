package router

import (
	"strings"
	"testing"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

func TestHPatchRecoveryDescriptionIsNonInstructional(t *testing.T) {
	const want = "Handle-local mutation of the latest rejected HPATCH/2 script. Invalid recovery leaves the retained script and workspace unchanged."
	if hpatchRecoveryDescription != want {
		t.Fatalf("hpatchRecoveryDescription = %q, want %q", hpatchRecoveryDescription, want)
	}
}

func TestRecoveryCommandsHashCompleteFramesAndPhysicalValueRows(t *testing.T) {
	script := "in file.go\n" +
		"type 1:ffff <<PATCH\n" +
		"first\n" +
		"second\n" +
		"PATCH\n"
	commands := recoveryCommands(script)
	if len(commands) != 2 {
		t.Fatalf("commands = %+v", commands)
	}
	if want := "C2:" + recoveryHash("type 1:ffff <<PATCH\nfirst\nsecond\nPATCH\n"); commands[1].handle != want {
		t.Fatalf("command handle = %q, want %q", commands[1].handle, want)
	}
	if len(commands[1].valueRows) != 2 ||
		commands[1].valueRows[1].handle != "V4:"+recoveryHash("second") {
		t.Fatalf("value rows = %+v", commands[1].valueRows)
	}
}

func TestRecoverScriptComposesFieldsAndValueRows(t *testing.T) {
	script := "in file.go\n" +
		"type- 1:aaaa <<PATCH\n" +
		"first\n" +
		"second\n" +
		"PATCH\n"
	command := recoveryCommands(script)[1]
	payload := strings.Join([]string{
		command.handle + " target 2:bbbb..3:cccc",
		command.handle + " operation type",
		command.handle + " " + command.valueRows[1].handle + ` value "SECOND"`,
	}, "\n")
	got, err := recoverScript(t.Context(), script, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" +
		"type 2:bbbb..3:cccc <<PATCH\n" +
		"first\n" +
		"SECOND\n" +
		"PATCH\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptParsesAndRetargetsUnanchoredLiteralMutation(t *testing.T) {
	script := "in file.go\n" + `type "old text" "new text"` + "\n"
	command := recoveryCommands(script)[1]
	if !command.parts.parsed || command.parts.target != `"old text"` || command.parts.value != "new text" {
		t.Fatalf("command parts = %+v", command.parts)
	}
	got, err := recoverScript(t.Context(), script, command.handle+` target "current text" 2`)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" + `type "current text" 2 "new text"` + "\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptRetargetsMultilineLiteralMutation(t *testing.T) {
	script := "in file.go\n" + `type 2:bbbb "old\ntext" "new text"` + "\n"
	command := recoveryCommands(script)[1]
	if !command.parts.parsed || command.parts.target != `2:bbbb "old\ntext"` || command.parts.value != "new text" {
		t.Fatalf("command parts = %+v", command.parts)
	}
	got, err := recoverScript(t.Context(), script, command.handle+` target "current\u000Atext"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" + `type "current\u000Atext" "new text"` + "\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptPreservesMultilineTargetSpellingWhenChangingValue(t *testing.T) {
	script := "in file.go\n" + `type "old\u000Atext" "new text"` + "\n"
	command := recoveryCommands(script)[1]
	got, err := recoverScript(t.Context(), script, command.handle+` value "newer text"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" + `type "old\u000Atext" "newer text"` + "\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoveryMultilineLiteralTargetsMirrorPublicControls(t *testing.T) {
	for _, target := range []string{`"line\ntext"`, `"line\u000Atext"`, `1:aaaa "line\ntext"`, `"tab\ttext"`} {
		if !recoveryTarget(target) {
			t.Errorf("recoveryTarget(%q) = false", target)
		}
	}
	for _, target := range []string{`""`, "\"raw\nnewline\"", `"return\r"`, `"return\u000D"`, `"control\u0001"`} {
		if recoveryTarget(target) {
			t.Errorf("recoveryTarget(%q) = true", target)
		}
	}
	for _, command := range []string{
		`type 1:aaaa "return\rtext" "value"`,
		`type 1:aaaa "control\u0001text" "value"`,
	} {
		if parts := recoveryCommandPartsOf(command, hpatchsyntax.CommandFrame{}); parts.parsed {
			t.Errorf("recoveryCommandPartsOf(%q) = %+v, want unparsed", command, parts)
		}
	}
}

func TestRecoverScriptDetailedReportsExactCompactDelta(t *testing.T) {
	script := "in file.go\n" +
		"type- 1:aaaa <<PATCH\n" +
		"first\n" +
		"second\n" +
		"PATCH\n"
	command := recoveryCommands(script)[1]
	payload := strings.Join([]string{
		command.handle + " target 2:bbbb..3:cccc",
		command.handle + " operation type",
		command.handle + " " + command.valueRows[1].handle + ` value "SECOND"`,
	}, "\n")
	recovered, err := recoverScriptDetailed(t.Context(), script, payload)
	if err != nil {
		t.Fatal(err)
	}
	wantDelta := strings.Join([]string{
		command.handle + " target: 1:aaaa -> 2:bbbb..3:cccc",
		command.handle + " operation: type- -> type",
		command.handle + " value: value row 2 replaced",
	}, "\n")
	if recovered.delta != wantDelta {
		t.Fatalf("recovery delta = %q, want %q", recovered.delta, wantDelta)
	}
}

func TestRecoverScriptResolvesValueRowsAgainstImmutableValue(t *testing.T) {
	script := "in file.go\n" +
		"type 1:aaaa <<PATCH\n" +
		"first\n" +
		"second\n" +
		"PATCH\n"
	command := recoveryCommands(script)[1]
	payload := strings.Join([]string{
		command.handle + " " + command.valueRows[0].handle + ` value- "before\n"`,
		command.handle + " " + command.valueRows[1].handle + ` value "SECOND"`,
	}, "\n")
	got, err := recoverScript(t.Context(), script, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" +
		"type 1:aaaa <<PATCH\n" +
		"before\n" +
		"first\n" +
		"SECOND\n" +
		"PATCH\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}

	conflicting := strings.Join([]string{
		command.handle + " " + command.valueRows[0].handle + ` value "FIRST"`,
		command.handle + " " + command.valueRows[0].handle + ` value "OTHER"`,
	}, "\n")
	if got, err := recoverScript(t.Context(), script, conflicting); err == nil || got != "" {
		t.Fatalf("conflicting value-row replacements = %q, %v; want atomic rejection", got, err)
	}
}

func TestRecoverScriptValueRowAppendKeepsHeredocDelimiterSeparate(t *testing.T) {
	script := "in file.go\n" +
		"type 1:aaaa <<PATCH\n" +
		"return value\n" +
		"PATCH\n"
	command := recoveryCommands(script)[1]
	payload := command.handle + " " + command.valueRows[0].handle + ` value+ "}\n"`

	got, err := recoverScript(t.Context(), script, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "in file.go\n" +
		"type 1:aaaa <<PATCH\n" +
		"return value\n" +
		"}\n" +
		"PATCH\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptStructuralOperations(t *testing.T) {
	script := "in one.go\n" +
		"type 1:aaaa \"one\"\n" +
		"in two.go\n" +
		"type 2:bbbb \"two\"\n"
	commands := recoveryCommands(script)
	payload := strings.Join([]string{
		commands[0].handle + " after in before.go",
		commands[1].handle + ` replace type 3:cccc "ONE"`,
		commands[2].handle + " drop",
		commands[3].handle + ` before type- 4:dddd "// before\n"`,
	}, "\n")
	got, err := recoverScript(t.Context(), script, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := "in one.go\n" +
		"in before.go\n" +
		"type 3:cccc \"ONE\"\n" +
		"type- 4:dddd \"// before\\n\"\n" +
		"type 2:bbbb \"two\"\n"
	if got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptDropsMalformedFrameEndingAtTerminalEmptyRow(t *testing.T) {
	script := "in file.go\n" +
		"type 1:aaaa <<PATCH\n" +
		"unterminated\n"
	commands := recoveryCommands(script)
	if len(commands) != 2 {
		t.Fatalf("commands = %+v", commands)
	}

	got, err := recoverScript(t.Context(), script, commands[1].handle+" drop")
	if err != nil {
		t.Fatal(err)
	}
	if want := "in file.go\n"; got != want {
		t.Fatalf("recoverScript() = %q, want %q", got, want)
	}
}

func TestRecoverScriptRejectsStaleConflictingAndExcludedPayloads(t *testing.T) {
	script := "in file.go\n" +
		"type 1:aaaa \"bad\"\n"
	handle := recoveryCommands(script)[1].handle
	for _, payload := range []string{
		"C2:ffff drop",
		handle + " drop trailing",
		handle + " target 2:bbbb\n" + handle + ` replace type 2:bbbb "value"`,
		handle + " value <<PATCH\nunterminated\n",
		handle + " accept",
		"",
	} {
		if got, err := recoverScript(t.Context(), script, payload); err == nil || got != "" {
			t.Fatalf("recoverScript(%q) = %q, %v; want atomic rejection", payload, got, err)
		}
	}
}

func TestRecoveryGrammarAllowsTrailingBlankLinesAndOmitsAccept(t *testing.T) {
	for _, want := range []string{
		`start: _blank_line* recovery (_separator recovery)* _blank_line*`,
		`HANDLE SP "drop"`,
		`HANDLE SP VHANDLE SP VALUE_OP SP value`,
		`STRUCTURAL_OP: "replace" | "before" | "after"`,
	} {
		if !strings.Contains(hpatchRecoveryGrammar, want) {
			t.Fatalf("recovery grammar does not contain %q", want)
		}
	}
	if strings.Contains(hpatchRecoveryGrammar, "accept") || strings.Contains(hpatchRecoveryGrammar, `"recover"`) {
		t.Fatal("recovery grammar contains an excluded operation or sentinel")
	}
}

func TestRecoveryGrammarMirrorsPublicMultilineTargetTerminal(t *testing.T) {
	public := grammarTerminalLine(t, hpatch.ToolGrammar(), "TARGET_QUOTED")
	recovery := grammarTerminalLine(t, hpatchRecoveryGrammar, "TARGET_QUOTED")
	if recovery != public {
		t.Fatalf("recovery TARGET_QUOTED = %q, public = %q", recovery, public)
	}
}

func grammarTerminalLine(t *testing.T, grammar, name string) string {
	t.Helper()
	prefix := name + ": "
	for line := range strings.SplitSeq(grammar, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("%s terminal not found", name)
	return ""
}
