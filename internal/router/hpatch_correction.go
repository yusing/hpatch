package router

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

var (
	hpatchReplacementPattern = regexp.MustCompile(`^([1-9][0-9]*)(?:\.([1-9][0-9]*))?:( ?)(.*)$`)
	hpatchDeletionPattern    = regexp.MustCompile(`^-([1-9][0-9]*)(?:\.([1-9][0-9]*))?$`)
	hpatchInsertBefore       = regexp.MustCompile(`^\+([1-9][0-9]*)(?:\.([1-9][0-9]*))?:( ?)(.*)$`)
	hpatchInsertAfter        = regexp.MustCompile(`^([1-9][0-9]*)(?:\.([1-9][0-9]*))?\+:( ?)(.*)$`)
	hpatchCorrectionOpener   = regexp.MustCompile(`^(?:[1-9][0-9]*(?:\.[1-9][0-9]*)?:|-[1-9][0-9]*(?:\.[1-9][0-9]*)?|\+[1-9][0-9]*(?:\.[1-9][0-9]*)?:|[1-9][0-9]*(?:\.[1-9][0-9]*)?\+:)`)
)

type hpatchCorrectionKind uint8

const (
	hpatchReplace hpatchCorrectionKind = iota
	hpatchAccept
	hpatchDelete
	hpatchInsertBeforeAnchor
	hpatchInsertAfterAnchor
)

func (k hpatchCorrectionKind) mutationVerb() string {
	switch k {
	case hpatchReplace:
		return "replaced"
	case hpatchAccept:
		return "accepted"
	case hpatchDelete:
		return "deleted"
	default:
		return "mutated"
	}
}

type hpatchCorrection struct {
	kind        hpatchCorrectionKind
	command     int
	valueRow    int
	replacement string
}

type hpatchScriptFrame struct {
	start     int
	end       int
	delimiter string
	bodyStart int
	bodyEnd   int
}

type hpatchCorrectionStats struct {
	scope              string
	valueRowOperations uint64
	baseValueRows      uint64
	baseCommands       []string
}

func hpatchCorrectionStatsOf(corrections []hpatchCorrection) hpatchCorrectionStats {
	stats := hpatchCorrectionStats{scope: "command"}
	for _, correction := range corrections {
		if correction.valueRow == 0 {
			continue
		}
		stats.scope = "value-row"
		stats.valueRowOperations++
	}
	return stats
}

func (s hpatchCorrectionStats) withBase(base string, corrections []hpatchCorrection) hpatchCorrectionStats {
	if s.valueRowOperations == 0 {
		return s
	}
	lines := hpatchsyntax.SplitPhysicalLines(base)
	frames := hpatchCommandFrames(lines)
	seen := make(map[int]bool)
	for _, correction := range corrections {
		if correction.valueRow == 0 || seen[correction.command] || correction.command < 1 || correction.command > len(frames) {
			continue
		}
		seen[correction.command] = true
		frame := frames[correction.command-1]
		s.baseValueRows += uint64(frame.bodyEnd - frame.bodyStart)
		var command strings.Builder
		for index := frame.start; index < frame.end; index++ {
			command.WriteString(lines[index].Text)
			command.WriteString(lines[index].Terminator)
		}
		s.baseCommands = append(s.baseCommands, command.String())
	}
	return s
}

func hpatchCorrectionScope(payload string) string {
	for _, line := range hpatchsyntax.SplitPhysicalLines(payload) {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			continue
		}
		header, _, _ := strings.Cut(text, ":")
		if strings.Contains(header, ".") {
			return "value-row"
		}
		return "command"
	}
	return ""
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
	commandMutations := make(map[int]hpatchCorrectionKind)
	valueMutations := make(map[[2]int]hpatchCorrectionKind)
	valueCommands := make(map[int]bool)
	for index := 0; index < len(lines); {
		headerIndex := index
		line := lines[index].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}

		kind, indexText, valueRowText, replacement, ok := parseHPatchCorrectionHeader(line)
		if !ok {
			return nil, fmt.Errorf("correction %q is not `INDEX: COMMAND`, `INDEX: accept`, `-INDEX`, `+INDEX: COMMAND`, `INDEX+: COMMAND`, or a multiline-value indexed operation", hpatchCorrectionPreview(line))
		}
		command, err := strconv.Atoi(indexText)
		if err != nil {
			return nil, fmt.Errorf("correction index %q is out of range", indexText)
		}
		valueRow := 0
		if valueRowText != "" {
			valueRow, err = strconv.Atoi(valueRowText)
			if err != nil {
				return nil, fmt.Errorf("multiline value row %q is out of range", valueRowText)
			}
			if kind == hpatchAccept {
				return nil, fmt.Errorf("command %d multiline value row %d has no displayed correction to accept", command, valueRow)
			}
			if _, exists := commandMutations[command]; exists {
				return nil, fmt.Errorf("command %d cannot combine complete-command and multiline-value mutations", command)
			}
			valueCommands[command] = true
		}

		if kind == hpatchReplace || kind == hpatchAccept || kind == hpatchDelete {
			if valueRow != 0 {
				key := [2]int{command, valueRow}
				if previous, exists := valueMutations[key]; exists {
					if previous != kind {
						return nil, fmt.Errorf("command %d multiline value row %d cannot be both %s and %s", command, valueRow, previous.mutationVerb(), kind.mutationVerb())
					}
					return nil, fmt.Errorf("correction for command %d multiline value row %d appears more than once", command, valueRow)
				}
				valueMutations[key] = kind
			} else if previous, exists := commandMutations[command]; exists {
				if previous != kind {
					return nil, fmt.Errorf("command %d cannot be both %s and %s", command, previous.mutationVerb(), kind.mutationVerb())
				}
				return nil, fmt.Errorf("correction for command %d appears more than once", command)
			} else {
				if valueCommands[command] {
					return nil, fmt.Errorf("command %d cannot combine complete-command and multiline-value mutations", command)
				}
				commandMutations[command] = kind
			}
		}

		if kind != hpatchDelete && kind != hpatchAccept {
			if valueRow != 0 {
				replacement, err = parseHPatchValueRowReplacement(command, valueRow, replacement)
				if err != nil {
					return nil, err
				}
			} else if strings.TrimSpace(replacement) == "" {
				return nil, fmt.Errorf("correction for command %d has no replacement command", command)
			} else {
				frame, frameErr := hpatchsyntax.FrameCommand(lines, headerIndex, replacement)
				if frameErr != nil {
					return nil, fmt.Errorf("correction for command %d: %w", command, frameErr)
				}
				index = frame.Next
				replacement = correctionFrameText(lines, headerIndex, frame.Next, replacement)
			}
		}

		corrections = append(corrections, hpatchCorrection{kind: kind, command: command, valueRow: valueRow, replacement: replacement})
	}
	if len(corrections) == 0 {
		return nil, fmt.Errorf("correction payload is empty")
	}
	return corrections, nil
}

func parseHPatchCorrectionHeader(line string) (hpatchCorrectionKind, string, string, string, bool) {
	if match := hpatchReplacementPattern.FindStringSubmatch(line); match != nil {
		if match[4] == "accept" {
			return hpatchAccept, match[1], match[2], "", true
		}
		return hpatchReplace, match[1], match[2], match[4], true
	}
	if match := hpatchDeletionPattern.FindStringSubmatch(line); match != nil {
		return hpatchDelete, match[1], match[2], "", true
	}
	if match := hpatchInsertBefore.FindStringSubmatch(line); match != nil {
		return hpatchInsertBeforeAnchor, match[1], match[2], match[4], true
	}
	if match := hpatchInsertAfter.FindStringSubmatch(line); match != nil {
		return hpatchInsertAfterAnchor, match[1], match[2], match[4], true
	}
	return 0, "", "", "", false
}

func parseHPatchValueRowReplacement(command, row int, encoded string) (string, error) {
	encoded = strings.TrimLeft(encoded, " \t")
	if encoded == "" {
		return "", fmt.Errorf("correction for command %d multiline value row %d requires a quoted value", command, row)
	}
	value, trailing, err := hpatchsyntax.DecodeQuoted(encoded)
	if err != nil {
		return "", fmt.Errorf("correction for command %d multiline value row %d has an invalid quoted value: %w", command, row, err)
	}
	if strings.Trim(trailing, " \t") != "" {
		return "", fmt.Errorf("correction for command %d multiline value row %d has trailing text", command, row)
	}
	if !isSinglePhysicalRow(value) {
		return "", fmt.Errorf("correction for command %d multiline value row %d must contain one physical row", command, row)
	}
	if hpatchValueContainsDelimiter(value, "PATCH") {
		return "", fmt.Errorf("correction for command %d multiline value row %d cannot materialize the fixed PATCH delimiter", command, row)
	}
	return value, nil
}

func isSinglePhysicalRow(value string) bool {
	lines := hpatchsyntax.SplitPhysicalLines(value)
	if len(lines) == 1 {
		return true
	}
	return len(lines) == 2 && lines[0].Terminator != "" && lines[1].Text == "" && lines[1].Terminator == ""
}

func hpatchValueContainsDelimiter(value, delimiter string) bool {
	for _, line := range hpatchsyntax.SplitPhysicalLines(value) {
		if line.Text == delimiter {
			return true
		}
	}
	return false
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
	valueRows      map[int]*hpatchValueRowTransform
}

type hpatchValueRowTransform struct {
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
func applyHPatchCorrections(base string, corrections []hpatchCorrection, suggestions ...map[int]string) (string, error) {
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
	var available map[int]string
	if len(suggestions) != 0 {
		available = suggestions[0]
	}
	for _, correction := range corrections {
		transform := &transforms[correction.command-1]
		if correction.valueRow != 0 {
			frame := frames[correction.command-1]
			if frame.delimiter == "" {
				return "", fmt.Errorf("command %d has no multiline <<PATCH value; replace the complete command", correction.command)
			}
			bodyRows := frame.bodyEnd - frame.bodyStart
			if correction.valueRow < 1 || correction.valueRow > bodyRows {
				return "", fmt.Errorf("command %d multiline value row %d does not exist; the value has %d rows", correction.command, correction.valueRow, bodyRows)
			}
			if transform.hasReplacement || transform.deleted {
				return "", fmt.Errorf("command %d cannot combine complete-command and multiline-value mutations", correction.command)
			}
			if transform.valueRows == nil {
				transform.valueRows = make(map[int]*hpatchValueRowTransform)
			}
			rowTransform := transform.valueRows[correction.valueRow]
			if rowTransform == nil {
				rowTransform = &hpatchValueRowTransform{}
				transform.valueRows[correction.valueRow] = rowTransform
			}
			switch correction.kind {
			case hpatchReplace:
				if rowTransform.hasReplacement || rowTransform.deleted {
					return "", fmt.Errorf("command %d multiline value row %d has conflicting replacement or deletion operations", correction.command, correction.valueRow)
				}
				rowTransform.replacement = correction.replacement
				rowTransform.hasReplacement = true
			case hpatchDelete:
				if rowTransform.hasReplacement || rowTransform.deleted {
					return "", fmt.Errorf("command %d multiline value row %d has conflicting replacement or deletion operations", correction.command, correction.valueRow)
				}
				rowTransform.deleted = true
			case hpatchInsertBeforeAnchor:
				rowTransform.before = append(rowTransform.before, correction.replacement)
				rowTransform.insertions = append(rowTransform.insertions, correction.replacement)
			case hpatchInsertAfterAnchor:
				rowTransform.after = append(rowTransform.after, correction.replacement)
				rowTransform.insertions = append(rowTransform.insertions, correction.replacement)
			default:
				return "", fmt.Errorf("command %d multiline value row %d has unsupported correction", correction.command, correction.valueRow)
			}
			continue
		}
		switch correction.kind {
		case hpatchReplace, hpatchAccept:
			if len(transform.valueRows) != 0 {
				return "", fmt.Errorf("command %d cannot combine complete-command and multiline-value mutations", correction.command)
			}
			replacement := correction.replacement
			if correction.kind == hpatchAccept {
				var ok bool
				replacement, ok = available[correction.command]
				if !ok {
					return "", fmt.Errorf("command %d has no displayed correction to accept", correction.command)
				}
			}
			if transform.hasReplacement || transform.deleted {
				return "", fmt.Errorf("command %d has conflicting replacement or deletion operations", correction.command)
			}
			transform.replacement = replacement
			transform.hasReplacement = true
		case hpatchDelete:
			if len(transform.valueRows) != 0 {
				return "", fmt.Errorf("command %d cannot combine complete-command and multiline-value mutations", correction.command)
			}
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
		if len(transform.before) == 0 && len(transform.after) == 0 && !transform.hasReplacement && !transform.deleted && len(transform.valueRows) == 0 {
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
			command, err := correctionFrameWithValueRows(lines, frame, transform.valueRows)
			if err != nil {
				return "", fmt.Errorf("command %d: %w", frameIndex+1, err)
			}
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

func correctionFrameWithValueRows(lines []hpatchsyntax.PhysicalLine, frame hpatchScriptFrame, transforms map[int]*hpatchValueRowTransform) (string, error) {
	if len(transforms) == 0 {
		return correctionFrameText(lines, frame.start, frame.end, lines[frame.start].Text), nil
	}
	var body strings.Builder
	for index := frame.bodyStart; index < frame.bodyEnd; index++ {
		row := index - frame.bodyStart + 1
		transform := transforms[row]
		if transform == nil {
			body.WriteString(lines[index].Text)
			body.WriteString(lines[index].Terminator)
			continue
		}
		if transform.deleted {
			for _, insertion := range transform.insertions {
				body.WriteString(insertion)
			}
			continue
		}
		for _, insertion := range transform.before {
			body.WriteString(insertion)
		}
		if transform.hasReplacement {
			body.WriteString(transform.replacement)
			if lineTerminatorOf(transform.replacement) == "" {
				body.WriteString(lines[index].Terminator)
			}
		} else {
			body.WriteString(lines[index].Text)
			body.WriteString(lines[index].Terminator)
		}
		for _, insertion := range transform.after {
			body.WriteString(insertion)
		}
	}
	if hpatchValueContainsDelimiter(body.String(), frame.delimiter) {
		return "", fmt.Errorf("multiline value corrections cannot materialize the fixed %s delimiter", frame.delimiter)
	}

	var command strings.Builder
	command.WriteString(lines[frame.start].Text)
	command.WriteString(lines[frame.start].Terminator)
	command.WriteString(body.String())
	command.WriteString(lines[frame.bodyEnd].Text)
	return command.String(), nil
}

func lineTerminatorOf(value string) string {
	switch {
	case strings.HasSuffix(value, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(value, "\n"):
		return "\n"
	case strings.HasSuffix(value, "\r"):
		return "\r"
	default:
		return ""
	}
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
		frame, frameErr := hpatchsyntax.FrameCommand(lines, start, lines[start].Text)
		index = frame.Next
		scriptFrame := hpatchScriptFrame{start: start, end: index}
		if frameErr == nil && frame.Delimiter != "" {
			scriptFrame.delimiter = frame.Delimiter
			scriptFrame.bodyStart = start + 1
			scriptFrame.bodyEnd = index - 1
		}
		frames = append(frames, scriptFrame)
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
