package capturer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestChatCaptureRecordsActualToolShapeAndCompletion(t *testing.T) {
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatal(err)
	}
	stream := "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"exec","arguments":"{\"input\":"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"text(1)\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	var record captureRecord
	output := observeChatResponse([]byte(stream), "text/event-stream", &record, codec)
	if record.ResponseStatus != "completed" || len(record.ToolCalls) != 1 || record.ToolCalls[0].CallID != "c1" || record.ToolCalls[0].Name != "exec" || !json.Valid(output) {
		t.Fatalf("record=%+v output=%s", record, output)
	}
	record = captureRecord{}
	if output := observeChatResponse([]byte(strings.TrimSuffix(stream, "data: [DONE]\n\n")), "text/event-stream", &record, codec); output != nil || record.ResponseStatus != "" {
		t.Fatal("truncated chat completion was counted as completed")
	}
	names := requestToolNames([]json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"exec"}}`)})
	if len(names) != 1 || names[0] != "exec" {
		t.Fatalf("names=%v", names)
	}
}
