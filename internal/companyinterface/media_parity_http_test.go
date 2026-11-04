package companyinterface_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func mediaDirectHeaders(f *activeHTTPFixture, method, path, key string, headers http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, fixtureCentral+path, nil)
	r.Header = headers.Clone()
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	f.central.ServeHTTP(w, r)
	return w
}
func TestCompanyMediaActualConditionalParity(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("conditional media")
	task := completeMediaTask(t, f, store, ctx, data)
	path := "/v1/generations/" + task.ID + "/content"
	original := resourceDirect(f, "GET", path, "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, original, 200)
	etag := original.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing real validator")
	}
	cases := []struct {
		name    string
		headers http.Header
		status  int
	}{
		{"etag-not-modified", http.Header{"If-None-Match": {etag}}, 304},
		{"etag-mismatch", http.Header{"If-None-Match": {`"different"`}}, 200},
		{"if-match-failed", http.Header{"If-Match": {`"different"`}}, 412},
		{"if-match-valid", http.Header{"If-Match": {etag}}, 200},
		{"if-range-valid", http.Header{"If-Range": {etag}, "Range": {"bytes=1-19"}}, 206},
		{"if-range-changed", http.Header{"If-Range": {`"different"`}, "Range": {"bytes=1-19"}}, 200},
		{"future-modified-date", http.Header{"If-Modified-Since": {time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)}}, 0},
		{"past-unmodified-date", http.Header{"If-Unmodified-Since": {time.Unix(1, 0).UTC().Format(http.TimeFormat)}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{"GET", "HEAD"} {
				direct := mediaDirectHeaders(f, method, path, v.Tokens.PlaygroundKey, tc.headers)
				got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) {
					for k, v := range tc.headers {
						r.Header[k] = append([]string(nil), v...)
					}
				})
				if tc.status != 0 && direct.Code != tc.status {
					t.Fatalf("direct %s status %d expected %d: %s", method, direct.Code, tc.status, direct.Body)
				}
				if got.Code != direct.Code || !bytes.Equal(got.Body.Bytes(), direct.Body.Bytes()) {
					t.Fatalf("%s parity direct=%d/%d delegated=%d/%d", method, direct.Code, direct.Body.Len(), got.Code, got.Body.Len())
				}
				for _, h := range []string{"ETag", "Last-Modified", "Content-Range", "Content-Length", "Content-Type"} {
					if got.Header().Get(h) != direct.Header().Get(h) {
						t.Fatalf("%s %s mismatch %q vs %q", method, h, got.Header().Get(h), direct.Header().Get(h))
					}
				}
				if got.Header().Get(ci.MediaCompleteHeader) != "" || got.Header().Get(ci.MediaLengthHeader) != "" {
					t.Fatal("internal marker leaked")
				}
			}
		})
	}
	for _, headers := range []http.Header{{"If-None-Match": {etag, etag}}, {"If-Match": {string(bytes.Repeat([]byte("x"), 1025))}}} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) {
			for k, v := range headers {
				r.Header[k] = v
			}
		}), 403)
	}
}

type mediaLogoFixture struct {
	api.ProviderAdministration
	data []byte
}

func (s *mediaLogoFixture) Logo(ctx context.Context, id string) ([]byte, error) {
	if identity.ProviderOwnerID(ctx) != identity.SharedOwnerID || id != "mock" {
		return nil, fmt.Errorf("wrong logo scope")
	}
	return s.data, nil
}
func (s *mediaLogoFixture) SaveLogo(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("read fixture only")
}
func TestCompanyMediaActualProviderIconHEAD(t *testing.T) {
	data := testmedia.PNG512("provider logo")
	f, _, _, _ := mediaHTTPFixture(t, func(d *api.Dependencies) { d.ProviderAdmin = &mediaLogoFixture{data: data} })
	v := f.current(t)
	path := "/provider-assets/" + identity.SharedOwnerID + "/mock/icon.png?v=fixture"
	for _, method := range []string{"GET", "HEAD"} {
		direct := resourceDirect(f, method, path, "", v.Tokens.PlaygroundKey, "")
		got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, direct, 200)
		expectFixture(t, got, 200)
		for _, h := range []string{"Content-Type", "Content-Length", "X-Content-Type-Options", "Content-Security-Policy"} {
			if got.Header().Get(h) != direct.Header().Get(h) {
				t.Fatalf("%s %s differs %q vs %q", method, h, got.Header().Get(h), direct.Header().Get(h))
			}
		}
		if method == "HEAD" {
			if got.Body.Len() != 0 || got.Header().Get("Content-Length") != strconv.Itoa(len(data)) {
				t.Fatal("HEAD representation length missing", got.Header())
			}
		} else if !bytes.Equal(got.Body.Bytes(), data) {
			t.Fatal("logo bytes changed")
		}
	}
	// The direct logo handler ignores Range; preserve its whole-image response.
	got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Range", "bytes=1-19") })
	expectFixture(t, got, 200)
	if !bytes.Equal(got.Body.Bytes(), data) {
		t.Fatal("logo invented range support")
	}
}
