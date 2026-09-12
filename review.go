package mekugi

import (
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// ReviewFile is an immutable original-to-final review projection. Unlike the
// executor patch it includes deleted content and preserves line-ending changes.
// Paths are empty on the absent side of an addition or deletion.
type ReviewFile struct {
	BeforePath string
	AfterPath  string
	Diff       string
}

func reviewFiles(changes []change) []ReviewFile {
	files := make([]ReviewFile, 0, len(changes))
	for _, change := range changes {
		file := ReviewFile{BeforePath: change.originalPath, AfterPath: change.path}
		before, after := change.original, change.content
		action := "update"
		switch change.kind {
		case changeAdd:
			action, file.BeforePath, before = "add", "", ""
		case changeDelete:
			action, file.AfterPath, after = "delete", "", ""
		default:
			if file.BeforePath != file.AfterPath {
				action = "move"
			}
		}
		var diff strings.Builder
		fmt.Fprintf(&diff, "%s %q -> %q\n", action, file.BeforePath, file.AfterPath)
		a, b := reviewLines(before), reviewLines(after)
		groups := difflib.NewMatcher(a, b).GetGroupedOpCodes(3)
		if len(groups) != 0 {
			fmt.Fprintf(&diff, "--- %s\n+++ %s\n", reviewPath(file.BeforePath), reviewPath(file.AfterPath))
		}
		for _, group := range groups {
			first, last := group[0], group[len(group)-1]
			fmt.Fprintf(&diff, "@@ -%s +%s @@\n", reviewRange(first.I1, last.I2), reviewRange(first.J1, last.J2))
			for _, op := range group {
				if op.Tag == 'e' {
					writeReviewLines(&diff, ' ', a[op.I1:op.I2])
				}
				if op.Tag == 'r' || op.Tag == 'd' {
					writeReviewLines(&diff, '-', a[op.I1:op.I2])
				}
				if op.Tag == 'r' || op.Tag == 'i' {
					writeReviewLines(&diff, '+', b[op.J1:op.J2])
				}
			}
		}
		file.Diff = diff.String()
		files = append(files, file)
	}
	return files
}

func reviewPath(path string) string {
	if path == "" {
		return "/dev/null"
	}
	return fmt.Sprintf("%q", path)
}

func reviewRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start+1, end-start)
}

func reviewLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeReviewLines(output *strings.Builder, prefix byte, lines []string) {
	for _, line := range lines {
		output.WriteByte(prefix)
		output.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			output.WriteString("\n\\ No newline at end of file\n")
		}
	}
}
