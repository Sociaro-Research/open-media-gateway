package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/docs"
	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
)

type storageBrowserOriginKey struct{}

// WithPublicAPI configures public resource origins without serving frontend files.
// The documentation origin exposes only its contract and appearance resources.
func WithPublicAPI(next http.Handler, apiURL, uiURL, docsURL string, readers ...branding.Reader) (http.Handler, error) {
	var reader branding.Reader
	if len(readers) > 0 {
		reader = readers[0]
	}
	origin := func(raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("public URL must be an HTTPS origin (HTTP is allowed only on localhost)")
		}
		return strings.TrimRight(u.String(), "/"), nil
	}
	var err error
	if apiURL, err = origin(apiURL); err != nil {
		return nil, err
	}
	if uiURL == "" {
		uiURL = apiURL
	}
	if uiURL, err = origin(uiURL); err != nil {
		return nil, err
	}
	if docsURL, err = origin(docsURL); err != nil {
		return nil, err
	}
	host := func(raw string) string { u, _ := url.Parse(raw); return u.Host }
	if docsURL != "" && (apiURL == "" || strings.EqualFold(host(docsURL), host(apiURL)) || strings.EqualFold(host(docsURL), host(uiURL))) {
		return nil, fmt.Errorf("separate documentation requires a distinct origin and a configured API origin")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		documentation := docsURL != "" && strings.EqualFold(r.Host, host(docsURL))
		if apiURL != "" && !documentation && !strings.EqualFold(r.Host, host(apiURL)) && r.URL.Path != "/healthz" {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		resource := r.URL.Path == "/site.json" || r.URL.Path == "/openapi.json" || r.URL.Path == "/openapi.yaml" || strings.HasPrefix(r.URL.Path, "/branding/assets/")
		if documentation && (!resource || (r.Method != "GET" && r.Method != "HEAD")) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/site.json" && (r.Method == "GET" || r.Method == "HEAD") {
			serveSiteConfiguration(w, r, apiURL, uiURL, docsURL, reader)
			return
		}
		if (r.URL.Path == "/openapi.json" || r.URL.Path == "/openapi.yaml") && (r.Method == "GET" || r.Method == "HEAD") {
			serveSiteSpec(w, r, apiURL)
			return
		}
		if uiURL != "" {
			r = r.WithContext(context.WithValue(r.Context(), storageBrowserOriginKey{}, uiURL))
		}
		next.ServeHTTP(w, r)
	}), nil
}

func serveSiteConfiguration(w http.ResponseWriter, r *http.Request, apiURL, uiURL, docsURL string, reader branding.Reader) {
	w.Header().Set("Cache-Control", "no-store")
	settings := readBranding(r.Context(), reader)
	// Public configuration describes assets; image bytes are fetched separately.
	assets := make(map[string]branding.Asset, len(settings.Assets))
	for key, asset := range settings.Assets {
		assets[key] = branding.Asset{MIME: asset.MIME}
	}
	settings.Assets = assets
	writeJSON(w, 200, map[string]any{"api_url": apiURL, "ui_url": uiURL, "docs_url": docsURL, "branding": settings})
}
func (s *Server) siteConfiguration(w http.ResponseWriter, r *http.Request) {
	serveSiteConfiguration(w, r, "", "", "", s.brandingReader())
}

// Browser-session preflight for static UI delivery. An API credential is not a
// browser session; no identity or session token is forwarded to the file server.
func (s *Server) browserSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Cookie")
	if s.auth != nil {
		if _, err := s.auth.Authenticate(r); err != nil {
			writeError(w, 401, "unauthorized", "Sign-in is required")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func serveSiteSpec(w http.ResponseWriter, r *http.Request, apiURL string) {
	if apiURL == "" {
		openAPISpec(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	if r.URL.Path == "/openapi.yaml" {
		quoted, _ := json.Marshal(apiURL)
		data := bytes.Replace(docs.OpenAPI, []byte("- url: \"/\""), append([]byte("- url: "), quoted...), 1)
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="gateway-openapi.yaml"`)
		_, _ = w.Write(data)
		return
	}
	data, err := docs.JSON()
	if err != nil {
		http.Error(w, "specification unavailable", 500)
		return
	}
	var spec map[string]any
	if json.Unmarshal(data, &spec) != nil {
		http.Error(w, "specification unavailable", 500)
		return
	}
	spec["servers"] = []map[string]string{{"url": apiURL, "description": "Gateway API"}}
	writeJSON(w, 200, spec)
}
