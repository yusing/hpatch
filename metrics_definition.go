package hpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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

// definitionTokens estimates the input cost of the hpatch tool definition the
// host installed and of the baseline patch tool it replaced. Both are counted
// only once per session, because a definition sent on every request is served
// from the provider's prompt cache after the first.
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

// definitionEntry returns the once-per-session definition contribution for this
// invocation. It returns a zero entry when the host named no session or when
// this session already recorded its definitions, so repeated invocations within
// one session do not multiply a cached cost.
func definitionEntry(dataDirectory string) (metrics, error) {
	session := os.Getenv(sessionEnvironment)
	definition := trimmedDefinition(os.Getenv(definitionEnvironment))
	baseline := trimmedDefinition(os.Getenv(baselineDefinitionEnvironment))
	if session == "" || (definition == "" && baseline == "") {
		return metrics{}, nil
	}
	fresh, err := claimSession(dataDirectory, session)
	if err != nil || !fresh {
		return metrics{}, err
	}
	definitionCount, baselineCount, err := definitionTokens(definition, baseline)
	if err != nil {
		return metrics{}, err
	}
	return metrics{
		Sessions:                      1,
		DefinitionInputTokens:         definitionCount,
		BaselineDefinitionInputTokens: baselineCount,
	}, nil
}

// claimSession reports whether session is seen here for the first time,
// recording it so later invocations in the same session do not re-charge the
// definition. Callers hold the metrics lock.
func claimSession(dataDirectory, session string) (bool, error) {
	directory := filepath.Join(dataDirectory, sessionMarkerDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, fmt.Errorf("creating session directory: %w", err)
	}
	digest := sha256.Sum256([]byte(session))
	marker := filepath.Join(directory, hex.EncodeToString(digest[:])+".seen")
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("recording session: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("recording session: %w", err)
	}
	return true, nil
}

// definitionOverhead is the per-session hpatch definition input attributable to
// hpatch after crediting the baseline definition it displaced. A host whose
// native patch tool costs more than hpatch's yields no overhead rather than a
// negative one, so the reported reduction never borrows from the baseline's
// definition.
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
