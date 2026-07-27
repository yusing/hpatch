package hpatch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvocationMetricsExportWithoutLocalPersistence(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	writeTestFile(t, root, "note.txt", "alpha\n", 0o600)
	outputPath := filepath.Join(t.TempDir(), "invocation.json")
	if err := os.WriteFile(outputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(metricsOutputFileVariable, outputPath)

	var stdout, stderr bytes.Buffer
	script := "in note.txt\nrsel 1:1\ntype \"beta\\n\"\n"
	if exitCode := Run([]string{"translate"}, strings.NewReader(script), &stdout, &stderr, root, dataDirectory); exitCode != 0 {
		t.Fatalf("successful translate = exit %d, stderr %q", exitCode, stderr.String())
	}
	successful := readInvocationExport(t, outputPath)
	if successful.Commands[commandOperationIndex("in")].Invocations != 1 || successful.Commands[commandOperationIndex("rsel")].Invocations != 1 {
		t.Fatalf("successful invocation export = %+v", successful.Commands)
	}
	if got, err := readMetrics(dataDirectory); err != nil || got != (metrics{}) {
		t.Fatalf("exported invocation persisted locally: %+v, error %v", got, err)
	}

	stdout.Reset()
	stderr.Reset()
	rejected := "in note.txt\nsel 99 1:1\n"
	if exitCode := Run([]string{"translate"}, strings.NewReader(rejected), &stdout, &stderr, root, dataDirectory); exitCode == 0 {
		t.Fatal("out-of-range selector unexpectedly succeeded")
	}
	failed := readInvocationExport(t, outputPath)
	sel := failed.Commands[commandOperationIndex("sel")]
	if sel.Invocations != 1 || sel.Errors != 1 || failed.Reasons[reasonCoordinateBounds] != 1 {
		t.Fatalf("rejected invocation export = %+v", failed)
	}
}

func TestInvocationMetricsExportUsesCallerV2Schema(t *testing.T) {
	value := invocationMetrics{}
	value.Commands[commandOperationIndex("new")].Invocations = 1
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"commands", "text_spans", "block_outcomes", "reasons", "command_reasons"} {
		if _, ok := object[key]; !ok {
			t.Fatalf("invocation JSON %s lacks %q", encoded, key)
		}
	}
	if _, ok := object["selector_variants"]; ok {
		t.Fatalf("invocation JSON %s retains the obsolete selector variants field", encoded)
	}
	if bytes.Contains(encoded, []byte("Invocations")) || !bytes.Contains(encoded, []byte(`"invocations":1`)) {
		t.Fatalf("command metric JSON is not stable snake_case: %s", encoded)
	}
}

func TestHostMetricRecordPersistsCompleteCallerEntry(t *testing.T) {
	dataDirectory := t.TempDir()
	invocation := invocationMetrics{}
	invocation.Commands[commandOperationIndex("new")].Invocations = 1
	record := hostMetricRecord{
		Invocation:                   &invocation,
		SessionID:                    "session-one",
		HPatchTokens:                 11,
		ApplyPatchTokens:             19,
		IneffectiveHPatchTokens:      7,
		FailedApplyPatchTokens:       5,
		ReportInputTokens:            3,
		DiagnosticInputTokens:        4,
		MetadataInputTokens:          6,
		DefinitionRequests:           1,
		DefinitionInputTokens:        13,
		RemovedDefinitionInputTokens: 9,
	}
	recordHostMetricForTest(t, dataDirectory, record)
	recordHostMetricForTest(t, dataDirectory, record)

	got, err := readMetrics(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	want := metrics{
		HPatchTokens:                 22,
		ApplyPatchTokens:             38,
		IneffectiveHPatchTokens:      14,
		FailedApplyPatchTokens:       10,
		ReportInputTokens:            6,
		DiagnosticInputTokens:        8,
		MetadataInputTokens:          12,
		Sessions:                     1,
		DefinitionRequests:           2,
		DefinitionInputTokens:        13,
		RemovedDefinitionInputTokens: 9,
	}
	want.Commands[commandOperationIndex("new")].Invocations = 2
	if got != want {
		t.Fatalf("persisted host metrics = %+v, want %+v", got, want)
	}
}

func TestHostMetricRecordRejectsIncompleteOrUnknownInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing invocation", body: `{}`, want: "require invocation counters"},
		{name: "unknown field", body: `{"invocation":{},"future":true}`, want: "unknown field"},
		{name: "definition tokens without request", body: `{"invocation":{},"definition_input_tokens":1}`, want: "require a definition request"},
		{name: "definition request without session", body: `{"invocation":{},"definition_requests":1}`, want: "require a session"},
		{name: "too many definition requests", body: `{"invocation":{},"session_id":"s","definition_requests":2}`, want: "more than one"},
		{name: "trailing data", body: `{"invocation":{}} {}`, want: "trailing data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDirectory := filepath.Join(t.TempDir(), "metrics")
			var stderr bytes.Buffer
			if exitCode := recordHostMetrics(strings.NewReader(test.body), &stderr, dataDirectory); exitCode == 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("record = exit %d, stderr %q, want %q", exitCode, stderr.String(), test.want)
			}
			if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
				t.Fatalf("invalid record created metrics directory: %v", err)
			}
		})
	}
}

func TestInvocationMetricsExportFailurePreventsMutation(t *testing.T) {
	root := t.TempDir()
	dataDirectory := t.TempDir()
	t.Setenv(metricsOutputFileVariable, filepath.Join(t.TempDir(), "missing", "invocation.json"))
	var stdout, stderr bytes.Buffer
	if exitCode := Run(nil, strings.NewReader("new note.txt\ntype \"hello\"\n"), &stdout, &stderr, root, dataDirectory); exitCode == 0 || !strings.Contains(stderr.String(), "writing invocation metrics") {
		t.Fatalf("Run = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("metrics export failure mutated workspace: %v", err)
	}
}

func readInvocationExport(t *testing.T, path string) invocationMetrics {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var value invocationMetrics
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode invocation export %s: %v", content, err)
	}
	return value
}

func recordHostMetricForTest(t *testing.T, dataDirectory string, record hostMetricRecord) {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if exitCode := recordHostMetrics(bytes.NewReader(encoded), &stderr, dataDirectory); exitCode != 0 {
		t.Fatalf("record host metrics = exit %d, stderr %q", exitCode, stderr.String())
	}
}
