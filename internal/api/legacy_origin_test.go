package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const legacyOrigin = "https://media-gateway-dev.sociaro.ai"
const stagingOrigin = "https://console-staging.sociaro.ai"

func legacyPublicFixture(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	// Preserve exact host admission without rewriting browser/security context.
	h, err := WithPublicAPIAndLegacyOrigin(next, stagingOrigin, stagingOrigin, "https://docs-staging.sociaro.ai", legacyOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func TestLegacyAPIOriginAdmitsExactHostWithoutRewriting(t *testing.T) {
	for _, origin := range []string{stagingOrigin, legacyOrigin} {
		for _, method := range []string{"GET", "POST"} {
			req := httptest.NewRequest(method, origin+"/v1/fixture", nil)
			req.Header.Set("Authorization", "Bearer synthetic-token")
			h := legacyPublicFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != req.Host || r.Header.Get("Authorization") != req.Header.Get("Authorization") || r.Method != method {
					t.Error("host/credential/method rewritten")
				}
				w.WriteHeader(204)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 204 {
				t.Fatalf("%s %s: got%d, want204", origin, method, w.Code)
			}
		}
	}
}

func TestLegacyAPIOriginDisabledStrictAndSpoofedHosts(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	disabled, err := WithPublicAPI(next, stagingOrigin, stagingOrigin, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	disabled.ServeHTTP(w, httptest.NewRequest("GET", legacyOrigin+"/v1/fixture", nil))
	if w.Code != 421 {
		t.Fatal("default admitted legacy host", w.Code)
	}
	for _, invalid := range []string{"http://media-gateway-dev.sociaro.ai", "https://a.test:8443", "https://a.test:0", "https://a.test:65536", "https://a.test:", "https://a.test:abc", "https://a.test/path", "https://a.test?x=1", "https://a.test#fragment", "https://user:password@a.test", "https://*.sociaro.ai", "https://a.test,https://b.test", " https://a.test", "https://a.test ", stagingOrigin, "https://docs-staging.sociaro.ai"} {
		if _, err := WithPublicAPIAndLegacyOrigin(next, stagingOrigin, stagingOrigin, "https://docs-staging.sociaro.ai", invalid); err == nil {
			t.Errorf("invalid legacy origin accepted: %q", invalid)
		}
	}
	if _, err := WithPublicAPIAndLegacyOrigin(next, "", "", "", legacyOrigin); err == nil {
		t.Error("legacy host without canonical accepted")
	}
	h := legacyPublicFixture(t, next)
	for _, host := range []string{"evil.test", "media-gateway-dev.sociaro.ai.evil.test", "xmedia-gateway-dev.sociaro.ai", "media-gateway-dev.sociaro.ai:8443"} {
		req := httptest.NewRequest("POST", "https://"+host+"/v1/fixture", nil)
		req.Header.Set("X-Forwarded-Host", "console-staging.sociaro.ai")
		req.Header.Set("Forwarded", "host=media-gateway-dev.sociaro.ai;proto=https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 421 {
			t.Error("unknown/spoofed host admitted", host, w.Code)
		}
	}
	normalized, err := WithPublicAPIAndLegacyOrigin(next, stagingOrigin, stagingOrigin, "", "https://MEDIA-GATEWAY-DEV.SOCIARO.AI:443/")
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	normalized.ServeHTTP(w, httptest.NewRequest("GET", legacyOrigin+"/v1/fixture", nil))
	if w.Code != 204 {
		t.Fatal("normalized exact origin unavailable", w.Code)
	}
}

func TestLegacyAPIOriginCanonicalMetadataAndDocsIsolation(t *testing.T) {
	for _, canonical := range []string{stagingOrigin, "https://console.sociaro.ai"} {
		h, err := WithPublicAPIAndLegacyOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Context().Value(storageBrowserOriginKey{}); got != canonical {
				t.Errorf("browser storage origin changed: %v", got)
			}
			w.WriteHeader(204)
		}), canonical, canonical, "https://docs-staging.sociaro.ai", legacyOrigin)
		if err != nil {
			t.Fatal(err)
		}
		for _, origin := range []string{canonical, legacyOrigin} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", origin+"/site.json", nil))
			var v map[string]any
			if json.Unmarshal(w.Body.Bytes(), &v) != nil {
				t.Fatal("invalid configuration")
			}
			role := "ordinary"
			if canonical == "https://console.sociaro.ai" {
				role = "central"
			}
			if v["api_url"] != canonical || v["ui_url"] != canonical || v["installation_role"] != role {
				t.Fatal("legacy changed canonical links/role")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", origin+"/openapi.json", nil))
			var spec struct {
				Servers []struct {
					URL string `json:"url"`
				} `json:"servers"`
			}
			if json.Unmarshal(w.Body.Bytes(), &spec) != nil || len(spec.Servers) != 1 || spec.Servers[0].URL != canonical {
				t.Fatal("noncanonical API spec link")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", origin+"/v1/fixture", nil))
			if w.Code != 204 {
				t.Fatal(w.Code)
			}
		}
		for _, method := range []string{"GET", "POST", "HEAD"} {
			for _, path := range []string{"/v1/info", "/auth/session", "/media/synthetic", "/references/synthetic"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, "https://docs-staging.sociaro.ai"+path, nil))
				if w.Code != 404 {
					t.Fatal("docs acquired API access", method, path, w.Code)
				}
			}
		}
	}
}

func TestLegacyAPIOriginBearerBrowserCSRFAndCookieIsolation(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := legacyPublicFixture(t, s.handler())
	for _, origin := range []string{stagingOrigin, legacyOrigin} {
		for _, cookie := range []bool{false, true} {
			req := httptest.NewRequest("GET", origin+"/auth/session", nil)
			req.Header.Set("Authorization", "Bearer service-secret")
			if cookie {
				req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			want := 401
			if cookie {
				want = 204
			}
			if w.Code != want {
				t.Fatal("bearer mistaken for browser session", origin, cookie, w.Code)
			}
		}
		for _, browserOrigin := range []string{origin, stagingOrigin, legacyOrigin, "https://attacker.test"} {
			req := httptest.NewRequest("POST", origin+"/v1/api-keys", strings.NewReader(`{"name":"Synthetic host migration test"}`))
			req.Header.Set("Origin", browserOrigin)
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			want := 403
			if browserOrigin == origin {
				want = 201
			}
			if w.Code != want {
				t.Fatal("browser mutation origin trust changed", origin, browserOrigin, w.Code)
			}
		}
	}
	bearer := legacyPublicFixture(t, New(serviceStub{task: core.Task{ID: "11111111-1111-4111-8111-111111111111", Status: core.TaskQueued}}, healthStub{}, "synthetic-bearer"))
	for _, origin := range []string{stagingOrigin, legacyOrigin} {
		for _, token := range []bool{false, true} {
			req := httptest.NewRequest("POST", origin+"/v1/videos", strings.NewReader(`{"model":"mock/mock-video-v1","prompt":"Synthetic"}`))
			if token {
				req.Header.Set("Authorization", "Bearer synthetic-bearer")
			}
			w := httptest.NewRecorder()
			bearer.ServeHTTP(w, req)
			want := 401
			if token {
				want = 202
			}
			if w.Code != want {
				t.Fatal("bearer admission changed", origin, token, w.Code)
			}
		}
	}
	jar, _ := cookiejar.New(nil)
	old, _ := url.Parse(legacyOrigin)
	current, _ := url.Parse(stagingOrigin)
	jar.SetCookies(old, []*http.Cookie{{Name: "session", Value: "synthetic", Path: "/", Secure: true, HttpOnly: true}})
	if len(jar.Cookies(old)) != 1 || len(jar.Cookies(current)) != 0 {
		t.Fatal("host-only old login cookie transferred")
	}
}

func TestLegacyAPIOriginSignedMediaAndNonexpiringReferences(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	data := referencePNG(t)
	stored, err := store.Archive(context.Background(), id, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data))
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{ID: id, Status: core.TaskSucceeded, StorageKey: &stored.StorageKey}
	repo := &referenceMemory{items: map[string]references.Image{}}
	library := references.New(repo, store)
	next := NewWithReferenceLibrary(serviceStub{task: task}, healthStub{}, "secret", store, "migration-synthetic-secret", time.Hour, nil, nil, library)
	h := legacyPublicFixture(t, next)
	upload := referenceUploadRequest(t, data)
	upload.URL.Scheme = "https"
	upload.URL.Host = "console-staging.sociaro.ai"
	upload.Host = upload.URL.Host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, upload)
	if w.Code != 201 {
		t.Fatal("reference fixture upload", w.Code)
	}
	var ref referenceResponse
	if json.Unmarshal(w.Body.Bytes(), &ref) != nil {
		t.Fatal("reference fixture JSON")
	}
	signer := newMediaURLSigner("migration-synthetic-secret", time.Hour)
	signed, _ := signer.create(id)
	for _, origin := range []string{stagingOrigin, legacyOrigin} {
		for _, route := range []string{signed, ref.URL} {
			u, err := url.Parse(route)
			if err != nil {
				t.Fatal(err)
			}
			target := origin + u.RequestURI()
			for _, method := range []string{"GET", "HEAD"} {
				for _, partial := range []bool{false, true} {
					req := httptest.NewRequest(method, target, nil)
					if partial {
						req.Header.Set("Range", "bytes=0-9")
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					want := 200
					if partial {
						want = 206
					}
					if w.Code != want {
						t.Fatal("signed content/Range", origin, method, partial, w.Code)
					}
					if method == "HEAD" && w.Body.Len() != 0 {
						t.Fatal("HEAD body exposed")
					}
					if method == "GET" && partial && w.Body.Len() != 10 {
						t.Fatal("Range bytes changed")
					}
				}
			}
			tampered := strings.Replace(target, "signature=", "signature=bad", 1)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tampered, nil))
			if w.Code != 403 {
				t.Fatal("tampered capability admitted", w.Code)
			}
		}
		expired := newMediaURLSigner("migration-synthetic-secret", -time.Hour)
		bad, _ := expired.create(id)
		for _, route := range []string{bad, "/media/" + id, signed + "0"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", origin+route, nil))
			if w.Code != 403 {
				t.Fatal("invalid/expired signature admitted", w.Code)
			}
		}
	}
	// Reference capabilities carry owner+ID proof without expiry. Failed expired
	// media requests above must not add a new reference expiration policy.
	u, _ := url.Parse(ref.URL)
	if u.Query().Has("expires") {
		t.Fatal("unexpected reference expiry")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", legacyOrigin+u.RequestURI(), nil))
	if w.Code != 200 {
		t.Fatal("existing nonexpiring reference failed", w.Code)
	}
}

func TestLegacyAPIOriginDirectMediaRejectsBothApplicationHosts(t *testing.T) {
	key := "s3:synthetic/active.svg"
	id := "11111111-1111-4111-8111-111111111111"
	owner := identity.SystemOwnerID
	referenceID := "22222222-2222-4222-8222-222222222222"
	for _, origin := range []string{stagingOrigin, legacyOrigin} {
		for _, destination := range []string{stagingOrigin + "/object.svg", legacyOrigin + "/object.svg", "https://CONSOLE-STAGING.SOCIARO.AI.:9443/object.svg", "https://MEDIA-GATEWAY-DEV.SOCIARO.AI:8443/object.svg", "https://separate-storage.example.test/object.svg"} {
			store := &directMedia{destination: destination}
			task := core.Task{ID: id, OwnerID: owner, Status: core.TaskSucceeded, StorageKey: &key}
			repo := &referenceMemory{items: map[string]references.Image{owner + "/" + referenceID: {ID: referenceID, StorageKey: key, ContentType: "image/svg+xml"}}}
			h := legacyPublicFixture(t, NewWithReferenceLibrary(serviceStub{task: task}, healthStub{}, "secret", store, "synthetic-migration-secret", time.Hour, nil, nil, references.New(repo, store)))
			signed, _ := newMediaURLSigner("synthetic-migration-secret", time.Hour).create(id)
			ref := "/references/" + owner + "/" + referenceID + "?signature=" + references.Signature([]byte("synthetic-migration-secret"), owner, referenceID)
			for _, route := range []string{signed, ref} {
				for _, method := range []string{"GET", "HEAD"} {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(method, origin+route, nil))
					want := 503
					if strings.Contains(destination, "separate-storage.example.test") {
						want = 307
					}
					if w.Code != want || want == 503 && w.Header().Get("Location") != "" {
						t.Fatal("application media isolation changed", origin, destination, method, w.Code)
					}
				}
			}
		}
	}
}
