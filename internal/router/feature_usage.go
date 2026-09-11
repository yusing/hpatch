package router

import "strings"

// Feature observations are operational debug evidence, not capture metrics.
// Keep categories allowlisted and identities bounded; never accept payload text,
// publisher capabilities, arbitrary attributes, or errors through this seam.
type featureUsageTrace struct {
	debug     *debugOutput
	requestID string
	threadID  string
	sessionID string
}

func (trace featureUsageTrace) record(feature, source, stage, outcome, callID, messageID string) {
	if trace.debug == nil || !validFeatureUsage(feature, source, stage, outcome) {
		return
	}
	fields := map[string]any{
		"event": "feature_usage", "schema_version": 1,
		"feature": feature, "source": source, "stage": stage, "outcome": outcome,
	}
	for key, value := range map[string]string{
		"request_id": trace.requestID, "thread_id": trace.threadID,
		"session_id": trace.sessionID, "call_id": callID, "message_id": messageID,
	} {
		if safeFeatureIdentity(value) {
			fields[key] = value
		}
	}
	trace.debug.event(fields)
}

func validFeatureUsage(feature, source, stage, outcome string) bool {
	switch feature {
	case "commentary":
		switch stage {
		case "authored":
			return source == "tool_field" && outcome == "observed"
		case "lowering":
			return source == "code_mode" && (outcome == "prepared" || outcome == "unavailable")
		case "publication":
			return (source == "shell" || source == "code_mode") &&
				(outcome == "accepted" || outcome == "blank" || outcome == "oversized" || outcome == "capacity")
		case "render":
			return (source == "tool_field" || source == "shell" || source == "code_mode") &&
				(outcome == "prepared" || outcome == "suppressed")
		}
	}
	return false
}

func safeFeatureIdentity(value string) bool {
	return value != "" && len(value) <= 256 && !strings.Contains(value, "://") && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == ':' || r == '/')
	}) == -1
}
