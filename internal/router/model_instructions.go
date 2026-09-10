package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/yusing/hpatch/capturer"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
)

const (
	hpatchInstructionsStartMarker = "<!-- hpatch-model-instructions:start -->"
	hpatchInstructionsEndMarker   = "<!-- hpatch-model-instructions:end -->"

	// Exact stock fragments make upstream prompt changes fail closed instead of
	// leaving conflicting editing guidance in the forwarded request.
	stockEditHeading            = "## File editing constraints"
	stockEditInstruction        = "Use `apply_patch` for local file edits. Do not create or edit files with `cat` or other shell write tricks. Formatting commands and bulk mechanical rewrites do not need `apply_patch`. Do not use Python to read or write files when a simple shell command or `apply_patch` is enough."
	stockRGInstruction          = "- When you search for text or files, you reach first for `rg` or `rg --files`; they are much faster than alternatives like `grep`. If `rg` is unavailable, you use the next best tool without fuss."
	stockExecInstruction        = "- Exercise caution when escaping text for exec_command calls - backticks and `$()` passed to the `cmd` argument will still execute. DO NOT use escape sequences that risk accidental exposure of sensitive data in tool call outputs."
	stockAstraIntroduction      = "You are Codex, an agent based on GPT-6. You and the user share one workspace, and your job is to collaborate with them until their intended goal is completely handled."
	stockWorkHeading            = "# Rules for getting work done"
	stockShellSafetyInstruction = "- Treat shell command text as code. `JSON.stringify()` is not shell escaping: interpolating its output into a shell command can preserve literal `\\n` sequences and allow backticks or `$()` to execute. Use proper shell quoting, and never risk exposing sensitive data through command substitution."
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

func rewriteReceivedModelInstructions(ctx context.Context, request *parsedResponsesRequest, customized bool, modelInstructions string) (rewriteErr error) {
	evidence := capturer.InstructionRewrite{Carrier: "none", Strategy: "unchanged", Workflow: codexinstructions.WorkflowForModel(request.model()), CustomConfigured: customized}
	defer func() {
		if rewriteErr != nil {
			evidence.Strategy = "rejected"
		}
		capturer.ObserveInstructionRewrite(ctx, evidence)
	}()

	if err := stripDeveloperModeInstructions(request); err != nil {
		return err
	}

	raw, present := request.fields["instructions"]
	var received *string
	if present {
		if err := json.Unmarshal(raw, &received); err != nil {
			evidence.Carrier, evidence.Strategy = "instructions", "rejected"
			return errors.New("responses instructions must be a string or null")
		}
	}
	if received == nil || *received == "" {
		rewritten, found, err := rewriteDeveloperModelInstructions(request.fields["input"], customized, modelInstructions, &evidence)
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
	evidence.Carrier = "instructions"
	rendered, strategy, err := renderModelInstructions(*received, customized, modelInstructions)
	evidence.Strategy = strategy
	if err != nil {
		return err
	}
	request.fields["instructions"] = mustMarshalJSON(rendered)
	return nil
}

// Ignore the closing tag in Codex's backtick-quoted tag pair. Keep the next
// character outside the block, including an adjacent block's opening bracket.
var developerModeBlocks = []*regexp.Regexp{
	regexp.MustCompile("(?s)(^|[^`])<collaboration_mode>.*?</collaboration_mode>([^`]|$)"),
	regexp.MustCompile("(?s)(^|[^`])<request_user_input>.*?</request_user_input>([^`]|$)"),
}

func stripDeveloperModeText(text string) string {
	for _, block := range developerModeBlocks {
		for {
			rewritten := block.ReplaceAllString(text, "$1$2")
			if rewritten == text {
				break
			}
			text = rewritten
		}
	}
	return text
}

func stripDeveloperModeInstructions(request *parsedResponsesRequest) error {
	raw := request.fields["input"]
	if len(raw) == 0 {
		return nil
	}
	input, err := decodeResponsesInput(raw)
	if err != nil {
		return fmt.Errorf("decode responses developer instructions: %w", err)
	}
	if !input.array {
		return nil
	}
	kept := make([]json.RawMessage, 0, len(input.items))
	changed := false
	for _, raw := range input.items {
		item, ok := decodeResponsesItem(raw)
		if !ok || item.Type != "message" || item.Role != "developer" {
			kept = append(kept, raw)
			continue
		}
		modified := false
		content, _, err := transformCTP2Content(item.Content, func(text string) string {
			rewritten := stripDeveloperModeText(text)
			modified = modified || rewritten != text
			return rewritten
		}, isCTP2InputTextPart)
		if err != nil {
			return err
		}
		if !modified {
			kept = append(kept, raw)
			continue
		}
		var text string
		if json.Unmarshal(content, &text) == nil {
			if strings.TrimSpace(text) == "" {
				changed = true
				continue
			}
		} else if parts, ok := decodeResponsesTextParts(content); ok {
			original, _ := decodeResponsesTextParts(item.Content)
			retained := parts[:0]
			for index, part := range parts {
				if part.text == nil || !isCTP2InputTextPart(part.typeName) || strings.TrimSpace(*part.text) != "" || *part.text == *original[index].text {
					retained = append(retained, part)
				}
			}
			if len(retained) == 0 {
				changed = true
				continue
			}
			content, err = encodeResponsesTextParts(retained)
			if err != nil {
				return err
			}
		}
		changed = true
		item.setContent(content)
		kept = append(kept, mustMarshalJSON(item))
	}
	if changed {
		input.items = kept
		request.fields["input"], err = input.encode()
	}
	return err
}

func rewriteDeveloperModelInstructions(raw json.RawMessage, customized bool, modelInstructions string, evidence *capturer.InstructionRewrite) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	input, err := decodeResponsesInput(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode responses input instructions: %w", err)
	}
	var rewriteErr error
	found, err := transformFirstDeveloperText(&input, func(received string) string {
		evidence.Carrier = "developer"
		rendered, strategy, err := renderModelInstructions(received, customized, modelInstructions)
		evidence.Strategy = strategy
		if err != nil {
			rewriteErr = err
			return received
		}
		return rendered
	})
	if err != nil {
		return nil, false, fmt.Errorf("encode responses input instructions: %w", err)
	}
	if rewriteErr != nil {
		return nil, false, rewriteErr
	}
	if !found {
		return nil, false, nil
	}
	rewritten, err := input.encode()
	if err != nil {
		return nil, false, fmt.Errorf("encode responses input instructions: %w", err)
	}
	return rewritten, true, nil
}

func renderModelInstructions(input string, appendIfMissing bool, modelInstructions string) (string, string, error) {
	lines := instructionLines(input)
	starts := matchingInstructionLines(lines, hpatchInstructionsStartMarker)
	ends := matchingInstructionLines(lines, hpatchInstructionsEndMarker)
	if len(starts) != 0 || len(ends) != 0 {
		if len(starts) != 1 || len(ends) != 1 {
			return "", "rejected", errors.New("responses instructions contain incomplete hpatch markers")
		}
		if starts[0].number >= ends[0].number {
			return "", "rejected", errors.New("responses instructions contain reversed hpatch markers")
		}
		return rewriteStockToolConflicts(input[:starts[0].start]) + modelInstructions + rewriteStockToolConflicts(input[ends[0].end:]), "marked", nil
	}

	stockHeadings := matchingInstructionLines(lines, stockEditHeading)
	stockInstructions := matchingInstructionLines(lines, stockEditInstruction)
	stockRGInstructions := matchingInstructionLines(lines, stockRGInstruction)
	stockExecInstructions := matchingInstructionLines(lines, stockExecInstruction)
	if len(stockHeadings) == 1 && len(stockInstructions) == 1 && len(stockRGInstructions) == 1 && len(stockExecInstructions) == 1 {
		if stockInstructions[0].number == stockHeadings[0].number+2 && lines[stockHeadings[0].number].text == "" {
			return renderStockModelInstructions(lines, stockHeadings[0], stockInstructions[0], stockRGInstructions[0], stockExecInstructions[0], modelInstructions), "stock-gpt5", nil
		}
		if !appendIfMissing {
			return "", "rejected", errors.New("stock file-editing heading, separator, and instruction are not one section")
		}
	}

	// Astra has no editing section. Replace its pinned search line in the
	// work rules instead. The active template may already have replaced the
	// exec-command warning with the transport-independent shell safety rule.
	astraIntroductions := matchingInstructionLines(lines, stockAstraIntroduction)
	workHeadings := matchingInstructionLines(lines, stockWorkHeading)
	shellSafetyInstructions := matchingInstructionLines(lines, stockShellSafetyInstruction)
	execAnchor := instructionLine{}
	if len(stockExecInstructions) == 1 {
		execAnchor = stockExecInstructions[0]
	} else if len(stockExecInstructions) == 0 && len(shellSafetyInstructions) == 1 {
		execAnchor = shellSafetyInstructions[0]
	}
	if len(astraIntroductions) == 1 && len(workHeadings) == 1 &&
		len(stockHeadings) == 0 && len(stockInstructions) == 0 &&
		len(stockRGInstructions) == 1 && execAnchor.number != 0 &&
		astraIntroductions[0].number < workHeadings[0].number &&
		stockRGInstructions[0].number == workHeadings[0].number+2 &&
		lines[workHeadings[0].number].text == "" &&
		execAnchor.number > stockRGInstructions[0].number {
		displacedExec := instructionLine{}
		if len(stockExecInstructions) == 1 {
			displacedExec = stockExecInstructions[0]
		}
		return renderStockModelInstructions(lines, stockRGInstructions[0], stockRGInstructions[0], stockRGInstructions[0], displacedExec, modelInstructions), "stock-astra", nil
	}

	if appendIfMissing {
		if input == "" {
			return modelInstructions, "custom-append", nil
		}
		separator := "\n\n"
		if strings.HasSuffix(input, "\n") {
			separator = "\n"
		}
		return rewriteStockToolConflicts(input) + separator + modelInstructions, "custom-append", nil
	}
	return "", "rejected", errors.New("responses instructions match neither stock nor marked hpatch guidance")
}

func renderStockModelInstructions(lines []instructionLine, first, last, rgInstruction, execInstruction instructionLine, modelInstructions string) string {
	var rendered strings.Builder
	for _, line := range lines {
		if line.number == first.number {
			rendered.WriteString(modelInstructions)
		}
		if line.number >= first.number && line.number <= last.number ||
			line.number == rgInstruction.number || line.number == execInstruction.number {
			continue
		}
		rendered.WriteString(rewriteStockToolConflicts(line.text))
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
