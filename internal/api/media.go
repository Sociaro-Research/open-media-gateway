package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

// mediaApplicationHostsKey preserves direct-storage isolation across an explicit
// API hostname migration. It does not affect authorization or browser trust.
type mediaApplicationHostsKey struct{}

type mediaURLSigner struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func newMediaURLSigner(secret string, ttl time.Duration) *mediaURLSigner {
	return &mediaURLSigner{secret: []byte(secret), ttl: ttl, now: time.Now}
}

func (s *mediaURLSigner) create(taskID string) (string, time.Time) {
	expiresAt := s.now().UTC().Add(s.ttl)
	expires := strconv.FormatInt(expiresAt.Unix(), 10)
	values := url.Values{"expires": {expires}, "signature": {s.signature(taskID, expires)}}
	return "/media/" + taskID + "?" + values.Encode(), expiresAt
}

func (s *mediaURLSigner) valid(taskID, expires, signature string) bool {
	timestamp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || s.now().UTC().After(time.Unix(timestamp, 0)) {
		return false
	}
	expected := s.signature(taskID, expires)
	return len(signature) == len(expected) && subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) == 1
}

func (s *mediaURLSigner) signature(taskID, expires string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = fmt.Fprintf(mac, "%s\n%s", taskID, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) mediaAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.generationTask(w, r); !ok {
		return
	}
	task, err := s.service.GetTask(r.Context(), r.PathValue("id"))
	if errors.Is(err, core.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "task could not be read")
		return
	}
	if s.mediaSigner == nil || task.Status != core.TaskSucceeded || task.StorageKey == nil || task.ResultDeletedAt != nil {
		writeError(w, http.StatusNotFound, "not_found", "stored media is not available")
		return
	}
	previewURL, expiresAt := s.mediaSigner.create(task.ID)
	downloadURL := previewURL + "&download=1"
	response := map[string]any{"preview_url": previewURL, "download_url": downloadURL, "expires_at": expiresAt}
	if task.LastFrameStorageKey != nil {
		response["last_frame_url"] = previewURL + "&artifact=last-frame"
	}
	if len(task.StoredArtifacts) > 0 {
		artifacts := []map[string]any{}
		for _, a := range task.StoredArtifacts {
			artifacts = append(artifacts, map[string]any{"id": a.ID, "media_type": a.MediaType, "format": a.Format, "preview_url": previewURL + "&artifact=" + url.QueryEscape(a.ID), "download_url": previewURL + "&artifact=" + url.QueryEscape(a.ID) + "&download=1"})
		}
		response["artifacts"] = artifacts
	}
	s.writeViewerJSON(w, r, http.StatusOK, response)
}

func (s *Server) serveAuthorizedMedia(w http.ResponseWriter, r *http.Request) {
	s.serveTaskMedia(w, r, r.PathValue("id"), false)
}

func (s *Server) serveAuthorizedLastFrame(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.generationTask(w, r); !ok {
		return
	}
	s.serveTaskMedia(w, r, r.PathValue("id"), true)
}

func (s *Server) serveSignedMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mediaSigner == nil || !s.mediaSigner.valid(id, r.URL.Query().Get("expires"), r.URL.Query().Get("signature")) {
		writeError(w, http.StatusForbidden, "invalid_media_url", "media URL is invalid or expired")
		return
	}
	s.serveTaskMedia(w, r, id, r.URL.Query().Get("artifact") == "last-frame")
}

func (s *Server) serveTaskMedia(w http.ResponseWriter, r *http.Request, id string, lastFrame bool) {
	if s.storage == nil {
		writeError(w, http.StatusNotFound, "not_found", "stored media is not available")
		return
	}
	task, err := s.service.GetTask(r.Context(), id)
	if errors.Is(err, core.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "task could not be read")
		return
	}
	if task.Status != core.TaskSucceeded || task.StorageKey == nil || task.ResultDeletedAt != nil {
		writeError(w, http.StatusNotFound, "not_found", "stored media is not available")
		return
	}
	key := task.StorageKey
	resultType, resultSHA := task.ResultType, task.ResultSHA256
	if lastFrame {
		key = task.LastFrameStorageKey
	}
	artifactID := r.PathValue("artifact")
	if artifactID == "" && strings.HasPrefix(r.URL.Path, "/media/") {
		artifactID = r.URL.Query().Get("artifact")
		if artifactID == "last-frame" {
			artifactID = ""
		}
	}
	if artifactID != "" {
		key = nil
		for _, a := range task.StoredArtifacts {
			if a.ID == artifactID {
				selected := a
				key = &selected.StorageKey
				resultType = &selected.ContentType
				resultSHA = &selected.SHA256
				break
			}
		}
	}

	if key == nil {
		writeError(w, http.StatusNotFound, "not_found", "stored artifact is not available")
		return
	}
	// S3 owns byte delivery, Range handling and slow-client connections.
	if direct, ok := s.storage.(storage.DirectReader); ok {
		contentType := ""
		if !lastFrame && resultType != nil {
			contentType = *resultType
		}
		disposition := "inline"
		if r.URL.Query().Get("download") == "1" || strings.EqualFold(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]), "application/octet-stream") {
			disposition = "attachment"
		}
		disposition += `; filename="` + task.ID + filepath.Ext(*key) + `"`
		location, err := direct.DirectURL(identity.WithOwnerID(r.Context(), task.OwnerID), *key, r.Method, contentType, disposition, 15*time.Minute)
		if err != nil {
			writeError(w, 503, "service_unavailable", "stored media could not be opened")
			return
		}
		if location != "" {
			if !isolatedMediaDestination(r, location) {
				writeError(w, 503, "unsafe_storage_origin", "direct media storage must use a separate hostname")
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			return
		}
	}
	// Signed links do not carry a user session. Resolve storage using the verified task owner.
	object, err := s.storage.Open(identity.WithOwnerID(r.Context(), task.OwnerID), *key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "stored media file is missing")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "stored media could not be opened")
		return
	}
	defer object.Reader.Close()
	contentType := object.ContentType
	if !lastFrame && resultType != nil && *resultType != "" {
		contentType = *resultType
	}
	if contentType == "" {
		contentType = "video/mp4"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Media is untrusted even when its MIME type is image/* (for example SVG).
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !lastFrame && resultSHA != nil {
		w.Header().Set("ETag", `"sha256-`+*resultSHA+`"`)
	}
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" || strings.EqualFold(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]), "application/octet-stream") {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+task.ID+filepath.Ext(object.Key)+`"`)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	http.ServeContent(w, r, filepath.Base(object.Key), object.ModTime, object.Reader)
}

// Cookies are shared by hostname even across ports. Mutable storage objects
// must never be delivered from the console/API hostname.
func isolatedMediaDestination(r *http.Request, location string) bool {
	u, err := url.Parse(location)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false
	}
	app := &url.URL{Host: r.Host}
	if strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(app.Hostname(), ".")) {
		return false
	}
	if hosts, ok := r.Context().Value(mediaApplicationHostsKey{}).([]string); ok {
		for _, host := range hosts {
			if strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(host, ".")) {
				return false
			}
		}
	}
	return true
}
