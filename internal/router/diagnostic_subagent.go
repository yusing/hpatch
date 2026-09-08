package router

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fixtures use isolated synthetic thread identities, but the same ancestry,
// collection, deduplication, and root-copy replay path as ordinary activity.
func (t *hpatchResponseTransform) diagnosticActivity(run, agent, source, kind, text string) {
	if t.ctx.Err() != nil {
		return
	}
	root := t.threadID
	child := "hpatch-diagnostic:" + root + ":" + agent
	t.proxy.activity.observe(root, "", "/root", false)
	t.proxy.activity.observe(child, root, "/root/diagnostic_"+agent, true)
	t.proxy.activity.collect(child, run+":"+source, kind, "Diagnostic fixture: "+text)
}

func (t *hpatchResponseTransform) subagentDiagnosticResponse(request *parsedResponsesRequest, args []string, items []map[string]json.RawMessage) (*http.Response, bool, error) {
	finish := func(text string) (*http.Response, bool, error) {
		return diagnosticHTTPResponse(request, []map[string]json.RawMessage{diagnosticMessage("final_answer", text)}), true, nil
	}
	mode := "all"
	if len(args) == 1 {
		mode = args[0]
	}
	if len(args) > 1 || len(args) == 1 && mode != "stream" && mode != "deferred" && mode != "wait" {
		return finish("Use :hpatch_diag subagent_commentary [stream|deferred|wait]. All activity is synthetic; no provider or real agent is used.")
	}
	if t.subagentTurn {
		return finish("Subagent commentary diagnostic is available only in a root conversation.")
	}
	callCount, resultCount := 0, 0
	var expectedCall string
	for _, item := range items {
		id := jsonString(item, "call_id")
		if !strings.HasPrefix(id, diagnosticCallPrefix) {
			continue
		}
		switch jsonString(item, "type") {
		case "function_call", "custom_tool_call":
			callCount++
			expectedCall = id
			history, exists := t.proxy.history(t.historySessionID, id)
			if !exists || !history.diagnostic {
				return finish("Diagnostic playback history is unavailable. No payload was repeated.")
			}
		case "function_call_output", "custom_tool_call_output":
			resultCount++
		}
	}
	if callCount == 0 {
		t.proxy.activity.discardDiagnostics(t.threadID)
	}
	if callCount > 1 || resultCount > 1 || resultCount > callCount {
		return finish("Diagnostic playback history is incomplete or duplicated. No payload was repeated.")
	}
	if callCount == 1 && resultCount == 0 {
		return finish("Diagnostic tool result is pending. No payload was repeated; native continuation remains host-owned.")
	}
	for _, item := range items {
		if !strings.HasPrefix(jsonString(item, "call_id"), diagnosticCallPrefix) {
			continue
		}
		if jsonString(item, "type") != "custom_tool_call_output" && jsonString(item, "type") != "function_call_output" {
			continue
		}
		if jsonString(item, "call_id") != expectedCall {
			return finish("Diagnostic result does not match its call. No payload was repeated.")
		}
		if strings.HasSuffix(jsonString(item, "call_id"), "_wait") {
			history, exists := t.proxy.history(t.historySessionID, jsonString(item, "call_id"))
			if !exists || !history.diagnostic {
				return finish("Diagnostic wait history is unavailable. No wait was repeated.")
			}
			return finish("Diagnostic wait fixture returned through the native host. Commentary was offered before the stream terminal; inspect the TUI ordering to determine whether the wait had actually begun. No real subagent was spawned.")
		}
		if _, err := diagnosticToolOutput(item["output"]); err != nil {
			return finish("Diagnostic continuation stopped: " + err.Error() + ". No further tool was requested.")
		}
		return finish("Diagnostic fixture result: substantive answer unchanged. Deferred activity was offered once on this continuation. Synthetic playback does not establish real child execution. Closed responses cannot receive more commentary.")
	}
	if mode == "wait" {
		return t.waitDiagnosticResponse(request)
	}
	run := diagnosticCallPrefix + rand.Text()
	output := []map[string]json.RawMessage{diagnosticMessage("commentary", "Local subagent_commentary diagnostic. Synthetic alpha/beta activity traverses the shared collector; no provider request or real subagent is used.")}
	var fixtureSteps []func()
	if mode == "stream" || mode == "all" {
		fixtureSteps = []func(){
			func() { t.diagnosticActivity(run, "alpha", "read", "operation", "Reading hook failure handling.") },
			func() {
				t.diagnosticActivity(run, "beta", "test", "operation", "Running the focused router tests; no result observed yet.")
			},
			func() {
				t.diagnosticActivity(run, "alpha", "reply", "reply", "Reply received: one failure path needs attention.")
			},
		}
		if !request.streamResponse {
			for _, step := range fixtureSteps {
				step()
			}
		}
	}

	var afterClose func()
	if mode == "deferred" || mode == "all" {
		callID := run + "_continuation"
		item := map[string]json.RawMessage{
			"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"),
			"id": mustMarshalJSON("item_" + callID), "call_id": mustMarshalJSON(callID),
			"input": mustMarshalJSON(":"), "status": mustMarshalJSON("completed"),
		}
		history, err := t.translateTool("shell", callID, ":", item)
		if err != nil {
			return nil, true, err
		}
		history.diagnostic = true
		t.local[callID] = history
		output = append(output, item)
		afterClose = func() {
			if t.ctx.Err() == nil {
				t.diagnosticActivity(run, "beta", "deferred", "operation", "Tests started before this continuation; no result observed yet.")
			}
		}
	} else {
		output = append(output, diagnosticMessage("final_answer", "Diagnostic fixture result: substantive answer unchanged. Synthetic playback does not establish real child execution."))
	}
	response := diagnosticHTTPResponse(request, output)
	if request.streamResponse && len(fixtureSteps) != 0 {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, true, err
		}
		_ = response.Body.Close()
		response.Body = &diagnosticActivityBody{frames: bytes.SplitAfter(body, []byte("\n\n")), steps: fixtureSteps}
	}
	if afterClose != nil {
		response.Body = &diagnosticBoundaryBody{ReadCloser: response.Body, afterClose: afterClose}
	}
	return response, true, nil
}

// Closing the consumed response establishes a genuine boundary before the host's
// next request. This hook never extends a production response or schedules turns.
type diagnosticBoundaryBody struct {
	io.ReadCloser
	afterClose func()
}

func (b *diagnosticBoundaryBody) Close() error {
	err := b.ReadCloser.Close()
	if b.afterClose != nil {
		f := b.afterClose
		b.afterClose = nil
		f()
	}
	return err
}

// Return one SSE frame at a time so fixture observations happen between real
// transformer invocations rather than being preformatted output labels.
type diagnosticActivityBody struct {
	ctx    context.Context
	frames [][]byte
	steps  []func()
	index  int
}

func (b *diagnosticActivityBody) Read(p []byte) (int, error) {
	if b.ctx != nil && b.ctx.Err() != nil {
		return 0, b.ctx.Err()
	}
	if len(b.frames) == 0 {
		return 0, io.EOF
	}
	if b.index > 0 && b.index <= len(b.steps) {
		b.steps[b.index-1]()
		b.steps[b.index-1] = func() {}
	}
	n := copy(p, b.frames[0])
	b.frames[0] = b.frames[0][n:]
	if len(b.frames[0]) == 0 {
		b.frames = b.frames[1:]
		b.index++
	}
	return n, nil
}
func (b *diagnosticActivityBody) Close() error { b.frames = nil; return nil }

// Source: Codex multi_agents_spec.rs wait_agent_tool_parameters_v2. This
// provider-free fixture is available only for the mailbox wait schema, never
// the v1 targets-based wait. Timeout policy stays with the advertised catalog.
func diagnosticWaitNamespace(tools *responsesToolCatalog) (string, bool) {
	sections := []*responsesToolSection{tools.top}
	for _, group := range tools.additional {
		sections = append(sections, group.tools)
	}
	for _, section := range sections {
		if section == nil || section.err != nil {
			continue
		}
		for i, namespace := range section.tools {
			if namespace == nil || namespace.Type != "namespace" {
				continue
			}
			node := section.nodes[i]
			if node == nil || node.nested == nil || node.nested.err != nil {
				continue
			}
			for _, tool := range node.nested.tools {
				if tool == nil || tool.Type != "function" || tool.Name != "wait_agent" {
					continue
				}
				var schema struct {
					Type       string
					Required   []string
					Properties map[string]struct{ Type, Description string }
				}
				if json.Unmarshal(tool.rawField("parameters"), &schema) != nil || schema.Type != "object" || len(schema.Required) != 0 || len(schema.Properties) != 1 {
					continue
				}
				timeout := schema.Properties["timeout_ms"]
				var defaultMS, minMS, maxMS int
				n, _ := fmt.Sscanf(timeout.Description, "Timeout in milliseconds. Defaults to %d, min %d, max %d.", &defaultMS, &minMS, &maxMS)
				if timeout.Type == "number" && n == 3 && minMS > 0 && minMS <= 10000 && maxMS >= 10000 {
					return namespace.Name, true
				}
			}
		}
	}
	return "", false
}

func (t *hpatchResponseTransform) waitDiagnosticResponse(request *parsedResponsesRequest) (*http.Response, bool, error) {
	namespace, available := diagnosticWaitNamespace(request.responseTools())
	if !available || !request.streamResponse {
		return diagnosticHTTPResponse(request, []map[string]json.RawMessage{diagnosticMessage("final_answer", "Diagnostic wait case unavailable: this case requires SSE and a catalog-supported bounded native mailbox wait. No substitute operation or agent interruption was attempted.")}), true, nil
	}
	run := diagnosticCallPrefix + rand.Text()
	callID := run + "_wait"
	call := map[string]json.RawMessage{
		"type": mustMarshalJSON("function_call"), "namespace": mustMarshalJSON(namespace), "name": mustMarshalJSON("wait_agent"),
		"id": mustMarshalJSON("item_" + callID), "call_id": mustMarshalJSON(callID), "arguments": mustMarshalJSON(`{"timeout_ms":10000}`), "status": mustMarshalJSON("completed"),
	}
	t.local[callID] = hpatchHistory{toolName: "__hpatch_diag_wait", upstreamItem: call, diagnostic: true, carrierName: "wait_agent", carrierKind: codeModeCarrierFunction, carrierPayload: jsonString(call, "arguments"), replayCarrier: true}
	response := diagnosticHTTPResponse(request, []map[string]json.RawMessage{
		diagnosticMessage("commentary", "Diagnostic wait fixture: requesting a 10-second native mailbox wait. The diagnostic stream remains open for two seconds after the complete call, then offers synthetic activity. This does not prove that native execution starts before the terminal."),
		call,
	})
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, true, err
	}
	frames := bytes.SplitAfter(raw, []byte("\n\n"))
	steps := make([]func(), len(frames))
	for i := range steps {
		steps[i] = func() {}
	}
	for i, frame := range frames {
		if i > 0 && bytes.HasPrefix(frame, []byte("event: response.completed")) {
			steps[i-1] = func() {
				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()
				select {
				case <-t.ctx.Done():
					return
				case <-timer.C:
				}
				t.diagnosticActivity(run, "alpha", "wait-stream", "operation", "Activity offered after the native wait call and before the response terminal.")
			}
		}
	}
	response.Body = &diagnosticActivityBody{frames: frames, steps: steps, ctx: t.ctx}
	return response, true, nil
}
