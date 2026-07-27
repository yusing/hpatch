package hpatch

import "errors"

type coordinateVariant uint8

const (
	coordinateNone coordinateVariant = iota
	coordinateAbsolute
	coordinateRelative
)

type textSpanVariant uint8

const (
	textSpanNone textSpanVariant = iota
	textSpanSingle
	textSpanMultiple
)

type commandAttempt struct {
	recognized bool
	coordinate coordinateVariant
	textSpan   textSpanVariant
}

type commandOutcome struct {
	blockRecovered bool
}

const (
	selectorVariantCount = 6
	textSpanVariantCount = 2
	blockOutcomeCount    = 4
)

type invocationMetrics struct {
	Commands         commandMetrics                      `json:"commands"`
	SelectorVariants [selectorVariantCount]commandMetric `json:"selector_variants"`
	TextSpans        [textSpanVariantCount]commandMetric `json:"text_spans"`
	BlockOutcomes    [blockOutcomeCount]uint64           `json:"block_outcomes"`
	Reasons          [failureReasonCount]uint64          `json:"reasons"`
	// CommandReasons attributes each error to the command that raised it. The
	// flat Reasons histogram cannot answer which primitive a reason belongs
	// to, which is the question that decides whether a command earns its
	// place in the language.
	CommandReasons [commandCount][failureReasonCount]uint64 `json:"command_reasons"`
}

var selectorVariantNames = [selectorVariantCount]string{
	"sel absolute", "sel relative",
	"tsel absolute", "tsel relative",
	"rsel absolute", "rsel relative",
}

var textSpanVariantNames = [textSpanVariantCount]string{"single", "multiple"}

var blockOutcomeNames = [blockOutcomeCount]string{
	"bsel exact", "bsel recovered",
	"bsel_next exact", "bsel_next recovered",
}

func (m *invocationMetrics) invoke(operation string, attempt commandAttempt) {
	m.Commands.invoke(operation)
	if index := selectorVariantIndex(operation, attempt.coordinate); index >= 0 {
		m.SelectorVariants[index].Invocations++
	}
	if operation == "tsel" && attempt.textSpan != textSpanNone {
		m.TextSpans[attempt.textSpan-1].Invocations++
	}
}

func (m *invocationMetrics) fail(operation string, attempt commandAttempt, reason failureReason) {
	if commandOperationIndex(operation) < 0 {
		return
	}
	m.Commands.fail(operation)
	if index := selectorVariantIndex(operation, attempt.coordinate); index >= 0 {
		m.SelectorVariants[index].Errors++
	}
	if operation == "tsel" && attempt.textSpan != textSpanNone {
		m.TextSpans[attempt.textSpan-1].Errors++
	}
	m.Reasons[reason]++
	m.CommandReasons[commandOperationIndex(operation)][reason]++
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

func selectorVariantIndex(operation string, variant coordinateVariant) int {
	if variant != coordinateAbsolute && variant != coordinateRelative {
		return -1
	}
	base := -1
	switch operation {
	case "sel":
		base = 0
	case "tsel":
		base = 2
	case "rsel":
		base = 4
	}
	if base < 0 {
		return -1
	}
	return base + int(variant-coordinateAbsolute)
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
	reasonRelativeDisabled
	reasonRelativeState
	reasonCoordinateBounds
	reasonOccurrenceMissing
	reasonAnchorMissing
	reasonAnchorAmbiguous
	reasonInvalidCount
	reasonOrderOrOverlap
	reasonEditConflict
	reasonActiveFile
	reasonSelectionRequired
	reasonFileMissing
	reasonFileConflict
	reasonPath
	reasonOther
	failureReasonCount
)

var failureReasonNames = [failureReasonCount]string{
	"syntax",
	"relative-disabled",
	"relative-state",
	"coordinate-bounds",
	"occurrence-missing",
	"anchor-missing",
	"anchor-ambiguous",
	"invalid-count",
	"order-or-overlap",
	"edit-conflict",
	"active-file",
	"selection-required",
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
