package router

import (
	"fmt"
	"hpatch/internal/hpatchsyntax"
	"regexp"
	"strconv"
	"strings"
)

var (
	hpatchReplacementPattern = regexp.MustCompile(`^([1-9][0-9]*):( ?)(.*)$`)
	hpatchDeletionPattern    = regexp.MustCompile(`^-([1-9][0-9]*)$`)
	hpatchInsertBefore       = regexp.MustCompile(`^\+([1-9][0-9]*):( ?)(.*)$`)
	hpatchInsertAfter        = regexp.MustCompile(`^([1-9][0-9]*)\+:( ?)(.*)$`)
	hpatchCorrectionOpener   = regexp.MustCompile(`^(?:[1-9][0-9]*:|-[1-9][0-9]*|\+[1-9][0-9]*:|[1-9][0-9]*\+:)`)
)

type hpatchCorrectionKind uint8

const (
	hpatchReplace hpatchCorrectionKind = iota
	hpatchDelete
	hpatchInsertBeforeAnchor
	hpatchInsertAfterAnchor
)

type hpatchCorrection struct {
	kind        hpatchCorrectionKind
	command     int
	replacement string
}

type hpatchScriptFrame struct {
	start int
	end   int
}

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
// header must be one operation. A heredoc replacement or insertion consumes its
// literal body and closing delimiter as part of that operation.
func parseHPatchCorrections(payload string) ([]hpatchCorrection, error) {
	lines := hpatchsyntax.SplitPhysicalLines(payload)
	var corrections []hpatchCorrection
	mutations := make(map[int]hpatchCorrectionKind)
	for index := 0; index < len(lines); {
		headerIndex := index
		line := lines[index].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}

		kind, indexText, replacement, ok := parseHPatchCorrectionHeader(line)
		if !ok {
			return nil, fmt.Errorf("correction %q is not `INDEX: COMMAND`, `-INDEX`, `+INDEX: COMMAND`, or `INDEX+: COMMAND`", hpatchCorrectionPreview(line))
		}
		command, err := strconv.Atoi(indexText)
		if err != nil {
			return nil, fmt.Errorf("correction index %q is out of range", indexText)
		}

		if kind == hpatchReplace || kind == hpatchDelete {
			if previous, exists := mutations[command]; exists {
				if previous != kind {
					return nil, fmt.Errorf("command %d cannot be both replaced and deleted", command)
				}
				return nil, fmt.Errorf("correction for command %d appears more than once", command)
			}
			mutations[command] = kind
		}

		if kind != hpatchDelete {
			if strings.TrimSpace(replacement) == "" {
				return nil, fmt.Errorf("correction for command %d has no replacement command", command)
			}
			frame, err := hpatchsyntax.FrameCommand(lines, headerIndex, replacement)
			if err != nil {
				return nil, fmt.Errorf("correction for command %d: %w", command, err)
			}
			index = frame.Next
			replacement = correctionFrameText(lines, headerIndex, frame.Next, replacement)
		}

		corrections = append(corrections, hpatchCorrection{kind: kind, command: command, replacement: replacement})
	}
	if len(corrections) == 0 {
		return nil, fmt.Errorf("correction payload is empty")
	}
	return corrections, nil
}

func parseHPatchCorrectionHeader(line string) (hpatchCorrectionKind, string, string, bool) {
	if match := hpatchReplacementPattern.FindStringSubmatch(line); match != nil {
		return hpatchReplace, match[1], match[3], true
	}
	if match := hpatchDeletionPattern.FindStringSubmatch(line); match != nil {
		return hpatchDelete, match[1], "", true
	}
	if match := hpatchInsertBefore.FindStringSubmatch(line); match != nil {
		return hpatchInsertBeforeAnchor, match[1], match[3], true
	}
	if match := hpatchInsertAfter.FindStringSubmatch(line); match != nil {
		return hpatchInsertAfterAnchor, match[1], match[3], true
	}
	return 0, "", "", false
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

type hpatchFrameTransform struct {
	before         []string
	after          []string
	insertions     []string
	replacement    string
	hasReplacement bool
	deleted        bool
}

// applyHPatchCorrections resolves every operation against the original command
// frames before rebuilding the script. Insertions retain payload order even when
// their original anchor is deleted.
func applyHPatchCorrections(base string, corrections []hpatchCorrection) (string, error) {
	lines := hpatchsyntax.SplitPhysicalLines(base)
	frames := hpatchCommandFrames(lines)
	if len(frames) == 0 {
		return "", fmt.Errorf("the rejected script has no commands to correct")
	}
	for _, correction := range corrections {
		if correction.command < 1 || correction.command > len(frames) {
			return "", fmt.Errorf("command %d does not exist; the rejected script has %d commands", correction.command, len(frames))
		}
	}

	transforms := make([]hpatchFrameTransform, len(frames))
	for _, correction := range corrections {
		transform := &transforms[correction.command-1]
		switch correction.kind {
		case hpatchReplace:
			if transform.hasReplacement || transform.deleted {
				return "", fmt.Errorf("command %d has conflicting replacement or deletion operations", correction.command)
			}
			transform.replacement = correction.replacement
			transform.hasReplacement = true
		case hpatchDelete:
			if transform.hasReplacement || transform.deleted {
				return "", fmt.Errorf("command %d has conflicting replacement or deletion operations", correction.command)
			}
			transform.deleted = true
		case hpatchInsertBeforeAnchor:
			transform.before = append(transform.before, correction.replacement)
			transform.insertions = append(transform.insertions, correction.replacement)
		case hpatchInsertAfterAnchor:
			transform.after = append(transform.after, correction.replacement)
			transform.insertions = append(transform.insertions, correction.replacement)
		}
	}

	var corrected strings.Builder
	frameIndex := 0
	for index := 0; index < len(lines); {
		if frameIndex >= len(frames) || frames[frameIndex].start != index {
			corrected.WriteString(lines[index].Text)
			corrected.WriteString(lines[index].Terminator)
			index++
			continue
		}
		frame := frames[frameIndex]
		transform := transforms[frameIndex]
		if len(transform.before) == 0 && len(transform.after) == 0 && !transform.hasReplacement && !transform.deleted {
			for lineIndex := frame.start; lineIndex < frame.end; lineIndex++ {
				corrected.WriteString(lines[lineIndex].Text)
				corrected.WriteString(lines[lineIndex].Terminator)
			}
			index = frame.end
			frameIndex++
			continue
		}

		var commands []string
		if transform.deleted {
			commands = append(commands, transform.insertions...)
		} else {
			commands = append(commands, transform.before...)
			command := correctionFrameText(lines, frame.start, frame.end, lines[frame.start].Text)
			if transform.hasReplacement {
				command = transform.replacement
			}
			commands = append(commands, command)
			commands = append(commands, transform.after...)
		}
		separator := hpatchFrameSeparator(lines, frame)
		for commandIndex, command := range commands {
			corrected.WriteString(command)
			if commandIndex < len(commands)-1 {
				corrected.WriteString(separator)
			} else {
				corrected.WriteString(lines[frame.end-1].Terminator)
			}
		}
		index = frame.end
		frameIndex++
	}
	return corrected.String(), nil
}

func hpatchFrameSeparator(lines []hpatchsyntax.PhysicalLine, frame hpatchScriptFrame) string {
	for index := frame.start; index < frame.end; index++ {
		if lines[index].Terminator != "" {
			return lines[index].Terminator
		}
	}
	for index := frame.start - 1; index >= 0; index-- {
		if lines[index].Terminator != "" {
			return lines[index].Terminator
		}
	}
	return "\n"
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

func hpatchCorrectionPreview(line string) string {
	const limit = 64
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	return string(runes[:limit]) + "…"
}
