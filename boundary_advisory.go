package mekugi

import (
	"fmt"
	"strings"
)

// boundaryAdvisory describes authored splices against the immutable baseline,
// not the final file: neighboring commands and language formatting may change
// those boundaries later. It never participates in mutation or validation.
func (e *editor) boundaryAdvisory(origin editOrigin, command instruction) string {
	var preserved, deleted, removed, blankBefore, blankAfter, joinedLeft int
	owned := "none"
	for _, edit := range e.edits {
		if edit.command != origin.command {
			continue
		}
		target := e.baseline[edit.start:edit.end]
		owned = endingLabel(target)
		if command.operation == "type" {
			if command.text == "" && edit.start != edit.end {
				deleted++
			}
			if lineTerminatorSuffix(target) != "" && lineTerminatorSuffix(command.text) == "" {
				if command.text != "" && (command.target.kind == targetLine || command.target.kind == targetRange) {
					preserved++
				} else {
					removed++
				}
			}
		}
		left, right := e.baseline[:edit.start], e.baseline[edit.end:]
		if blankBoundary(left, edit.replacement) {
			blankBefore++
		}
		if blankBoundary(edit.replacement, right) {
			blankAfter++
		}
		if command.operation == "add" && command.target.kind == targetEOF &&
			left != "" && lineTerminatorSuffix(left) == "" &&
			edit.replacement != "" && edit.replacement[0] != '\r' && edit.replacement[0] != '\n' {
			joinedLeft++
		}
	}
	if !origin.multilineValue && preserved+deleted+removed+blankBefore+blankAfter+joinedLeft == 0 {
		return ""
	}

	value := endingLabel(command.text)
	if command.text == "" {
		value = "empty"
	}
	var report strings.Builder
	fmt.Fprintf(&report, "baseline-boundary owned=%s value=%s", owned, value)
	if command.delimiter != "" {
		fmt.Fprintf(&report, " mode=%s", command.delimiter)
	}
	for _, count := range []struct {
		name string
		n    int
	}{
		{"preserves-ending", preserved},
		{"deletes", deleted},
		{"removes-ending", removed},
		{"blank-before", blankBefore},
		{"blank-after", blankAfter},
		{"joins-left", joinedLeft},
	} {
		if count.n != 0 {
			fmt.Fprintf(&report, " %s=%d", count.name, count.n)
		}
	}
	return report.String()
}

func endingLabel(text string) string {
	switch lineTerminatorSuffix(text) {
	case "\r\n":
		return "CRLF"
	case "\n":
		return "LF"
	case "\r":
		return "CR"
	default:
		return "none"
	}
}

// blankBoundary identifies a blank (possibly space/tab-only) line where a
// terminating left side meets the right side. A split CRLF is one terminator,
// not an empty line. Only the adjacent line is inspected.
func blankBoundary(left, right string) bool {
	ending := lineTerminatorSuffix(left)
	if ending == "" || right == "" || (ending == "\r" && right[0] == '\n') {
		return false
	}
	tail := strings.TrimLeft(right, " \t")
	return strings.HasPrefix(tail, "\r") || strings.HasPrefix(tail, "\n")
}
