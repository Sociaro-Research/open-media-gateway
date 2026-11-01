package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

type sourcePrivacyTransport func(*http.Request) (*http.Response, error)

func (f sourcePrivacyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type sourcePrivacyBody struct{ closed int }

func (b *sourcePrivacyBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *sourcePrivacyBody) Close() error             { b.closed++; return nil }

func assertSourceErrorPrivate(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected synthetic source failure")
	}
	for _, private := range []string{"https://", "cdn.example.test", "signature", "private-signed-value", "private-transport-detail", "private-status-detail", "private-redirect-detail"} {
		if strings.Contains(err.Error(), private) {
			t.Errorf("private source diagnostic escaped: %q", err.Error())
		}
	}
}

func TestSourceArchiveErrorsKeepSignedURLsAndTransportDiagnosticsPrivate(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		raw  string
	}{
		{"parse", context.Background(), "https://cdn.example.test/%zz?signature=private-signed-value"},
		{"request", nil, "https://cdn.example.test/video?signature=private-signed-value"},
		{"download", context.Background(), "https://cdn.example.test/video?signature=private-signed-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: sourcePrivacyTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.raw {
					t.Fatal("source destination changed")
				}
				return nil, errors.New("private-transport-detail")
			})}
			s, err := NewLocal(t.TempDir(), client, 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Archive(tc.ctx, "source-error", tc.raw)
			assertSourceErrorPrivate(t, err)
			if (tc.name == "download" && calls != 1) || (tc.name != "download" && calls != 0) {
				t.Fatal("unexpected transport calls", calls)
			}
		})
	}
}

func TestSourceArchiveDownloadErrorsPreserveCancellationClassification(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			client := &http.Client{Transport: sourcePrivacyTransport(func(*http.Request) (*http.Response, error) {
				return nil, cause
			})}
			s, err := NewLocal(t.TempDir(), client, 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Archive(context.Background(), "cancelled-source", "https://cdn.example.test/video?signature=private-signed-value")
			assertSourceErrorPrivate(t, err)
			if !errors.Is(err, cause) {
				t.Fatal("source error lost cancellation classification", err)
			}
		})
	}
}

func TestSourceArchiveStatusAndRedirectErrorsCloseBodiesWithoutPrivateDetails(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		name := "status"
		if redirect {
			name = "redirect response and error"
		}
		t.Run(name, func(t *testing.T) {
			body := &sourcePrivacyBody{}
			client := &http.Client{Transport: sourcePrivacyTransport(func(*http.Request) (*http.Response, error) {
				status, code := "403 private-status-detail", 403
				header := http.Header{}
				if redirect {
					status, code = "302 private-status-detail", 302
					header.Set("Location", "https://cdn.example.test/redirect?signature=private-signed-value")
				}
				return &http.Response{StatusCode: code, Status: status, Header: header, Body: body}, nil
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("private-redirect-detail") }}
			s, err := NewLocal(t.TempDir(), client, 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Archive(context.Background(), "response-error", "https://cdn.example.test/video?signature=private-signed-value")
			assertSourceErrorPrivate(t, err)
			if body.closed == 0 {
				t.Fatal("rejected response body was not closed")
			}
		})
	}
}

func TestSourceArchivePrivateDestinationRemainsBlockedAndClassified(t *testing.T) {
	// A literal private address is rejected before DNS lookup or any dial.
	s, err := NewLocal(t.TempDir(), httpclient.NewPublic(time.Second), 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Archive(context.Background(), "blocked-source", "https://127.0.0.1/video?signature=private-signed-value")
	assertSourceErrorPrivate(t, err)
	if !errors.Is(err, httpclient.ErrOutboundPrivateDestination) {
		t.Fatal("source error weakened or lost private-network classification", err)
	}
}
