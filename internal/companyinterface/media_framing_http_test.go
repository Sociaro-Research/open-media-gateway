package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

type mediaResponseWire struct {
	base   http.RoundTripper
	modify func(*http.Response)
}

func (w mediaResponseWire) RoundTrip(r *http.Request) (*http.Response, error) {
	response, e := w.base.RoundTrip(r)
	if e == nil && strings.Contains(r.URL.Path, "/customer-") {
		w.modify(response)
	}
	return response, e
}

type mediaEOFHook struct {
	io.ReadCloser
	after func()
	done  bool
}

func (r *mediaEOFHook) Read(p []byte) (int, error) {
	n, e := r.ReadCloser.Read(p)
	if e == io.EOF && !r.done {
		r.done = true
		r.after()
	}
	return n, e
}
func mediaWire(t *testing.T, f *activeHTTPFixture, modify func(*http.Response)) *facadeWire {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ci.ProtocolBase+"/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ci.Protocol{Purpose: ci.Purpose, Version: ci.Version, Capabilities: ci.RequiredCapabilities()})
			return
		}
		f.central.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	wire := &facadeWire{base: server.Client().Transport, target: target}
	client := &http.Client{Transport: mediaResponseWire{wire, modify}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	f.client = ci.NewFixtureClient(fixtureCentral, client)
	f.restart(t)
	return wire
}
func TestCompanyMediaFramingRejectsIncompleteOrForgedCompletion(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("tail remains private")
	task := completeMediaTask(t, f, store, ctx, data)
	path := "/v1/generations/" + task.ID + "/content"
	for _, kind := range []string{"missing-trailer", "forged-header", "wrong-terminal", "short-length", "conflicting-length", "encoded", "late-local-epoch"} {
		t.Run(kind, func(t *testing.T) {
			wire := mediaWire(t, f, func(response *http.Response) {
				switch kind {
				case "missing-trailer":
					response.Trailer = nil
				case "forged-header":
					response.Header.Set(ci.MediaCompleteHeader, "complete")
				case "wrong-terminal":
					response.Body = &mediaEOFHook{ReadCloser: response.Body, after: func() { response.Trailer.Set(ci.MediaCompleteHeader, "failed") }}
				case "short-length":
					response.Header.Set(ci.MediaLengthHeader, "999999")
				case "conflicting-length":
					response.Header.Set("Content-Length", "1")
				case "encoded":
					response.Header.Set("Content-Encoding", "gzip")
				case "late-local-epoch":
					response.Body = &mediaEOFHook{ReadCloser: response.Body, after: func() {
						if e := f.repo.AdvanceLocalRevision(context.Background(), v.Revision); e != nil {
							t.Error(e)
						}
					}}
				}
			})
			got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
			want := 503
			if kind == "late-local-epoch" {
				want = 409
			}
			expectFixture(t, got, want)
			if wire.calls.Load() != 1 {
				t.Fatal("fault did not reach real central response", wire.calls.Load())
			}
			if bytes.Contains(got.Body.Bytes(), data) || got.Header().Get(ci.MediaCompleteHeader) != "" || got.Header().Get(ci.MediaLengthHeader) != "" {
				t.Fatal("unfinished media/marker released")
			}
		})
	}
}

type mediaRedirectStore struct {
	*storage.Local
	directCalls, openCalls int
}

func (s *mediaRedirectStore) DirectURL(context.Context, string, string, string, string, time.Duration) (string, error) {
	s.directCalls++
	return "https://isolated-storage.example/bucket/object?bounded-signature=fixture", nil
}
func (s *mediaRedirectStore) Open(ctx context.Context, key string) (storage.Object, error) {
	s.openCalls++
	return s.Local.Open(ctx, key)
}
func TestCompanyMediaDirectStorageHandoffRemainsExplicitlyPartial(t *testing.T) {
	var direct *mediaRedirectStore
	f, store, _, ctx := mediaHTTPFixture(t, func(d *api.Dependencies) {
		direct = &mediaRedirectStore{Local: d.Storage.(*storage.Local)}
		d.Storage = direct
	})
	v := f.current(t)
	task := completeMediaTask(t, f, store, ctx, testmedia.PNG512("direct storage fixture"))
	path := "/v1/generations/" + task.ID + "/content"
	original := resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, original, 307)
	if original.Header().Get("Location") == "" {
		t.Fatal("fixture did not return direct storage")
	}
	wire := mediaWire(t, f, func(*http.Response) {})
	for _, browser := range []bool{true, false} {
		for _, method := range []string{"GET", "HEAD"} {
			got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, browser, nil)
			expectFixture(t, got, 409)
			if got.Header().Get("Location") != "" || strings.Contains(got.Body.String(), "isolated-storage") || method == "HEAD" && got.Body.Len() != 0 {
				t.Fatal("unsafe external handoff")
			}
		}
	}
	if wire.calls.Load() != 4 || direct.directCalls != 5 || direct.openCalls != 0 {
		t.Fatal("redirect followed or relay fallback", wire.calls.Load(), direct.directCalls, direct.openCalls)
	}
}
func TestCompanyMediaCallerMarkersAndActualErrors(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("caller cannot finish protocol")
	task := completeMediaTask(t, f, store, ctx, data)
	path := "/v1/generations/" + task.ID + "/content"
	got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) {
		r.Header.Set(ci.MediaCompleteHeader, "forged")
		r.Header.Set(ci.MediaLengthHeader, "1")
		r.Header.Set("Trailer", ci.MediaCompleteHeader)
	})
	expectFixture(t, got, 200)
	if !bytes.Equal(got.Body.Bytes(), data) || got.Header().Get(ci.MediaCompleteHeader) != "" {
		t.Fatal("caller controlled completion")
	}
	for _, method := range []string{"GET", "HEAD"} {
		expectFixture(t, facadeRequest(t, f, method, path, "", strings.Repeat("x", 43), true, nil), 403)
		unknown := "/v1/generations/00000000-0000-0000-0000-000000000001/content"
		central := resourceDirect(f, method, unknown, "", v.Tokens.PlaygroundKey, "")
		got := facadeRequest(t, f, method, unknown, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, central, 404)
		expectFixture(t, got, 404)
		if method == "HEAD" && got.Body.Len() != 0 || method == "GET" && !bytes.Equal(central.Body.Bytes(), got.Body.Bytes()) {
			t.Fatal("inner error parity")
		}
	}
}
