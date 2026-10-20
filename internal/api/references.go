package api

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func (s *Server) registerReferenceRoutes(mux *http.ServeMux) {
	if s.references == nil || s.mediaSigner == nil {
		return
	}
	mux.Handle("GET /v1/organizations/{org}/references", s.authenticate(http.HandlerFunc(s.listReferences)))
	mux.Handle("DELETE /v1/organizations/{org}/references/{id}", s.authenticate(http.HandlerFunc(s.deleteReference)))
	mux.Handle("GET /v1/references", s.authenticate(http.HandlerFunc(s.listReferences)))
	mux.Handle("POST /v1/references/uploads", s.authenticate(http.HandlerFunc(s.beginReferenceUpload)))
	mux.Handle("POST /v1/references/uploads/{id}/complete", s.authenticate(http.HandlerFunc(s.completeReferenceUpload)))
	mux.Handle("POST /v1/references", s.authenticate(http.HandlerFunc(s.uploadReference)))
	mux.Handle("DELETE /v1/references/{id}", s.authenticate(http.HandlerFunc(s.deleteReference)))
	mux.HandleFunc("GET /references/{owner}/{id}", s.serveReference)
	mux.HandleFunc("HEAD /references/{owner}/{id}", s.serveReference)
}

type referenceResponse struct {
	references.Image
	URL string `json:"url"`
}

// A domain-separated capability URL lets providers fetch private storage without
// cookies or bucket credentials. It remains valid until deletion or key rotation,
// so queued generations and copied tasks do not inherit expired input URLs.
func (s *Server) referenceSignature(owner, id string) string {
	return references.Signature(s.mediaSigner.secret, owner, id)
}
func (s *Server) referenceView(r *http.Request, item references.Image) referenceResponse {
	owner := identity.OwnerID(r.Context())
	return referenceResponse{Image: item, URL: fmt.Sprintf("/references/%s/%s?signature=%s", url.PathEscape(owner), item.ID, s.referenceSignature(owner, item.ID))}
}
func (s *Server) referenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, references.ErrInUse):
		writeError(w, 409, "reference_in_use", err.Error())
	case errors.Is(err, references.ErrUploadBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "upload_busy", err.Error())
	case errors.Is(err, references.ErrDirectUploadRequired):
		writeError(w, 409, "direct_upload_required", err.Error())
	case errors.Is(err, references.ErrUploadExpired):
		writeError(w, 410, "upload_expired", err.Error())
	case errors.Is(err, storage.ErrInvalidConfig):
		writeError(w, 400, "invalid_upload", "Uploaded file does not match the declared size or checksum, or the storage configuration is invalid.")
	case errors.Is(err, references.ErrDuplicate):
		writeError(w, 409, "duplicate_reference", "This file already exists in the organization. Ask a manager to locate it.")
	case errors.Is(err, storage.ErrObjectNotFound):
		writeError(w, 404, "not_found", "reference file not found")
	case errors.Is(err, references.ErrInvalidImage):
		writeError(w, 400, "invalid_reference", err.Error())
	default:
		writeError(w, 503, "service_unavailable", "reference operation failed; check storage connection and try again")
	}
}
func (s *Server) listReferences(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			writeError(w, 400, "invalid_request", "offset must be from 0 to 1000000")
			return
		}
	}
	const pageSize = 48
	items, err := s.references.List(r.Context(), pageSize+1, offset)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	hasMore := len(items) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	data := make([]referenceResponse, len(items))
	for i, item := range items {
		data[i] = s.referenceView(r, item)
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": data, "has_more": hasMore, "next_offset": offset + len(data)})
}
func (s *Server) beginReferenceUpload(w http.ResponseWriter, r *http.Request) {
	var input references.UploadInput
	if !decodeSystem(w, r, &input) {
		return
	}
	plan, err := s.references.BeginUpload(r.Context(), input)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	if plan.Existing != nil {
		s.writeViewerJSON(w, r, 200, map[string]any{"mode": "existing", "reference": s.referenceView(r, *plan.Existing)})
		return
	}
	s.writeViewerJSON(w, r, 201, plan)
}
func (s *Server) completeReferenceUpload(w http.ResponseWriter, r *http.Request) {
	item, err := s.references.FinishUpload(r.Context(), r.PathValue("id"))
	if err != nil {
		s.referenceError(w, err)
		return
	}
	s.writeViewerJSON(w, r, 200, s.referenceView(r, item))
}
func (s *Server) uploadReference(w http.ResponseWriter, r *http.Request) {
	// Reject S3 uploads before parsing or spooling a multipart request body.
	if direct, err := s.references.RequiresDirectUpload(r.Context()); err != nil {
		s.referenceError(w, err)
		return
	} else if direct {
		s.referenceError(w, references.ErrDirectUploadRequired)
		return
	}
	release, err := s.references.AdmitMultipart()
	if err != nil {
		s.referenceError(w, err)
		return
	}
	defer release()
	// Allow slow uploads independently of the short JSON request deadline.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, references.MaxBytes+(1<<20))
	defer r.Body.Close()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, 400, "invalid_upload", "send one supported media file in the multipart file field (up to 200 MB)")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		writeError(w, 400, "invalid_upload", "send exactly one file")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "invalid_upload", "file is required")
		return
	}
	defer file.Close()
	item, err := s.references.Upload(r.Context(), header.Filename, file)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	status := http.StatusCreated
	if item.Duplicate {
		status = http.StatusOK
	}
	s.writeViewerJSON(w, r, status, s.referenceView(r, item))
}
func (s *Server) deleteReference(w http.ResponseWriter, r *http.Request) {
	if err := s.references.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.referenceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) serveReference(w http.ResponseWriter, r *http.Request) {
	owner, id := r.PathValue("owner"), r.PathValue("id")
	if _, err := uuid.Parse(owner); err != nil {
		http.NotFound(w, r)
		return
	}
	if !hmac.Equal([]byte(r.URL.Query().Get("signature")), []byte(s.referenceSignature(owner, id))) {
		writeError(w, 403, "invalid_reference_url", "reference URL is invalid")
		return
	}
	direct, err := s.references.DirectURL(identity.WithOwnerID(r.Context(), owner), id, r.Method, 15*time.Minute)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	if direct != "" {
		if !isolatedMediaDestination(r, direct) {
			writeError(w, 503, "unsafe_storage_origin", "direct media storage must use a separate hostname")
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, direct, http.StatusTemporaryRedirect)
		return
	}
	item, object, err := s.references.Open(identity.WithOwnerID(r.Context(), owner), id)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	defer object.Reader.Close()
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	http.ServeContent(w, r, item.ID, object.ModTime, object.Reader)
}
