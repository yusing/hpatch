package router

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yusing/hpatch"
	codexinstructions "github.com/yusing/hpatch/contrib/codex"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

func hpatchRecoveryGuidance(
	script string,
	rejections []hpatch.HostRejection,
	refreshed bool,
) string {
	references, eligible := hpatchRecoveryReferences(script, rejections, refreshed)
	if !eligible {
		return "\nThis rejection requires one complete corrected HPATCH/2 script through functions.hpatch; functions.hpatch_recover changes stale targets only.\n"
	}
	return codexinstructions.RecoveryGuidance(references)
}

func hpatchRecoveryReferences(
	script string,
	rejections []hpatch.HostRejection,
	refreshed bool,
) (string, bool) {
	commands := recoveryCommands(script)
	relevant := make(map[int]struct{})
	for _, rejection := range rejections {
		if rejection.Reason != "row-stale" || rejection.Command < 1 || rejection.Command > len(commands) {
			return "", false
		}
		command := commands[rejection.Command-1]
		if !command.parts.parsed || command.parts.target == "" {
			return "", false
		}
		relevant[rejection.Command] = struct{}{}
	}
	if len(relevant) == 0 {
		return "", false
	}

	var output strings.Builder
	if refreshed {
		output.WriteString("This re-rejection changed no workspace file. Earlier C... handles are stale; use only the current handles below.\n\n")
	}
	output.WriteString("Rejected target commands:\n")
	indices := make([]int, 0, len(relevant))
	for index := range relevant {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	for _, index := range indices {
		command := commands[index-1]
		fmt.Fprintf(&output, "    %s %s\n", command.handle, hpatchRecoveryCommandSummary(command))
	}
	output.WriteString("\nSend one line per listed command as C... CURRENT_TARGET. Put all corrections in one functions.hpatch_recover payload; the router preserves every operation and value and reevaluates the complete script.\n")
	return output.String(), true
}

func hpatchRecoveryCommandSummary(command recoveryCommandReference) string {
	if command.parts.parsed {
		summary := command.parts.operation
		if command.parts.target != "" {
			summary += " " + command.parts.target
		}
		if command.parts.multiline {
			return summary + " [heredoc value]"
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
