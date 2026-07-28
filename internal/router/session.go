package router

// Source: session.go:11:100 bounded session identity selection.

import (
	"net/http"
	"strings"
)

const (
	sessionIDHeader       = "session-id"
	clientRequestIDHeader = "x-client-request-id"
	maxSessionIDBytes     = 512
	maxSessionHistories   = 256
	maxSessionTurns       = 128
)

func routingSessionID(headers http.Header, request parsedResponsesRequest) string {
	for _, name := range []string{sessionIDHeader, clientRequestIDHeader} {
		for _, value := range headers.Values(name) {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return request.promptCacheKey()
}
