package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type generationCursor struct {
	Time  time.Time `json:"t"`
	ID    string    `json:"id"`
	Scope string    `json:"s"`
}

func generationCursorScope(r *http.Request) string {
	_, user := identity.UserFromContext(r.Context())
	value, _ := json.Marshal([]any{user, identity.OwnerID(r.Context()), identity.ActorID(r.Context()), identity.ContentAuthorFilter(r.Context()), identity.OrganizationFromContext(r.Context()).Role, r.URL.Query().Get("project_id")})
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func readGenerationCursor(raw, scope string) (*core.GenerationPosition, bool) {
	if len(raw) > 1024 {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	var c generationCursor
	if json.Unmarshal(b, &c) != nil || c.Scope != scope || c.Time.IsZero() || c.Time.Year() < 1 || c.Time.Year() > 9999 {
		return nil, false
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return nil, false
	}
	return &core.GenerationPosition{CreatedAt: c.Time, ID: c.ID}, true
}

func writeGenerationCursor(task core.Task, scope string) string {
	b, _ := json.Marshal(generationCursor{Time: task.CreatedAt, ID: task.ID, Scope: scope})
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) listGenerationsCursor(w http.ResponseWriter, r *http.Request, limit int) {
	scope := generationCursorScope(r)
	q := core.GenerationPageQuery{Limit: limit, ProjectID: r.URL.Query().Get("project_id")}
	if view := r.URL.Query().Get("view"); view != "" {
		if view != "versions" {
			invalidParameter(w, "view", "view must be versions or omitted")
			return
		}
		q.VersionsOnly = true
	}
	if raw := r.URL.Query().Get("include_total"); raw != "" {
		if raw != "true" && raw != "false" {
			invalidParameter(w, "include_total", "include_total must be true or false")
			return
		}
		q.SkipTotal = raw == "false"
	}
	if r.URL.Query().Has("ids") {
		ids := strings.Split(r.URL.Query().Get("ids"), ",")
		seen := map[string]bool{}
		if len(ids) > 50 {
			invalidParameter(w, "ids", "ids must contain 1 to 50 unique generation UUIDs")
			return
		}
		for _, id := range ids {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.String() != id || seen[id] {
				invalidParameter(w, "ids", "ids must contain 1 to 50 unique generation UUIDs")
				return
			}
			seen[id] = true
		}
		q.IDs = ids
	}
	for name, target := range map[string]**core.GenerationPosition{"cursor": &q.Before, "through": &q.Through} {
		if raw := r.URL.Query().Get(name); raw != "" {
			position, ok := readGenerationCursor(raw, scope)
			if !ok {
				invalidParameter(w, name, name+" must be a cursor returned for the same viewer and project filter")
				return
			}
			*target = position
		}
	}
	reader, ok := s.service.(core.GenerationPageReader)
	if !ok {
		writeError(w, 503, "service_unavailable", "generation cursor pagination is unavailable")
		return
	}
	page, err := reader.ListGenerationPage(r.Context(), q)
	if errors.Is(err, core.ErrProjectInvalid) {
		projectError(w, err)
		return
	}
	if err != nil {
		writeError(w, 503, "service_unavailable", "generations could not be read")
		return
	}
	data := make([]any, 0, len(page.Versions))
	var next any
	for i, version := range page.Versions {
		revision := scope + ":" + strconv.FormatInt(version.Revision, 10)
		if q.VersionsOnly {
			data = append(data, map[string]any{"id": version.ID, "journal_revision": revision})
		} else {
			view := s.generationView(r, page.Tasks[i])
			switch fields := view.(type) {
			case map[string]json.RawMessage:
				fields["journal_revision"], _ = json.Marshal(revision)
			case map[string]any:
				fields["journal_revision"] = revision
			}
			data = append(data, view)
		}
		next = writeGenerationCursor(core.Task{ID: version.ID, CreatedAt: version.CreatedAt}, scope)
	}
	result := map[string]any{"object": "list", "data": data, "has_more": page.HasMore, "next_cursor": next, "journal_scope": scope}
	if !q.SkipTotal {
		result["total"] = page.Total
	}
	s.writeViewerJSON(w, r, 200, result)
}
