package ollama

import "context"

type httpStatusObserverKey struct{}

// WithHTTPStatusObserver observes chat HTTP response statuses without changing
// provider payloads. Retries may report several statuses; no callback means the
// caller has not observed an HTTP response. The observer must be concurrency-safe.
func WithHTTPStatusObserver(ctx context.Context, observer func(int)) context.Context {
	return context.WithValue(ctx, httpStatusObserverKey{}, observer)
}

// ObserveHTTPStatus reports an HTTP response, not the success of its streamed body.
func ObserveHTTPStatus(ctx context.Context, status int) {
	if observer, ok := ctx.Value(httpStatusObserverKey{}).(func(int)); ok && observer != nil {
		observer(status)
	}
}
