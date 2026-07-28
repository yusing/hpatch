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
	fields             map[string]json.RawMessage
	streamResponse     bool
	backgroundResponse bool
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
	return parsedResponsesRequest{
		fields: request, streamResponse: streamResponse, backgroundResponse: backgroundResponse,
	}, nil
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
