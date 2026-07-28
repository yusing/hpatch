package hpatch

import "errors"

type textSpanVariant uint8

const (
	textSpanNone textSpanVariant = iota
	textSpanSingle
	textSpanMultiple
)

type commandAttempt struct {
	recognized bool
	textSpan   textSpanVariant
}

type commandOutcome struct {
	blockRecovered bool
}

const (
	textSpanVariantCount = 2
	blockOutcomeCount    = 4
)

type invocationMetrics struct {
	Commands      commandMetrics                      `json:"commands"`
	TextSpans     [textSpanVariantCount]commandMetric `json:"text_spans"`
	BlockOutcomes [blockOutcomeCount]uint64           `json:"block_outcomes"`
	Reasons       [failureReasonCount]uint64          `json:"reasons"`
	// CommandReasons attributes each error to the command that raised it. The
	// flat Reasons histogram cannot answer which primitive a reason belongs
	// to, which is the question that decides whether a command earns its
	// place in the language.
	CommandReasons [commandCount][failureReasonCount]uint64 `json:"command_reasons"`
}

var textSpanVariantNames = [textSpanVariantCount]string{"single", "multiple"}

var blockOutcomeNames = [blockOutcomeCount]string{
	"bsel exact", "bsel recovered",
	"bsel_next exact", "bsel_next recovered",
}

func (m *invocationMetrics) invoke(operation string, attempt commandAttempt) {
	index := commandOperationIndex(operation)
	if index < 0 {
		return
	}
	m.Commands[index].Invocations++
	if operation == "tsel" && attempt.textSpan != textSpanNone {
		m.TextSpans[attempt.textSpan-1].Invocations++
	}
}

func (m *invocationMetrics) fail(operation string, attempt commandAttempt, reason failureReason) {
	index := commandOperationIndex(operation)
	if index < 0 {
		return
	}
	m.Commands[index].Errors++
	if operation == "tsel" && attempt.textSpan != textSpanNone {
		m.TextSpans[attempt.textSpan-1].Errors++
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

func (m *invocationMetrics) recordOutcome(operation string, outcome commandOutcome) {
	if index := blockOutcomeIndex(operation, outcome.blockRecovered); index >= 0 {
		m.BlockOutcomes[index]++
	}
}

func blockOutcomeIndex(operation string, recovered bool) int {
	base := -1
	switch operation {
	case "bsel":
		base = 0
	case "bsel_next":
		base = 2
	}
	if base < 0 {
		return -1
	}
	if recovered {
		return base + 1
	}
	return base
}

type failureReason uint8

const (
	reasonSyntax failureReason = iota
	reasonCoordinateBounds
	reasonOccurrenceMissing
	reasonAnchorMissing
	reasonAnchorAmbiguous
	reasonInvalidCount
	reasonOrderOrOverlap
	reasonEditConflict
	reasonActiveFile
	reasonSelectionRequired
	reasonClipboardEmpty
	reasonFileMissing
	reasonFileConflict
	reasonPath
	reasonOther
	failureReasonCount
)

var failureReasonNames = [failureReasonCount]string{
	"syntax",
	"coordinate-bounds",
	"occurrence-missing",
	"anchor-missing",
	"anchor-ambiguous",
	"invalid-count",
	"order-or-overlap",
	"edit-conflict",
	"active-file",
	"selection-required",
	"clipboard-empty",
	"file-missing",
	"file-conflict",
	"path",
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
