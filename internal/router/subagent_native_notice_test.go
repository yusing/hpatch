package router

import (
	"encoding/json"
	"testing"
)

func TestNativeCollaborationNoticesAreNotDuplicated(t *testing.T) {
	for _, name := range []string{"send_message", "wait_agent"} {
		t.Run(name, func(t *testing.T) {
			item := map[string]json.RawMessage{
				"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON("collaboration"),
				"name": mustMarshalJSON(name), "call_id": mustMarshalJSON("native-notice"),
				"arguments": mustMarshalJSON(`{"target":"/root/worker"}`),
			}
			original := string(mustMarshalJSON(item))
			message, matched := subagentCallCommentary(item,
				map[string]struct{}{functionToolKey("collaboration", name): {}},
				"model", "medium", "/root")
			if matched || message != nil {
				t.Fatal("router duplicated native collaboration notice")
			}
			if string(mustMarshalJSON(item)) != original {
				t.Fatal("native collaboration call changed")
			}
		})
	}
}

