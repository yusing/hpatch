package router

import (
	"encoding/json"
	"time"
)

// tokenCounts carries one provider-authoritative terminal usage observation to
// Mentor Handoff, user-only commentary, and the capturer. Aggregate reporting
// belongs to the capture package.
type tokenCounts struct {
	InputTokens         uint64
	UncachedInputTokens uint64
	OutputTokens        uint64
	ReasoningTokens     uint64
}

type requestOutcome uint8

const (
	requestOutcomeUnknown requestOutcome = iota
	requestOutcomeCompleted
	requestOutcomeFailed
	requestOutcomeCanceledBeforeResponse
	requestOutcomeCanceledAfterResponse
	requestOutcomeTimedOut
	requestOutcomeStreamIdleTimedOut
)

func (outcome requestOutcome) String() string {
	switch outcome {
	case requestOutcomeCompleted:
		return "completed"
	case requestOutcomeFailed:
		return "failed"
	case requestOutcomeCanceledBeforeResponse:
		return "canceled_before_response"
	case requestOutcomeCanceledAfterResponse:
		return "canceled_after_response"
	case requestOutcomeTimedOut:
		return "timed_out"
	case requestOutcomeStreamIdleTimedOut:
		return "stream_idle_timed_out"
	default:
		return "unknown"
	}
}

type requestObservation struct {
	outcome requestOutcome

	totalDuration    time.Duration
	upstreamDuration time.Duration
	usageCounts      tokenCounts
	usageObserved    bool
}

func usageFromResponsePayload(body []byte, streamEvent bool) (tokenCounts, bool) {
	var envelope struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
		Usage    json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return tokenCounts{}, false
	}
	raw := envelope.Usage
	if streamEvent {
		switch envelope.Type {
		case "response.completed", "response.failed", "response.incomplete":
		default:
			return tokenCounts{}, false
		}
		var terminal struct {
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(envelope.Response, &terminal) != nil {
			return tokenCounts{}, false
		}
		raw = terminal.Usage
	}
	var usage struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
		InputDetails struct {
			CachedTokens uint64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails struct {
			ReasoningTokens uint64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &usage) != nil {
		return tokenCounts{}, false
	}
	return tokenCounts{
		InputTokens:         usage.InputTokens,
		UncachedInputTokens: usage.InputTokens - min(usage.InputTokens, usage.InputDetails.CachedTokens),
		OutputTokens:        usage.OutputTokens,
		ReasoningTokens:     usage.OutputDetails.ReasoningTokens,
	}, true
}
