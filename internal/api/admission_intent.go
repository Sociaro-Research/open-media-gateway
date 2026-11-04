package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"io"
	"net/http"
	"strings"
	"time"
)

// Runs after authentication/role/scope checks, before mutable catalogue lookup.
// JSON intent is canonicalized without interpreting models, references or prices.
func (s *Server) generationIntent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveUnrecovered := func() {
			if r.Context().Value(generationStorageAdmissionKey{}) == true && !s.storageAdmission(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		}

		if r.Method != "POST" || r.Header.Get("Idempotency-Key") == "" || !isGenerationPath(r.URL.Path) || strings.HasSuffix(r.URL.Path, "/estimate") {
			serveUnrecovered()
			return
		}
		service, ok := s.service.(interface {
			RecoverAcceptedIntent(context.Context, string, string) (core.Task, bool, error)
		})
		if !ok {
			serveUnrecovered()
			return
		}
		var value any
		encoding := "json"
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			encoding = "multipart"
			if r.URL.Path != "/v1/images/edits" && r.URL.Path != "/v1/videos" {
				serveUnrecovered()
				return
			}
			// Do not drain a new file upload when this storage requires direct S3.
			// A current actor's stored receipt permits bounded replay hashing only.
			if s.references != nil {
				direct, e := s.references.RequiresDirectUpload(r.Context())
				if e != nil {
					s.referenceError(w, e)
					return
				}
				if direct {
					known := false
					if hint, ok := s.service.(interface {
						HasAcceptedIntent(context.Context, string) (bool, error)
					}); ok {
						known, e = hint.HasAcceptedIntent(r.Context(), r.Header.Get("Idempotency-Key"))
						if e != nil {
							writeError(w, 503, "service_unavailable", "accepted generation could not be checked")
							return
						}
					}
					if !known {
						files := true
						if r.URL.Path == "/v1/videos" {
							r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
							_, found, ok := videoMultipartFields(w, r)
							if !ok {
								return
							}
							files = found
						}
						if files {
							s.referenceError(w, references.ErrDirectUploadRequired)
							return
						}
					}
				}
			}
			cleanup, ok := s.multipartIntent(w, r, &value)
			if !ok {
				return
			}
			defer cleanup()
			r = r.WithContext(context.WithValue(r.Context(), multipartIntentKey{}, true))
		} else {
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			r.Body.Close()
			if e != nil {
				invalidParameter(w, "body", "send one JSON object, up to 65536 bytes")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var object map[string]any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if d.Decode(&object) != nil || object == nil || d.Decode(&struct{}{}) != io.EOF {
				serveUnrecovered()
				return
			}
			value = object
		}
		intent, e := core.FingerprintAdmissionIntent(r.Context(), encoding+":"+r.URL.Path+"?"+r.URL.Query().Encode(), value)
		if e != nil {
			invalidParameter(w, "body", "invalid generation fields")
			return
		}
		task, found, e := service.RecoverAcceptedIntent(r.Context(), r.Header.Get("Idempotency-Key"), intent)
		if e != nil {
			if errors.Is(e, core.ErrIdempotencyConflict) {
				writeError(w, 409, "idempotency_conflict", e.Error())
			} else if errors.Is(e, core.ErrKeyUnavailable) {
				writeError(w, 401, "unauthorized", e.Error())
			} else {
				writeError(w, 503, "service_unavailable", "accepted generation could not be recovered")
			}
			return
		}
		if found {
			w.Header().Set("Location", core.GenerationView(task).StatusURL)
			s.writeGenerationJSON(w, r, 202, task)
			return
		}
		response := &generationIntentResponse{ResponseWriter: w}
		quotaRejected := r.Context().Value(generationStorageAdmissionKey{}) == true && !s.storageAdmission(response, r)
		if !quotaRejected {
			next.ServeHTTP(response, r.WithContext(core.WithAdmissionIntent(r.Context(), intent)))
		}
		if response.buffered {
			if response.code != 401 && (response.code != 403 || quotaRejected) && response.code != 429 {
				accepted, exists, recoveryErr := service.RecoverAcceptedIntent(r.Context(), r.Header.Get("Idempotency-Key"), intent)
				if recoveryErr == nil && exists {
					w.Header().Del("Content-Length")
					w.Header().Set("Location", core.GenerationView(accepted).StatusURL)
					s.writeGenerationJSON(w, r, 202, accepted)
					return
				}
			}
			w.WriteHeader(response.code)
			_, _ = w.Write(response.body.Bytes())
		}
	})
}

// Only error JSON is buffered, with a strict cap. Successful responses stream
// unchanged. This is recovery of a committed receipt, never a mutation retry.
type generationIntentResponse struct {
	http.ResponseWriter
	code     int
	buffered bool
	body     bytes.Buffer
}

func (w *generationIntentResponse) WriteHeader(code int) {
	if w.code != 0 {
		return
	}
	w.code = code
	w.buffered = code >= 400
	if !w.buffered {
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *generationIntentResponse) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	if !w.buffered {
		return w.ResponseWriter.Write(b)
	}
	if w.body.Len()+len(b) > 65536 {
		w.ResponseWriter.WriteHeader(w.code)
		_, e := w.ResponseWriter.Write(w.body.Bytes())
		w.buffered = false
		if e != nil {
			return 0, e
		}
		return w.ResponseWriter.Write(b)
	}
	return w.body.Write(b)
}

func (w *generationIntentResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type generationStorageAdmissionKey struct{}

type multipartIntentKey struct{}

func (s *Server) multipartIntent(w http.ResponseWriter, r *http.Request, value *any) (func(), bool) {
	release := func() {}
	if s.references != nil {
		var e error
		release, e = s.references.AdmitMultipart()
		if e != nil {
			s.referenceError(w, e)
			return nil, false
		}
	}
	cleanup := func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		release()
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		cleanup()
		invalidParameter(w, "body", "invalid multipart request or upload exceeds 33554432 bytes")
		return nil, false
	}
	type fileIntent struct {
		Name, ContentType, Digest string
		Size                      int64
	}
	files := map[string][]fileIntent{}
	for name, headers := range r.MultipartForm.File {
		for _, header := range headers {
			f, e := header.Open()
			if e != nil {
				cleanup()
				invalidParameter(w, "body", "could not read uploaded file")
				return nil, false
			}
			hash := sha256.New()
			n, e := io.Copy(hash, io.LimitReader(f, (32<<20)+1))
			closeErr := f.Close()
			if e != nil || closeErr != nil || n != header.Size || n > 32<<20 {
				cleanup()
				invalidParameter(w, "body", "could not read bounded uploaded file")
				return nil, false
			}
			files[name] = append(files[name], fileIntent{header.Filename, header.Header.Get("Content-Type"), hex.EncodeToString(hash.Sum(nil)), n})
		}
	}
	*value = struct {
		Values map[string][]string
		Files  map[string][]fileIntent
	}{r.MultipartForm.Value, files}
	return cleanup, true
}
