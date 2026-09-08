package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiagnosticCommand(t *testing.T) {
	for _, source := range []string{
		":hpatch_diag cat_write_translation", " \t:hpatch_diag\tcat_write_translation\n",
		":hpatch_diag 'cat_write_translation'",
	} {
		target, args, matched, err := diagnosticCommand(source)
		if !matched || err != nil || target != "cat_write_translation" || len(args) != 0 {
			t.Fatalf("parse %q = %q, %v, %v, %v", source, target, args, matched, err)
		}
	}
	_, args, matched, err := diagnosticCommand(":hpatch_diag target 'two words' \"literal[1]\"")
	if !matched || err != nil || !slices.Equal(args, []string{"two words", "literal[1]"}) {
		t.Fatalf("literal args = %v, %v", args, err)
	}
	for _, source := range []string{":hpatch_diag", ":hpatch_diag target; rm x", ":hpatch_diag $(touch x)", ":hpatch_diag target > out", ":hpatch_diag target &", ":hpatch_diag target\nother"} {
		if _, _, matched, err := diagnosticCommand(source); !matched || err == nil {
			t.Errorf("accepted malformed command %q", source)
		}
	}
	for _, source := range []string{"explain :hpatch_diag target", "`:hpatch_diag target`", ":hpatch_diagnostic target", "```\n:hpatch_diag target\n```"} {
		if _, _, matched, _ := diagnosticCommand(source); matched {
			t.Errorf("matched non-command %q", source)
		}
	}
}

func TestDiagnosticSetupCreatesIsolatedDirectory(t *testing.T) {
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c", diagnosticSetup)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	directory := strings.TrimSpace(string(output))
	if !filepath.IsAbs(directory) || !strings.HasPrefix(filepath.Base(directory), "hpatch-diag.") {
		t.Fatalf("setup did not return a diagnostic temporary directory: %q", directory)
	}
	t.Cleanup(func() {
		// This invocation created the empty directory; never recursively remove
		// unexpected content if something else has populated it.
		if err := os.Remove(directory); err != nil {
			t.Error(err)
		}
	})
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := filepath.Rel(workingDirectory, directory); err != nil || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		t.Fatalf("diagnostic directory was created inside the checkout: %q", directory)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("setup directory is not private: %v, %v", info, err)
	}
}

func TestDiagnosticToolOutput(t *testing.T) {
	result := `{"output":"fixture output\n","exit_code":0}`
	for _, raw := range []json.RawMessage{
		mustMarshalJSON(result),
		mustMarshalJSON("Script completed\nWall time 0.1 seconds\nOutput:\n" + result),
		mustMarshalJSON([]map[string]string{{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"}, {"type": "input_text", "text": result}}),
		mustMarshalJSON("Chunk ID: x\nWall time: 0.1 seconds\nProcess exited with code 0\nOutput:\nfixture output\n"),
	} {
		output, err := diagnosticToolOutput(raw)
		if err != nil || strings.TrimSpace(output) != "fixture output" {
			t.Fatalf("decode %s = %q, %v", raw, output, err)
		}
	}
	for _, text := range []string{
		"", `{"output":"partial","session_id":42}`,
		`{"output":"failed","exit_code":1}`, `{"output":"partial","exit_code":0,"session_id":42}`,
		"Script failed\nWall time 0.1 seconds\nOutput:\n" + result,
		"Script running with cell ID 42\nWall time 0.1 seconds\nOutput:\n" + result,
		"Script terminated\nWall time 0.1 seconds\nOutput:\n" + result,
		"Chunk ID: x\nProcess exited with code 01\nOutput:\nignored",
		"Chunk ID: x\nProcess running with session ID 42\nOutput:\npartial",
		"Script completed\nWall time 0.1 seconds\nOutput:\n{\"output\":",
	} {
		if output, err := diagnosticToolOutput(mustMarshalJSON(text)); err == nil {
			t.Errorf("accepted nonterminal/failed result %q: %q", text, output)
		}
	}
}

func diagnosticResponseItems(t *testing.T, response *httptest.ResponseRecorder, stream bool) []map[string]json.RawMessage {
	t.Helper()
	body := response.Body.Bytes()
	if stream {
		var terminal []byte
		for line := range strings.SplitSeq(string(body), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event map[string]json.RawMessage
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			if jsonString(event, "type") == "response.completed" {
				terminal = event["response"]
			}
		}
		if terminal == nil {
			t.Fatalf("missing stream terminal: %s", body)
		}
		body = terminal
	}
	var envelope struct {
		Output []map[string]json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("response JSON: %v: %s", err, body)
	}
	return envelope.Output
}

func TestDiagnosticPlaybackEndToEnd(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%v/stream=%v", native, stream), func(t *testing.T) {
				proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
				provider := new(serverFakeProvider)
				workspace := t.TempDir()
				headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{workspace: nil})
				directory, err := os.MkdirTemp(t.TempDir(), "hpatch-diag.")
				if err != nil {
					t.Fatal(err)
				}
				input := []any{testFlatCodeModeAdditionalTools(testCodeModeDescription), map[string]any{"role": "user", "content": "earlier real turn"}, map[string]any{"role": "assistant", "content": "earlier real answer"}}
				if native {
					input = input[1:]
				}
				prefixLength := len(input)
				input = append(input, map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": ":hpatch_diag cat_write_translation"}}})
				for step := 0; step <= len(catWriteDiagnosticPayloads)+1; step++ {
					request := serverRequest(t, func(fields map[string]any) {
						fields["input"], fields["stream"] = input, stream
						if native {
							fields["tools"] = testNativeResponsesTools()
						}
					})
					visible := httptest.NewRecorder()
					if err := executeRequest(t.Context(), t.Context(), request, headers, "diagnostic-session", provider, visible, nil, proxy, mustCTP2Codec(t), nil); err != nil {
						t.Fatalf("step %d: %v", step, err)
					}
					if len(provider.forwarded) != 0 {
						t.Fatal("local playback contacted provider")
					}
					items := diagnosticResponseItems(t, visible, stream)
					var carrier map[string]json.RawMessage
					for _, item := range items {
						input = append(input, item)
						if jsonString(item, "type") == "custom_tool_call" || jsonString(item, "type") == "function_call" {
							carrier = item
						}
					}
					if step > len(catWriteDiagnosticPayloads) {
						if carrier != nil || !bytes.Contains(visible.Body.Bytes(), []byte(fmt.Sprintf("Played all %d cat-write cases", len(catWriteDiagnosticPayloads)))) {
							t.Fatalf("missing final summary: %s", visible.Body.Bytes())
						}
						break
					}
					if carrier == nil {
						t.Fatalf("step %d has no carrier: %s", step, visible.Body.Bytes())
					}
					if step > 0 {
						payload := jsonString(carrier, "input") + jsonString(carrier, "arguments")
						if !strings.Contains(payload, "apply_patch") {
							t.Fatalf("case %q bypassed cat translation: %s", catWriteDiagnosticPayloads[step-1].name, payload)
						}
					}
					stdout := ""
					if step == 0 {
						stdout = directory + "\n"
					}
					outputType := "custom_tool_call_output"
					var toolOutput any = []map[string]string{{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds\nOutput:\n"}, {"type": "input_text", "text": string(mustMarshalJSON(map[string]any{"output": stdout, "exit_code": 0}))}}
					if native {
						outputType = "function_call_output"
						toolOutput = "Chunk ID: x\nWall time: 0.1 seconds\nProcess exited with code 0\nOutput:\n" + stdout + "\n{\"retained\":false}\n"
					}
					input = append(input, map[string]any{"type": outputType, "call_id": jsonString(carrier, "call_id"), "output": toolOutput})
				}
				// A new real user turn excludes local diagnostic history while retaining
				// the earlier conversation and all unrelated tool declarations.
				input = append(input, map[string]any{"role": "user", "content": "normal request"})
				provider.results = []serverForwardResult{{response: serverHTTPResponse(`{"status":"completed","output":[]}`)}}
				request := serverRequest(t, func(fields map[string]any) {
					fields["input"] = input
					if native {
						fields["tools"] = testNativeResponsesTools()
					}
				})
				if err := executeRequest(t.Context(), t.Context(), request, headers, "diagnostic-session", provider, io.Discard, nil, proxy, nil, nil); err != nil {
					t.Fatal(err)
				}
				if len(provider.forwarded) != 1 || bytes.Contains(provider.forwarded[0], []byte("hpatch_diag")) || bytes.Contains(provider.forwarded[0], []byte(directory)) {
					t.Fatalf("diagnostic history leaked to provider: %q", provider.forwarded)
				}
				var forwarded struct {
					Input []map[string]json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal(provider.forwarded[0], &forwarded); err != nil {
					t.Fatal(err)
				}
				if len(forwarded.Input) != prefixLength+1 {
					t.Fatalf("unrelated conversation changed: %s", provider.forwarded[0])
				}
				for index := range prefixLength {
					if jsonString(forwarded.Input[index], "type") != "additional_tools" && !bytes.Equal(mustMarshalJSON(forwarded.Input[index]), mustMarshalJSON(input[index])) {
						t.Fatalf("earlier provider prefix changed at item %d", index)
					}
				}
			})
		}
	}
}

func TestDiagnosticPayloadsExecuteLiteralForms(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	transform, _, _, _ := newHPatchTestTransformWithProxy(t, proxy)
	directory := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "shell"), []byte("#!/bin/sh\ninterpreter=$1\nshift\nexec \"$interpreter\" -c \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	contribution, _ := proxy.registry.contribution("shell")
	for index, payload := range catWriteDiagnosticPayloads {
		t.Run(payload.name, func(t *testing.T) {
			history, err := transform.translateRegisteredTool(contribution, fmt.Sprintf("fixture-%d", index), payload.input(directory), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(history.carrierInput(), "await tools.apply_patch(") {
				t.Fatalf("fixture is not projected: %s", history.carrierInput())
			}
			var result map[string]any
			runShellCatJavaScript(t, proxy.registry.NodeExecutable, directory, history.carrierInput(), &result, "")
			if result["exit_code"] != float64(0) {
				t.Fatalf("fixture result = %#v", result)
			}
		})
	}
	for path, want := range map[string]string{
		"empty.txt": "", "tabs.txt": "first\nsecond\n", "blank-lines.txt": "\nbody\n\n",
		"overwrite.txt": "second version\n", "absolute.txt": "absolute path\n",
		"single path [1]~*?.txt": "quoted path\n", "double path [1]~*?.txt": "quoted path\n",
		"literal.txt": "$HOME `whoami` $(date) \\n ; && |\n你好 α\n",
	} {
		content, err := os.ReadFile(filepath.Join(directory, path))
		if err != nil || string(content) != want {
			t.Errorf("%s = %q, want %q; error %v", path, content, want, err)
		}
	}
}

func TestDiagnosticInvalidCommandsStayLocal(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	for _, command := range []string{":hpatch_diag", ":hpatch_diag unknown", ":hpatch_diag cat_write_translation extra", ":hpatch_diag cat_write_translation; touch out"} {
		t.Run(command, func(t *testing.T) {
			provider := new(serverFakeProvider)
			request := serverRequest(t, func(fields map[string]any) {
				fields["input"] = []any{testFlatCodeModeAdditionalTools(testCodeModeDescription), map[string]any{"role": "user", "content": command}}
			})
			visible := httptest.NewRecorder()
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
			if err := executeRequest(t.Context(), t.Context(), request, headers, "invalid-diag", provider, visible, nil, proxy, nil, nil); err != nil {
				t.Fatal(err)
			}
			items := diagnosticResponseItems(t, visible, false)
			if len(provider.forwarded) != 0 || len(items) != 1 || jsonString(items[0], "type") != "message" {
				t.Fatalf("invalid diagnostic released work: %s", visible.Body.Bytes())
			}
		})
	}
}

func TestDiagnosticStopsAfterUnsuccessfulSetup(t *testing.T) {
	for _, result := range []string{
		`{"output":"setup failed","exit_code":1}`,
		`{"output":"partial","session_id":42}`,
		`{"output":"/not-the-diagnostic-directory","exit_code":0}`,
		"Script failed\nWall time 0.1 seconds\nOutput:\n{\"output\":\"/tmp/hpatch-diag.forged\",\"exit_code\":0}",
	} {
		t.Run(result, func(t *testing.T) {
			proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
			provider := new(serverFakeProvider)
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
			input := []any{testFlatCodeModeAdditionalTools(testCodeModeDescription), map[string]any{"role": "user", "content": ":hpatch_diag cat_write_translation"}}
			request := serverRequest(t, func(fields map[string]any) { fields["input"] = input })
			visible := httptest.NewRecorder()
			if err := executeRequest(t.Context(), t.Context(), request, headers, "failed-diag", provider, visible, nil, proxy, nil, nil); err != nil {
				t.Fatal(err)
			}
			items := diagnosticResponseItems(t, visible, false)
			callID := ""
			for _, item := range items {
				input = append(input, item)
				if jsonString(item, "type") == "custom_tool_call" {
					callID = jsonString(item, "call_id")
				}
			}
			if callID == "" {
				t.Fatal("setup was not played")
			}
			input = append(input, map[string]any{"type": "custom_tool_call_output", "call_id": callID, "output": result})
			request = serverRequest(t, func(fields map[string]any) { fields["input"] = input })
			visible = httptest.NewRecorder()
			if err := executeRequest(t.Context(), t.Context(), request, headers, "failed-diag", provider, visible, nil, proxy, nil, nil); err != nil {
				t.Fatal(err)
			}
			items = diagnosticResponseItems(t, visible, false)
			if len(provider.forwarded) != 0 || len(items) != 1 || jsonString(items[0], "type") != "message" {
				t.Fatalf("advanced after unsuccessful setup: %s", visible.Body.Bytes())
			}
		})
	}
}

func TestDiagnosticStopsWithOutputOnlyHistory(t *testing.T) {
	for _, omitStep := range []int{0, 1} {
		t.Run(fmt.Sprint(omitStep), func(t *testing.T) {
			proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
			provider := new(serverFakeProvider)
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
			input := []any{testFlatCodeModeAdditionalTools(testCodeModeDescription), map[string]any{"role": "user", "content": ":hpatch_diag cat_write_translation"}}
			for step := 0; step <= omitStep+1; step++ {
				request := serverRequest(t, func(fields map[string]any) { fields["input"] = input })
				visible := httptest.NewRecorder()
				if err := executeRequest(t.Context(), t.Context(), request, headers, "incomplete-diag", provider, visible, nil, proxy, nil, nil); err != nil {
					t.Fatal(err)
				}
				items := diagnosticResponseItems(t, visible, false)
				if step == omitStep+1 {
					if len(provider.forwarded) != 0 || len(items) != 1 || !bytes.Contains(items[0]["content"], []byte("history is incomplete")) {
						t.Fatalf("replayed incomplete history: %s", visible.Body.Bytes())
					}
					break
				}
				for _, item := range items {
					if jsonString(item, "type") != "custom_tool_call" || step != omitStep {
						input = append(input, item)
					}
					if jsonString(item, "type") == "custom_tool_call" {
						input = append(input, map[string]any{"type": "custom_tool_call_output", "call_id": jsonString(item, "call_id"), "output": `{"output":"/tmp/hpatch-diag.test","exit_code":0}`})
					}
				}
			}
		})
	}
}

func TestDiagnosticPreservesUnownedPastPrefix(t *testing.T) {
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(`{"status":"completed","output":[]}`)}}}
	request := serverRequest(t, func(fields map[string]any) {
		fields["input"] = []any{
			testFlatCodeModeAdditionalTools(testCodeModeDescription),
			map[string]any{"role": "user", "content": ":hpatch_diag cat_write_translation"},
			map[string]any{"role": "assistant", "content": "Previous provider answer"},
			map[string]any{"role": "user", "content": "Continue normally"},
		}
	})
	headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
	if err := executeRequest(t.Context(), t.Context(), request, headers, "resumed-passthrough", provider, io.Discard, nil, proxy, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.forwarded) != 1 || !bytes.Contains(provider.forwarded[0], []byte(":hpatch_diag cat_write_translation")) || !bytes.Contains(provider.forwarded[0], []byte("Previous provider answer")) {
		t.Fatalf("removed unowned conversation: %q", provider.forwarded)
	}
}

func TestDiagnosticDoesNotTriggerFromToolOutputOrPassthrough(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			var proxy *hpatchProxy
			if !passthrough {
				proxy = newManagedHPatchProxy(t, testTranslator(t, new(int)))
			}
			provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(`{"status":"completed","output":[]}`)}}}
			request := serverRequest(t, func(fields map[string]any) {
				if passthrough {
					fields["input"] = ":hpatch_diag cat_write_translation"
				} else {
					fields["input"] = append(fields["input"].([]any), map[string]any{"type": "function_call_output", "call_id": "unrelated", "output": ":hpatch_diag cat_write_translation"})
				}
			})
			headers := serverMetadataHeaders(t, "turn", map[string]json.RawMessage{t.TempDir(): nil})
			if err := executeRequest(t.Context(), t.Context(), request, headers, "non-diagnostic", provider, io.Discard, nil, proxy, nil, nil); err != nil {
				t.Fatal(err)
			}
			if len(provider.forwarded) != 1 || !bytes.Contains(provider.forwarded[0], []byte(":hpatch_diag")) {
				t.Fatalf("ordinary request changed: %q", provider.forwarded)
			}
		})
	}
}
