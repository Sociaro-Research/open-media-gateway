package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateDeleteAdministratorVersionAndRetainedContent(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	id, other := uuid.NewString(), uuid.NewString()
	// Removal must work for an incomplete draft with an unavailable model.
	d := templates.Definition{Title: "Saved title", MediaType: "image", Provider: "unavailable", Model: "unavailable", Slots: []templates.Slot{{ID: "image:1", Label: ""}}}
	repo.items[id] = templates.Record{ID: id, Version: 7, Draft: d}
	repo.items[other] = templates.Record{ID: other, Version: 2, Draft: templates.Definition{Title: "Unrelated"}}
	before, _ := json.Marshal(repo.items)
	for _, tc := range []struct {
		actor, body string
		status      int
	}{
		{"bob", `{"version":7}`, 403},
		{"", `{"version":7}`, 401},
		{"alice", `{}`, 409},
		{"alice", `{"version":6}`, 409},
	} {
		w := sendTest(s.handler(), "DELETE", "/v1/system/templates/"+id, tc.body, tc.actor, "")
		after, _ := json.Marshal(repo.items)
		if w.Code != tc.status || !bytes.Equal(before, after) || repo.saves != 0 {
			t.Fatal("rejected deletion mutated content", w.Code, w.Body.String())
		}
	}
	w := sendTest(s.handler(), "DELETE", "/v1/system/templates/"+id, `{"version":7}`, "alice", "")
	var removed templates.Record
	if err := json.Unmarshal(w.Body.Bytes(), &removed); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || removed.ID != id || removed.Version != 8 || removed.Published != nil || removed.Draft.Title != d.Title || svc.calls != 0 {
		t.Fatal("wrong deletion contract", w.Code, w.Body.String())
	}
	if _, err := repo.Get(context.Background(), id); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("removed template remains readable", err)
	}
	if repo.items[other].Draft.Title != "Unrelated" || repo.items[other].Version != 2 {
		t.Fatal("unrelated template changed")
	}
	if w := sendTest(s.handler(), "DELETE", "/v1/system/templates/"+id, `{"version":7}`, "alice", ""); w.Code != 404 || repo.saves != 1 {
		t.Fatal("repeat deletion mutated state or claimed success", w.Code, w.Body.String())
	}
}

func TestTemplateDeletePreservesSavedMediaValidationFailure(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 3, Draft: templates.Definition{Title: "Retain edits", MediaType: "video", OverrideSoundID: uuid.NewString()}}
	before, _ := json.Marshal(repo.items[id])
	w := sendTest(s.handler(), "DELETE", "/v1/system/templates/"+id, `{"version":3}`, "alice", "")
	after, _ := json.Marshal(repo.items[id])
	if w.Code != 404 || repo.saves != 0 || !bytes.Equal(before, after) {
		t.Fatal("missing saved media was bypassed or content changed", w.Code, w.Body.String())
	}
}
