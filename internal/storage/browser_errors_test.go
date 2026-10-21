package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Exercise the real SDK serialization and error decoder against HTTP fixtures.
// These are not live B2 API tests.
func TestBrowserCORSFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, code, want string
		status           int
	}{
		{"denied", "AccessDenied", "AccessDenied", 403},
		{"bucket", "NoSuchBucket", "NoSuchBucket", 404},
		{"signature", "SignatureDoesNotMatch", "signing region", 403},
		{"unsupported", "NotImplemented", "does not support", 501},
		{"format", "InvalidRequest", "request format", 400},
		{"service", "ServiceUnavailable", "temporarily unavailable", 503},
		{"unknown", "secret-from-provider", "response could not be read", 400},
		{"malformed", "", "response could not be read", 200},
		{"network", "", "network connectivity", 0},
	} {
		for _, phase := range []string{"read", "update", "verify"} {
			if tc.name == "malformed" && phase == "update" {
				continue
			}
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				reads, writes := 0, 0
				client := &http.Client{Transport: referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
					w := httptest.NewRecorder()
					if r.Method == "OPTIONS" {
						w.WriteHeader(403)
						return w.Result(), nil
					}
					if !r.URL.Query().Has("cors") || r.Header.Get("Authorization") == "" {
						t.Fatal("expected signed bucket CORS request")
					}
					if tc.name == "denied" && phase == "read" {
						t.Logf("SDK request: %s %s://%s%s?%s (Authorization omitted)", r.Method, r.URL.Scheme, r.URL.Host, r.URL.Path, r.URL.RawQuery)
					}
					current := "update"
					if r.Method == "GET" {
						reads++
						current = "read"
						if writes > 0 {
							current = "verify"
						}
					} else if r.Method == "PUT" {
						writes++
					} else {
						t.Fatal(r.Method)
					}
					if current != phase {
						if r.Method == "GET" {
							fmt.Fprint(w, `<CORSConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`)
						}
						return w.Result(), nil
					}
					if tc.status == 0 {
						return nil, errors.New("secret-from-provider")
					}
					w.WriteHeader(tc.status)
					if tc.code == "" {
						fmt.Fprint(w, `<broken secret-from-provider`)
					} else {
						fmt.Fprintf(w, `<Error><Code>%s</Code><Message>secret-from-provider</Message></Error>`, tc.code)
					}
					return w.Result(), nil
				})}
				store, err := NewS3(S3Config{Endpoint: "https://s3.eu-central-003.backblazeb2.com", Region: "eu-central-003", Bucket: "test-bucket", PathStyle: true, AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, client, 1024)
				if err != nil {
					t.Fatal(err)
				}
				opts := store.client.Options()
				opts.RetryMaxAttempts = 1
				store.client = s3.New(opts)
				_, err = store.EnsureBrowser(context.Background(), "https://gateway.example.test")
				if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "could not "+phase) {
					t.Fatalf("want %s / %s, got %v", phase, tc.want, err)
				}
				if tc.status != 0 && !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", tc.status)) && tc.name != "malformed" {
					t.Fatal("missing status", err)
				}
				for _, secret := range []string{"secret-from-provider", "test-key", "test-secret"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatal("unsafe error", err)
					}
				}
				if tc.code != "AccessDenied" && strings.Contains(err.Error(), "permissions") {
					t.Fatal("misleading permission diagnosis", err)
				}
				if phase == "read" && writes != 0 {
					t.Fatal("read error mutated bucket")
				}
				if phase != "read" && (!strings.Contains(err.Error(), "unconfirmed") || strings.Contains(err.Error(), "were not changed")) {
					t.Fatal("uncertain write reported as unchanged", err)
				}
			})
		}
	}
}
