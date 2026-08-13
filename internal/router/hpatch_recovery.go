package router

import (
	"fmt"
	"strings"

	"github.com/yusing/hpatch"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryContextLines = 2

func hpatchRecoveryGuidance(
	script string,
	rejections []hpatch.HostRejection,
	failures []hpatch.HostFailure,
	refreshed bool,
) string {
	return codexinstructions.RecoveryGuidance(hpatchRecoveryReferences(script, rejections, failures, refreshed))
}

func hpatchRecoveryReferences(
	script string,
	rejections []hpatch.HostRejection,
	failures []hpatch.HostFailure,
	refreshed bool,
) string {
	commands := recoveryCommands(script)
	relevant := make(map[int][]hpatch.HostRejection)
	for _, rejection := range rejections {
		if rejection.Command > 0 {
			relevant[rejection.Command] = append(relevant[rejection.Command], rejection)
		}
	}
	scopes := make(map[int]string)
	for _, failure := range failures {
		if failure.Command > 0 && failure.Scope != "" {
			scopes[failure.Command] = failure.Scope
		}
	}

	var output strings.Builder
	if refreshed {
		output.WriteString("This re-rejection replaced the recovery baseline. Every C... and V... handle from earlier diagnostics is stale; use only the current handles below.\n\n")
	}
	output.WriteString("Current rejected-script command manifest (complete):\n")
	for _, command := range commands {
		fmt.Fprintf(&output, "    %s %s", command.handle, hpatchRecoveryCommandSummary(command))
		if scope := scopes[command.index]; scope != "" {
			fmt.Fprintf(&output, " [correction scope: %s]", scope)
		}
		if _, ok := relevant[command.index]; ok {
			output.WriteString(" [rejected]")
		}
		output.WriteByte('\n')
	}

	if len(relevant) != 0 {
		output.WriteString("\nLocalized recovery context:\n")
	}
	for _, command := range commands {
		rejected, selected := relevant[command.index]
		if !selected {
			continue
		}
		fmt.Fprintf(&output, "\n%s %s\n", command.handle, hpatchRecoveryCommandSummary(command))
		if scope := scopes[command.index]; scope != "" {
			fmt.Fprintf(&output, "    correction scope: %s\n", scope)
		}
		if len(command.valueRows) == 0 {
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
	output.WriteString("\nSend only handle-based corrections to functions.hpatch_recover. Put every known independent correction in one payload; do not resubmit the complete rejected script.\n")
	return output.String()
}

func hpatchRecoveryCommandSummary(command recoveryCommandReference) string {
	if command.parts.parsed {
		summary := command.parts.operation
		if command.parts.target != "" {
			summary += " " + command.parts.target
		}
		if len(command.valueRows) != 0 {
			return fmt.Sprintf("%s [%d value rows]", summary, len(command.valueRows))
		}
		return summary + " [inline value]"
	}
	header, _, _ := strings.Cut(command.command, "\n")
	operation, operands := recoveryToken(header)
	if operation == "type" || operation == "type-" || operation == "type+" {
		target, _ := recoveryToken(operands)
		if recoveryRowOrRange(target) {
			return operation + " " + target + " [malformed value]"
		}
		return operation + " [malformed operands]"
	}
	return header
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
