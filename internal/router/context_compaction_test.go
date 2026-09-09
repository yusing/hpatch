package router

import (
	"encoding/json"
	"strings"
	"testing"
)

func compactTestCall(id, command string) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": "function_call", "name": "exec_command", "call_id": id,
		"arguments": string(mustMarshalJSON(map[string]any{"cmd": command, "workdir": "/workspace"})),
	})
}

func compactTestOutput(id, output string, exitCode any) json.RawMessage {
	return mustMarshalJSON(map[string]any{
		"type": "function_call_output", "call_id": id,
		"output": string(mustMarshalJSON(map[string]any{
			"output": output, "exit_code": exitCode, "wall_time_seconds": 1,
		})),
	})
}

func TestContextCompactionPreservesIntentAndContinuation(t *testing.T) {
	items := []json.RawMessage{
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Only change the router. Do not commit."}),
		compactTestCall("tests", "go test -v ./internal/router"),
		compactTestOutput("tests", strings.Repeat("=== RUN   TestRoute\n--- PASS: TestRoute (0.01s)\n", 20)+"PASS\nok  example/router 0.1s\n", 0),
		mustMarshalJSON(map[string]any{"type": "message", "role": "assistant", "content": "Router tests passed, but the endpoint is not finished."}),
		mustMarshalJSON(map[string]any{"type": "reasoning", "summary": []any{map[string]string{"type": "summary_text", "text": "Need to check continuation before deployment."}}, "encrypted_content": "opaque"}),
		mustMarshalJSON(map[string]any{"type": "message", "role": "user", "content": "Actually keep the existing endpoint too."}),
		compactTestCall("last", "go test -v ./internal/router"),
		compactTestOutput("last", "=== RUN   TestRoute\n--- PASS: TestRoute (0.01s)\nPASS\nok  example/router 0.1s\n", 0),
	}
	before := string(mustMarshalJSON(items))
	got := reduceContextCompaction(items)
	if len(got) != len(items) {
		t.Fatal("compaction removed conversation items")
	}
	for index := range items {
		if index != 2 && string(got[index]) != string(items[index]) {
			t.Fatalf("protected item %d changed", index)
		}
	}
	if string(got[2]) == string(items[2]) || !strings.Contains(string(got[2]), "ok  example/router") {
		t.Fatalf("routine test detail was not reduced with package evidence retained: %s (command kind %q)", got[2], contextCompactionCommand("go test -v ./internal/router"))
	}
	if string(mustMarshalJSON(items)) != before {
		t.Fatal("compaction modified the input")
	}
	if string(mustMarshalJSON(reduceContextCompaction(got))) != string(mustMarshalJSON(got)) {
		t.Fatal("repeated compaction changed already compacted context")
	}
}

func TestContextCompactionKeepsUncertainExecutionEvidence(t *testing.T) {
	for _, test := range []struct {
		name, command, output string
		exitCode              any
	}{
		{"failed", "go test -v ./...", "=== RUN   TestA\n--- FAIL: TestA (0.1s)\nassertion details\nFAIL\n", 1},
		{"running", "go test -v ./...", "=== RUN   TestA\n", nil},
		{"compound", "go test -v ./...; echo done", "=== RUN   TestA\n--- PASS: TestA (0.1s)\nPASS\n", 0},
		{"pipeline", "go test -v ./... | tee result", "=== RUN   TestA\n--- PASS: TestA (0.1s)\nPASS\n", 0},
		{"substitution", "go test $(echo ./...)", "=== RUN   TestA\n--- PASS: TestA (0.1s)\nPASS\n", 0},
		{"wrapper", "rtk go test -v ./...", "=== RUN   TestA\n--- PASS: TestA (0.1s)\nPASS\n", 0},
		{"unknown", "make check", "=== RUN   TestA\n--- PASS: TestA (0.1s)\nPASS\n", 0},
		{"no_matches", "rg missing .", "", 1},
		{"search_error", "rg match missing", "rg: missing: No such file or directory", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := []json.RawMessage{compactTestCall("first", test.command), compactTestOutput("first", test.output, test.exitCode),
				compactTestCall("last", "pwd"), compactTestOutput("last", "/workspace\n", 0)}
			if string(mustMarshalJSON(reduceContextCompaction(items))) != string(mustMarshalJSON(items)) {
				t.Fatal("uncertain, unfinished, failed, or unrecognized evidence changed")
			}
		})
	}
}

func TestContextCompactionSearchRequiresRetainedExactEvidence(t *testing.T) {
	listing := strings.Repeat("src/example.go\n", 40)
	items := []json.RawMessage{
		compactTestCall("search", "rg --files src"), compactTestOutput("search", listing, 0),
		compactTestCall("read", "cat files.txt"), compactTestOutput("read", listing, 0),
	}
	got := reduceContextCompaction(items)
	if string(got[1]) == string(items[1]) || !strings.Contains(string(got[1]), "read") {
		t.Fatal("duplicate search listing lacks retained-source reference")
	}
	if string(got[3]) != string(items[3]) {
		t.Fatal("replacement evidence was modified")
	}
	items[3] = compactTestOutput("read", "different evidence\n", 0)
	if string(mustMarshalJSON(reduceContextCompaction(items))) != string(mustMarshalJSON(items)) {
		t.Fatal("search evidence removed without exact retained replacement")
	}
}

func TestContextCompactionKeepsUnknownTestDiagnostics(t *testing.T) {
	items := []json.RawMessage{
		compactTestCall("tests", "go test -v ./..."),
		compactTestOutput("tests", strings.Repeat("=== RUN   TestA\n--- PASS: TestA (0.1s)\n", 20)+"    test.go:10: important diagnostic\nPASS\nok  example 0.1s\n", 0),
		compactTestCall("last", "pwd"), compactTestOutput("last", "/workspace\n", 0),
	}
	got := reduceContextCompaction(items)
	if string(got[1]) == string(items[1]) || !strings.Contains(string(got[1]), "important diagnostic") {
		t.Fatal("non-routine diagnostic removed")
	}
}

func TestContextCompactionKeepsLiveHandlesAndAmbiguousCalls(t *testing.T) {
	log := strings.Repeat("=== RUN   TestA\n--- PASS: TestA (0.1s)\n", 20)
	live := mustMarshalJSON(map[string]any{
		"type": "function_call_output", "call_id": "tests",
		"output": string(mustMarshalJSON(map[string]any{"output": log, "exit_code": 0, "session_id": 42})),
	})
	for _, items := range [][]json.RawMessage{
		{compactTestCall("tests", "go test -v ./..."), live, compactTestCall("last", "pwd"), compactTestOutput("last", "/workspace\n", 0)},
		{compactTestCall("tests", "go test -v ./..."), compactTestCall("tests", "pwd"), compactTestOutput("tests", log, 0), compactTestOutput("last", "/workspace\n", 0)},
	} {
		if string(mustMarshalJSON(reduceContextCompaction(items))) != string(mustMarshalJSON(items)) {
			t.Fatal("live continuation or ambiguous call identity changed")
		}
	}
}

func TestContextCompactionNativeExecOutput(t *testing.T) {
	header := "Chunk ID: abc\nWall time: 1.2500 seconds\nProcess exited with code 0\nOriginal token count: 900\nOutput:\n"
	log := strings.Repeat("=== RUN   TestNative\n--- PASS: TestNative (0.1s)\n", 30) + "PASS\nok  example 0.1s\n"
	for _, prefix := range []string{header, strings.Replace(header, "code 0", "code 1", 1), strings.Replace(header, "Process exited with code 0", "Process running with session ID 42", 1)} {
		result := mustMarshalJSON(map[string]any{"type": "function_call_output", "call_id": "tests", "output": prefix + log})
		items := []json.RawMessage{compactTestCall("tests", "go test -v ./..."), result, compactTestCall("last", "pwd"), compactTestOutput("last", "/workspace\n", 0)}
		got := reduceContextCompaction(items)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(got[1], &fields)
		text := jsonString(fields, "output")
		if !strings.HasPrefix(text, prefix) {
			t.Fatal("native execution header changed")
		}
		if prefix == header {
			if text == prefix+log || !strings.Contains(text, "ok  example") {
				t.Fatal("native successful test output not reduced")
			}
		} else if string(got[1]) != string(result) {
			t.Fatal("native failure or running output changed")
		}
	}
}
