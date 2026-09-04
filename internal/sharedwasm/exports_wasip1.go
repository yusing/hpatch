package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
	"unsafe"

	"github.com/yusing/hpatch/internal/golex"
	"github.com/yusing/hpatch/internal/hpatchsyntax"
	"github.com/yusing/hpatch/internal/shellsyntax"
	"github.com/yusing/hpatch/internal/sourcekind"
	"github.com/yusing/hpatch/internal/verifiedrow"
)

const abiVersion = 1

const (
	operationParseRow = iota + 1
	operationParsePositiveInteger
	operationDecodeQuotedOperand
	operationClassifySourcePath
	operationIsGoIdentifier
	operationDecodeGoStringLiteral
	operationParseShellHeader
	operationInterpreterIdentity
)

const maxJavaScriptSafeInteger = 1<<53 - 1

var (
	inputBuffer  []byte
	resultBuffer []byte
	boundsBuffer [3]uint32
)

type coreError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type coreResponse struct {
	OK    bool       `json:"ok"`
	Value any        `json:"value,omitempty"`
	Error *coreError `json:"error,omitempty"`
}

//go:wasmexport hpatch_core_abi_version
func exportedABIVersion() uint32 {
	return abiVersion
}

//go:wasmexport hpatch_core_reserve_input
func reserveInput(size uint32) uint32 {
	if cap(inputBuffer) < int(size) {
		inputBuffer = make([]byte, size)
	} else {
		inputBuffer = inputBuffer[:size]
	}
	if len(inputBuffer) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(inputBuffer))))
}

//go:wasmexport hpatch_core_hash16
func hash16() uint32 {
	return verifiedrow.Hash16(inputBuffer)
}

//go:wasmexport hpatch_core_line_count
func lineCount() uint32 {
	return uint32(verifiedrow.Count(string(inputBuffer)))
}

//go:wasmexport hpatch_core_line_bounds
func lineBounds(lineNumber uint32) uint32 {
	line, ok := verifiedrow.At(string(inputBuffer), int(lineNumber))
	if !ok {
		return 0
	}
	boundsBuffer = [3]uint32{uint32(line.Start), uint32(line.ContentEnd), uint32(line.End)}
	return uint32(uintptr(unsafe.Pointer(&boundsBuffer[0])))
}

//go:wasmexport hpatch_core_invoke
func invoke(operation uint32) uint32 {
	if !utf8.Valid(inputBuffer) {
		return encodeFailure("invalid_utf8", "shared-core input is not UTF-8")
	}
	input := string(inputBuffer)
	var value any
	var err *coreError
	switch operation {
	case operationParseRow:
		value, err = parseRow(input)
	case operationParsePositiveInteger:
		value, err = parsePositiveInteger(input)
	case operationDecodeQuotedOperand:
		decoded, rest, decodeErr := hpatchsyntax.DecodeQuoted(input)
		if decodeErr != nil {
			err = &coreError{Code: "invalid_quoted_operand", Message: decodeErr.Error()}
		} else {
			value = map[string]string{"value": decoded, "rest": rest}
		}
	case operationClassifySourcePath:
		format, ok := sourcekind.Classify(input)
		if ok {
			value = format
		}
	case operationIsGoIdentifier:
		value = golex.IsIdentifier(input)
	case operationDecodeGoStringLiteral:
		decoded, decodeErr := golex.DecodeStringLiteral(input)
		if decodeErr != nil {
			err = &coreError{Code: "invalid_go_string_literal", Message: decodeErr.Error()}
		} else {
			value = decoded
		}
	case operationParseShellHeader:
		parsed, parseErr := shellsyntax.Parse(input)
		if parseErr != nil {
			err = &coreError{Code: "invalid_shell_header", Message: parseErr.Error()}
		} else {
			value = parsed
		}
	case operationInterpreterIdentity:
		value = shellsyntax.InterpreterIdentity(input)
	default:
		err = &coreError{Code: "unknown_operation", Message: "shared-core operation is unavailable"}
	}
	if err != nil {
		return encode(coreResponse{Error: err})
	}
	return encode(coreResponse{OK: true, Value: value})
}

//go:wasmexport hpatch_core_result_pointer
func resultPointer() uint32 {
	if len(resultBuffer) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(resultBuffer))))
}

func parseRow(input string) (any, *coreError) {
	reference, err := verifiedrow.ParseReference(input)
	if err != nil {
		if errors.Is(err, verifiedrow.ErrLineOutOfRange) {
			return nil, &coreError{Code: "integer_out_of_range", Message: "row line is too large"}
		}
		return nil, &coreError{Code: "invalid_row_reference", Message: err.Error()}
	}
	if reference.Line > maxJavaScriptSafeInteger {
		return nil, &coreError{Code: "integer_out_of_range", Message: "row line is too large"}
	}
	return map[string]any{"line": reference.Line, "hash": reference.Hash}, nil
}

func parsePositiveInteger(input string) (any, *coreError) {
	if input == "" || input[0] == '0' {
		return nil, &coreError{Code: "invalid_positive_integer", Message: "value must be a positive decimal integer"}
	}
	for _, character := range input {
		if character < '0' || character > '9' {
			return nil, &coreError{Code: "invalid_positive_integer", Message: "value must be a positive decimal integer"}
		}
	}
	value, err := strconv.ParseUint(input, 10, 53)
	if err != nil || value > maxJavaScriptSafeInteger {
		return nil, &coreError{Code: "integer_out_of_range", Message: "value is too large"}
	}
	return value, nil
}

func encodeFailure(code, message string) uint32 {
	return encode(coreResponse{Error: &coreError{Code: code, Message: message}})
}

func encode(response coreResponse) uint32 {
	encoded, err := json.Marshal(response)
	if err != nil {
		encoded = []byte(`{"ok":false,"error":{"code":"encoding_failure","message":"shared-core result could not be encoded"}}`)
	}
	resultBuffer = encoded
	return uint32(len(resultBuffer))
}
