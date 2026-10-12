package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type requestIDKey struct{}

// WithRequestID returns a context that carries the request id. The log handler adds it to
// every record as request_id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id in ctx, or "".
func RequestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

// NewRequestID returns 16 random hex characters.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}
