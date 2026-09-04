package router

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryDescription = `Target correction for the latest rejected HPATCH/2 script. Invalid recovery leaves the retained script and workspace unchanged.`

//go:embed hpatch_recovery_grammar.lark
var hpatchRecoveryGrammar string

type recoveryCommandReference struct {
	handle string

	index  int
	header int
	end    int
	source string
	parts  recoveryCommandParts
}

type recoveryCommandParts struct {
	operation string
	target    string
	value     string
	multiline bool
	parsed    bool
	identity  hpatch.TargetIdentity
}

type recoveryOperation struct {
	sequence int
	command  *recoveryCommandReference
	target   string
}

type recoveryEdit struct {
	sequence int
	script   string
}

func recoveryCommands(script string) []recoveryCommandReference {
	lines := hpatchsyntax.SplitPhysicalLines(script)
	offsets := make([]int, len(lines)+1)
	for index, line := range lines {
		offsets[index+1] = offsets[index] + len(line.Text) + len(line.Terminator)
	}
	commands := make([]recoveryCommandReference, 0)
	for index := 0; index < len(lines); {
		header := index
		line := lines[index].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}
		frame, _ := hpatchsyntax.FrameCommand(lines, header, line)
		index = max(frame.Next, index)
		source := script[offsets[header]:offsets[index]]
		commands = append(commands, recoveryCommandReference{
			handle: fmt.Sprintf("C%d:%s", len(commands)+1, recoveryHash(source)),
			index:  len(commands) + 1,
			header: header,
			end:    index,
			source: source,
			parts:  recoveryCommandPartsOf(line, frame),
		})
	}
	return commands
}

// recoveryCommandPartsOf parses a command header and frame into recovery command parts.
func recoveryCommandPartsOf(header string, frame hpatchsyntax.CommandFrame) recoveryCommandParts {
	operation, operands := recoveryToken(header)
	if operation != "type" && operation != "add" {
		return recoveryCommandParts{}
	}
	if frame.Delimiter != "" {
		target := strings.TrimSpace(strings.TrimSuffix(operands, "<<PATCH"))
		parts := recoveryCommandParts{
			operation: operation,
			target:    target,
			value:     frame.Body,
			multiline: true,
		}
		if operation == "type" && target == "" || operation == "add" && target == "EOF" {
			parts.parsed = true
			return parts
		}
		identity, trailing, err := hpatch.ParseTargetIdentity(target, false)
		if err == nil && strings.TrimSpace(trailing) == "" {
			parts.parsed = true
			parts.identity = identity
		}
		return parts
	}
	if operation == "type" && strings.HasPrefix(strings.TrimSpace(operands), `"`) {
		value, trailing, err := hpatchsyntax.DecodeQuoted(strings.TrimSpace(operands))
		if err == nil && strings.TrimSpace(trailing) == "" {
			return recoveryCommandParts{operation: operation, value: value, parsed: true}
		}
	}
	if operation == "add" {
		destination, trailing := recoveryToken(operands)
		if destination == "EOF" {
			value, rest, err := hpatchsyntax.DecodeQuoted(trailing)
			if err == nil && strings.TrimSpace(rest) == "" {
				return recoveryCommandParts{
					operation: operation,
					target:    destination,
					value:     value,
					parsed:    true,
				}
			}
			return recoveryCommandParts{operation: operation}
		}
	}
	identity, trailing, err := hpatch.ParseTargetIdentity(operands, true)
	if err != nil {
		return recoveryCommandParts{operation: operation}
	}
	target := strings.TrimSpace(operands[:len(operands)-len(trailing)])
	value, rest, err := hpatchsyntax.DecodeQuoted(trailing)
	if err != nil || strings.TrimSpace(rest) != "" {
		return recoveryCommandParts{operation: operation}
	}
	return recoveryCommandParts{
		operation: operation,
		target:    target,
		value:     value,
		parsed:    true,
		identity:  identity,
	}
}

type recoveredScript struct {
	script string
	delta  string
}

func recoverScriptDetailed(ctx context.Context, rejectedScript, payload string) (recoveredScript, error) {
	if ctx == nil {
		return recoveredScript{}, fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return recoveredScript{}, err
	}
	commands := recoveryCommands(rejectedScript)
	operations, err := parseRecoveryPayload(commands, payload)
	if err != nil {
		return recoveredScript{}, err
	}
	edits, err := planRecoveryEdits(rejectedScript, operations)
	if err != nil {
		return recoveredScript{}, err
	}
	var editScript strings.Builder
	for _, edit := range edits {
		editScript.WriteString(edit.script)
		editScript.WriteByte('\n')
	}
	rebuilt, err := hpatch.EditText(ctx, rejectedScript, editScript.String())
	if err != nil {
		return recoveredScript{}, err
	}
	return recoveredScript{script: rebuilt, delta: formatRecoveryDelta(operations)}, nil
}

func formatRecoveryDelta(operations []recoveryOperation) string {
	var delta strings.Builder
	for _, operation := range operations {
		fmt.Fprintf(
			&delta,
			"%s: %s -> %s\n",
			operation.command.handle,
			operation.command.parts.target,
			operation.target,
		)
	}
	return strings.TrimSuffix(delta.String(), "\n")
}

func parseRecoveryPayload(
	commands []recoveryCommandReference,
	payload string,
) ([]recoveryOperation, error) {
	lines := hpatchsyntax.SplitPhysicalLines(payload)
	operations := make([]recoveryOperation, 0)
	for index, line := range lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		handle, target, ok := strings.Cut(line.Text, " ")
		if !ok || handle == "" || target == "" || strings.HasPrefix(target, " ") ||
			target != strings.TrimRight(target, " \t") {
			return nil, recoveryError(index+1, "expected one command handle and one target")
		}
		command, err := resolveRecoveryCommand(commands, handle)
		if err != nil {
			return nil, recoveryError(index+1, err.Error())
		}
		replacementTarget, trailing, targetErr := hpatch.ParseTargetIdentity(target, false)
		if !command.parts.parsed || command.parts.target == "" || command.parts.target == "EOF" ||
			targetErr != nil || strings.TrimSpace(trailing) != "" || target == "EOF" ||
			!hpatchsyntax.ValidOperandSpacing(target) {
			return nil, recoveryError(index+1, "command must be target-bearing and the replacement target must be valid")
		}
		if replacementTarget == command.parts.identity {
			return nil, recoveryError(index+1, "replacement target must differ from the rejected target")
		}
		operations = append(operations, recoveryOperation{
			sequence: len(operations) + 1,
			command:  command,
			target:   target,
		})
	}
	if len(operations) == 0 {
		return nil, recoveryError(1, "recovery payload must contain at least one target correction")
	}
	return operations, nil
}

func resolveRecoveryCommand(
	commands []recoveryCommandReference,
	handle string,
) (*recoveryCommandReference, error) {
	if len(handle) < 7 || handle[0] != 'C' {
		return nil, fmt.Errorf("invalid command handle %q", handle)
	}
	indexText, hash, ok := strings.Cut(handle[1:], ":")
	if !ok || len(hash) != 4 || !recoveryLowerHex(hash) || !recoveryPositiveDecimal(indexText) {
		return nil, fmt.Errorf("invalid command handle %q", handle)
	}
	index, err := strconv.Atoi(indexText)
	if err != nil || index > len(commands) {
		return nil, fmt.Errorf("command handle %q is stale or unavailable", handle)
	}
	command := &commands[index-1]
	if command.handle != handle {
		return nil, fmt.Errorf("command handle %q is stale; latest handle is %s", handle, command.handle)
	}
	return command, nil
}

func planRecoveryEdits(script string, operations []recoveryOperation) ([]recoveryEdit, error) {
	seen := make(map[int]struct{}, len(operations))
	lines := hpatchsyntax.SplitPhysicalLines(script)
	logicalRows := hpatchLogicalRowsByPhysicalLine(script, lines)
	edits := make([]recoveryEdit, 0, len(operations))
	for _, operation := range operations {
		if _, duplicate := seen[operation.command.index]; duplicate {
			return nil, recoveryError(operation.sequence, "duplicate target correction for one command")
		}
		seen[operation.command.index] = struct{}{}
		commandTarget, err := recoveryPhysicalTarget(
			script,
			logicalRows,
			operation.command.header,
			operation.command.end-1,
		)
		if err != nil {
			return nil, recoveryError(operation.sequence, err.Error())
		}
		replacement := renderRecoveryMutation(
			operation.command,
			operation.command.parts.operation,
			operation.target,
			operation.command.parts.value,
			operation.command.parts.multiline,
		)
		edits = append(edits, recoveryEdit{
			sequence: operation.sequence,
			script:   "type " + commandTarget + " " + strconv.Quote(replacement),
		})
	}
	return edits, nil
}

func renderRecoveryMutation(
	command *recoveryCommandReference,
	operation, target, value string,
	multiline bool,
) string {
	terminator := recoveryTerminator(hpatchsyntax.SplitPhysicalLines(command.source)[0])
	finalTerminator := recoveryTerminatorSuffix(command.source)
	header := operation
	if target != "" {
		header += " " + target
	}
	if multiline {
		return header + " <<PATCH" + terminator + value + "PATCH" + finalTerminator
	}
	return header + " " + strconv.Quote(value) + finalTerminator
}

func recoveryPhysicalTarget(script string, logicalRows [][]int, start, end int) (string, error) {
	if start < 0 || end >= len(logicalRows) || start > end {
		return "", fmt.Errorf("physical target is unavailable")
	}
	for start <= end && len(logicalRows[start]) == 0 {
		start++
	}
	for end >= start && len(logicalRows[end]) == 0 {
		end--
	}
	if start > end {
		return "", fmt.Errorf("physical target has no logical row")
	}
	first := recoveryLogicalHandle(script, logicalRows[start][0])
	if start == end && len(logicalRows[start]) == 1 {
		return first, nil
	}
	lastRows := logicalRows[end]
	return first + ".." + recoveryLogicalHandle(script, lastRows[len(lastRows)-1]), nil
}

func recoveryLogicalHandle(script string, row int) string {
	reference := hpatch.TextReferences(script, row)
	handle, _ := recoveryToken(reference)
	return handle
}

func recoveryPositiveDecimal(value string) bool {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func recoveryLowerHex(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func recoveryToken(value string) (string, string) {
	value = strings.TrimLeft(value, " \t")
	for index, character := range value {
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' {
			return value[:index], value[index:]
		}
	}
	return value, ""
}

func recoveryHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:2])
}

func recoveryTerminator(line hpatchsyntax.PhysicalLine) string {
	if line.Terminator != "" {
		return line.Terminator
	}
	return "\n"
}

func recoveryTerminatorSuffix(value string) string {
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

func recoveryError(line int, message string) error {
	return fmt.Errorf("recovery operation at line %d: %s", line, message)
}
