package hpatch

import "context"

// AttemptMetadata identifies one router-owned hpatch attempt and its recovery chain.
type AttemptMetadata struct {
	SessionID     string
	Title         string
	CorrelationID string
	CallID        string
	Attempt       int
	Model         string
	Correction    bool
}

type attemptMetadataContextKey struct{}

// WithAttemptMetadata attaches host-owned attempt identity to diagnostics and hooks.
func WithAttemptMetadata(ctx context.Context, metadata AttemptMetadata) context.Context {
	return context.WithValue(ctx, attemptMetadataContextKey{}, metadata)
}

func attemptMetadataFromContext(ctx context.Context) (AttemptMetadata, bool) {
	if ctx == nil {
		return AttemptMetadata{}, false
	}
	metadata, ok := ctx.Value(attemptMetadataContextKey{}).(AttemptMetadata)
	if !ok || metadata.SessionID == "" || metadata.CorrelationID == "" || metadata.CallID == "" || metadata.Attempt < 1 {
		return AttemptMetadata{}, false
	}
	return metadata, true
}
