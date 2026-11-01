package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func (m *referenceMemory) Rename(ctx context.Context, id, name string) error {
	key := identity.OwnerID(ctx) + "/" + id
	item := m.items[key]
	item.Name = name
	m.items[key] = item
	return nil
}

func TestUnifiedTemplateAllEntryPointsCrossAdminAndUser(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	s.system.(*systemFixture).admins["carol"] = true
	s.auth = templateLibraryAuth{}
	h := s.handler()
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	ids := []string{}
	for i, entry := range []string{"library", "slot", "cover"} {
		path := "/v1/system/template-references"
		if entry != "library" {
			path = "/v1/system/templates/" + id + "/assets"
		}
		result := validationUpload(t, h, path, "", "alice", entry+".png", testmedia.PNG512(entry), nil)
		if result.Code != http.StatusCreated {
			t.Fatal(entry, result.Code, result.Body.String())
		}
		var asset references.Image
		if err := json.Unmarshal(result.Body.Bytes(), &asset); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, asset.ID)
		list := sendTest(h, "GET", "/v1/system/template-references", "", "carol", "")
		if list.Code != 200 || !strings.Contains(list.Body.String(), asset.ID) {
			t.Fatal(entry, "not visible cross-admin", list.Code)
		}
		renamed := sendTest(h, "PATCH", "/v1/system/template-references/"+asset.ID, `{"name":"Renamed `+entry+`.png"}`, "carol", "")
		if renamed.Code != 200 {
			t.Fatal(renamed.Code, renamed.Body.String())
		}
		selected := sendTest(h, "POST", "/v1/system/templates/"+id+"/assets", `{"library_reference_id":"`+asset.ID+`"}`, "alice", "")
		if selected.Code != 201 || !strings.Contains(selected.Body.String(), asset.ID) {
			t.Fatal("selection copied identity", selected.Code, selected.Body.String())
		}
		var again references.Image
		json.Unmarshal(selected.Body.Bytes(), &again)
		if again.ID != asset.ID || again.SHA256 != asset.SHA256 {
			t.Fatal(again, asset)
		}
		read := sendTest(h, "GET", "/v1/system/template-references/"+asset.ID+"/content", "", "bob", "")
		if read.Code != 200 || !bytes.Equal(read.Body.Bytes(), testmedia.PNG512(entry)) {
			t.Fatal("ordinary read", i, read.Code)
		}
		for _, method := range []string{"PATCH", "DELETE"} {
			if out := sendTest(h, method, "/v1/system/template-references/"+asset.ID, `{"name":"forged.png"}`, "bob", ""); out.Code != 403 {
				t.Fatal("consumer mutation", method, out.Code)
			}
		}
	}
	list := sendTest(h, "GET", "/v1/system/template-references", "", "bob", "")
	for _, id := range ids {
		if !strings.Contains(list.Body.String(), id) {
			t.Fatal("consumer list missing", id)
		}
	}
	invalid := sendTest(h, "PATCH", "/v1/system/template-references/"+ids[0], `{"name":"../unsafe"}`, "alice", "")
	if invalid.Code != 400 {
		t.Fatal("invalid rename", invalid.Code)
	}
	replace := sendTest(h, "PATCH", "/v1/system/template-references/"+ids[0], `{"name":"changed","sha256":"different"}`, "alice", "")
	if replace.Code != 400 {
		t.Fatal("bytes replacement accepted", replace.Code)
	}
}
