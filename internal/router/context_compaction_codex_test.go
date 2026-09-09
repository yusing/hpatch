package router

import (
	"context"
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
	binary := os.Getenv("HPATCH_COMPACTION_CODEX_BIN")
	if binary == "" {
		t.Skip("set HPATCH_COMPACTION_CODEX_BIN to exercise an installed Codex client")
	}
	for _, probe := range []struct {
		legacy bool
		scope  string
	}{{false, "total"}, {false, "body_after_prefix"}, {true, "total"}, {true, "body_after_prefix"}} {
		t.Run(fmt.Sprintf("legacy=%v/scope=%s", probe.legacy, probe.scope), func(t *testing.T) {
			prompt := "Run the Go tests, then print the working directory, then report completion. Preserve the test result.\n" +
				strings.Repeat("Keep the original user constraint. ", 10000) +
				"\nThis final instruction must also survive intact."

			directory, home := t.TempDir(), t.TempDir()
			for name, content := range map[string]string{
				"go.mod": "module compactionprobe\n\ngo 1.26\n",
				"probe_test.go": `package compactionprobe
import ("fmt"; "testing")
func TestProbe(t *testing.T) {
	for i := range 100 { t.Run(fmt.Sprintf("Case%d", i), func(t *testing.T) {}) }
}
`,
			} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
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
				switch step {
				case 1, 2:
					command, callID := "go test -v ./...", "probe_go"
					if step == 2 {
						command, callID, inputTokens = "pwd", "probe_pwd", 300000
					}
					item = map[string]any{
						"type": "function_call", "id": fmt.Sprintf("fc_probe_%d", step), "call_id": callID,
						"name": "exec_command", "status": "completed",
						"arguments": string(mustMarshalJSON(map[string]any{"cmd": command, "workdir": directory, "yield_time_ms": 10000, "max_output_tokens": 15000})),
					}
				default:
					var input []map[string]json.RawMessage
					_ = json.Unmarshal(request["input"], &input)
					userCopies := 0
					for _, record := range input {
						if jsonString(record, "type") == "message" && jsonString(record, "role") == "user" {
							var content []map[string]json.RawMessage
							_ = json.Unmarshal(record["content"], &content)
							for _, part := range content {
								if jsonString(part, "text") == prompt {
									userCopies++
								}
							}
						}
						if jsonString(record, "type") == "function_call_output" && jsonString(record, "call_id") == "probe_go" {
							output := jsonString(record, "output")
							restored.Store(strings.Contains(output, "[hpatch: omitted") && strings.Contains(output, "compactionprobe"))
						}
						if strings.HasPrefix(jsonString(record, "encrypted_content"), "hpatch.compaction.") {
							t.Error("local ciphertext reached the model fixture")
						}
					}
					if userCopies != 1 {
						t.Errorf("restored full user request copies = %d, want exactly one", userCopies)
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
tool_output_token_limit = 20000
[model_providers.loopback]
name = "Azure"
base_url = %q
wire_api = "responses"
requires_openai_auth = false
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
			if probe.legacy {
				args = append(args, "--disable", "remote_compaction_v2")
			}
			args = append(args, "-")
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Stdin = strings.NewReader(prompt)
			command.Dir = directory
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "CODEX_HOME=" + home}
			output, err := command.CombinedOutput()
			if err != nil || compacted.Load() != 1 || normal.Load() != 3 || !restored.Load() {
				t.Fatalf("client round trip: err=%v normal=%d compactions=%d restored=%v; output=%s", err, normal.Load(), compacted.Load(), restored.Load(), output)
			}
		})
	}
}
