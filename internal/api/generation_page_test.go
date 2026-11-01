package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

type generationPageFixture struct {
	*openAIServiceFixture
	query core.GenerationPageQuery
}

func (s *generationPageFixture) ListGenerationPage(_ context.Context, q core.GenerationPageQuery) (core.GenerationPage, error) {
	s.query = q
	return core.GenerationPage{Tasks: []core.Task{{ID: "11111111-1111-4111-8111-111111111111", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)}}, Total: 123, HasMore: true, Versions: []core.GenerationRecordVersion{{GenerationPosition: core.GenerationPosition{ID: "11111111-1111-4111-8111-111111111111", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)}, Revision: 1}}}, nil
}

func TestGenerationCursorAPI(t *testing.T) {
	s := &generationPageFixture{openAIServiceFixture: openAIFixture()}
	h := New(s, healthStub{}, "secret")
	send := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := send("/v1/generations?cursor=&limit=10&project_id=none")
	var page struct {
		NextCursor string `json:"next_cursor"`
		Total      int64  `json:"total"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Total != 123 || !page.HasMore || page.NextCursor == "" || s.query.Limit != 10 || s.query.ProjectID != "none" {
		t.Fatal(w.Code, w.Body.String(), s.query, err)
	}
	w = send("/v1/generations?limit=10&project_id=none&cursor=" + page.NextCursor + "&through=" + page.NextCursor)
	if w.Code != 200 || s.query.Before == nil || s.query.Through == nil || s.query.Before.CreatedAt.Nanosecond() != 123456000 {
		t.Fatal("precision/continuation", w.Code, w.Body.String(), s.query)
	}
	w = send("/v1/generations?cursor=&limit=100&view=versions&include_total=false")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"total"`) || strings.Contains(w.Body.String(), `"status"`) || !strings.Contains(w.Body.String(), `"journal_revision"`) || !s.query.VersionsOnly || !s.query.SkipTotal {
		t.Fatal("narrow revision response without repeated count", w.Code, w.Body.String(), s.query)
	}
	w = send("/v1/generations?cursor=&limit=50&ids=11111111-1111-4111-8111-111111111111")
	if w.Code != 200 || len(s.query.IDs) != 1 || !strings.Contains(w.Body.String(), `"journal_revision"`) {
		t.Fatal("changed-card read", w.Code, w.Body.String(), s.query)
	}
	for _, query := range []string{"cursor=bad", "cursor=" + page.NextCursor, "cursor=&through=bad", "cursor=&offset=0", "through=" + page.NextCursor, "cursor=&limit=0", "cursor=&limit=101", "cursor=&extra=x", "cursor=&view=full", "cursor=&include_total=0", "cursor=&ids=", "cursor=&ids=bad", "cursor=&ids=" + strings.TrimSuffix(strings.Repeat("11111111-1111-4111-8111-111111111111,", 51), ","), "cursor=&ids=11111111-1111-4111-8111-111111111111,11111111-1111-4111-8111-111111111111", "view=versions", "ids=11111111-1111-4111-8111-111111111111", "include_total=false"} {
		w := send("/v1/generations?" + query)
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	// Cursors are positions only, bound to a viewer/filter. They never provide
	// authorization, even if a client decodes or alters their contents.
	r := httptest.NewRequest("GET", "/v1/generations?project_id=none", nil)
	r = r.WithContext(identity.WithOwnerID(r.Context(), uuid.NewString()))
	if _, ok := readGenerationCursor(page.NextCursor, generationCursorScope(r)); ok {
		t.Fatal("cursor crossed viewer boundary")
	}
	noReader := New(openAIFixture(), healthStub{}, "secret")
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/generations?cursor=", nil)
	r.Header.Set("Authorization", "Bearer secret")
	noReader.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "unavailable") {
		t.Fatal("must not silently download an unbounded fallback", w.Code, w.Body.String())
	}
}
