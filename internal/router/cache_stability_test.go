package router

import (
	"strings"
	"testing"
)

func TestKnownReplayCarrierRejectsTamperedIdentity(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	history := hpatchHistory{carrierName: "exec", patch: "patch", report: "report"}
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-1": history}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		kind string
		tool string
		want string
	}{
		{name: "name", kind: "custom_tool_call", tool: "lookup", want: "changed carrier name"},
		{name: "type", kind: "function_call", tool: "exec", want: "changed item type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{map[string]any{
				"type": test.kind, "name": test.tool, "call_id": "call-1", "input": history.carrierInput(""),
			}}}))
			if err != nil {
				t.Fatal(err)
			}
			err = proxy.restoreInputPrefix(&request, "session", "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestKnownReplayOutputRemainsValid(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch("session", map[string]hpatchHistory{"call-1": {carrierName: "exec"}}); err != nil {
		t.Fatal(err)
	}
	request, err := parseResponsesRequest(mustTestJSON(t, map[string]any{"input": []any{map[string]any{
		"type": "custom_tool_call_output", "call_id": "call-1", "output": "ok",
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.restoreInputPrefix(&request, "session", ""); err != nil {
		t.Fatal(err)
	}
}
