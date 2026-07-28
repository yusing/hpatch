package router

import (
	"fmt"
	"hpatch/internal/hpatchsyntax"
	"regexp"
	"strconv"
	"strings"
)

// hpatchCorrectionPattern matches one correction entry: a one-based command
// index, a colon, an optional single separating space, and the replacement
// command. Only the first space after the colon is separator, because a
// replacement command never begins with whitespace.
var hpatchCorrectionPattern = regexp.MustCompile(`^([1-9][0-9]*):( ?)(.*)$`)

// hpatchCorrectionOpener matches the shape that distinguishes a correction
// payload from an editing script. A script's first command must select a file
// with in, new, or mv, so a leading command index can never begin one.
var hpatchCorrectionOpener = regexp.MustCompile(`^[1-9][0-9]*:`)

// hpatchCorrection replaces one command of a previously rejected script.
type hpatchCorrection struct {
	command     int
	replacement string
}

type hpatchScriptFrame struct {
	start int
	end   int
}

// isHPatchCorrection reports whether payload corrects a rejected script rather
// than supplying a new one. Blank leading lines are ignored so the test matches
// hpatch's own tolerance for surrounding blank lines.
func isHPatchCorrection(payload string) bool {
	for _, line := range hpatchsyntax.SplitPhysicalLines(payload) {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		return hpatchCorrectionOpener.MatchString(line.Text)
	}
	return false
}

// parseHPatchCorrections reads a correction payload. Every nonblank command
// header must be one entry. A heredoc replacement consumes its literal body and
// closing delimiter as part of that entry.
func parseHPatchCorrections(payload string) ([]hpatchCorrection, error) {
	lines := hpatchsyntax.SplitPhysicalLines(payload)
	var corrections []hpatchCorrection
	seen := make(map[int]bool)
	for index := 0; index < len(lines); {
		headerIndex := index
		line := lines[index].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := hpatchCorrectionPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("correction %q is not `INDEX: COMMAND`; every command header of a correction must name the command it replaces", hpatchCorrectionPreview(line))
		}
		command, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("correction index %q is out of range", match[1])
		}
		replacement := match[3]
		if strings.TrimSpace(replacement) == "" {
			return nil, fmt.Errorf("correction for command %d has no replacement command; resend the complete script to remove a command", command)
		}
		frame, err := hpatchsyntax.FrameCommand(lines, headerIndex, replacement)
		if err != nil {
			return nil, fmt.Errorf("correction for command %d: %w", command, err)
		}
		index = frame.Next
		if seen[command] {
			return nil, fmt.Errorf("correction for command %d appears more than once", command)
		}
		seen[command] = true
		corrections = append(corrections, hpatchCorrection{
			command:     command,
			replacement: correctionFrameText(lines, headerIndex, frame.Next, replacement),
		})
	}
	if len(corrections) == 0 {
		return nil, fmt.Errorf("correction payload is empty")
	}
	return corrections, nil
}

func correctionFrameText(lines []hpatchsyntax.PhysicalLine, start, end int, command string) string {
	if end == start+1 {
		return command
	}
	var replacement strings.Builder
	replacement.WriteString(command)
	replacement.WriteString(lines[start].Terminator)
	for index := start + 1; index < end; index++ {
		replacement.WriteString(lines[index].Text)
		if index < end-1 {
			replacement.WriteString(lines[index].Terminator)
		}
	}
	return replacement.String()
}

// applyHPatchCorrections rebuilds base with each named command frame replaced.
// The replacement inherits the replaced frame's final line terminator; heredoc
// body terminators come from the correction payload itself.
func applyHPatchCorrections(base string, corrections []hpatchCorrection) (string, error) {
	lines := hpatchsyntax.SplitPhysicalLines(base)
	frames := hpatchCommandFrames(lines)
	if len(frames) == 0 {
		return "", fmt.Errorf("the rejected script has no commands to correct")
	}
	type frameReplacement struct {
		end  int
		text string
	}
	replacements := make(map[int]frameReplacement, len(corrections))
	for _, correction := range corrections {
		if correction.command > len(frames) {
			return "", fmt.Errorf("command %d does not exist; the rejected script has %d commands", correction.command, len(frames))
		}
		frame := frames[correction.command-1]
		replacements[frame.start] = frameReplacement{end: frame.end, text: correction.replacement}
	}

	var corrected strings.Builder
	for index := 0; index < len(lines); {
		if replacement, ok := replacements[index]; ok {
			corrected.WriteString(replacement.text)
			corrected.WriteString(lines[replacement.end-1].Terminator)
			index = replacement.end
			continue
		}
		corrected.WriteString(lines[index].Text)
		corrected.WriteString(lines[index].Terminator)
		index++
	}
	return corrected.String(), nil
}

func hpatchCommandFrames(lines []hpatchsyntax.PhysicalLine) []hpatchScriptFrame {
	var frames []hpatchScriptFrame
	for index := 0; index < len(lines); {
		if strings.TrimSpace(lines[index].Text) == "" {
			index++
			continue
		}
		start := index
		frame, _ := hpatchsyntax.FrameCommand(lines, start, lines[start].Text)
		index = frame.Next
		frames = append(frames, hpatchScriptFrame{start: start, end: index})
	}
	return frames
}

// hpatchCorrectionPreview bounds a rejected line so a malformed payload cannot
// push an arbitrary amount of model text into a diagnostic.
func hpatchCorrectionPreview(line string) string {
	const limit = 64
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	return string(runes[:limit]) + "…"
}
