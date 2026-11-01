package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

type storageBrowserOriginKey struct{}

// WithPublicAPI configures public resource origins without serving frontend files.
// The documentation origin exposes only its contract and appearance resources.
func WithPublicAPI(next http.Handler, apiURL, uiURL, docsURL string, readers ...branding.Reader) (http.Handler, error) {
	return WithPublicAPIAndLegacyOrigin(next, apiURL, uiURL, docsURL, "", readers...)
}

// WithPublicAPIAndLegacyOrigin preserves one retired HTTPS API origin's host
// admission. It never changes request Host, authentication, browser trust,
// signed capabilities, canonical links or the configured installation role.
func WithPublicAPIAndLegacyOrigin(next http.Handler, apiURL, uiURL, docsURL, legacyURL string, readers ...branding.Reader) (http.Handler, error) {
	var reader branding.Reader
	if len(readers) > 0 {
		reader = readers[0]
	}
	origin := func(raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		return installation.NormalizeOrigin(raw)
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
	if legacyURL, err = installation.NormalizeLegacyAPIOrigin(legacyURL); err != nil {
		return nil, err
	}
	if legacyURL != "" && apiURL == "" {
		return nil, fmt.Errorf("legacy API origin requires a configured canonical API origin")
	}

	metadata, err := installation.Describe(uiURL)
	if err != nil {
		return nil, err
	}
	host := func(raw string) string { u, _ := url.Parse(raw); return u.Host }
	matchesHost := func(requestHost, configured string) bool {
		u, e := url.Parse(configured)
		if e != nil || configured == "" {
			return false
		}
		candidate, e := installation.NormalizeOrigin(u.Scheme + "://" + requestHost)
		return e == nil && candidate == configured
	}

	if docsURL != "" && (apiURL == "" || strings.EqualFold(host(docsURL), host(apiURL)) || strings.EqualFold(host(docsURL), host(uiURL))) {
		return nil, fmt.Errorf("separate documentation requires a distinct origin and a configured API origin")
	}
	if legacyURL != "" && (strings.EqualFold(host(legacyURL), host(apiURL)) || strings.EqualFold(host(legacyURL), host(uiURL)) || strings.EqualFold(host(legacyURL), host(docsURL))) {
		return nil, fmt.Errorf("legacy API origin must be distinct from canonical API, UI and documentation origins")
	}

	var applicationHosts []string
	if legacyURL != "" {
		canonical, _ := url.Parse(apiURL)
		legacy, _ := url.Parse(legacyURL)
		applicationHosts = []string{canonical.Hostname(), legacy.Hostname()}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original := r
		defer func() { original.Pattern = r.Pattern }()
		r = r.WithContext(installation.WithContext(r.Context(), metadata))
		if len(applicationHosts) > 0 {
			r = r.WithContext(context.WithValue(r.Context(), mediaApplicationHostsKey{}, applicationHosts))
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		documentation := docsURL != "" && matchesHost(r.Host, docsURL)
		if apiURL != "" && !documentation && !matchesHost(r.Host, apiURL) && !matchesHost(r.Host, legacyURL) && r.URL.Path != "/healthz" {
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
		if metadata.Role == "central" && centralRemoteClientRequest(r) {
			centralModeError(w)
			return
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
	meta := installation.FromContext(r.Context())
	writeJSON(w, 200, map[string]any{"api_url": apiURL, "ui_url": uiURL, "docs_url": docsURL, "branding": settings, "installation_role": meta.Role, "central_origin": meta.CentralOrigin})
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
