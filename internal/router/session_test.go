package router

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRoutingSessionIDKeepsCorrectionHistoryAcrossRequestIDs(t *testing.T) {
	request := parsedResponsesRequest{fields: map[string]json.RawMessage{
		"prompt_cache_key": json.RawMessage(`"prompt-cache"`),
	}}
	firstHeaders := make(http.Header)
	firstHeaders.Set(clientRequestIDHeader, "request-1")
	secondHeaders := make(http.Header)
	secondHeaders.Set(clientRequestIDHeader, "request-2")
	firstSession := routingSessionID(firstHeaders, request)
	secondSession := routingSessionID(secondHeaders, request)
	if firstSession != secondSession {
		t.Fatalf("routing sessions = %q and %q, want one stable session", firstSession, secondSession)
	}

	proxy := newManagedHPatchProxy(t, testTranslator(t, new(int)))
	if err := proxy.rememberBatch(firstSession, map[string]hpatchHistory{
		"call": {
			toolName:         hpatchToolName,
			script:           testHPatchScript,
			translationError: "rejected",
		},
	}); err != nil {
		t.Fatal(err)
	}
	history, err := proxy.correctableHistory(secondSession)
	if err != nil || history.script != testHPatchScript {
		t.Fatalf("correction history = %+v, error %v", history, err)
	}
}

func TestRoutingSessionIDPrefersStableIdentity(t *testing.T) {
	request := parsedResponsesRequest{fields: map[string]json.RawMessage{
		"prompt_cache_key": json.RawMessage(`"prompt-cache"`),
	}}
	tests := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{
			name: "explicit session",
			headers: http.Header{
				http.CanonicalHeaderKey(sessionIDHeader):       []string{"session"},
				http.CanonicalHeaderKey(clientRequestIDHeader): []string{"request"},
			},
			want: "session",
		},
		{
			name:    "prompt cache before request",
			headers: http.Header{http.CanonicalHeaderKey(clientRequestIDHeader): []string{"request"}},
			want:    "prompt-cache",
		},
		{
			name:    "request fallback",
			headers: http.Header{http.CanonicalHeaderKey(clientRequestIDHeader): []string{"request"}},
			want:    "request",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			if test.name == "request fallback" {
				candidate.fields = map[string]json.RawMessage{}
			}
			if got := routingSessionID(test.headers, candidate); got != test.want {
				t.Fatalf("routingSessionID() = %q, want %q", got, test.want)
			}
		})
	}
}
