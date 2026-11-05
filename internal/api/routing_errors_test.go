package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assertRoutingError(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string  `json:"code"`
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Param   *string `json:"param"`
		} `json:"error"`
	}
	decoder := json.NewDecoder(w.Body)
	if err := decoder.Decode(&body); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("routing error has trailing content")
	}
	if w.Header().Get("Content-Type") != "application/json" || body.Error.Code != code || body.Error.Message == "" || body.Error.Type != "invalid_request_error" || body.Error.Param != nil {
		t.Fatalf("unexpected routing error: %d %+v", w.Code, body)
	}
	if strings.Contains(body.Error.Message, "private-value") {
		t.Fatal("routing error echoed request path or query")
	}
}

func TestAPIRoutingErrorsPreserveServeMuxDecisions(t *testing.T) {
	mux := http.NewServeMux()
	read := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route", r.Pattern)
		_, _ = io.WriteString(w, r.PathValue("id"))
	}
	mux.HandleFunc("GET /v1/items/{id}", read)
	mux.HandleFunc("PUT /v1/items/fixed", read)
	mux.HandleFunc("POST /v1/write", read)
	mux.HandleFunc("GET /v1/tree/", read)
	mux.HandleFunc("GET /auth/session", read)
	native, wrapped := headResponses(mux), headResponses(apiRoutingErrors(mux))
	for _, path := range []string{"/v1", "/auth", "/v1/private-value?token=private-value", "/auth/private-value", "/v1/items/fixed", "/v1/items/private-value", "/v1/write", "/v1/tree", "/v1//items/private-value", "/v1/items/%70rivate-value"} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "TRACE", "CONNECT", "CUSTOM"} {
			t.Run(method+" "+path, func(t *testing.T) {
				a, b := httptest.NewRecorder(), httptest.NewRecorder()
				ra, rb := httptest.NewRequest(method, path, nil), httptest.NewRequest(method, path, nil)
				native.ServeHTTP(a, ra)
				wrapped.ServeHTTP(b, rb)
				if a.Code != b.Code || ra.Pattern != rb.Pattern {
					t.Fatalf("mux status/pattern changed: %d/%q to %d/%q", a.Code, ra.Pattern, b.Code, rb.Pattern)
				}
				for _, name := range []string{"Allow", "Location", "X-Route", "X-Content-Type-Options"} {
					if a.Header().Get(name) != b.Header().Get(name) {
						t.Fatalf("%s changed: %q to %q", name, a.Header().Get(name), b.Header().Get(name))
					}
				}
				if method == "HEAD" && b.Body.Len() != 0 {
					t.Fatal("HEAD returned a body")
				}
				if a.Code == 404 || a.Code == 405 {
					if method == "HEAD" {
						if b.Header().Get("Content-Type") != "application/json" || b.Header().Get("Content-Length") == "" {
							t.Fatal("HEAD lost JSON representation headers")
						}
					} else {
						code := "not_found"
						if a.Code == 405 {
							code = "method_not_allowed"
						}
						assertRoutingError(t, b, code)
					}
				} else if a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
					t.Fatal("matched response or redirect changed")
				}
			})
		}
	}
}

func TestAPIRoutingErrorsPreserveOtherRepresentations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", openAPISpec)
	mux.HandleFunc("GET /media/{id}", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	for _, path := range []string{"/", "/v10/missing", "/authentic/missing", "/ui/missing.js", "/site.json", "/legal/missing", "/branding/assets/missing", "/provider-assets/owner/id/icon.png", "/reference-media/owner/id", "/template-media/missing", "/media/missing", "/healthz", "/openapi.json", "/openapi.yaml"} {
		for _, method := range []string{"GET", "HEAD", "POST"} {
			a, b := httptest.NewRecorder(), httptest.NewRecorder()
			headResponses(mux).ServeHTTP(a, httptest.NewRequest(method, path, nil))
			headResponses(apiRoutingErrors(mux)).ServeHTTP(b, httptest.NewRequest(method, path, nil))
			if a.Code != b.Code || a.Body.String() != b.Body.String() || !equalHeaders(a.Header(), b.Header()) {
				t.Fatalf("%s %s representation changed", method, path)
			}
		}
	}
}

func equalHeaders(a, b http.Header) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}

func TestAPIRoutingErrorsDoNotWrapMatchedStreams(t *testing.T) {
	mux := http.NewServeMux()
	writer := httptest.NewRecorder()
	mux.HandleFunc("GET /v1/stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		if w != writer || r.PathValue("id") != "fixture" || r.Pattern != "GET /v1/stream/{id}" {
			t.Fatal("matched writer or route dispatch changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: fixture\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	})
	apiRoutingErrors(mux).ServeHTTP(writer, httptest.NewRequest("GET", "/v1/stream/fixture", nil))
	if !writer.Flushed || writer.Body.String() != "data: fixture\n\n" {
		t.Fatal("stream was buffered or changed")
	}
	mux.HandleFunc("GET /v1/content", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture.txt", time.Time{}, strings.NewReader("fixture"))
	})
	for _, span := range []string{"bytes=1-3", "bytes=99-100"} {
		a, b := httptest.NewRecorder(), httptest.NewRecorder()
		for i, h := range []http.Handler{mux, apiRoutingErrors(mux)} {
			r := httptest.NewRequest("GET", "/v1/content", nil)
			r.Header.Set("Range", span)
			h.ServeHTTP([]*httptest.ResponseRecorder{a, b}[i], r)
		}
		if a.Code != b.Code || a.Body.String() != b.Body.String() || !equalHeaders(a.Header(), b.Header()) {
			t.Fatal("media range representation changed")
		}
	}
}

func TestAPIRoutingErrorsRespectHostAndDocumentationBoundary(t *testing.T) {
	s, _ := newPersonalTestServer()
	h, err := WithPublicAPI(s.handler(), "https://api.example.test", "https://console.example.test", "https://docs.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		code int
	}{{"api.example.test", 404}, {"docs.example.test", 404}, {"unknown.example.test", 421}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://"+tc.host+"/v1/missing", nil)
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc.host, w.Code)
		}
		if tc.host == "api.example.test" {
			assertRoutingError(t, w, "not_found")
		} else if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatal("host/docs boundary representation changed")
		}
	}
}

func TestAPIRoutingErrorsRemainBehindBrowserProtection(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	for _, path := range []string{"/v1/missing", "/v1/me"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("DELETE", path, nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		h.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "cross_origin_request") {
			t.Fatal("browser protection was bypassed", w.Code, w.Body)
		}
	}
	w := methodSend(h, "GET", "/v1/me", "", "", "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "unauthorized") {
		t.Fatal("matched endpoint authentication changed", w.Code, w.Body)
	}
}

func TestAPIRoutingErrorHEADRepresentationLength(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	for _, path := range []string{"/v1/missing", "/auth/missing", "/v1/generations/estimate", "/v1/audio/speech"} {
		get := methodSend(h, "GET", path, "", "", "")
		head := methodSend(h, "HEAD", path, "", "", "")
		if head.Code != get.Code || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "application/json" || head.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) || head.Header().Get("Allow") != get.Header().Get("Allow") {
			t.Fatalf("HEAD representation changed for %s: %d %v", path, head.Code, head.Header())
		}
	}
	server := httptest.NewServer(h)
	defer server.Close()
	res, err := server.Client().Head(server.URL + "/v1/audio/speech")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || len(body) != 0 || res.StatusCode != 405 || res.Header.Get("Allow") != "POST" || res.ContentLength <= 0 || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("real HTTP HEAD changed: %d %v", res.StatusCode, res.Header)
	}
}
