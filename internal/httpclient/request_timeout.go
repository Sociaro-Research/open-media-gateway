package httpclient

import (
	"context"
	"net/http"
	"time"
)

type requestTimeoutKey struct{}

// WithRequestTimeout sets the HTTP budget for generation calls only. It does
// not extend an earlier caller deadline or alter a shared client's timeout.
func WithRequestTimeout(ctx context.Context, timeout time.Duration) context.Context {
	return context.WithValue(ctx, requestTimeoutKey{}, timeout)
}

// Do applies a per-generation override after adapter-specific client copies.
// Keeping the deadline on http.Client also bounds reading the response body.
func Do(client *http.Client, request *http.Request) (*http.Response, error) {
	if timeout, ok := request.Context().Value(requestTimeoutKey{}).(time.Duration); ok && timeout > 0 {
		copied := *client
		copied.Timeout = timeout
		client = &copied
	}
	return client.Do(request)
}
