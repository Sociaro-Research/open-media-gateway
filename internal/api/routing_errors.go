package api

import (
	"net/http"
	"strings"
)

// Only the API namespaces use JSON routing errors. Public documents, static
// assets and capability media retain their existing HTTP representations.
func apiRoutingErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		api := path == "/v1" || strings.HasPrefix(path, "/v1/") || path == "/auth" || strings.HasPrefix(path, "/auth/")
		if api {
			// An empty pattern identifies a ServeMux-generated response. Never
			// wrap matched handlers: their media, proxy and streaming responses
			// must keep the original writer and path-value dispatch.
			if _, pattern := mux.Handler(r); pattern == "" {
				w = &routingErrorWriter{ResponseWriter: w}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

type routingErrorWriter struct {
	http.ResponseWriter
	replaced bool
}

func (w *routingErrorWriter) WriteHeader(status int) {
	code, message := "", ""
	switch status {
	case http.StatusNotFound:
		code, message = "not_found", "API endpoint not found. Check the request path."
	case http.StatusMethodNotAllowed:
		code, message = "method_not_allowed", "HTTP method is not allowed for this endpoint. Use a method listed in the Allow header."
	default:
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.replaced = true
	// Keep the mux's exact status and Allow header, replacing only the body
	// representation. HEAD suppression remains in the existing outer middleware.
	w.Header().Del("Content-Length")
	writeError(w.ResponseWriter, status, code, message)
}

func (w *routingErrorWriter) Write(data []byte) (int, error) {
	if w.replaced {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
