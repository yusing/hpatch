package router

import (
	"fmt"
	"strings"

	"github.com/yusing/hpatch"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryContextLines = 2

func hpatchRecoveryGuidance(script string, rejections []hpatch.HostRejection) string {
	return codexinstructions.RecoveryGuidance(hpatchRecoveryReferences(script, rejections))
}

func hpatchRecoveryReferences(script string, rejections []hpatch.HostRejection) string {
	commands := recoveryCommands(script)
	relevant := make(map[int][]hpatch.HostRejection)
	for _, rejection := range rejections {
		if rejection.Command > 0 {
			relevant[rejection.Command] = append(relevant[rejection.Command], rejection)
		}
	}
	var output strings.Builder
	output.WriteString("Recoverable rejected-script commands:\n")
	for _, command := range commands {
		if len(relevant) != 0 {
			if _, ok := relevant[command.index]; !ok {
				continue
			}
		}
		fmt.Fprintf(&output, "\n%s\n", command.handle)
		for line := range strings.SplitSeq(command.command, "\n") {
			fmt.Fprintf(&output, "    %s\n", line)
		}
		rejected, selected := relevant[command.index]
		if !selected || len(command.valueRows) == 0 {
			continue
		}
		output.WriteString("    value rows:\n")
		valuePhysicalRows := hpatchRecoveryValueRows(command, rejected)
		for index, row := range command.valueRows {
			physicalRow := command.header + index + 1
			if !hpatchRecoveryRowVisible(physicalRow, valuePhysicalRows) {
				continue
			}
			fmt.Fprintf(&output, "        %s %s\n", row.handle, row.value)
		}
	}
	output.WriteString("\nUse functions.hpatch_recover with these handles.\n")
	return output.String()
}

func hpatchRecoveryValueRows(command recoveryCommandReference, rejections []hpatch.HostRejection) []int {
	rows := make([]int, 0, len(rejections))
	for _, rejection := range rejections {
		if rejection.ValueLine > 0 {
			rows = append(rows, command.header+rejection.ValueLine+1)
		}
	}
	return rows
}

func hpatchRecoveryRowVisible(physicalRow int, rejectedRows []int) bool {
	for _, rejectedRow := range rejectedRows {
		if physicalRow >= rejectedRow-hpatchRecoveryContextLines &&
			physicalRow <= rejectedRow+hpatchRecoveryContextLines {
			return true
		}
	}
	return false
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
