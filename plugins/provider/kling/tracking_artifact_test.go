package kling

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type trackingTestBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *trackingTestBody) Close() error { b.closed.Add(1); return nil }

type trackingZeros struct{}

func (trackingZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestTrackingArtifactIsOpaqueScopedAndNeverUsesProviderAuth(t *testing.T) {
	ctx := legacyScopeFixture("task-owner", "provider-owner", "connection-one", "revision-one")
	providerCalls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		providerCalls++
		return nil, errors.New("provider client must not download artifacts")
	})
	raw := "https://cdn.example.test/private-tracking?signature=private-synthetic-value"
	artifact, err := a.createTrackingArtifact(ctx, raw)
	if err != nil || artifact.MediaType != "binary" || artifact.Format != "bin" || artifact.ID != "selection_tracking" || artifact.URL != "" || artifact.Text != "" || strings.Contains(artifact.SourceReference, "cdn.example.test") {
		t.Fatal(artifact, err)
	}
	encoded := strings.TrimPrefix(artifact.SourceReference, trackingReferencePrefix)
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || bytes.Contains(ciphertext, []byte(raw)) {
		t.Fatal("tracking source was not encrypted")
	}
	for name, foreign := range map[string]context.Context{
		"same key other connection": legacyScopeFixture("task-owner", "provider-owner", "connection-two", "revision-one"),
		"data owner":                legacyScopeFixture("other-owner", "provider-owner", "connection-one", "revision-one"),
		"provider owner":            legacyScopeFixture("task-owner", "other-provider-owner", "connection-one", "revision-one"),
		"revision":                  legacyScopeFixture("task-owner", "provider-owner", "connection-one", "revision-two"),
		"blank revision":            legacyScopeFixture("task-owner", "provider-owner", "connection-one", ""),
		"no scope":                  context.Background(),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}
			if _, err := a.openTrackingArtifact(foreign, artifact.SourceReference, client); err == nil || calls != 0 {
				t.Fatal("foreign source reached network", err, calls)
			}
		})
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(raw)
	jar.SetCookies(u, []*http.Cookie{{Name: "private-cookie", Value: "never-forward"}})
	body := &trackingTestBody{Reader: strings.NewReader("opaque tracking bytes")}
	client := &http.Client{Jar: jar, Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != raw || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") == "" {
			t.Fatal("incorrect tracking fetch scope or headers")
		}
		return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{"Content-Type": {"text/html"}}, Body: body}, nil
	})}
	// A newly created adapter can recover the reference using the accepted scope.
	restarted := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("API client used"); return nil, nil })
	source, err := restarted.openTrackingArtifact(ctx, artifact.SourceReference, client)
	if err != nil || source.ContentType != "application/octet-stream" || source.SizeBytes != -1 {
		t.Fatal(source, err)
	}
	data, err := io.ReadAll(source.Reader)
	source.Reader.Close()
	source.Reader.Close()
	if err != nil || string(data) != "opaque tracking bytes" || body.closed.Load() != 1 || providerCalls != 0 {
		t.Fatal(err, body.closed.Load(), providerCalls)
	}
	for _, token := range []string{raw, artifact.SourceReference + "x", trackingReferencePrefix + "invalid"} {
		if _, err := a.trackingSourceURL(ctx, token); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	for _, bad := range []string{"http://cdn.example.test/x", "https://127.0.0.1/x", "https://localhost/x", "https://u:p@cdn.example.test/x", "https://cdn.example.test/x#fragment"} {
		if _, err := a.createTrackingArtifact(ctx, bad); err == nil {
			t.Fatal("unsafe source accepted", bad)
		}
	}
}
func TestTrackingArtifactResponseLimitsClosureAndRedaction(t *testing.T) {
	ctx := legacyScopeFixture("owner", "provider-owner", "connection", "revision")
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("provider network"); return nil, nil })
	ref, err := a.privateSourceReference(ctx, "https://cdn.example.test/tracking?secret=synthetic-private-value")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status int
		size   int64
	}{{"too large", 200, maxTrackingArtifactBytes + 1}, {"redirect", 307, -1}, {"partial", 206, 10}, {"bad status", 403, -1}} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackingTestBody{Reader: strings.NewReader("private upstream body")}
			client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, ContentLength: tc.size, Body: body, Header: http.Header{}}, nil
			})}
			_, err := a.openTrackingArtifact(ctx, ref, client)
			if err == nil || body.closed.Load() != 1 || strings.Contains(err.Error(), "synthetic-private") || strings.Contains(err.Error(), "upstream") {
				t.Fatal(err, body.closed.Load())
			}
		})
	}
	for _, length := range []int64{maxTrackingArtifactBytes, maxTrackingArtifactBytes + 1} {
		body := &trackingTestBody{Reader: io.LimitReader(trackingZeros{}, length)}
		client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: body}, nil
		})}
		source, err := a.openTrackingArtifact(ctx, ref, client)
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, source.Reader)
		source.Reader.Close()
		if (err == nil) != (length == maxTrackingArtifactBytes) || n > maxTrackingArtifactBytes || body.closed.Load() != 1 {
			t.Fatal(length, n, err, body.closed.Load())
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	calls := 0
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}
	if _, err := a.openTrackingArtifact(cancelled, ref, client); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
}
