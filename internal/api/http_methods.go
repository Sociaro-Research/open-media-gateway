package api

import (
	"net/http"
	"strconv"
)

// ServeMux also dispatches HEAD to GET routes. Shared read/write handlers must
// select their read branch for both methods without changing the request method.
func readMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// OAuth GET endpoints intentionally establish login state. An implicit HEAD
// must not begin sign-in or consume a verified callback code/session exchange.
func getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET for sign-in")
			return
		}
		next(w, r)
	}
}

// Generation-only keys may inspect these media aliases. This exception must not
// exempt every organization route from the management-scope requirement.
func organizationArtifactHEAD(r *http.Request) bool {
	if r.Method != http.MethodHead {
		return false
	}
	switch r.Pattern {
	case "HEAD /v1/organizations/{org}/generations/{id}/content",
		"HEAD /v1/organizations/{org}/generations/{id}/last-frame",
		"HEAD /v1/organizations/{org}/reports/{id}/content",
		"HEAD /v1/organizations/{org}/reports/{id}/last-frame":
		return true
	}
	return false
}

// Suppress response bodies for every HEAD outcome, including authorization and
// routing errors, while retaining the representation headers. Media handlers
// still receive HEAD and preserve their existing direct-URL and range behavior.
func headResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		head := &headResponse{ResponseWriter: w}
		next.ServeHTTP(head, r)
		if head.status == 0 {
			head.status = http.StatusOK
		}
		if head.bytes > 0 && head.status != http.StatusNoContent && head.status != http.StatusNotModified && head.Header().Get("Content-Length") == "" {
			head.Header().Set("Content-Length", strconv.FormatInt(head.bytes, 10))
		}
		w.WriteHeader(head.status)
	})
}

type headResponse struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *headResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *headResponse) WriteHeader(status int) {
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
	}
}

func (w *headResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if len(data) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", http.DetectContentType(data))
	}
	w.bytes += int64(len(data))
	return len(data), nil
}
