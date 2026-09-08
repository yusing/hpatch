package router

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const (
	diagnosticPrefix        = ":hpatch_diag"
	diagnosticCallPrefix    = "call_hpatch_diag_"
	diagnosticMessagePrefix = "msg_hpatch_diag_"
	diagnosticSetup         = "mktemp -d -t hpatch-diag.XXXXXXXXXX"
)

// Only actual user messages select playback. Tool output, agent messages, quoted
// examples, and older commands cannot start a new run.
func diagnosticUserText(item map[string]json.RawMessage) (string, bool) {
	if jsonString(item, "role") != "user" || jsonString(item, "type") != "" && jsonString(item, "type") != "message" {
		return "", false
	}
	var text string
	if json.Unmarshal(item["content"], &text) == nil {
		return text, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(item["content"], &parts) != nil {
		return "", true
	}
	var joined strings.Builder
	for _, part := range parts {
		if part.Type == "input_text" || part.Type == "text" {
			joined.WriteString(part.Text)
		}
	}
	return joined.String(), true
}

func diagnosticCommand(text string) (target string, args []string, matched bool, err error) {
	text = strings.TrimSpace(text)
	rest, found := strings.CutPrefix(text, diagnosticPrefix)
	if !found || rest != "" && !strings.ContainsRune(" \t\r\n", rune(rest[0])) {
		return "", nil, false, nil
	}
	matched = true
	program, parseErr := syntax.NewParser().Parse(strings.NewReader("hpatch_diag "+rest), "")
	if parseErr != nil || len(program.Stmts) != 1 {
		return "", nil, true, errors.New("expected :hpatch_diag <target> [<args...>] with literal arguments")
	}
	statement := program.Stmts[0]
	call, ok := statement.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) < 2 || len(call.Assigns) != 0 || len(statement.Redirs) != 0 || statement.Background || statement.Negated || statement.Semicolon.IsValid() {
		return "", nil, true, errors.New("expected :hpatch_diag <target> [<args...>] with literal arguments")
	}
	for _, word := range call.Args[1:] {
		if !shellCatLiteralParts(word.Parts, false) {
			return "", nil, true, errors.New("diagnostic arguments must be literal; shell expansion is not supported")
		}
		value, expandErr := expand.Literal(nil, word)
		if expandErr != nil {
			return "", nil, true, expandErr
		}
		args = append(args, value)
	}
	return args[0], args[1:], true, nil
}

// Retire completed/interrupted local turns before normal request preparation.
// Preserve unrelated catalog, system/developer, and user items; only the command
// and router-authored playback items are omitted from subsequent provider input.
func stripPastDiagnosticTurns(request *parsedResponsesRequest) {
	var items []map[string]json.RawMessage
	if json.Unmarshal(request.fields["input"], &items) != nil {
		return
	}
	lastUser := -1
	for index, item := range items {
		if _, user := diagnosticUserText(item); user {
			lastUser = index
		}
	}
	localTurn, changed := false, false
	ownedTurns := make(map[int]bool)
	commandIndex := -1
	for index, item := range items {
		if text, user := diagnosticUserText(item); user {
			commandIndex = -1
			if _, _, matched, _ := diagnosticCommand(text); matched && index < lastUser {
				commandIndex = index
			}
		} else if commandIndex >= 0 && (strings.HasPrefix(jsonString(item, "call_id"), diagnosticCallPrefix) || strings.HasPrefix(jsonString(item, "id"), diagnosticMessagePrefix)) {
			ownedTurns[commandIndex] = true
		}
	}
	kept := make([]map[string]json.RawMessage, 0, len(items))
	for index, item := range items {
		if _, user := diagnosticUserText(item); user {
			localTurn = ownedTurns[index]
			if localTurn {
				changed = true
				continue
			}
		}
		if localTurn && (strings.HasPrefix(jsonString(item, "call_id"), diagnosticCallPrefix) || strings.HasPrefix(jsonString(item, "id"), diagnosticMessagePrefix)) {
			changed = true
			continue
		}
		kept = append(kept, item)
	}
	if changed {
		request.setInput(mustMarshalJSON(kept))
	}
}

func (t *hpatchResponseTransform) diagnosticResponse(request *parsedResponsesRequest) (*http.Response, bool, error) {
	if t == nil {
		return nil, false, nil
	}
	var items []map[string]json.RawMessage
	var userText string
	if json.Unmarshal(request.fields["input"], &userText) != nil {
		if json.Unmarshal(request.fields["input"], &items) != nil {
			return nil, false, nil
		}
		lastUser := -1
		for index, item := range items {
			if text, user := diagnosticUserText(item); user {
				userText, lastUser = text, index
			}
		}
		if lastUser == -1 {
			return nil, false, nil
		}
		items = items[lastUser+1:]
	}
	target, args, matched, commandErr := diagnosticCommand(userText)
	if !matched {
		t.proxy.activity.discardDiagnostics(t.threadID)
		return nil, false, nil
	}
	finish := func(message string) (*http.Response, bool, error) {
		return diagnosticHTTPResponse(request, []map[string]json.RawMessage{diagnosticMessage("final_answer", message)}), true, nil
	}
	if commandErr != nil {
		return finish(commandErr.Error() + ". Available targets: cat_write_translation, subagent_commentary.")
	}
	if target == "subagent_commentary" {
		return t.subagentDiagnosticResponse(request, args, items)
	}
	if target != "cat_write_translation" {
		return finish("Unknown diagnostic target " + strconv.Quote(target) + ". Available targets: cat_write_translation, subagent_commentary.")
	}
	return t.catWriteDiagnosticResponse(request, args, items)
}

func (t *hpatchResponseTransform) catWriteDiagnosticResponse(request *parsedResponsesRequest, args []string, items []map[string]json.RawMessage) (*http.Response, bool, error) {
	finish := func(message string) (*http.Response, bool, error) {
		return diagnosticHTTPResponse(request, []map[string]json.RawMessage{diagnosticMessage("final_answer", message)}), true, nil
	}
	if len(args) != 0 {
		return finish("cat_write_translation takes no arguments. Use :hpatch_diag cat_write_translation.")
	}
	var calls []map[string]json.RawMessage
	callIDs := make(map[string]bool)
	results := make(map[string]json.RawMessage)
	for _, item := range items {
		callID := jsonString(item, "call_id")
		if !strings.HasPrefix(callID, diagnosticCallPrefix) {
			continue
		}
		switch jsonString(item, "type") {
		case "custom_tool_call":
			history, exists := t.proxy.history(t.historySessionID, callID)
			if !exists || !history.diagnostic || history.toolName != "shell" {
				return finish("Diagnostic playback state is unavailable. No payload was replayed; start a new diagnostic command.")
			}
			calls = append(calls, item)
			callIDs[callID] = true
		case "custom_tool_call_output", "function_call_output":
			if _, duplicate := results[callID]; duplicate {
				return finish("Diagnostic playback received duplicate tool results. No further payloads were started.")
			}
			results[callID] = item["output"]
		}
	}
	for callID := range results {
		if !callIDs[callID] {
			return finish("Diagnostic playback history is incomplete. No further payloads were started; start a new diagnostic command.")
		}
	}
	runID := diagnosticCallPrefix + rand.Text()
	directory := ""
	if len(calls) != 0 {
		var valid bool
		runID, valid = strings.CutSuffix(jsonString(calls[0], "call_id"), "_0")
		if !valid {
			return finish("Diagnostic setup is missing. No further payloads were started.")
		}
		for index, call := range calls {
			callID := jsonString(call, "call_id")
			if callID != runID+"_"+strconv.Itoa(index) {
				return finish("Diagnostic playback history is out of order. No further payloads were started.")
			}
			stdout, err := diagnosticToolOutput(results[callID])
			if err != nil {
				return finish(fmt.Sprintf("Diagnostic stopped at step %d: %v. No remaining payloads were started; any existing native continuation remains host-owned.", index, err))
			}
			if index == 0 {
				directory, _, _ = strings.Cut(strings.TrimSpace(stdout), "\n")
				if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsAny(directory, "\r\x00") || !strings.HasPrefix(filepath.Base(directory), "hpatch-diag.") {
					return finish("Diagnostic setup did not return its temporary directory. No file-write payloads were started.")
				}
			}
		}
	}
	if len(calls) > len(catWriteDiagnosticPayloads) {
		return finish(fmt.Sprintf("Played all %d cat-write cases locally without a provider request. Files remain in %s for inspection.", len(catWriteDiagnosticPayloads), directory))
	}
	source := diagnosticSetup
	progress := "Local cat_write_translation diagnostic: preparing an isolated temporary directory. Host tools will create files and leave them for inspection; no provider request is made."
	if len(calls) != 0 {
		payload := catWriteDiagnosticPayloads[len(calls)-1]
		source = payload.input(directory)
		progress = fmt.Sprintf("Local cat_write_translation %d/%d: %s.", len(calls), len(catWriteDiagnosticPayloads), payload.name)
	}
	callID := runID + "_" + strconv.Itoa(len(calls))
	item := map[string]json.RawMessage{
		"type": mustMarshalJSON("custom_tool_call"), "name": mustMarshalJSON("shell"),
		"id": mustMarshalJSON("item_" + callID), "call_id": mustMarshalJSON(callID),
		"input": mustMarshalJSON(source), "status": mustMarshalJSON("completed"),
	}
	// Mark provenance in the existing bounded call history, not a second playback
	// session store. The response transformer reuses this translation in JSON/SSE.
	history, err := t.translateTool("shell", callID, source, item)
	if err != nil {
		return nil, true, err
	}
	history.diagnostic = true
	t.local[callID] = history
	return diagnosticHTTPResponse(request, []map[string]json.RawMessage{diagnosticMessage("commentary", progress), item}), true, nil
}

// Source: Codex core/src/tools/code_mode/mod.rs and tools/context.rs. Fail closed
// on yielded/failed/truncated envelopes, even if they contain a successful inner
// result from an earlier statement. Neither playback nor this decoder resumes jobs.
func diagnosticToolOutput(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
			return "", errors.New("tool result is missing or unrecognized")
		}
		var joined strings.Builder
		for _, part := range parts {
			if part.Type != "input_text" && part.Type != "text" {
				return "", errors.New("tool result contains non-text content")
			}
			joined.WriteString(part.Text)
			joined.WriteByte('\n')
		}
		text = joined.String()
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "Script ") {
		if !strings.HasPrefix(text, "Script completed\n") {
			return "", errors.New("Code Mode execution did not complete successfully")
		}
		_, body, found := strings.Cut(text, "\nOutput:\n")
		if !found {
			return "", errors.New("Code Mode output is incomplete")
		}
		text = strings.TrimSpace(body)
	}
	if strings.HasPrefix(text, "Chunk ID:") || strings.HasPrefix(text, "Wall time:") {
		header, stdout, found := strings.Cut(text, "\nOutput:")
		success := false
		for line := range strings.SplitSeq(header, "\n") {
			success = success || line == "Process exited with code 0"
		}
		if !found || !success || strings.Contains(header, "Process running with session ID") {
			return "", errors.New("native execution did not complete successfully")
		}
		return strings.TrimPrefix(stdout, "\n"), nil
	}
	var result struct {
		Output   string          `json:"output"`
		ExitCode *int            `json:"exit_code"`
		Session  json.RawMessage `json:"session_id"`
	}
	if json.Unmarshal([]byte(text), &result) != nil || result.ExitCode == nil || *result.ExitCode != 0 || len(result.Session) != 0 && string(result.Session) != "null" {
		return "", errors.New("execution did not return a successful terminal result")
	}
	return result.Output, nil
}

func diagnosticMessage(phase, text string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"type": mustMarshalJSON("message"), "role": mustMarshalJSON("assistant"),
		"id": mustMarshalJSON(diagnosticMessagePrefix + rand.Text()), "status": mustMarshalJSON("completed"),
		"phase":   mustMarshalJSON(phase),
		"content": mustMarshalJSON([]map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}}),
	}
}

func diagnosticHTTPResponse(request *parsedResponsesRequest, output []map[string]json.RawMessage) *http.Response {
	response := map[string]json.RawMessage{
		"id": mustMarshalJSON("resp_hpatch_diag_" + rand.Text()), "object": mustMarshalJSON("response"),
		"created_at": mustMarshalJSON(time.Now().Unix()), "status": mustMarshalJSON("completed"),
		"model": mustMarshalJSON(request.model()), "output": mustMarshalJSON(output),
		"parallel_tool_calls": mustMarshalJSON(false),
	}
	contentType := "application/json"
	body := mustMarshalJSON(response)
	if request.streamResponse {
		contentType = "text/event-stream"
		var stream bytes.Buffer
		sequence := 0
		emit := func(kind string, fields map[string]any) {
			fields["type"], fields["sequence_number"] = kind, sequence
			sequence++
			fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", kind, mustMarshalJSON(fields))
		}
		created := maps.Clone(response)
		created["status"], created["output"] = mustMarshalJSON("in_progress"), mustMarshalJSON([]any{})
		emit("response.created", map[string]any{"response": created})
		for index, item := range output {
			added := maps.Clone(item)
			added["status"] = mustMarshalJSON("in_progress")
			if jsonString(item, "type") == "custom_tool_call" {
				added["input"] = mustMarshalJSON("")
				emit("response.output_item.added", map[string]any{"output_index": index, "item": added})
				emit("response.custom_tool_call_input.delta", map[string]any{"output_index": index, "item_id": jsonString(item, "id"), "delta": jsonString(item, "input")})
				emit("response.custom_tool_call_input.done", map[string]any{"output_index": index, "item_id": jsonString(item, "id"), "input": jsonString(item, "input")})
			} else if jsonString(item, "type") == "function_call" {
				added["arguments"] = mustMarshalJSON("")
				emit("response.output_item.added", map[string]any{"output_index": index, "item": added})
				emit("response.function_call_arguments.delta", map[string]any{"output_index": index, "item_id": jsonString(item, "id"), "delta": jsonString(item, "arguments")})
				emit("response.function_call_arguments.done", map[string]any{"output_index": index, "item_id": jsonString(item, "id"), "arguments": jsonString(item, "arguments")})
			} else {
				added["content"] = mustMarshalJSON([]any{})
				emit("response.output_item.added", map[string]any{"output_index": index, "item": added})
				var parts []map[string]json.RawMessage
				_ = json.Unmarshal(item["content"], &parts)
				for partIndex, part := range parts {
					fields := map[string]any{"output_index": index, "item_id": jsonString(item, "id"), "content_index": partIndex}
					blank := maps.Clone(part)
					blank["text"] = mustMarshalJSON("")
					addedPart := maps.Clone(fields)
					addedPart["part"] = blank
					emit("response.content_part.added", addedPart)
					delta := maps.Clone(fields)
					delta["delta"] = jsonString(part, "text")
					emit("response.output_text.delta", delta)
					done := maps.Clone(fields)
					done["text"] = jsonString(part, "text")
					emit("response.output_text.done", done)
					fields["part"] = part
					emit("response.content_part.done", fields)
				}
			}
			emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
		}
		emit("response.completed", map[string]any{"response": response})
		body = stream.Bytes()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(body))}
}
