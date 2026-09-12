package router

import (
	"encoding/json"
	"strings"

	"github.com/yusing/mekugi/capturer"
	"mvdan.cc/sh/v3/syntax"
)

const axCarrierCallIDPrefix = "# mekugi:ax:call_id="

// Read explicit router-owned carrier metadata, not arbitrary environment
// assignments. Direct external commands and wrapped workers share this marker;
// their executable names cannot establish a logical parent. This is local
// correlation evidence, not an authentication claim about the supplied rollout.
func inspectionAXCallID(encoded json.RawMessage) string {
	var command string
	if json.Unmarshal(encoded, &command) != nil {
		var argv []string
		if json.Unmarshal(encoded, &argv) != nil || len(argv) != 3 ||
			(shellInterpreterName(argv[0]) != "bash" && shellInterpreterName(argv[0]) != "sh") ||
			(argv[1] != "-c" && argv[1] != "-lc") {
			return ""
		}
		command = argv[2]
	}
	metadata, body, newline := strings.Cut(command, "\n")
	identity, marked := strings.CutPrefix(metadata, axCarrierCallIDPrefix)
	if !newline || !marked || !capturer.ValidAXIdentity(identity) {
		return ""
	}
	program, err := syntax.NewParser().Parse(strings.NewReader(body), "")
	if err != nil || len(program.Stmts) == 0 {
		return ""
	}
	return identity
}
