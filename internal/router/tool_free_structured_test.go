package router

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func titleRequestFields() map[string]any {
	return map[string]any{
		"model":        "gpt-test",
		"instructions": "Generate a short title. Keep these instructions unchanged.",
		"input":        []any{map[string]any{"type": "message", "role": "user", "content": "!ctp2 D\nFormat Run previews"}},
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "title", "strict": true,
			"schema": map[string]any{"type": "object", "properties": map[string]any{
				"title": map[string]any{"type": "string"},
			}, "required": []string{"title"}, "additionalProperties": false},
		}},
	}
}

func TestExecuteToolFreeStructuredRequest(t *testing.T) {
	for _, catalog := range []string{"omitted", "null", "empty", "additional", "both"} {
		t.Run(catalog, func(t *testing.T) {
			fields := titleRequestFields()
			switch catalog {
			case "null":
				fields["tools"] = nil
			case "empty", "both":
				fields["tools"] = []any{}
			}
			if catalog == "additional" || catalog == "both" {
				fields["input"] = append([]any{map[string]any{"type": "additional_tools", "tools": []any{}}}, fields["input"].([]any)...)
			}
			request, err := parseResponsesRequest(mustTestJSON(t, fields))
			if err != nil {
				t.Fatal(err)
			}
			response := `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"title\":\"Format Run previews\"}"}]}]}`
			provider := &serverFakeProvider{results: []serverForwardResult{{response: serverHTTPResponse(response)}}}
			proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
			var output bytes.Buffer
			err = executeRequest(t.Context(), t.Context(), request, serverMetadataHeaders(t, "turn", nil), "title-session", provider, &output, nil, proxy, mustCTP2Codec(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.forwarded) != 1 || !sameJSONValue(provider.forwarded[0], mustTestJSON(t, fields)) {
				t.Fatalf("structured request changed: %s", provider.forwarded)
			}
			if !sameJSONValue(output.Bytes(), []byte(response)) {
				t.Fatalf("structured response changed: %s", output.Bytes())
			}
		})
	}
}

func TestToolFreeStructuredRequestDoesNotBypassAdmission(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any, http.Header)
	}{
		{"nonempty tools", func(f map[string]any, _ http.Header) {
			f["tools"] = []any{map[string]any{"type": "function", "name": "lookup"}}
		}},
		{"malformed tools", func(f map[string]any, _ http.Header) { f["tools"] = "invalid" }},
		{"nonempty additional tools", func(f map[string]any, _ http.Header) {
			f["input"] = []any{map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "function", "name": "lookup"}}}}
		}},
		{"malformed additional tools", func(f map[string]any, _ http.Header) {
			f["input"] = []any{map[string]any{"type": "additional_tools", "tools": "invalid"}}
		}},
		{"unstructured", func(f map[string]any, _ http.Header) { delete(f, "text") }},
		{"missing metadata", func(_ map[string]any, h http.Header) { h.Del(codexTurnMetadataHeader) }},
		{"missing thread", func(_ map[string]any, h http.Header) { h.Del(threadIDHeader) }},
		{"generating prewarm", func(_ map[string]any, h http.Header) { h.Set(codexTurnMetadataHeader, `{"request_kind":"prewarm"}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := titleRequestFields()
			headers := serverMetadataHeaders(t, "turn", nil)
			test.mutate(fields, headers)
			request, err := parseResponsesRequest(mustTestJSON(t, fields))
			if err != nil {
				t.Fatal(err)
			}
			provider := &serverFakeProvider{}
			err = executeRequest(t.Context(), t.Context(), request, headers, "title-session", provider, io.Discard, nil, newManagedHPatchProxy(t, testTranslator(t, new(int))), nil, nil)
			if err == nil || len(provider.forwarded) != 0 {
				t.Fatalf("invalid request admitted: error=%v forwards=%d", err, len(provider.forwarded))
			}
		})
	}
}

func TestResponsesWebSocketToolFreeStructuredTurnAfterPrewarm(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	headers := codexAuthHeaders()
	headers.Set(sessionIDHeader, "title-session")
	headers.Set(threadIDHeader, "title-thread")
	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	conn := testResponsesSocket(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.CloseNow()
		for _, id := range []string{"warm", "title"} {
			request, err := providerSocketRead(ctx, upstream)
			if err != nil {
				t.Error(err)
				return
			}
			if id == "title" {
				if jsonString(request, "previous_response_id") != "warm" || !sameJSONValue(request["text"], mustTestJSON(t, titleRequestFields()["text"])) {
					t.Errorf("title continuation changed: %s", mustMarshalJSON(request))
				}
				if strings.Contains(string(request["input"]), "hpatch-model-instructions") || len(request["tools"]) != 0 {
					t.Error("title request acquired editing guidance or tools")
				}
			}
			if err := providerSocketWrite(ctx, upstream, socketEvent("response.completed", id)); err != nil {
				t.Error(err)
				return
			}
		}
		_, _, _ = upstream.Read(ctx)
	}), proxy, nil, headers)
	metadata := func(kind string) map[string]string {
		return map[string]string{codexTurnMetadataHeader: string(mustMarshalJSON(codexTurnMetadata{RequestKind: kind}))}
	}
	socketWrite(t, ctx, conn, map[string]any{
		"type": "response.create", "model": "gpt-test", "generate": false,
		"input":           []any{map[string]any{"type": "additional_tools", "tools": []any{}}},
		"client_metadata": metadata("prewarm"),
	})
	if event := socketRead(t, ctx, conn); jsonString(event, "type") != "response.completed" {
		t.Fatalf("prewarm failed: %s", mustMarshalJSON(event))
	}
	fields := titleRequestFields()
	fields["type"] = "response.create"
	fields["previous_response_id"] = "warm"
	fields["client_metadata"] = metadata("turn")
	socketWrite(t, ctx, conn, fields)
	if event := socketRead(t, ctx, conn); jsonString(event, "type") != "response.completed" {
		t.Fatalf("title turn failed: %s", mustMarshalJSON(event))
	}
}
