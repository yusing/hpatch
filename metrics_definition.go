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

// definitionTokens estimates the observed request-input tokens for the
// standalone hpatch definition added by the router and the exact Code Mode
// apply_patch section it removed. Cached definitions remain input tokens, so
// every accounted request contributes while Sessions remains a distinct-session
// counter.
func definitionTokens(definition, removedDefinition string) (uint64, uint64, error) {
	if definition == "" && removedDefinition == "" {
		return 0, 0, nil
	}
	codec, err := gpt5Codec()
	if err != nil {
		return 0, 0, err
	}
	var counts [2]uint64
	for index, text := range [2]string{definition, removedDefinition} {
		if text == "" {
			continue
		}
		count, err := codec.Count(text)
		if err != nil {
			return 0, 0, fmt.Errorf("tokenizing tool definition routing: %w", err)
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
	definitionCount, removedCount, err := definitionTokens(definition, baseline)
	if err != nil {
		return metrics{}, "", err
	}
	return metrics{
		DefinitionInputTokens:        definitionCount,
		RemovedDefinitionInputTokens: removedCount,
		DefinitionRequests:           1,
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

// describeDefinitionSources reports which routing inputs the host supplied, so a
// zero credit is not mistaken for a request that removed a free definition.
func describeDefinitionSources(m metrics) string {
	switch {
	case m.Sessions == 0:
		return "not measured (missing " + sessionEnvironment + ")"
	case m.DefinitionInputTokens == 0:
		return "removal only (missing " + definitionEnvironment + ")"
	case m.RemovedDefinitionInputTokens == 0:
		return "installation only (missing " + baselineDefinitionEnvironment + ")"
	default:
		return "installation and removal measured"
	}
}

// trimmedDefinition normalizes definition text so incidental trailing
// whitespace from shell heredocs does not shift counts between hosts.
func trimmedDefinition(text string) string {
	return strings.TrimRight(text, "\n")
}
