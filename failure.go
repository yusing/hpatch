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

// Existing filesystem failures share the public HPATCH/2 file-path reason.
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
