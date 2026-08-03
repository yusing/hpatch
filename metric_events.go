package hpatch

import "errors"

type targetVariant uint8

const (
	targetVariantNone targetVariant = iota
	targetVariantLine
	targetVariantRange
	targetVariantTextSingle
	targetVariantTextMultiple
)

const targetVariantCount = 4

var targetVariantNames = [targetVariantCount]string{"line", "range", "text-single", "text-multiple"}

type commandAttempt struct {
	recognized bool
	target     targetVariant
}

type invocationMetrics struct {
	Commands commandMetrics                    `json:"commands"`
	Targets  [targetVariantCount]commandMetric `json:"targets"`
	Reasons  [failureReasonCount]uint64        `json:"reasons"`
	// CommandReasons attributes each error to the command that raised it.
	CommandReasons [commandCount][failureReasonCount]uint64 `json:"command_reasons"`
}

func (m *invocationMetrics) invoke(operation string, attempt commandAttempt) {
	index := commandOperationIndex(operation)
	if index < 0 {
		return
	}
	m.Commands[index].Invocations++
	if attempt.target != targetVariantNone {
		m.Targets[attempt.target-1].Invocations++
	}
}

func (m *invocationMetrics) fail(operation string, attempt commandAttempt, reason failureReason) {
	index := commandOperationIndex(operation)
	if index < 0 {
		return
	}
	m.Commands[index].Errors++
	if attempt.target != targetVariantNone {
		m.Targets[attempt.target-1].Errors++
	}
	m.Reasons[reason]++
	m.CommandReasons[index][reason]++
}

func (m *invocationMetrics) invokeFailure(operation string, attempt commandAttempt, reason failureReason) {
	if !attempt.recognized {
		return
	}
	m.invoke(operation, attempt)
	m.fail(operation, attempt, reason)
}

type failureReason uint8

const (
	reasonSyntax failureReason = iota
	reasonRowMissing
	reasonRowStale
	reasonOccurrenceMissing
	reasonInvalidCount
	reasonTargetOrder
	reasonEditConflict
	reasonActiveFile
	reasonInitialization
	reasonFilePath
	reasonLanguageSyntax
	reasonOther
	failureReasonCount
)

// Existing filesystem owners classify their more specific failures into the
// HPATCH/2 file-path aggregate without duplicating persisted reason families.
const (
	reasonFileMissing  = reasonFilePath
	reasonFileConflict = reasonFilePath
	reasonPath         = reasonFilePath
)

var failureReasonNames = [failureReasonCount]string{
	"script-syntax",
	"row-missing",
	"row-stale",
	"occurrence-missing",
	"invalid-count",
	"target-order",
	"edit-conflict",
	"active-file",
	"initialization",
	"file-path",
	"language-syntax",
	"other",
}

type reasonedError struct {
	reason failureReason
	err    error
}

func (e *reasonedError) Error() string { return e.err.Error() }
func (e *reasonedError) Unwrap() error { return e.err }

func withReason(reason failureReason, err error) error {
	if err == nil {
		return nil
	}
	return &reasonedError{reason: reason, err: err}
}

func reasonOf(err error, fallback failureReason) failureReason {
	if classified, ok := errors.AsType[*reasonedError](err); ok {
		return classified.reason
	}
	if command, ok := errors.AsType[*commandError](err); ok {
		return command.Reason
	}
	return fallback
}
