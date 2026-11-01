package api

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/docs"
)

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
