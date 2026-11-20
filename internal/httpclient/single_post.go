package httpclient

import (
	"context"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
)

var ErrSinglePOSTConfiguration = errors.New("non-idempotent provider POST conflicts with its reviewed route, settings or timeout")

// SinglePOSTRoutes describes provider-declared synchronous endpoints. Paths are
// reserved on that provider connection, including when an override targets one.
type SinglePOSTRoutes struct {
	Origin      string
	Paths       []string
	MinimumWait time.Duration
}

type singlePOSTDeadlineKey struct{}

// WithSinglePOSTDeadline supplies the worker's execution budget to synchronous
// routes without changing polling or legacy adapters.
func WithSinglePOSTDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, singlePOSTDeadlineKey{}, deadline)
}

func (p *SinglePOSTRoutes) MatchesPath(value string) bool {
	return p != nil && slices.Contains(p.Paths, path.Clean(value))
}

// Prepare checks the final destination and prevents net/http from rewinding a
// nonempty body. The configured connection must call it after its transformations.
func (p *SinglePOSTRoutes) Prepare(r *http.Request, expectedPath string) error {
	if !slices.Contains(p.Paths, expectedPath) || r.Method != http.MethodPost ||
		r.URL.Scheme != "https" || r.URL.Opaque != "" || r.URL.Path != expectedPath || r.URL.String() != p.Origin+expectedPath ||
		(r.Host != "" && r.Host != r.URL.Host) || r.Body == nil || r.Body == http.NoBody {
		return ErrSinglePOSTConfiguration
	}
	for key := range r.Header {
		if strings.EqualFold(key, "Idempotency-Key") || strings.EqualFold(key, "X-Idempotency-Key") {
			return ErrSinglePOSTConfiguration
		}
	}
	r.GetBody = nil
	return nil
}

// Do gives the native response an explicit budget. A shorter explicit request
// or connection budget is rejected before dispatch. Caller cancellation and
// lease loss still interrupt the HTTP operation.
func (p *SinglePOSTRoutes) Do(client *http.Client, r *http.Request, expectedPath string, connectionTimeout time.Duration) (*http.Response, error) {
	if err := p.Prepare(r, expectedPath); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	timeout := connectionTimeout
	if override, ok := r.Context().Value(requestTimeoutKey{}).(time.Duration); ok {
		timeout = override
	}
	maximum := p.MinimumWait + 30*time.Second
	if timeout == 0 || timeout > maximum {
		timeout = maximum
	}
	if deadline, ok := r.Context().Value(singlePOSTDeadlineKey{}).(time.Time); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	if deadline, ok := r.Context().Deadline(); timeout < p.MinimumWait || (ok && time.Until(deadline) < p.MinimumWait) {
		_ = r.Body.Close()
		return nil, ErrSinglePOSTConfiguration
	}
	copy := *client
	copy.Timeout = timeout
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return copy.Do(r)
}
