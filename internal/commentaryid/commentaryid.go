// Package commentaryid owns the reserved IDs used for router-generated messages.
package commentaryid

import "strings"

const (
	OperationPrefix = "msg_hpatch_commentary_"
	SubagentPrefix  = "msg_hpatch_subagent_commentary_"
)

// Generated reports membership in a router-owned message ID namespace. Message
// text and phase are not provenance: a model may emit identical commentary.
func Generated(id string) bool {
	return strings.HasPrefix(id, OperationPrefix) || strings.HasPrefix(id, SubagentPrefix)
}
