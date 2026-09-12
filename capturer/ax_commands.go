package capturer

import (
	"errors"
	"math"
	"time"
)

// Command timings come from executor events, never source scanning or response
// duration. A gap includes all intervening work; it is not attributed overhead.
type AXCommandObservation struct {
	ItemID        string     `json:"item_id"`
	LogicalCallID string     `json:"logical_call_id,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DurationMS    *int64     `json:"duration_ms,omitempty"`
	GapBeforeMS   *int64     `json:"gap_before_ms,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
}

type AXCommandMetrics struct {
	State          string                 `json:"state"`
	Started        uint64                 `json:"started"`
	Completed      uint64                 `json:"completed"`
	UnpairedEvents uint64                 `json:"unpaired_events"`
	DurationMS     int64                  `json:"duration_ms"`
	GapMS          int64                  `json:"gap_ms"`
	Commands       []AXCommandObservation `json:"commands"`
	Coverage       string                 `json:"coverage"`
}

type AXCommandAccumulator struct {
	metrics AXCommandMetrics
	indices map[string]int
	active  int
	lastEnd time.Time
}

func (a *AXCommandAccumulator) Observe(kind, id, logicalCallID string, at time.Time, exitCode *int) error {
	if a.indices == nil {
		a.indices = make(map[string]int)
	}
	if (kind != "item_started" && kind != "item_completed") || !ValidAXIdentity(id) || at.IsZero() {
		a.metrics.UnpairedEvents++
		return nil
	}
	index, exists := a.indices[id]
	if !exists {
		if len(a.indices) >= 10000 {
			return errors.New("session exceeds 10000 command identities")
		}
		index = len(a.metrics.Commands)
		a.indices[id] = index
		a.metrics.Commands = append(a.metrics.Commands, AXCommandObservation{ItemID: id})
	}
	command := &a.metrics.Commands[index]
	if ValidAXIdentity(logicalCallID) {
		if command.LogicalCallID != "" && command.LogicalCallID != logicalCallID {
			return errors.New("conflicting command call identity")
		}
		command.LogicalCallID = logicalCallID
	}
	if kind == "item_started" {
		if command.StartedAt != nil || command.CompletedAt != nil {
			a.metrics.UnpairedEvents++
			return nil
		}
		command.StartedAt = new(at)
		a.metrics.Started++
		if a.active == 0 && !a.lastEnd.IsZero() && !at.Before(a.lastEnd) {
			gap := at.Sub(a.lastEnd).Milliseconds()
			command.GapBeforeMS = new(gap)
			a.metrics.GapMS = saturatedAXDuration(a.metrics.GapMS, gap)
		}
		a.active++
		return nil
	}
	if command.CompletedAt != nil {
		a.metrics.UnpairedEvents++
		return nil
	}
	command.CompletedAt = new(at)
	a.metrics.Completed++
	if exitCode != nil && *exitCode >= 0 && *exitCode <= 255 {
		command.ExitCode = new(*exitCode)
	}
	if command.StartedAt == nil {
		a.metrics.UnpairedEvents++
	} else {
		a.active--
		if at.Before(*command.StartedAt) {
			a.metrics.UnpairedEvents++
		} else {
			elapsed := at.Sub(*command.StartedAt).Milliseconds()
			command.DurationMS = new(elapsed)
			a.metrics.DurationMS = saturatedAXDuration(a.metrics.DurationMS, elapsed)
		}
	}
	// A backwards or unpaired timestamp cannot establish the start of an idle gap.
	if command.DurationMS != nil {
		if at.After(a.lastEnd) {
			a.lastEnd = at
		}
	} else {
		a.lastEnd = time.Time{}
	}
	return nil
}

func saturatedAXDuration(total, delta int64) int64 {
	if delta > math.MaxInt64-total {
		return math.MaxInt64
	}
	return total + delta
}

func (a *AXCommandAccumulator) Result() AXCommandMetrics {
	result := a.metrics
	result.State = "unavailable"
	result.UnpairedEvents += uint64(a.active)
	if result.Started > 0 || result.Completed > 0 || result.UnpairedEvents > 0 {
		result.State = "observed"
	}
	if result.Commands == nil {
		result.Commands = []AXCommandObservation{}
	}
	result.Coverage = "recorded CommandExecution items only; missing starts have no duration; gaps include all intervening work, not inferred batch overhead"
	return result
}
