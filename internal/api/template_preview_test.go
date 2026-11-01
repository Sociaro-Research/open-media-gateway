package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

type editorPreviewStore struct{ *storage.Local }

func (s editorPreviewStore) DirectURL(context.Context, string, string, string, string, time.Duration) (string, error) {
	return "https://storage.fixture.test/media.mp4", nil
}
func (s editorPreviewStore) Open(ctx context.Context, key string) (storage.Object, error) {
	if !strings.Contains(key, identity.OwnerID(ctx)+"/references/") {
		return storage.Object{}, storage.ErrObjectNotFound
	}
	return s.Local.Open(ctx, key)
}
func TestAuthenticatedTemplatePreviewStreamsWithoutRedirect(t *testing.T) {
	for _, owner := range []string{identity.SharedOwnerID, templates.LibraryOwnerID} {
		t.Run(owner, func(t *testing.T) {
			s, repo, _, local := templateFixture(t)
			s.storage = editorPreviewStore{local}
			id, assetID := uuid.NewString(), uuid.NewString()
			data := testmedia.MP4(5000, 1000, 150)
			stored, e := local.StoreTemplateMedia(identity.WithDataOwnerID(context.Background(), owner), assetID, data, "video/mp4")
			if e != nil {
				t.Fatal(e)
			}
			repo.items[id] = templates.Record{ID: id, Version: 1}
			repo.assets[assetID] = templates.Asset{ID: assetID, ContentType: "video/mp4", StorageKey: stored.StorageKey, SizeBytes: stored.SizeBytes, SHA256: stored.SHA256, StorageOwnerID: owner}
			h := s.handler()
			path := "/v1/system/templates/" + id + "/assets/" + assetID
			normal := sendTest(h, "GET", path, "", "alice", "")
			if normal.Code != 307 {
				t.Fatal(normal.Code, normal.Body.String())
			}
			preview := sendTest(h, "GET", path+"?preview=1", "", "alice", "")
			if preview.Code != 200 || preview.Header().Get("Location") != "" || !bytes.Equal(preview.Body.Bytes(), data) || preview.Header().Get("Content-Type") != "video/mp4" {
				t.Fatal("same-origin preview", preview.Code, preview.Header())
			}
			r := httptest.NewRequest("GET", path+"?preview=1", nil)
			r.Header.Set("Range", "bytes=0-15")
			r.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), data[:16]) {
				t.Fatal("range preview failed", w.Code)
			}
			if denied := sendTest(h, "GET", path+"?preview=1", "", "bob", ""); denied.Code != 403 {
				t.Fatal("consumer preview privilege", denied.Code)
			}
		})
	}

}
