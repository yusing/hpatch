package hpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment inputs describing the host tool surface. A host that exposes
// hpatch to a model pays for the tool definition as request input on every
// request, and forgoes whatever definition its native patch tool would have
// cost. Only hpatch knows its own definition text, so the host supplies both.
const (
	definitionEnvironment         = "HPATCH_TOOL_DEFINITION"
	baselineDefinitionEnvironment = "HPATCH_BASELINE_TOOL_DEFINITION"
	sessionEnvironment            = "HPATCH_SESSION_ID"
)

const sessionMarkerDirectory = "sessions"

// definitionTokens estimates the input tokens for the hpatch definition and
// the baseline definition displaced on this invocation. Cached definitions are
// still input tokens, so every invocation contributes while Sessions remains a
// distinct-session counter.
func definitionTokens(definition, baselineDefinition string) (uint64, uint64, error) {
	if definition == "" && baselineDefinition == "" {
		return 0, 0, nil
	}
	codec, err := gpt5Codec()
	if err != nil {
		return 0, 0, err
	}
	var counts [2]uint64
	for index, text := range [2]string{definition, baselineDefinition} {
		if text == "" {
			continue
		}
		count, err := codec.Count(text)
		if err != nil {
			return 0, 0, fmt.Errorf("tokenizing tool definition: %w", err)
		}
		counts[index] = uint64(count)
	}
	return counts[0], counts[1], nil
}

func definitionEntry(accounting metricAccounting) (metrics, string, error) {
	definition := trimmedDefinition(accounting.Definition)
	baseline := trimmedDefinition(accounting.BaselineDefinition)
	if accounting.SessionID == "" || (definition == "" && baseline == "") {
		return metrics{}, "", nil
	}
	definitionCount, baselineCount, err := definitionTokens(definition, baseline)
	if err != nil {
		return metrics{}, "", err
	}
	return metrics{
		DefinitionInputTokens:         definitionCount,
		BaselineDefinitionInputTokens: baselineCount,
	}, accounting.SessionID, nil
}

// claimSession records the metrics generation that will first include session.
// A generation newer than the durable metrics slot is an interrupted claim and
// is safely reused by the next writer. Callers hold the metrics lock.
func claimSession(dataDirectory, session string, currentGeneration, nextGeneration uint64) (bool, error) {
	directory := filepath.Join(dataDirectory, sessionMarkerDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, fmt.Errorf("creating session directory: %w", err)
	}
	digest := sha256.Sum256([]byte(session))
	marker := filepath.Join(directory, hex.EncodeToString(digest[:])+".seen")
	content, err := os.ReadFile(marker)
	if err == nil {
		text := strings.TrimSpace(string(content))
		if text == "" {
			return false, nil // marker from the prior format
		}
		recorded, parseErr := strconv.ParseUint(text, 10, 64)
		if parseErr != nil {
			return false, fmt.Errorf("reading session marker: %w", parseErr)
		}
		if recorded <= currentGeneration {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("reading session marker: %w", err)
	}
	if err := os.WriteFile(marker, []byte(strconv.FormatUint(nextGeneration, 10)+"\n"), 0o600); err != nil {
		return false, fmt.Errorf("recording session: %w", err)
	}
	return true, nil
}

func (m metrics) definitionOverhead() uint64 {
	if m.BaselineDefinitionInputTokens >= m.DefinitionInputTokens {
		return 0
	}
	return m.DefinitionInputTokens - m.BaselineDefinitionInputTokens
}

// meanApplyPatchTokens is the average direct apply_patch output across recorded
// effective invocations. It stands in for the patch a failed script never
// produced.
func (m metrics) meanApplyPatchTokens() float64 {
	if m.EffectiveInvocations == 0 {
		return 0
	}
	return float64(m.ApplyPatchTokens) / float64(m.EffectiveInvocations)
}

// baselineIneffectiveTokens estimates direct apply_patch output wasted by
// failures a baseline would have hit too. Those retries are a cost of editing,
// not of hpatch, so charging them to hpatch alone overstates its overhead.
func (m metrics) baselineIneffectiveTokens() float64 {
	return float64(m.BaselineFailures) * m.meanApplyPatchTokens()
}

// describeDefinitionSources reports which definition inputs the host supplied,
// so a zero definition line is not mistaken for a free tool.
func describeDefinitionSources(m metrics) string {
	switch {
	case m.Sessions == 0:
		return "not measured; host set no " + sessionEnvironment
	case m.DefinitionInputTokens == 0:
		return "baseline only; host set no " + definitionEnvironment
	case m.BaselineDefinitionInputTokens == 0:
		return "hpatch only; host set no " + baselineDefinitionEnvironment
	default:
		return "hpatch and baseline measured"
	}
}

// trimmedDefinition normalizes definition text so incidental trailing
// whitespace from shell heredocs does not shift counts between hosts.
func trimmedDefinition(text string) string {
	return strings.TrimRight(text, "\n")
}
