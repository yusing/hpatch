package router

import (
	"strings"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryContextLines = 2

// isHPatchRecoveryCandidate reserves mutation-leading payloads for restricted
// root text editing. The root editor decides whether the complete candidate is
// a valid recovery, so a malformed attempt cannot replace its rejected baseline.
func isHPatchRecoveryCandidate(script string) bool {
	for _, line := range hpatchsyntax.SplitPhysicalLines(script) {
		fields := strings.Fields(line.Text)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "type", "type-", "type+":
			return true
		default:
			return false
		}
	}
	return false
}

func hpatchRecoveryGuidance(script string, rejections []hpatch.HostRejection) string {
	references := hpatch.TextReferences(script, hpatchRecoveryRows(script, rejections)...)
	var guidance strings.Builder
	if references != "" {
		guidance.WriteString("\nRejected script `LINE:HASH` rows:\n")
		guidance.WriteString(references)
	}
	guidance.WriteString("\nUse hpatch without `in` to patch the rejected script.\n")
	return guidance.String()
}

func hpatchRecoveryRows(script string, rejections []hpatch.HostRejection) []int {
	lines := hpatchsyntax.SplitPhysicalLines(script)
	logicalRows := hpatchLogicalRowsByPhysicalLine(script, lines)
	rows := make([]int, 0, len(rejections)*4)
	appendPhysicalRows := func(physicalRow int) {
		if physicalRow < 1 || physicalRow > len(logicalRows) {
			return
		}
		rows = append(rows, logicalRows[physicalRow-1]...)
	}
	for _, rejection := range rejections {
		header := rejection.SourceLine
		if header < 1 || header > len(lines) || len(logicalRows[header-1]) == 0 {
			continue
		}
		appendPhysicalRows(header)

		frame, err := hpatchsyntax.FrameCommand(lines, header-1, lines[header-1].Text)
		if err != nil {
			if frame.Next > header {
				appendPhysicalRows(frame.Next)
			}
			continue
		}
		if frame.Delimiter == "" {
			continue
		}
		if rejection.ValueLine > 0 {
			valueRow := header + rejection.ValueLine
			bodyStart := header + 1
			bodyEnd := min(frame.Next-1, len(lines))
			for row := max(bodyStart, valueRow-hpatchRecoveryContextLines); row <= min(bodyEnd, valueRow+hpatchRecoveryContextLines); row++ {
				appendPhysicalRows(row)
			}
		}
		appendPhysicalRows(frame.Next)
	}
	return rows
}

func hpatchLogicalRowsByPhysicalLine(script string, lines []hpatchsyntax.PhysicalLine) [][]int {
	mapped := make([][]int, len(lines))
	offset := 0
	logicalRow := 1
	for index, line := range lines {
		next := offset + len(line.Text) + len(line.Terminator)
		count := hpatch.TextLineCount(script[offset:next])
		for range count {
			mapped[index] = append(mapped[index], logicalRow)
			logicalRow++
		}
		offset = next
	}
	return mapped
}
