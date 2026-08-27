package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	hpatchInstructionsStartMarker = "<!-- hpatch-model-instructions:start -->"
	hpatchInstructionsEndMarker   = "<!-- hpatch-model-instructions:end -->"

	// Exact stock fragments make upstream prompt changes fail closed instead of
	// leaving conflicting editing guidance in the forwarded request.
	stockEditHeading     = "## File editing constraints"
	stockEditInstruction = "Use `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough."
	stockRGInstruction   = "- When you search for text or files, you reach first for `rg` or `rg --files`; they are much faster than alternatives like `grep`. If `rg` is unavailable, you use the next best tool without fuss."
	stockExecInstruction = "- Exercise caution when escaping text for exec_command calls - backticks and `$()` passed to the `cmd` argument will still execute. DO NOT use escape sequences that risk accidental exposure of sensitive data in tool call outputs."
)

type instructionLine struct {
	number int
	start  int
	end    int
	text   string
}

func codexModelInstructionFileConfigured() (bool, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, fmt.Errorf("determine Codex home: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return modelInstructionFileConfiguredAt(filepath.Join(codexHome, "config.toml"))
}

func modelInstructionFileConfiguredAt(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Codex config: %w", err)
	}
	var config struct {
		ModelInstructionFile *string `toml:"model_instructions_file"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("parse Codex config: %w", err)
	}
	return config.ModelInstructionFile != nil, nil
}

func rewriteReceivedModelInstructions(request *parsedResponsesRequest, customized bool, modelInstructions string) error {
	raw, present := request.fields["instructions"]
	var received *string
	if present {
		if err := json.Unmarshal(raw, &received); err != nil {
			return errors.New("responses instructions must be a string or null")
		}
	}
	if received == nil || *received == "" {
		rewritten, found, err := rewriteDeveloperModelInstructions(request.fields["input"], customized, modelInstructions)
		if err != nil {
			return err
		}
		if found {
			request.fields["input"] = rewritten
			return nil
		}
	}
	if !present || received == nil {
		return nil
	}
	rendered, err := renderModelInstructions(*received, customized, modelInstructions)
	if err != nil {
		return err
	}
	request.fields["instructions"] = mustMarshalJSON(rendered)
	return nil
}

func rewriteDeveloperModelInstructions(raw json.RawMessage, customized bool, modelInstructions string) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	input, err := decodeJSONValue(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode responses input instructions: %w", err)
	}
	var rewriteErr error
	found := transformFirstDeveloperText(input, func(received string) string {
		rendered, err := renderModelInstructions(received, customized, modelInstructions)
		if err != nil {
			rewriteErr = err
			return received
		}
		return rendered
	})
	if rewriteErr != nil {
		return nil, false, rewriteErr
	}
	if !found {
		return nil, false, nil
	}
	rewritten, err := json.Marshal(input)
	if err != nil {
		return nil, false, fmt.Errorf("encode responses input instructions: %w", err)
	}
	return rewritten, true, nil
}

func renderModelInstructions(input string, appendIfMissing bool, modelInstructions string) (string, error) {
	lines := instructionLines(input)
	starts := matchingInstructionLines(lines, hpatchInstructionsStartMarker)
	ends := matchingInstructionLines(lines, hpatchInstructionsEndMarker)
	if len(starts) != 0 || len(ends) != 0 {
		if len(starts) != 1 || len(ends) != 1 {
			return "", errors.New("responses instructions contain incomplete hpatch markers")
		}
		if starts[0].number >= ends[0].number {
			return "", errors.New("responses instructions contain reversed hpatch markers")
		}
		return input[:starts[0].start] + modelInstructions + input[ends[0].end:], nil
	}

	stockHeadings := matchingInstructionLines(lines, stockEditHeading)
	stockInstructions := matchingInstructionLines(lines, stockEditInstruction)
	stockRGInstructions := matchingInstructionLines(lines, stockRGInstruction)
	stockExecInstructions := matchingInstructionLines(lines, stockExecInstruction)
	if len(stockHeadings) == 1 && len(stockInstructions) == 1 && len(stockRGInstructions) == 1 && len(stockExecInstructions) == 1 {
		if stockInstructions[0].number == stockHeadings[0].number+2 && lines[stockHeadings[0].number].text == "" {
			return renderStockModelInstructions(lines, stockHeadings[0], stockInstructions[0], stockRGInstructions[0], stockExecInstructions[0], modelInstructions), nil
		}
		if !appendIfMissing {
			return "", errors.New("stock file-editing heading, separator, and instruction are not one section")
		}
	}

	if appendIfMissing {
		if input == "" {
			return modelInstructions, nil
		}
		separator := "\n\n"
		if strings.HasSuffix(input, "\n") {
			separator = "\n"
		}
		return input + separator + modelInstructions, nil
	}
	return "", errors.New("responses instructions match neither stock nor marked hpatch guidance")
}

func renderStockModelInstructions(lines []instructionLine, heading, instruction, rgInstruction, execInstruction instructionLine, modelInstructions string) string {
	var rendered strings.Builder
	for _, line := range lines {
		if line.number == heading.number {
			rendered.WriteString(modelInstructions)
		}
		if line.number >= heading.number && line.number <= instruction.number ||
			line.number == rgInstruction.number || line.number == execInstruction.number {
			continue
		}
		rendered.WriteString(line.text)
		if line.end > line.start+len(line.text) {
			rendered.WriteByte('\n')
		}
	}
	return rendered.String()
}

func instructionLines(input string) []instructionLine {
	if input == "" {
		return nil
	}
	lines := make([]instructionLine, 0, strings.Count(input, "\n")+1)
	for start, number := 0, 1; start < len(input); number++ {
		end := strings.IndexByte(input[start:], '\n')
		if end < 0 {
			end = len(input)
		} else {
			end += start + 1
		}
		textEnd := end
		if input[end-1] == '\n' {
			textEnd--
		}
		lines = append(lines, instructionLine{number: number, start: start, end: end, text: input[start:textEnd]})
		start = end
	}
	return lines
}

func matchingInstructionLines(lines []instructionLine, text string) []instructionLine {
	matches := make([]instructionLine, 0, 1)
	for _, line := range lines {
		if line.text == text {
			matches = append(matches, line)
		}
	}
	return matches
}
