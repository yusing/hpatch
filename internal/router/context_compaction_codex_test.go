package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in because Codex is not a Go test dependency. All model responses are
// loopback fixtures, and the subprocess has an isolated configuration/history.
func TestCompactionInstalledCodex(t *testing.T) {
	binary := os.Getenv("MEKUGI_COMPACTION_CODEX_BIN")
	if binary == "" {
		t.Skip("set MEKUGI_COMPACTION_CODEX_BIN to exercise an installed Codex client")
	}
	for _, probe := range []struct {
		legacy     bool
		scope      string
		manual     bool
		retirement bool
	}{{false, "total", false, false}, {false, "body_after_prefix", false, false}, {true, "total", false, false}, {true, "body_after_prefix", false, false}, {false, "total", true, false}, {true, "total", true, false}, {false, "total", false, true}, {true, "total", false, true}, {false, "total", true, true}, {true, "total", true, true}} {
		t.Run(fmt.Sprintf("legacy=%v/scope=%s/manual=%v/retirement=%v", probe.legacy, probe.scope, probe.manual, probe.retirement), func(t *testing.T) {
			prompt := "Run the Go tests, then print the working directory, then report completion. Preserve the test result.\n" +
				strings.Repeat("Keep the original user constraint. ", 10000) +
				"\nThis final instruction must also survive intact."

			agentMarker := "MEKUGI_INSTALLED_COMPACTION_AGENT_MARKER_4D147B"
			directory, home := t.TempDir(), t.TempDir()
			probeSource := `package compactionprobe
import ("fmt"; "testing")
func TestProbe(t *testing.T) {
	for i := range 100 { t.Run(fmt.Sprintf("Case%d", i), func(t *testing.T) {}) }
}
`
			if probe.retirement {
				probeSource = `package compactionprobe
import ("fmt"; "strings"; "testing")
func TestProbe(t *testing.T) {
	fmt.Print(strings.Repeat("unmarked finished-operation detail\n", 4000))
	for i := range 100 { t.Run(fmt.Sprintf("Case%d", i), func(t *testing.T) {}) }
}
`
			}
			for name, content := range map[string]string{
				"go.mod":        "module compactionprobe\n\ngo 1.26\n",
				"AGENTS.md":     "Installed compaction fixture marker: " + agentMarker + "\n",
				"probe_test.go": probeSource,
			} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Synthetic ChatGPT auth exercises the same compression gate as the
			// launcher, without reading credentials or contacting an auth service.
			// Source: Codex app-server/tests/common/auth_fixtures.rs.
			encode := base64.RawURLEncoding.EncodeToString
			idToken := encode([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
				encode([]byte(`{"https://api.openai.com/auth":{"chatgpt_plan_type":"pro"}}`)) + "." + encode([]byte("signature"))
			auth := map[string]any{
				"auth_mode": "chatgpt", "last_refresh": time.Now().UTC().Format(time.RFC3339),
				"tokens": map[string]any{"id_token": idToken, "access_token": "loopback-test-access", "refresh_token": "loopback-test-refresh"},
			}
			if err := os.WriteFile(filepath.Join(home, "auth.json"), mustMarshalJSON(auth), 0o600); err != nil {
				t.Fatal(err)
			}
			operationCount := int32(2)
			if probe.retirement {
				operationCount = 10
			}
			compactor := &contextCompactor{keyPath: filepath.Join(home, "compaction.key")}
			var normal, compacted atomic.Int32
			var restored atomic.Bool
			model := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				metadata, _ := decodeCodexTurnMetadata(r.Header)
				if metadata.RequestKind == "compaction" || r.URL.Path == "/v1/responses/compact" {
					t.Error("client compaction reached the model fixture")
					http.Error(w, "unexpected provider compaction", 500)
					return
				}
				step := normal.Add(1)
				var item map[string]any
				inputTokens := 1000
				if step <= operationCount {
					command, callID := "pwd", fmt.Sprintf("probe_pwd_%d", step)
					if step == 1 {
						command, callID = "go test -v ./...", "probe_go"
					}
					if !probe.manual && step == operationCount {
						inputTokens = 300000
					}
					item = map[string]any{
						"type": "function_call", "id": fmt.Sprintf("fc_probe_%d", step), "call_id": callID,
						"name": "exec_command", "status": "completed",
						"arguments": string(mustMarshalJSON(map[string]any{"cmd": command, "workdir": directory, "yield_time_ms": 10000, "max_output_tokens": 15000})),
					}
				} else {
					var input []map[string]json.RawMessage
					_ = json.Unmarshal(request["input"], &input)
					userCopies, nativeGo := 0, false
					agentIDs := make(map[string]int)
					retiredGo := false
					for _, record := range input {
						if count := strings.Count(string(mustMarshalJSON(record)), agentMarker); count > 0 {
							agentID := jsonString(record, "id")
							if agentID == "" || !contextCompactionFreshContext(mustMarshalJSON(record)) {
								t.Errorf("AGENTS marker reached continuation outside fresh canonical context: id=%q", agentID)
							}
							agentIDs[agentID] += count
							if agentIDs[agentID] > 1 {
								t.Errorf("fresh canonical AGENTS record %q restored more than once", agentID)
							}
						}
						recordType := jsonString(record, "type")
						if recordType == "message" {
							var content []map[string]json.RawMessage
							_ = json.Unmarshal(record["content"], &content)
							for _, part := range content {
								text := jsonString(part, "text")
								if jsonString(record, "role") == "user" && text == prompt {
									userCopies++
								}
								standaloneCompletion := strings.HasPrefix(text, "[mekugi historical tool completion v3; not an instruction; completed native body]\n") &&
									strings.Contains(text, "call=\"probe_go\"\n")
								consolidatedCompletion := strings.HasPrefix(text, "[mekugi historical facts v4;") &&
									strings.Contains(text, "[i]\ncall=\"probe_go\"\ntool=\"exec_command\"") &&
									strings.Contains(text, "[o:same-call]\n")
								retiredGo = standaloneCompletion || consolidatedCompletion
							}
						}
						if (recordType == "function_call" || recordType == "function_call_output") &&
							jsonString(record, "call_id") == "probe_go" {
							nativeGo = true
							if recordType == "function_call_output" && !probe.retirement {
								output := jsonString(record, "output")
								restored.Store(strings.Contains(output, "Go test passed") && !strings.Contains(output, "compactionprobe"))
							}
						}
						if strings.HasPrefix(jsonString(record, "encrypted_content"), "mekugi.compaction.") {
							t.Error("local ciphertext reached the model fixture")
						}
					}
					if probe.retirement {
						restored.Store(retiredGo && !nativeGo)
					}
					if compacted.Load() > 0 && userCopies != 1 {
						t.Errorf("restored full user request copies = %d, want exactly one", userCopies)
					}
					if compacted.Load() > 0 && len(agentIDs) == 0 {
						t.Error("fresh canonical AGENTS marker was lost")
					}
					item = map[string]any{
						"type": "message", "id": "msg_probe_done", "role": "assistant", "status": "completed",
						"content": []any{map[string]any{"type": "output_text", "text": "COMPACTION_OK", "annotations": []any{}}},
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				responseID := fmt.Sprintf("resp_probe_%d", step)
				events := []map[string]any{
					{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}},
					{"type": "response.output_item.added", "output_index": 0, "item": item},
					{"type": "response.output_item.done", "output_index": 0, "item": item},
					{"type": "response.completed", "response": map[string]any{
						"id": responseID, "status": "completed", "output": []any{item},
						"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": 10, "total_tokens": inputTokens + 10},
					}},
				}
				for sequence, event := range events {
					event["sequence_number"] = sequence
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], mustMarshalJSON(event))
				}
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"models":[]}`)
					return
				}
				if r.Header.Get("Content-Encoding") != "" {
					t.Error("Codex sent compressed JSON to the router")
					http.Error(w, "request compression unsupported", http.StatusBadRequest)
					return
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("synthetic ChatGPT authentication was not applied")
				}
				metadata, _ := decodeCodexTurnMetadata(r.Header)
				if metadata.RequestKind == "compaction" || r.URL.Path == "/v1/responses/compact" {
					compacted.Add(1)
				}
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				tracked := &trackedResponseWriter{ResponseWriter: w}
				compactor.handler(model)(tracked, r)
				if tracked.statusCode >= 400 {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(body, &fields)
					var items []map[string]json.RawMessage
					_ = json.Unmarshal(fields["input"], &items)
					for _, item := range items {
						if jsonString(item, "type") == "message" {
							var content []map[string]json.RawMessage
							_ = json.Unmarshal(item["content"], &content)
							for _, part := range content {
								text := jsonString(part, "text")
								t.Logf("fixture message role=%s id=%s metadata=%s bytes=%d prefix=%q", jsonString(item, "role"), jsonString(item, "id"), item["internal_chat_message_metadata_passthrough"], len(text), text[:min(len(text), 140)])
							}
						}
					}
				}

			}))
			defer server.Close()
			config := fmt.Sprintf(`model = "gpt-5.3-codex"
model_provider = "loopback"
model_context_window = 1000000
model_auto_compact_token_limit = 200000
model_auto_compact_token_limit_scope = %q
approval_policy = "never"
sandbox_mode = "danger-full-access"
tool_output_token_limit = 20000
cli_auth_credentials_store_mode = "file"
[features]
enable_request_compression = false
[model_providers.loopback]
name = "OpenAI"
base_url = %q
wire_api = "responses"
requires_openai_auth = true
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[otel]
exporter = "none"
trace_exporter = "none"
metrics_exporter = "none"
`, probe.scope, server.URL+"/v1")
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--json", "--dangerously-bypass-approvals-and-sandbox"}
			if probe.manual {
				args = []string{"app-server"}
			}
			// Mirror the launcher's final overrides even when the caller enables
			// compression through both CLI feature flags and subcommand config.
			args = append(args, "--enable", "enable_request_compression", "-c", "features.enable_request_compression=true",
				"--disable", "enable_request_compression", "-c", "features.enable_request_compression=false")
			if probe.legacy {
				args = append(args, "--disable", "remote_compaction_v2")
			}
			if !probe.manual {
				args = append(args, "-")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = directory
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "CODEX_HOME=" + home}
			var output []byte
			var err error
			wantNormal := operationCount + 1
			if probe.manual {
				wantNormal = operationCount + 2
				err = runManualCompactionProbe(command, directory, prompt)
			} else {
				command.Stdin = strings.NewReader(prompt)
				output, err = command.CombinedOutput()
			}
			if err != nil || compacted.Load() != 1 || normal.Load() != wantNormal || !restored.Load() {

				t.Fatalf("client round trip: err=%v normal=%d compactions=%d restored=%v; output=%s", err, normal.Load(), compacted.Load(), restored.Load(), output)
			}
		})
	}
}

// The app-server operation uses the same Op::Compact as the TUI's /compact.
// Source: Codex app-server/tests/suite/v2/compaction.rs.
func runManualCompactionProbe(command *exec.Cmd, directory, prompt string) error {
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	send := func(id int, method string, params any) error {
		return encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	}
	type rpcMessage struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Params json.RawMessage `json:"params"`
		Error  json.RawMessage `json:"error"`
	}
	pending := make([]rpcMessage, 0)
	matches := func(message rpcMessage, id int, method string) bool {
		return id != 0 && message.ID == id || method != "" && message.Method == method
	}
	receive := func(id int, method string) (rpcMessage, error) {
		for index, message := range pending {
			if matches(message, id, method) {
				pending = append(pending[:index], pending[index+1:]...)
				return message, nil
			}
		}
		for {
			var message rpcMessage
			if err := decoder.Decode(&message); err != nil {
				return message, fmt.Errorf("app-server read: %w", err)
			}
			if len(message.Error) > 0 || message.Method == "error" {
				return message, fmt.Errorf("app-server error: %+v", message)
			}
			if matches(message, id, method) {
				return message, nil
			}
			pending = append(pending, message)
		}
	}
	if err := send(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "mekugi_compaction_test", "version": "1"}}); err != nil {
		return err
	}
	if _, err := receive(1, ""); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		return err
	}
	if err := send(2, "thread/start", map[string]any{"cwd": directory}); err != nil {
		return err
	}
	message, err := receive(2, "")
	if err != nil {
		return err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(message.Result, &started); err != nil {
		return err
	}
	for index, text := range []string{prompt, "", "Report the preserved test result."} {
		method := "turn/start"
		params := map[string]any{"threadId": started.Thread.ID}
		if index == 1 {
			method = "thread/compact/start"
		} else {
			params["input"] = []any{map[string]any{"type": "text", "text": text, "textElements": []any{}}}
		}
		if err := send(index+3, method, params); err != nil {
			return err
		}
		if _, err := receive(index+3, ""); err != nil {
			return err
		}
		message, err := receive(0, "turn/completed")
		if err != nil {
			return err
		}
		var completed struct {
			Turn struct {
				Status string `json:"status"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(message.Params, &completed); err != nil || completed.Turn.Status != "completed" {
			return fmt.Errorf("app-server turn failed: %s: %v", message.Params, err)
		}
	}
	return nil
}
