package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserStorageCheckRejectsMissingCORS(t *testing.T) {
	const origin = "https://gateway.test:9443"
	for _, failure := range []string{"", "denied", "origin", "method", "digest", "type", "wildcard", "redirect", "get", "head"} {
		t.Run(failure, func(t *testing.T) {
			requests := 0
			remote := &memoryS3{objects: map[string][]byte{}, types: map[string]string{}}
			transport := referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "OPTIONS" {
					return remote.RoundTrip(r)
				}
				requests++
				if r.Header.Get("Origin") != origin || r.Header.Get("Access-Control-Request-Method") == "" {
					t.Fatal("incorrect browser preflight")
				}
				for _, name := range []string{"Authorization", "Cookie", "X-API-Key"} {
					if r.Header.Get(name) != "" {
						t.Fatal("preflight leaked credentials", name)
					}
				}
				if r.URL.Query().Get("X-Amz-Signature") == "" {
					t.Fatal("probe did not use a signed storage destination")
				}
				w := httptest.NewRecorder()
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, HEAD")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Amz-Content-Sha256, Range")
				switch failure {
				case "denied":
					w.WriteHeader(403)
				case "origin":
					w.Header().Set("Access-Control-Allow-Origin", "https://another.test")
				case "method":
					w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD")
				case "digest":
					w.Header().Set("Access-Control-Allow-Headers", "content-type")
				case "type":
					w.Header().Set("Access-Control-Allow-Headers", "x-amz-content-sha256")
				case "wildcard":
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Set("Access-Control-Allow-Headers", "*")
				case "get", "head":
					if strings.EqualFold(r.Header.Get("Access-Control-Request-Method"), failure) {
						w.WriteHeader(403)
					}
				case "redirect":
					w.Header().Set("Location", "https://another.test/leak")
					w.WriteHeader(307)
				}
				return w.Result(), nil
			})
			m := newTestManager(t, newSettingsMemory(), remote)
			m.client = &http.Client{Transport: transport}
			input := s3Update()
			// Headless use does not require browser CORS and must not send OPTIONS.
			if err := m.Check(context.Background(), input); err != nil || requests != 0 {
				t.Fatalf("headless check: %v, preflights %d", err, requests)
			}
			input.BrowserOrigin = origin
			allowed := failure == "" || failure == "wildcard"
			err := m.Check(context.Background(), input)
			if (err == nil) != allowed {
				t.Fatalf("check: %v", err)
			}
			if !allowed && (!errors.Is(err, ErrInvalidConfig) || strings.Contains(err.Error(), "X-Amz-Signature")) {
				t.Fatalf("unsafe CORS error: %v", err)
			}
			if allowed && requests != 3 {
				t.Fatal("PUT, GET and HEAD must be checked", requests)
			}
			if len(m.repo.(*settingsMemory).active) != 0 {
				t.Fatal("check activated storage")
			}

		})
	}
}

func TestB2BrowserCheckRequiresExplicitAuthorizationHeader(t *testing.T) {
	for _, allowedHeaders := range []string{"*", "content-type,x-amz-date,x-amz-content-sha256", "Content-Type,Authorization,X-Amz-Date,X-Amz-Content-Sha256,Range"} {
		s, err := NewS3(S3Config{Endpoint: "https://s3.eu-central-003.backblazeb2.com", Region: "eu-central-003", Bucket: "test-bucket", PathStyle: true, AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, &http.Client{Transport: referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != "OPTIONS" || (r.Header.Get("Access-Control-Request-Method") == "PUT" && !strings.Contains(r.Header.Get("Access-Control-Request-Headers"), "authorization")) || r.Header.Get("Authorization") != "" {
				t.Fatal("B2 preflight must request Authorization without sending credentials")
			}
			w := httptest.NewRecorder()
			w.Header().Set("Access-Control-Allow-Origin", "https://test1.example")
			w.Header().Set("Access-Control-Allow-Methods", "PUT, GET, HEAD")
			w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			return w.Result(), nil
		})}, 1024)
		if err != nil {
			t.Fatal(err)
		}
		err = s.CheckBrowser(context.Background(), "https://test1.example")
		if (err == nil) != strings.Contains(allowedHeaders, "Authorization") {
			t.Fatalf("headers %q: %v", allowedHeaders, err)
		}
	}
}
