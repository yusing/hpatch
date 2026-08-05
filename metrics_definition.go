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

// Session claims share the metrics slot revision so a format reset cannot be
// suppressed by attribution state from an older aggregate.
const sessionMarkerDirectory = "sessions"

// claimSession records the metrics generation that will first include session.
// A generation newer than the durable metrics slot is an interrupted claim and
// is safely reused by the next writer. Callers hold the metrics lock.
func claimSession(dataDirectory, session string, currentGeneration, nextGeneration uint64, reset bool) (bool, error) {
	directory := filepath.Join(dataDirectory, sessionMarkerDirectory, metricsMagic)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, fmt.Errorf("creating session directory: %w", err)
	}
	digest := sha256.Sum256([]byte(session))
	marker := filepath.Join(directory, hex.EncodeToString(digest[:])+".seen")
	if !reset {
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
		return "not measured (missing caller session)"
	case m.DefinitionInputTokens == 0:
		return "removal only"
	case m.RemovedDefinitionInputTokens == 0:
		return "installation only"
	default:
		return "installation and removal measured"
	}
}
