package agnes

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestExplicitAuthenticationResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{"valid", 200, `{"object":"list","data":[{"id":"agnes-video-2.5"}]}`, true},
		{"empty authenticated catalog", 200, `{"data":[]}`, true},
		{"invalid key", 401, `{"error":{"message":"fixture-secret"}}`, false},
		{"forbidden", 403, `{}`, false}, {"quota", 429, `{}`, false}, {"unavailable", 503, `{}`, false},
		{"semantic error", 200, `{"error":{"message":"fixture-secret"},"data":[]}`, false},
		{"semantic code", 200, `{"code":401,"data":[]}`, false},
		{"semantic success false", 200, `{"success":false,"data":[]}`, false},
		{"malformed", 200, `{`, false}, {"absent data", 200, `{}`, false},
		{"null data", 200, `{"data":null}`, false}, {"bad metadata", 200, `{"data":[{}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/models" || !providerkit.IsCredentialCheck(r.Context()) || r.Header.Get("Authorization") == "" {
					t.Fatal("wrong auth request")
				}
				return response(tc.status, tc.body), nil
			})
			if !a.CredentialStatus().CheckedAt.IsZero() {
				t.Fatal("unrequested verification")
			}
			err := a.CheckCredentials(context.Background())
			if (err == nil) != tc.valid || calls != 1 || a.CredentialStatus().CheckedAt.IsZero() || (a.CredentialStatus().Err == nil) != tc.valid {
				t.Fatal(err, calls, a.CredentialStatus())
			}
			if err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("response leaked")
			}
			a.check.CheckedAt = time.Now().Add(-6 * time.Minute)
			if !a.CredentialStatus().CheckedAt.IsZero() {
				t.Fatal("stale verification")
			}
		})
	}
}

func TestAuthenticationFailureReplacesSuccess(t *testing.T) {
	fail := false
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		if fail {
			return nil, errors.New("private fixture-secret")
		}
		return response(200, `{"data":[]}`), nil
	})
	if err := a.CheckCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := a.CheckCredentials(context.Background()); err == nil || strings.Contains(err.Error(), "fixture-secret") || a.CredentialStatus().Err == nil {
		t.Fatal(err)
	}
	if _, err := New("", DefaultBaseURL, nil, 0); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestAuthenticationDoesNotBlockStatusAndQueuedCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return response(200, `{"data":[]}`), nil
	})
	done := make(chan error, 1)
	go func() { done <- a.CheckCredentials(context.Background()) }()
	<-started
	statusDone := make(chan struct{})
	go func() { a.CredentialStatus(); close(statusDone) }()
	select {
	case <-statusDone:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("status blocked by check")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.CheckCredentials(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
