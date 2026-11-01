package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func templateLibraryContext(ctx context.Context) context.Context {
	ctx = identity.WithOrganization(ctx, "", false)
	ctx = identity.WithDataOwnerID(ctx, templates.LibraryOwnerID)
	return identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
}

// Keep template sources in a distinct data namespace using the installation's
// configured storage. The adapter intentionally uses server-validated template
// writes, including S3, rather than issuing a public source capability URL.
type templateReferenceStore struct{ storage.Store }

func (s templateReferenceStore) StoreReference(ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	if _, err := mediafile.Inspect(data, kind); err != nil {
		return core.StoredMedia{}, fmt.Errorf("%w: %v", references.ErrInvalidImage, err)
	}
	target, ok := s.Store.(interface {
		StoreTemplateMedia(context.Context, string, []byte, string) (core.StoredMedia, error)
	})
	if !ok {
		return core.StoredMedia{}, storage.ErrInvalidConfig
	}
	return target.StoreTemplateMedia(ctx, id, data, kind)
}
func (s templateReferenceStore) DeleteReference(ctx context.Context, key string) error {
	target, ok := s.Store.(interface {
		DeleteReference(context.Context, string) error
	})
	if !ok {
		return storage.ErrInvalidConfig
	}

	return target.DeleteReference(ctx, key)
}

func (s *Server) sharedTemplateReferences(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" && !s.templatePublisher(w, r) {
		return
	}
	if s.templateReferences == nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	r = r.WithContext(templateLibraryContext(r.Context()))
	if r.Method == "POST" {
		if !s.storageAdmission(w, r) {
			return
		}
		// Reuse the bounded multipart transport without changing private libraries.
		s.uploadReferenceLibrary(w, r, s.templateReferences, func(_ *http.Request, item references.Image) referenceResponse { return s.templateLibraryView(item) })
		return
	}
	if r.Method == "PATCH" {
		var in struct {
			Name string `json:"name"`
		}
		if !decodeSystem(w, r, &in) {
			return
		}
		if err := s.templateReferences.Rename(r.Context(), r.PathValue("id"), in.Name); err != nil {
			s.referenceError(w, err)
			return
		}
		item, err := s.templateReferences.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			s.referenceError(w, err)
			return
		}
		s.writeViewerJSON(w, r, 200, s.templateLibraryView(item))
		return
	}
	if r.Method == "DELETE" {
		if err := s.templateReferences.Delete(r.Context(), r.PathValue("id")); err != nil {
			s.referenceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if id := r.PathValue("id"); id != "" {
		item, object, err := s.templateReferences.Open(r.Context(), id)
		if err != nil {
			s.referenceError(w, err)
			return
		}
		defer object.Reader.Close()
		w.Header().Set("Content-Type", item.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
		if r.Method != "HEAD" {
			_, _ = io.Copy(w, object.Reader)
		}
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000000 {
			writeError(w, 400, "invalid_request", "offset must be from 0 to 1000000")
			return
		}
		offset = value
	}
	items, err := s.templateReferences.List(r.Context(), 49, offset)
	if err != nil {
		s.referenceError(w, err)
		return
	}
	more := len(items) > 48
	if more {
		items = items[:48]
	}
	data := make([]referenceResponse, len(items))
	for i, item := range items {
		data[i] = s.templateLibraryView(item)
	}
	s.writeViewerJSON(w, r, 200, map[string]any{"data": data, "has_more": more, "next_offset": offset + len(data)})
}
func (s *Server) templateLibraryView(item references.Image) referenceResponse {
	return referenceResponse{Image: item, URL: fmt.Sprintf("/references/%s/%s?signature=%s", templates.LibraryOwnerID, item.ID, s.referenceSignature(templates.LibraryOwnerID, item.ID))}
}

func (s *Server) templateAssetMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.templatePublisher(w, r) {
		return
	}
	asset, err := s.templates.Asset(r.Context(), r.PathValue("asset"))
	if err != nil {
		templateError(w, err)
		return
	}
	if _, err := s.templates.Get(r.Context(), r.PathValue("template")); err != nil {
		templateError(w, templates.ErrNotFound)
		return
	}
	s.writeViewerJSON(w, r, 200, asset)
}

func (s *Server) generationReference(ctx context.Context, id string) (references.Image, context.Context, error) {
	item, err := s.references.Get(ctx, id)
	if err == nil {
		return item, ctx, nil
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		return item, ctx, err
	}
	if s.templateReferences == nil {
		return item, ctx, err
	}
	shared := templateLibraryContext(ctx)
	item, err = s.templateReferences.Get(shared, id)
	return item, shared, err
}

// Only these shared asset reads are generation-scoped; all mutations and
// template-authoring routes retain system:manage and Administrator checks.
func sharedTemplateRead(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	if r.URL.Path == "/v1/system/template-references" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return len(parts) == 5 && parts[0] == "v1" && parts[1] == "system" && parts[2] == "template-references" && parts[4] == "content"
}
