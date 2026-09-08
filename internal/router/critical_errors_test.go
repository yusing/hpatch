package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func queueCritical(c *CriticalErrors, session string) {
	c.record(&requestFinalization{sessionID: session, failurePhase: requestFailurePrepare,
		observation: requestObservation{outcome: requestOutcomeFailed}}, incompatibleRequest("unsupported_tool_catalog", "Enable supported tools."))
}

func TestCriticalErrorsDeduplicateReserveAndRetainUntilDelivery(t *testing.T) {
	c := NewCriticalErrors()
	queueCritical(c, "one")
	queueCritical(c, "one")
	if pending := c.Pending(); len(pending) != 1 || !strings.Contains(pending[0], "2 times") {
		t.Fatalf("pending = %v", pending)
	}
	first := c.transform("one", false)
	if len(first.messages) != 1 || len(c.transform("two", false).messages) != 0 || len(c.transform("one", false).messages) != 0 {
		t.Fatal("notice crossed a session or concurrent response")
	}
	first.finish(false)
	retry := c.transform("one", false)
	if len(retry.messages) != 1 {
		t.Fatal("failed delivery lost notice")
	}
	body, err := retry.TransformJSON([]byte(`{"status":"completed","output":[{"type":"message","id":"answer","content":[]}]}`))
	if err != nil || !strings.Contains(string(body), "Enable supported tools.") {
		t.Fatalf("JSON = %s, %v", body, err)
	}
	retry.finish(true)
	if len(c.Pending()) != 0 {
		t.Fatal("delivered notice still pending")
	}
	queueCritical(c, "one")
	if len(c.transform("one", false).messages) != 0 {
		t.Fatal("repeat flooded the session")
	}
	if pending := c.Pending(); len(pending) != 1 || !strings.Contains(pending[0], "3 times") {
		t.Fatalf("repeat summary = %v", pending)
	}
}

func TestCriticalErrorsReplayRemovesOnlyOwnedSessionMessages(t *testing.T) {
	c := NewCriticalErrors()
	queueCritical(c, "one")
	queueCritical(c, "two")
	first := c.transform("one", false)
	other := c.transform("two", false)
	authored := assistantCommentaryMessage("model-owned", "Enable supported tools.")
	request := serverRequest(t, func(fields map[string]any) { fields["input"] = []any{first.messages[0], other.messages[0], authored} })
	c.stripInput(&request, "one")
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(request.fields["input"], &input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 || jsonString(input[0], "id") != jsonString(other.messages[0], "id") || jsonString(input[1], "id") != "model-owned" {
		t.Fatalf("input = %s", request.fields["input"])
	}
}

func TestCriticalErrorsStreamingPreservesSubagentResult(t *testing.T) {
	for _, subagent := range []bool{false, true} {
		c := NewCriticalErrors()
		queueCritical(c, "one")
		tr := c.transform("one", subagent)
		events, err := tr.TransformSSE([]byte(`{"type":"response.created","response":{"id":"response"}}`))
		if err != nil || len(events) != map[bool]int{false: 2, true: 1}[subagent] {
			t.Fatalf("created: %d, %v", len(events), err)
		}
		events, err = tr.TransformSSE([]byte(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","id":"answer","content":[]}]}}`))
		if err != nil || len(events) != 1 {
			t.Fatalf("terminal: %d, %v", len(events), err)
		}
		var terminal struct {
			Response struct{ Output []map[string]json.RawMessage }
		}
		if err := json.Unmarshal(events[0], &terminal); err != nil {
			t.Fatal(err)
		}
		if len(terminal.Response.Output) != 2 || jsonString(terminal.Response.Output[1], "id") != "answer" {
			t.Fatalf("substantive result replaced: %s", events[0])
		}
		tr.finish(true)
		if len(c.Pending()) != 0 {
			t.Fatal("stream delivery not acknowledged")
		}
	}
}

func TestCriticalErrorsBoundedAndCancellationSilent(t *testing.T) {
	c := NewCriticalErrors()
	for _, outcome := range []requestOutcome{requestOutcomeCompleted, requestOutcomeCanceledBeforeResponse, requestOutcomeCanceledAfterResponse} {
		c.record(&requestFinalization{observation: requestObservation{outcome: outcome}}, context.Canceled)
	}
	if len(c.Pending()) != 0 {
		t.Fatal("success or cancellation emitted a notice")
	}
	for i := range 300 {
		queueCritical(c, fmt.Sprint(i))
	}
	if len(c.entries) != 256 || c.overflow != 44 || len(c.Pending()) != 257 {
		t.Fatal("unbounded error queue or missing overflow summary")
	}
}

func TestPermanentRewriteFailureIsBadRequestAndQueued(t *testing.T) {
	c := NewCriticalErrors()
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	provider := &serverFakeProvider{}
	request := serverRequest(t, func(fields map[string]any) { fields["tool_choice"] = map[string]any{"type": "custom", "name": "exec"} })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(request.originalBody)))
	req.Header = serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
	req.Header.Set(sessionIDHeader, "one")
	output := httptest.NewRecorder()
	responsesHandler(t.Context(), time.Minute, provider, c, proxy, nil, nil)(output, req)
	if output.Code != 400 || !strings.Contains(output.Body.String(), "restricted_tool_choice") || len(provider.forwarded) != 0 {
		t.Fatalf("response %d: %s", output.Code, output.Body.String())
	}
	if len(c.Pending()) != 1 {
		t.Fatal("permanent failure was not retained")
	}
}

func TestRequestCompatibilityMissingNativeTools(t *testing.T) {
	for _, test := range []struct {
		name  string
		tools []any
		code  string
	}{
		{"editing", []any{map[string]any{"type": "function", "name": "exec_command"}}, "missing_apply_patch"},
		{"execution", []any{map[string]any{"type": "custom", "name": "apply_patch"}}, "missing_exec_command"},
	} {
		fields := map[string]json.RawMessage{"tools": mustTestJSON(t, test.tools)}
		_, replaced, err := replaceNativeTools(fields, decodeResponsesToolCatalog(fields), testInstalledTools())
		compatibility, ok := errors.AsType[*requestCompatibilityError](err)
		if replaced || !ok || compatibility.code != test.code {
			t.Fatalf("%s: %v", test.name, err)
		}
	}
}

func TestInvalidNativeCatalogIsBadRequestBeforeForwarding(t *testing.T) {
	tool := func(kind, name string) any { return map[string]any{"type": kind, "name": name} }
	for _, tools := range [][]any{
		{tool("function", "apply_patch"), tool("function", "exec_command")},
		{tool("custom", "apply_patch"), tool("custom", "exec_command")},
		{tool("custom", "apply_patch"), tool("custom", "apply_patch"), tool("function", "exec_command")},
		{tool("custom", "apply_patch"), tool("function", "exec_command"), tool("function", "exec_command")},
		{tool("custom", "apply_patch"), tool("function", "exec_command"), tool("custom", "hpatch")},
	} {
		proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
		provider := &serverFakeProvider{}
		parsed := serverRequest(t, func(fields map[string]any) { fields["input"] = []any{}; fields["tools"] = tools })
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(parsed.originalBody)))
		request.Header = serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
		request.Header.Set(sessionIDHeader, "one")
		output := httptest.NewRecorder()
		responsesHandler(t.Context(), time.Minute, provider, NewCriticalErrors(), proxy, nil, nil)(output, request)
		if output.Code != 400 || len(provider.forwarded) != 0 {
			t.Fatalf("catalog reached upstream or remained retryable: %d %s", output.Code, output.Body.String())
		}
	}
}
