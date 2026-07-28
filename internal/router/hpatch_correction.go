package router

import (
	"fmt"
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

// isHPatchCorrection reports whether payload corrects a rejected script rather
// than supplying a new one. Blank leading lines are ignored so the test matches
// hpatch's own tolerance for surrounding blank lines.
func isHPatchCorrection(payload string) bool {
	for line := range hpatchScriptLines(payload) {
		return hpatchCorrectionOpener.MatchString(line)
	}
	return false
}

// hpatchScriptLines yields the nonblank lines of a script or correction payload
// in order. It mirrors hpatch's own parser: a trailing carriage return is not
// part of the line, and a blank line is not a command. Command indices count
// only the lines this yields, while hpatch's "source line" diagnostics count
// every line, so the two numbers diverge whenever a script contains a blank
// line. Corrections key on the command index.
func hpatchScriptLines(payload string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for raw := range strings.SplitSeq(payload, "\n") {
			line := strings.TrimSuffix(raw, "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !yield(line) {
				return
			}
		}
	}
}

// parseHPatchCorrections reads a correction payload. Every nonblank line must
// be one entry, because a payload that mixes corrections with raw commands has
// no unambiguous reading.
func parseHPatchCorrections(payload string) ([]hpatchCorrection, error) {
	var corrections []hpatchCorrection
	seen := make(map[int]bool)
	for line := range hpatchScriptLines(payload) {
		match := hpatchCorrectionPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("correction %q is not `INDEX: COMMAND`; every line of a correction must name the command it replaces", hpatchCorrectionPreview(line))
		}
		command, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("correction index %q is out of range", match[1])
		}
		replacement := match[3]
		if strings.TrimSpace(replacement) == "" {
			return nil, fmt.Errorf("correction for command %d has no replacement command; resend the complete script to remove a command", command)
		}
		if seen[command] {
			return nil, fmt.Errorf("correction for command %d appears more than once", command)
		}
		seen[command] = true
		corrections = append(corrections, hpatchCorrection{command: command, replacement: replacement})
	}
	if len(corrections) == 0 {
		return nil, fmt.Errorf("correction payload is empty")
	}
	return corrections, nil
}

// applyHPatchCorrections rebuilds base with each named command replaced. Line
// terminators and blank lines are preserved so that the reconstructed script's
// command indices and source lines both stay stable across a chain of
// corrections.
func applyHPatchCorrections(base string, corrections []hpatchCorrection) (string, error) {
	raw := strings.Split(base, "\n")
	positions := make([]int, 0, len(raw))
	for index, line := range raw {
		if strings.TrimSpace(strings.TrimSuffix(line, "\r")) == "" {
			continue
		}
		positions = append(positions, index)
	}
	if len(positions) == 0 {
		return "", fmt.Errorf("the rejected script has no commands to correct")
	}
	for _, correction := range corrections {
		if correction.command > len(positions) {
			return "", fmt.Errorf("command %d does not exist; the rejected script has %d commands", correction.command, len(positions))
		}
		index := positions[correction.command-1]
		terminator := ""
		if strings.HasSuffix(raw[index], "\r") {
			terminator = "\r"
		}
		raw[index] = correction.replacement + terminator
	}
	return strings.Join(raw, "\n"), nil
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
