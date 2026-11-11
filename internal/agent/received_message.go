package agent

import "context"

type receivedMessageKey struct{}

// WithReceivedMessage identifies the user row atomically saved with the
// browser's receipt. Chat must reuse it rather than recording the request twice.
func WithReceivedMessage(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, receivedMessageKey{}, id)
}
