package router

// Source: execution.go:14:205 parsedResponsesRequest and request parsing.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type parsedResponsesRequest struct {
	fields         map[string]json.RawMessage
	streamResponse bool
	toolCatalog    *responsesToolCatalog
}

func (r *parsedResponsesRequest) responseTools() *responsesToolCatalog {
	if r.toolCatalog == nil {
		r.toolCatalog = decodeResponsesToolCatalog(r.fields)
	}
	return r.toolCatalog
}

func (r *parsedResponsesRequest) setInput(input json.RawMessage) {
	r.fields["input"] = input
	if r.toolCatalog == nil {
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(input, &items) != nil {
		return
	}
	groupIndex := 0
	for itemIndex, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil || jsonString(item, "type") != "additional_tools" {
			continue
		}
		if groupIndex >= len(r.toolCatalog.additional) {
			return
		}
		group := r.toolCatalog.additional[groupIndex]
		group.itemIndex = itemIndex
		group.item = item
		groupIndex++
	}
	r.toolCatalog.inputItems = items
}

func (r parsedResponsesRequest) model() string {
	raw, ok := r.fields["model"]
	if !ok {
		return ""
	}
	var model string
	if json.Unmarshal(raw, &model) != nil {
		return ""
	}
	return strings.TrimSpace(model)
}

func (r parsedResponsesRequest) modelDescription() string {
	model := r.model()
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if raw, ok := r.fields["reasoning"]; ok {
		_ = json.Unmarshal(raw, &reasoning)
	}
	return strings.TrimSpace(model + " " + strings.TrimSpace(reasoning.Effort))
}

func (r *parsedResponsesRequest) setModelAndReasoningEffort(model, effort string) error {
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("encode model: %w", err)
	}
	reasoning := map[string]json.RawMessage{}
	if raw, ok := r.fields["reasoning"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &reasoning); err != nil || reasoning == nil {
			return errors.New("reasoning must be an object")
		}
	}
	encodedEffort, err := json.Marshal(effort)
	if err != nil {
		return fmt.Errorf("encode reasoning effort: %w", err)
	}
	reasoning["effort"] = encodedEffort
	encodedReasoning, err := json.Marshal(reasoning)
	if err != nil {
		return fmt.Errorf("encode reasoning: %w", err)
	}
	r.fields["model"] = encodedModel
	r.fields["reasoning"] = encodedReasoning
	return nil
}

func (r parsedResponsesRequest) promptCacheKey() string {
	raw, ok := r.fields["prompt_cache_key"]
	if !ok {
		return ""
	}
	var key string
	if json.Unmarshal(raw, &key) != nil || strings.TrimSpace(key) == "" {
		return ""
	}
	return key
}

func parseResponsesRequest(body []byte) (parsedResponsesRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var request map[string]json.RawMessage
	if err := decoder.Decode(&request); err != nil {
		return parsedResponsesRequest{}, fmt.Errorf("decode Responses request: %w", err)
	}
	if request == nil {
		return parsedResponsesRequest{}, errors.New("responses request must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return parsedResponsesRequest{}, errors.New("decode Responses request: multiple JSON values")
		}
		return parsedResponsesRequest{}, fmt.Errorf("decode Responses request trailing data: %w", err)
	}

	var streamResponse bool
	_ = json.Unmarshal(request["stream"], &streamResponse)
	var backgroundResponse bool
	_ = json.Unmarshal(request["background"], &backgroundResponse)
	if backgroundResponse {
		return parsedResponsesRequest{}, errors.New("background Responses requests are not supported")
	}
	return parsedResponsesRequest{fields: request, streamResponse: streamResponse}, nil
}

func readResponsesRequest(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read Responses request: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("responses request body is empty")
	}
	return body, nil
}
