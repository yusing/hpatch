package capturer

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

var errDecodedPayloadTooLarge = errors.New("decoded response exceeds capture observation limit")

func decodedCapturePayload(payload []byte, contentEncoding string) ([]byte, error) {
	switch strings.TrimSpace(strings.ToLower(contentEncoding)) {
	case "":
		return payload, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		decompressed, readErr := io.ReadAll(io.LimitReader(reader, maxObservedResponseBytes+1))
		closeErr := reader.Close()
		if len(decompressed) > maxObservedResponseBytes {
			return nil, errors.Join(errDecodedPayloadTooLarge, readErr, closeErr)
		}
		return decompressed, errors.Join(readErr, closeErr)
	default:
		return nil, errors.New("unsupported content encoding")
	}
}

func observeResponse(payload []byte, contentType string, record *captureRecord, codec tokenizer.Codec) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || capturedPayloadLooksLikeSSE(payload) {
		var dataParts [][]byte
		observeEvent := func() {
			if len(dataParts) != 0 {
				observeResponseJSON(bytes.Join(dataParts, []byte{'\n'}), record, codec)
				dataParts = dataParts[:0]
			}
		}
		for line := range bytes.SplitSeq(payload, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				observeEvent()
				continue
			}
			if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				dataParts = append(dataParts, bytes.TrimSpace(data))
			}
		}
		observeEvent()
		return
	}
	observeResponseJSON(payload, record, codec)
}

func capturedPayloadLooksLikeSSE(payload []byte) bool {
	payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
	for line := range bytes.SplitSeq(payload, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == ':' {
			return true
		}
		field, _, _ := bytes.Cut(line, []byte{':'})
		return bytes.Equal(field, []byte("data")) || bytes.Equal(field, []byte("event")) ||
			bytes.Equal(field, []byte("id")) || bytes.Equal(field, []byte("retry"))
	}
	return false
}

func observeResponseJSON(payload []byte, record *captureRecord, codec tokenizer.Codec) {
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	var event struct {
		Type     string          `json:"type"`
		Item     json.RawMessage `json:"item"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		if record.CaptureError == "" {
			record.CaptureError = "invalid response JSON"
		}
		return
	}
	if len(event.Item) != 0 {
		observeOutputItem(event.Item, record, codec)
	}
	if len(event.Response) != 0 {
		observeResponseEnvelope(event.Response, record, codec)
		return
	}
	observeResponseEnvelope(payload, record, codec)
}

func observeResponseEnvelope(payload []byte, record *captureRecord, codec tokenizer.Codec) {
	var response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  *struct {
			InputTokens  uint64 `json:"input_tokens"`
			OutputTokens uint64 `json:"output_tokens"`
			InputDetails struct {
				CachedTokens uint64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens uint64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return
	}
	if response.Status != "" {
		record.ResponseStatus = response.Status
	}
	for _, item := range response.Output {
		observeOutputItem(item, record, codec)
	}
	if response.Usage != nil {
		record.Usage = &tokenUsage{
			InputTokens:     response.Usage.InputTokens,
			CachedTokens:    response.Usage.InputDetails.CachedTokens,
			OutputTokens:    response.Usage.OutputTokens,
			ReasoningTokens: response.Usage.OutputDetails.ReasoningTokens,
		}
	}
}

func observeOutputItem(payload []byte, record *captureRecord, codec tokenizer.Codec) {
	var item struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Input     string `json:"input"`
		Arguments string `json:"arguments"`
	}
	if json.Unmarshal(payload, &item) != nil || (item.Type != "custom_tool_call" && item.Type != "function_call") {
		return
	}
	callID := cmp.Or(item.CallID, item.ID)
	input := cmp.Or(item.Input, item.Arguments)
	inputTokens, inputErr := codec.Count(input)
	itemTokens, itemErr := codec.Count(string(payload))
	if inputErr != nil || itemErr != nil || inputTokens < 0 || itemTokens < 0 || callID == "" || item.Name == "" {
		return
	}
	metric := toolCallMetrics{
		CallID: callID, Name: item.Name,
		InputBytes: uint64(len(input)), InputTokens: uint64(inputTokens),
		ItemBytes: uint64(len(payload)), ItemTokens: uint64(itemTokens),
	}
	metric.Kind, metric.Diagnostic = classifyToolInput(item.Name, input)
	for index := range record.ToolCalls {
		if record.ToolCalls[index].CallID == callID {
			if metric.InputBytes != 0 || record.ToolCalls[index].InputBytes == 0 {
				record.ToolCalls[index] = metric
			}
			return
		}
	}
	record.ToolCalls = append(record.ToolCalls, metric)
}

func classifyToolInput(name, input string) (string, string) {
	if name != "exec" {
		return "", ""
	}
	switch {
	case strings.Contains(input, "tools.apply_patch("):
		return "apply_patch", ""
	case strings.Contains(input, "tools.exec_command("):
		return "exec_command", ""
	}
	encoded, ok := strings.CutPrefix(input, "text(")
	if !ok {
		return "other", ""
	}
	encoded, ok = strings.CutSuffix(encoded, ");")
	if !ok {
		return "other", ""
	}
	text, err := strconv.Unquote(encoded)
	if err != nil {
		return "other", ""
	}
	if strings.HasPrefix(text, "in ") && strings.Contains(text, "\nlast ") && strings.Contains(text, "\nfiles ") {
		return "hpatch_report", ""
	}
	if code := hpatchDiagnosticCode(text); code != "" {
		return "hpatch_diagnostic", code
	}
	return "other", ""
}

func hpatchDiagnosticCode(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	command, reason, ok := strings.Cut(line, ", reason ")
	if !ok || !strings.Contains(command, ": command ") {
		return ""
	}
	code, _, ok := strings.Cut(reason, ":")
	if !ok {
		return ""
	}
	switch code {
	case "script-syntax", "row-missing", "row-stale", "occurrence-missing", "invalid-count",
		"target-order", "edit-conflict", "active-file", "initialization", "file-path",
		"language-syntax", "other":
		return code
	default:
		return ""
	}
}

func requestToolNames(tools []json.RawMessage) []string {
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		var tool struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &tool) == nil && tool.Name != "" && !slices.Contains(names, tool.Name) {
			names = append(names, tool.Name)
		}
	}
	slices.Sort(names)
	return names
}
