package router

import (
	"cmp"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yusing/hpatch"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
)

const hpatchRecoveryDescription = `Handle-local mutation of the latest rejected HPATCH/2 script. Invalid recovery leaves the retained script and workspace unchanged.`

//go:embed hpatch_recovery_grammar.lark
var hpatchRecoveryGrammar string

type recoveryValueReference struct {
	handle string
	value  string
}

type recoveryCommandReference struct {
	handle    string
	command   string
	valueRows []recoveryValueReference

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
}

type recoveryOperation struct {
	sequence int
	command  *recoveryCommandReference
	kind     string

	target    string
	operation string
	value     string
	multiline bool
	valueRow  int
	embedded  string
}

type recoveryChanges struct {
	command *recoveryCommandReference
	first   int

	structural *recoveryOperation
	target     *recoveryOperation
	operation  *recoveryOperation
	value      *recoveryOperation
	rows       []*recoveryOperation
	before     []*recoveryOperation
	after      []*recoveryOperation
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
		command := recoveryCommandReference{
			handle:  fmt.Sprintf("C%d:%s", len(commands)+1, recoveryHash(source)),
			command: strings.TrimSuffix(source, lines[index-1].Terminator),
			index:   len(commands) + 1,
			header:  header,
			end:     index,
			source:  source,
			parts:   recoveryCommandPartsOf(line, frame),
		}
		if frame.Delimiter != "" {
			for body := header + 1; body < index-1; body++ {
				command.valueRows = append(command.valueRows, recoveryValueReference{
					handle: fmt.Sprintf("V%d:%s", body+1, recoveryHash(lines[body].Text)),
					value:  lines[body].Text,
				})
			}
		}
		commands = append(commands, command)
	}
	return commands
}

func recoveryCommandPartsOf(header string, frame hpatchsyntax.CommandFrame) recoveryCommandParts {
	operation, operands := recoveryToken(header)
	if operation != "type" && operation != "type-" && operation != "type+" {
		return recoveryCommandParts{}
	}
	if frame.Delimiter != "" {
		target := strings.TrimSpace(strings.TrimSuffix(operands, "<<PATCH"))
		return recoveryCommandParts{
			operation: operation,
			target:    target,
			value:     frame.Body,
			multiline: true,
			parsed:    operation == "type" && target == "" || recoveryTarget(target),
		}
	}
	if operation == "type" && strings.HasPrefix(strings.TrimSpace(operands), `"`) {
		value, trailing, err := hpatchsyntax.DecodeQuoted(strings.TrimSpace(operands))
		if err == nil && strings.TrimSpace(trailing) == "" {
			return recoveryCommandParts{
				operation: operation,
				value:     value,
				parsed:    true,
			}
		}
	}
	target, value, ok := recoveryInlineMutation(operands)
	return recoveryCommandParts{operation: operation, target: target, value: value, parsed: ok}
}

func recoveryInlineMutation(operands string) (string, string, bool) {
	row, trailing := recoveryToken(operands)
	if recoveryRowOrRange(row) {
		trailing = strings.TrimLeft(trailing, " \t")
		if !strings.HasPrefix(trailing, `"`) {
			return "", "", false
		}
		first, afterFirst, err := hpatchsyntax.DecodeQuoted(trailing)
		if err != nil {
			return "", "", false
		}
		if strings.TrimSpace(afterFirst) == "" {
			return row, first, true
		}
		if !recoveryValidTargetLiteral(first) {
			return "", "", false
		}
		literalSource := trailing[:len(trailing)-len(afterFirst)]
		return recoveryTargetAndValue(row, literalSource, afterFirst)
	}
	operands = strings.TrimLeft(operands, " \t")
	if !strings.HasPrefix(operands, `"`) {
		return "", "", false
	}
	literal, afterLiteral, err := hpatchsyntax.DecodeQuoted(operands)
	if err != nil || !recoveryValidTargetLiteral(literal) {
		return "", "", false
	}
	literalSource := operands[:len(operands)-len(afterLiteral)]
	return recoveryTargetAndValue("", literalSource, afterLiteral)
}

func recoveryTargetAndValue(row, literalSource, trailing string) (string, string, bool) {
	target := literalSource
	if row != "" {
		target = row + " " + target
	}
	trailing = strings.TrimLeft(trailing, " \t")
	if trailing == "" {
		return "", "", false
	}
	if strings.HasPrefix(trailing, `"`) {
		value, rest, err := hpatchsyntax.DecodeQuoted(trailing)
		if err != nil || strings.TrimSpace(rest) != "" {
			return "", "", false
		}
		return target, value, true
	}
	count, afterCount := recoveryToken(trailing)
	if !recoveryPositiveDecimal(count) {
		return "", "", false
	}
	afterCount = strings.TrimLeft(afterCount, " \t")
	value, rest, err := hpatchsyntax.DecodeQuoted(afterCount)
	if err != nil || strings.TrimSpace(rest) != "" {
		return "", "", false
	}
	return target + " " + count, value, true
}

type recoveredScript struct {
	script string
	delta  string
}

func recoverScript(ctx context.Context, rejectedScript, payload string) (string, error) {
	recovered, err := recoverScriptDetailed(ctx, rejectedScript, payload)
	return recovered.script, err
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
	edits, err := planRecoveryEdits(ctx, rejectedScript, operations)
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
		fmt.Fprintf(&delta, "%s %s", operation.command.handle, operation.kind)
		switch operation.kind {
		case "target":
			fmt.Fprintf(&delta, ": %s -> %s", operation.command.parts.target, operation.target)
		case "operation":
			fmt.Fprintf(&delta, ": %s -> %s", operation.command.parts.operation, operation.operation)
		case "value":
			if operation.valueRow == 0 {
				fmt.Fprintf(&delta, ": %d bytes -> %d bytes", len(operation.command.parts.value), len(operation.value))
			} else {
				fmt.Fprintf(&delta, ": value row %d replaced", operation.valueRow)
			}
		case "value-", "value+":
			fmt.Fprintf(&delta, ": value row %d, %d bytes", operation.valueRow, len(operation.value))
		case "replace", "before", "after":
			fmt.Fprintf(&delta, ": %d-byte command", len(operation.embedded))
		}
		delta.WriteByte('\n')
	}
	return strings.TrimSuffix(delta.String(), "\n")
}

func parseRecoveryPayload(
	commands []recoveryCommandReference,
	payload string,
) ([]recoveryOperation, error) {
	lines := hpatchsyntax.SplitPhysicalLines(payload)
	operations := make([]recoveryOperation, 0)
	for index := 0; index < len(lines); {
		header := index
		line := lines[index].Text
		index++
		if strings.TrimSpace(line) == "" {
			continue
		}
		handle, trailing := recoveryToken(line)
		command, err := resolveRecoveryCommand(commands, handle)
		if err != nil {
			return nil, recoveryError(header+1, err.Error())
		}
		action, operands := recoveryToken(trailing)
		if action == "" {
			return nil, recoveryError(header+1, "recovery operation is missing")
		}
		synthetic := ""
		switch {
		case action == "value" && strings.TrimSpace(operands) == "<<PATCH":
			synthetic = "type <<PATCH"
		case strings.HasPrefix(action, "V"):
			valueOperation, valueOperands := recoveryToken(operands)
			if (valueOperation == "value" || valueOperation == "value-" || valueOperation == "value+") &&
				strings.TrimSpace(valueOperands) == "<<PATCH" {
				synthetic = "type <<PATCH"
			}
		case action == "replace" || action == "before" || action == "after":
			embedded := strings.TrimLeft(operands, " \t")
			if strings.HasSuffix(embedded, " <<PATCH") {
				synthetic = embedded
			}
		}
		frame := hpatchsyntax.CommandFrame{Next: index}
		if synthetic != "" {
			frame, err = hpatchsyntax.FrameCommand(lines, header, synthetic)
			index = frame.Next
			if err != nil {
				return nil, recoveryError(header+1, err.Error())
			}
		}
		operation, err := parseRecoveryOperation(
			command,
			len(operations)+1,
			header+1,
			action,
			operands,
			frame,
			lines,
		)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	if len(operations) == 0 {
		return nil, recoveryError(1, "recovery payload must contain at least one operation")
	}
	return operations, nil
}

func parseRecoveryOperation(
	command *recoveryCommandReference,
	sequence, sourceLine int,
	action, operands string,
	frame hpatchsyntax.CommandFrame,
	lines []hpatchsyntax.PhysicalLine,
) (recoveryOperation, error) {
	operation := recoveryOperation{sequence: sequence, command: command, kind: action}
	switch action {
	case "drop":
		if strings.TrimSpace(operands) != "" {
			return operation, recoveryError(sourceLine, "drop does not accept operands")
		}
	case "target":
		if !command.parts.parsed || command.parts.target == "" || !recoveryTarget(strings.TrimSpace(operands)) {
			return operation, recoveryError(sourceLine, "target requires a parsed target-bearing command and valid target")
		}
		operation.target = strings.TrimSpace(operands)
	case "operation":
		if !command.parts.parsed || command.parts.target == "" {
			return operation, recoveryError(sourceLine, "operation requires a parsed target-bearing command")
		}
		value := strings.TrimSpace(operands)
		if value != "type" && value != "type-" && value != "type+" {
			return operation, recoveryError(sourceLine, "operation must be type, type-, or type+")
		}
		operation.operation = value
	case "value":
		if !command.parts.parsed {
			return operation, recoveryError(sourceLine, "value requires a parsed target-bearing command")
		}
		value, multiline, err := recoveryValue(sourceLine, operands, frame)
		if err != nil {
			return operation, err
		}
		operation.value, operation.multiline = value, multiline
	case "replace", "before", "after":
		embedded, err := recoveryEmbeddedCommand(sourceLine, operands, frame, lines)
		if err != nil {
			return operation, err
		}
		operation.embedded = embedded
	default:
		valueIndex := slices.IndexFunc(command.valueRows, func(reference recoveryValueReference) bool {
			return reference.handle == action
		})
		if valueIndex < 0 {
			return operation, recoveryError(sourceLine, "value-row handle is stale or unavailable")
		}
		valueOperation, valueOperands := recoveryToken(operands)
		if valueOperation != "value" && valueOperation != "value-" && valueOperation != "value+" {
			return operation, recoveryError(sourceLine, "value-row operation must be value, value-, or value+")
		}
		value, multiline, err := recoveryValue(sourceLine, valueOperands, frame)
		if err != nil {
			return operation, err
		}
		operation.kind = valueOperation
		operation.valueRow = valueIndex + 1
		operation.value = value
		operation.multiline = multiline
	}
	return operation, nil
}

func recoveryValue(
	sourceLine int,
	operands string,
	frame hpatchsyntax.CommandFrame,
) (string, bool, error) {
	if frame.Delimiter != "" {
		if strings.TrimSpace(operands) != "<<PATCH" {
			return "", false, recoveryError(sourceLine, "trailing text before heredoc value")
		}
		return frame.Body, true, nil
	}
	value, trailing, err := hpatchsyntax.DecodeQuoted(operands)
	if err != nil {
		return "", false, recoveryError(sourceLine, "invalid quoted recovery value: "+err.Error())
	}
	if strings.TrimSpace(trailing) != "" {
		return "", false, recoveryError(sourceLine, "trailing text after recovery value")
	}
	return value, false, nil
}

func recoveryEmbeddedCommand(
	sourceLine int,
	operands string,
	frame hpatchsyntax.CommandFrame,
	lines []hpatchsyntax.PhysicalLine,
) (string, error) {
	header := strings.TrimLeft(operands, " \t")
	if header == "" {
		return "", recoveryError(sourceLine, "structural operation requires a complete command")
	}
	embedded := header
	if frame.Delimiter != "" {
		var body strings.Builder
		body.WriteString(header)
		body.WriteString(lines[sourceLine-1].Terminator)
		body.WriteString(frame.Body)
		body.WriteString("PATCH")
		body.WriteString(lines[frame.Next-1].Terminator)
		embedded = body.String()
	}
	if !recoveryCompleteCommand(embedded) {
		return "", recoveryError(sourceLine, "invalid embedded command")
	}
	return embedded, nil
}

func resolveRecoveryCommand(
	commands []recoveryCommandReference,
	handle string,
) (*recoveryCommandReference, error) {
	if len(handle) < 7 || handle[0] != 'C' {
		return nil, fmt.Errorf("invalid command handle %q", handle)
	}
	indexText, hash, ok := strings.Cut(handle[1:], ":")
	if !ok || len(hash) != 4 || !recoveryLowerHex(hash) ||
		!recoveryPositiveDecimal(indexText) {
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

func planRecoveryEdits(
	ctx context.Context,
	script string,
	operations []recoveryOperation,
) ([]recoveryEdit, error) {
	changes := make(map[int]*recoveryChanges)
	for index := range operations {
		operation := &operations[index]
		change := changes[operation.command.index]
		if change == nil {
			change = &recoveryChanges{command: operation.command, first: operation.sequence}
			changes[operation.command.index] = change
		}
		switch operation.kind {
		case "before":
			change.before = append(change.before, operation)
		case "after":
			change.after = append(change.after, operation)
		case "drop", "replace":
			if change.structural != nil || change.target != nil || change.operation != nil ||
				change.value != nil || len(change.rows) != 0 {
				return nil, recoveryError(operation.sequence, "conflicting operations address the same command")
			}
			change.structural = operation
		case "target":
			if change.structural != nil || change.target != nil {
				return nil, recoveryError(operation.sequence, "conflicting target operations")
			}
			change.target = operation
		case "operation":
			if change.structural != nil || change.operation != nil {
				return nil, recoveryError(operation.sequence, "conflicting command operations")
			}
			change.operation = operation
		case "value":
			if operation.valueRow != 0 {
				if change.structural != nil || change.value != nil {
					return nil, recoveryError(operation.sequence, "conflicting value operations")
				}
				change.rows = append(change.rows, operation)
				break
			}
			if change.structural != nil || change.value != nil || len(change.rows) != 0 {
				return nil, recoveryError(operation.sequence, "conflicting value operations")
			}
			change.value = operation
		case "value-", "value+":
			if change.structural != nil || change.value != nil {
				return nil, recoveryError(operation.sequence, "conflicting value operations")
			}
			change.rows = append(change.rows, operation)
		}
	}

	lines := hpatchsyntax.SplitPhysicalLines(script)
	logicalRows := hpatchLogicalRowsByPhysicalLine(script, lines)
	var edits []recoveryEdit
	for _, change := range changes {
		command := change.command
		startTarget, err := recoveryPhysicalTarget(script, logicalRows, command.header, command.header)
		if err != nil {
			return nil, recoveryError(change.first, err.Error())
		}
		commandTarget, err := recoveryPhysicalTarget(script, logicalRows, command.header, command.end-1)
		if err != nil {
			return nil, recoveryError(change.first, err.Error())
		}
		for _, operation := range change.before {
			value := operation.embedded
			if recoveryTerminatorSuffix(value) == "" {
				value += recoveryTerminator(lines[command.header])
			}
			edits = append(edits, recoveryEdit{
				sequence: operation.sequence,
				script:   "type- " + startTarget + " " + strconv.Quote(value),
			})
		}
		if change.structural != nil {
			operation := change.structural
			value := ""
			if operation.kind == "replace" {
				value = recoveryEmbeddedBoundary(operation.embedded, command.end < len(lines))
			}
			edits = append(edits, recoveryEdit{
				sequence: operation.sequence,
				script:   "type " + commandTarget + " " + strconv.Quote(value),
			})
		} else if change.target != nil || change.operation != nil || change.value != nil || len(change.rows) != 0 {
			operation := command.parts.operation
			target := command.parts.target
			value := command.parts.value
			multiline := command.parts.multiline
			sequence := change.first
			if change.operation != nil {
				operation = change.operation.operation
				sequence = min(sequence, change.operation.sequence)
			}
			if change.target != nil {
				target = change.target.target
				sequence = min(sequence, change.target.sequence)
			}
			if change.value != nil {
				value, multiline = change.value.value, change.value.multiline
				sequence = min(sequence, change.value.sequence)
			}
			if len(change.rows) != 0 {
				valueLines := hpatchsyntax.SplitPhysicalLines(value)
				logicalValueRows := hpatchLogicalRowsByPhysicalLine(value, valueLines)
				var rowEdits strings.Builder
				for _, row := range change.rows {
					if row.valueRow < 1 || row.valueRow > len(valueLines) {
						return nil, recoveryError(row.sequence, "value-row handle is stale or unavailable")
					}
					valueTarget, err := recoveryPhysicalTarget(
						value,
						logicalValueRows,
						row.valueRow-1,
						row.valueRow-1,
					)
					if err != nil {
						return nil, recoveryError(row.sequence, err.Error())
					}
					mutation := map[string]string{"value": "type", "value-": "type-", "value+": "type+"}[row.kind]
					fmt.Fprintf(&rowEdits, "%s %s %s\n", mutation, valueTarget, strconv.Quote(row.value))
					sequence = min(sequence, row.sequence)
				}
				rebuilt, err := hpatch.EditText(ctx, value, rowEdits.String())
				if err != nil {
					return nil, recoveryError(change.first, err.Error())
				}
				value = rebuilt
				multiline = true
			}
			replacement := renderRecoveryMutation(command, operation, target, value, multiline)
			edits = append(edits, recoveryEdit{
				sequence: sequence,
				script:   "type " + commandTarget + " " + strconv.Quote(replacement),
			})
		}
		for _, operation := range change.after {
			value := operation.embedded
			if command.end == len(lines) && recoveryTerminatorSuffix(command.source) == "" {
				value = recoveryTerminator(lines[command.header]) + value
			}
			value = recoveryEmbeddedBoundary(value, command.end < len(lines))
			edits = append(edits, recoveryEdit{
				sequence: operation.sequence,
				script:   "type+ " + commandTarget + " " + strconv.Quote(value),
			})
		}
	}
	slices.SortFunc(edits, func(left, right recoveryEdit) int {
		return cmp.Compare(left.sequence, right.sequence)
	})
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

func recoveryCompleteCommand(command string) bool {
	lines := hpatchsyntax.SplitPhysicalLines(command)
	for index, line := range lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		frame, err := hpatchsyntax.FrameCommand(lines, index, line.Text)
		if err != nil || frame.Next < len(lines)-1 {
			return false
		}
		operation, operands := recoveryToken(line.Text)
		switch operation {
		case "in", "new", "mv":
			return strings.TrimSpace(operands) != ""
		case "rm":
			return strings.TrimSpace(operands) == ""
		case "type", "type-", "type+":
			if frame.Delimiter != "" {
				return operation == "type" && strings.TrimSpace(strings.TrimSuffix(operands, "<<PATCH")) == "" ||
					recoveryCommandPartsOf(line.Text, frame).parsed
			}
			return recoveryCommandPartsOf(line.Text, frame).parsed
		default:
			return false
		}
	}
	return false
}

func recoveryTarget(target string) bool {
	row, trailing := recoveryToken(target)
	if recoveryRowOrRange(row) {
		trailing = strings.TrimLeft(trailing, " \t")
		if trailing == "" {
			return true
		}
		return recoveryLiteralTarget(trailing)
	}
	return recoveryLiteralTarget(strings.TrimLeft(target, " \t"))
}

func recoveryLiteralTarget(target string) bool {
	literal, rest, err := hpatchsyntax.DecodeQuoted(target)
	if err != nil || !recoveryValidTargetLiteral(literal) {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "" || recoveryPositiveDecimal(rest)
}

func recoveryValidTargetLiteral(literal string) bool {
	if literal == "" || strings.ContainsRune(literal, '\r') {
		return false
	}
	for _, character := range literal {
		if character < 0x20 && character != '\t' && character != '\n' {
			return false
		}
	}
	return true
}

func recoveryRowOrRange(value string) bool {
	start, end, rangeTarget := strings.Cut(value, "..")
	if rangeTarget {
		return !strings.Contains(end, "..") && recoveryRow(start) && recoveryRow(end)
	}
	return recoveryRow(value)
}

func recoveryRow(value string) bool {
	line, hash, ok := strings.Cut(value, ":")
	return ok && recoveryPositiveDecimal(line) && len(hash) == 4 && recoveryLowerHex(hash)
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

func recoveryEmbeddedBoundary(value string, followed bool) string {
	if followed && recoveryTerminatorSuffix(value) == "" {
		return value + "\n"
	}
	return value
}

func recoveryError(line int, message string) error {
	return fmt.Errorf("recovery operation at line %d: %s", line, message)
}
