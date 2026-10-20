package api

import (
	"net/http"

	"github.com/Sociaro-Research/media-gateway-dev/docs"
	providerassets "github.com/Sociaro-Research/media-gateway-dev/plugins/provider"
)

func providerIcon(w http.ResponseWriter, r *http.Request) {
	icon, ok := providerassets.Icon(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(icon)
}

func providerSVGIcon(w http.ResponseWriter, r *http.Request) {
	icon, ok := providerassets.SVGIcon(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'none'; sandbox")
	_, _ = w.Write(icon)
}

func openAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/openapi.yaml" {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="gateway-openapi.yaml"`)
		_, _ = w.Write(docs.OpenAPI)
		return
	}
	data, err := docs.JSON()
	if err != nil {
		writeError(w, 500, "internal_error", "API specification unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
