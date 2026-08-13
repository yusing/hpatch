package codexinstructions

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	stockRGInstruction   = "- When you search for text or files, you reach first for `rg` or `rg --files`; they are much faster than alternatives like `grep`. If `rg` is unavailable, you use the next best tool without fuss."
	stockExecInstruction = "- Exercise caution when escaping text for exec_command calls - backticks and `$()` passed to the `cmd` argument will still execute. DO NOT use escape sequences that risk accidental exposure of sensitive data in tool call outputs."
	stockEditSection     = "## File editing constraints\n\nUse `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough.\n"
)

func TestRendererPreservesCustomizedStockLinesOutsideMarkedSection(t *testing.T) {
	input := "custom prefix\n" + stockRGInstruction + "\n" + Instructions() +
		stockExecInstruction + "\ncustom suffix\n"
	inputPath := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	got := runCommand(t, nil, "sh", "render-model-instructions.sh", inputPath)
	if got != input {
		t.Fatalf("rendered instructions changed customized text\n got: %q\nwant: %q", got, input)
	}
}

func TestInstallerReadsQuotedKeyAndLiteralString(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "custom instructions.md")
	if err := os.WriteFile(target, []byte("prefix\n"+Instructions()+"suffix\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tempDir, "config.toml")
	config := "\"model_instructions_file\" = '" + target + "' # preserved\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	runInstaller(t, configPath)

	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConfig) != config {
		t.Fatalf("config changed\n got: %q\nwant: %q", gotConfig, config)
	}
	gotTarget, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gotTarget), "prefix\n") || !strings.HasSuffix(string(gotTarget), "suffix\n") {
		t.Fatalf("customized text was not preserved: %q", gotTarget)
	}
}

func TestInstallerPreservesConfiguredSymlink(t *testing.T) {
	tempDir := t.TempDir()
	referent := filepath.Join(tempDir, "managed.md")
	if err := os.WriteFile(referent, []byte(Instructions()), 0o640); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tempDir, "configured.md")
	if err := os.Symlink(referent, target); err != nil {
		t.Fatal(err)
	}
	encodedTarget, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model_instructions_file = "+string(encodedTarget)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runInstaller(t, configPath)

	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configured path is no longer a symlink: mode %v", info.Mode())
	}
}

func TestMakeInstallInstructionsPreservesQuotedPaths(t *testing.T) {
	tempDir := t.TempDir()
	configDirectory := filepath.Join(tempDir, "codex \"quoted\"")
	fakeBin := filepath.Join(tempDir, "bin \"quoted\"")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.toml")
	target := filepath.Join(configDirectory, "hpatch-model-instructions.md")
	fakeCodex := filepath.Join(fakeBin, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nprintf '%s' \"$FAKE_MODELS_JSON\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := "before\n" + stockEditSection + stockRGInstruction + "\n" + stockExecInstruction + "\nafter\n"
	models, err := json.Marshal(map[string]any{
		"models": []map[string]any{{
			"slug":              "test-model",
			"priority":          1,
			"base_instructions": base,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "install-instructions",
		"CODEX_CONFIG_FILE="+configPath,
		"CODEX_MODEL=test-model",
	)
	command.Dir = root
	command.Env = append(os.Environ(),
		"FAKE_MODELS_JSON="+string(models),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("make install-instructions: %v\n%s", err, output)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), Instructions()) {
		t.Fatalf("installed instructions omit central source: %q", got)
	}
	requireShellWorkflow(t, string(got))
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	encodedTarget, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "model_instructions_file = "+string(encodedTarget)) {
		t.Fatalf("config does not contain exact target path: %q", config)
	}
}

func TestInstallerPatchesAgentModelInstructionFiles(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	mainTarget := filepath.Join(tempDir, "main.instructions.md")
	if err := os.WriteFile(mainTarget, []byte("main prefix\n"+Instructions()+"main suffix\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	encodedMainTarget, err := json.Marshal(mainTarget)
	if err != nil {
		t.Fatal(err)
	}
	mainConfig := "model_instructions_file = " + string(encodedMainTarget) + "\n"
	if err := os.WriteFile(configPath, []byte(mainConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	agentsDirectory := filepath.Join(tempDir, "agents")
	if err := os.MkdirAll(agentsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	agentTarget := filepath.Join(agentsDirectory, "fast.instructions.md")
	if err := os.WriteFile(agentTarget, []byte("agent custom instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agentConfigPath := filepath.Join(agentsDirectory, "fast.toml")
	agentConfig := "name = \"fast\"\nmodel_instructions_file = './fast.instructions.md'\n"
	if err := os.WriteFile(agentConfigPath, []byte(agentConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDirectory, "without-override.toml"), []byte("name = \"plain\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runInstaller(t, configPath)

	for path, want := range map[string]string{
		configPath:      mainConfig,
		agentConfigPath: agentConfig,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s changed\n got: %q\nwant: %q", path, got, want)
		}
	}
	gotMain, err := os.ReadFile(mainTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gotMain), "main prefix\n") || !strings.HasSuffix(string(gotMain), "main suffix\n") {
		t.Fatalf("%s customized text was not preserved: %q", mainTarget, gotMain)
	}
	requireShellWorkflow(t, string(gotMain))
	gotAgent, err := os.ReadFile(agentTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gotAgent), "agent custom instructions\n\n") ||
		!strings.HasSuffix(string(gotAgent), Instructions()) {
		t.Fatalf("%s custom instructions or appended hpatch section were not preserved: %q", agentTarget, gotAgent)
	}
	requireShellWorkflow(t, string(gotAgent))
}

func TestMakeUninstallRemovesDefaultInstructionsAndConfigEntry(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	target := filepath.Join(tempDir, "hpatch-model-instructions.md")
	encodedTarget, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	config := "custom = true\nmodel_instructions_file = " + string(encodedTarget) + "\n[features]\nresponses = true\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(Instructions()), 0o600); err != nil {
		t.Fatal(err)
	}

	runUninstaller(t, configPath)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("default instructions still exist or cannot be inspected: %v", err)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := "custom = true\n[features]\nresponses = true\n"
	if string(gotConfig) != wantConfig {
		t.Fatalf("config after uninstall\n got: %q\nwant: %q", gotConfig, wantConfig)
	}
}

func TestMakeUninstallStripsCustomizedMainAndAgentInstructions(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	mainTarget := filepath.Join(tempDir, "main.instructions.md")
	encodedMainTarget, err := json.Marshal(mainTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("model_instructions_file = "+string(encodedMainTarget)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainTarget, []byte("main prefix\n"+Instructions()+"main suffix\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	agentsDirectory := filepath.Join(tempDir, "agents")
	if err := os.MkdirAll(agentsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	agentTarget := filepath.Join(agentsDirectory, "fast.instructions.md")
	if err := os.WriteFile(agentTarget, []byte("agent prefix\n"+Instructions()+"agent suffix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDirectory, "fast.toml"), []byte("model_instructions_file = './fast.instructions.md'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runUninstaller(t, configPath)

	for path, want := range map[string]string{
		mainTarget:  "main prefix\nmain suffix\n",
		agentTarget: "agent prefix\nagent suffix\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s after uninstall\n got: %q\nwant: %q", path, got, want)
		}
	}
}

func TestMakeUninstallRejectsIncompleteMarkers(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	target := filepath.Join(tempDir, "custom.instructions.md")
	encodedTarget, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("model_instructions_file = "+string(encodedTarget)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contents := "prefix\n<!-- hpatch-model-instructions:start -->\nincomplete\n"
	if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runUninstallerCommand(t, configPath).CombinedOutput()
	if err == nil {
		t.Fatalf("make uninstall-instructions succeeded for incomplete markers\n%s", output)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != contents {
		t.Fatalf("instructions changed after rejected uninstall\n got: %q\nwant: %q", got, contents)
	}
}

func requireShellWorkflow(t *testing.T, instructions string) {
	t.Helper()
	for _, required := range []string{
		"The default interpreter is Bash.",
		"accepts exactly one `{.}` placeholder",
		"`hread @shell/<reference>`",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("installed instructions omit shell workflow %q", required)
		}
	}
}

func runInstaller(t *testing.T, configPath string) {
	t.Helper()
	runCommand(t, []string{"CODEX_CONFIG_FILE=" + configPath}, "sh", "install-model-instructions.sh")
}

func runUninstaller(t *testing.T, configPath string) {
	t.Helper()
	command := runUninstallerCommand(t, configPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("make uninstall-instructions: %v\n%s", err, output)
	}
}

func runUninstallerCommand(t *testing.T, configPath string) *exec.Cmd {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "uninstall-instructions", "CODEX_CONFIG_FILE="+configPath)
	command.Dir = root
	return command
}

func runCommand(t *testing.T, environment []string, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return string(output)
}
