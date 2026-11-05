package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestReferenceTargetExactIdentity(t *testing.T) {
	catalog := []core.ProviderDescriptor{
		{ID: "mock", Models: []core.MediaModel{
			{ID: "mock-video-v1"},
			{ID: "vendor/native", PublicID: "studio/cinema"},
			{ID: "collision", PublicID: "unique"},
			{ID: "another", PublicID: "collision"},
			{ID: "duplicate-a", PublicID: "duplicate"},
			{ID: "duplicate-b", PublicID: "duplicate"},
			{ID: "same", PublicID: "same"},
		}},
		{ID: "other", Models: []core.MediaModel{{ID: "mock-video-v1", PublicID: "other-video"}}},
	}
	for _, tc := range []struct {
		provider, id, want string
	}{
		{"mock", "mock-video-v1", "mock-video-v1"},
		{"mock", "mock/mock-video-v1", "mock-video-v1"},
		{"mock", "studio/cinema", "vendor/native"},
		{"mock", "vendor/native", "vendor/native"},
		{"mock", "same", "same"},
		{"mock", "mock/vendor/native", ""}, // renamed public ID is not an alias
		{"mock", "cinema", ""},             // no suffix/prefix guessing
		{"mock", "MOCK/mock-video-v1", ""},
		{"mock", "mock/mock-video-v1 ", ""},
		{"mock", "other-video", ""},
		{"other", "mock/mock-video-v1", ""},
		{"", "mock/mock-video-v1", ""},
		{"mock", "", ""},
		{"missing", "mock-video-v1", ""},
		{"mock", "collision", ""},
		{"mock", "duplicate", ""},
	} {
		t.Run(tc.provider+"/"+tc.id, func(t *testing.T) {
			got, ok := referenceTargetModel(catalog, tc.provider, tc.id)
			if ok != (tc.want != "") || got.ID != tc.want {
				t.Fatalf("got %q, %v; want %q", got.ID, ok, tc.want)
			}
		})
	}
}

func TestReferenceTargetPublicIDsAcrossUploadAndValidation(t *testing.T) {
	for _, publicID := range []string{"", "studio/cinema"} {
		t.Run("public="+publicID, func(t *testing.T) {
			s, _, svc, _ := templateFixture(t)
			svc.catalog[0].Models[0].PublicID = publicID
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"reference-identity"}`)
			listed := sendTest(h, "GET", "/v1/models", "", "", key)
			var models struct {
				Data []modelResponse `json:"data"`
			}
			if err := json.Unmarshal(listed.Body.Bytes(), &models); err != nil || listed.Code != 200 || len(models.Data) != 1 {
				t.Fatal(listed.Code, listed.Body.String(), err)
			}
			for _, id := range []string{models.Data[0].ID, "dance"} {
				target := &referencevalidation.Target{Provider: "mock", Model: id, Options: core.MediaOptions{ReferenceMode: "references"}}
				upload := validationUpload(t, h, "/v1/references", key, "", "synthetic.png", referencePNG(t), target)
				if upload.Code != 201 && upload.Code != 200 {
					t.Fatal(id, upload.Code, upload.Body.String())
				}
				var saved referenceResponse
				if err := json.Unmarshal(upload.Body.Bytes(), &saved); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]any{"validation": target, "reference_ids": []string{saved.ID}})
				if out := sendTest(h, "POST", "/v1/references/validate", string(raw), "", key); out.Code != 200 {
					t.Fatal(id, out.Code, out.Body.String())
				}
				// Public IDs must enforce the same inspected media constraints.
				bad := validationUpload(t, h, "/v1/references", key, "", "bad-fps.mp4", testmedia.MP4(125956, 12288, 244), target)
				if bad.Code != 400 || !strings.Contains(bad.Body.String(), "24–60 FPS") {
					t.Fatal(id, bad.Code, bad.Body.String())
				}
				if out := validationUpload(t, h, "/v1/system/template-references", "", "alice", "synthetic.png", referencePNG(t), target); out.Code != 201 && out.Code != 200 {
					t.Fatal("shared library", id, out.Code, out.Body.String())
				}
			}
			if svc.calls != 0 {
				t.Fatal("reference checks submitted generation")
			}
		})
	}
}

func TestReferenceTargetPublicUploadLimit(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	m := uploadLimitKlingModel(t)
	m.PublicID = "studio/video"
	svc.catalog = []core.ProviderDescriptor{{ID: "connection", Models: []core.MediaModel{m}}}
	r := httptest.NewRequest("POST", "/v1/references/uploads", nil)
	for _, id := range []string{m.ID, m.PublicID} {
		limit, err := s.referenceUploadLimit(r, &referencevalidation.Target{Provider: "connection", Model: id, Options: core.MediaOptions{ReferenceMode: "references"}})
		if err != nil || limit != references.HardMaxBytes {
			t.Fatal(id, limit, err)
		}
	}
}

func TestReferenceTargetCurrentAccessAndConflicts(t *testing.T) {
	s, _, svc, local := templateFixture(t)
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	if err := policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "mock", Model: "dance"}}); err != nil {
		t.Fatal(err)
	}
	policy.models[0].PublicID = "studio/cinema"
	s.modelAccess = policy
	repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
	s.references = references.New(repo, local)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"reference-access"}`)
	for _, tc := range []struct {
		provider, id string
		status       int
	}{
		{"mock", "studio/cinema", 200},
		{"mock", "dance", 200},
		{"mock", "mock/dance", 400},
		{"other", "studio/cinema", 400},
		{"mock", "missing", 400},
	} {
		target := &referencevalidation.Target{Provider: tc.provider, Model: tc.id, Options: core.MediaOptions{ReferenceMode: "references"}}
		raw, _ := json.Marshal(map[string]any{"validation": target})
		if out := sendTest(h, "POST", "/v1/references/validate", string(raw), "", key); out.Code != tc.status {
			t.Fatal(tc, out.Code, out.Body.String())
		}
	}
	policy.models[0].EnabledByDefault = false
	for _, id := range []string{"studio/cinema", "dance", "mock/dance"} {
		target := &referencevalidation.Target{Provider: "mock", Model: id, Options: core.MediaOptions{ReferenceMode: "references"}}
		raw, _ := json.Marshal(map[string]any{"validation": target})
		if out := sendTest(h, "POST", "/v1/references/validate", string(raw), "", key); out.Code != 400 {
			t.Fatal("hidden model validated", id, out.Code, out.Body.String())
		}
		upload := validationUpload(t, h, "/v1/references", key, "", "hidden.png", referencePNG(t), target)
		if upload.Code != 400 {
			t.Fatal("hidden model uploaded", id, upload.Code, upload.Body.String())
		}
		raw, _ = json.Marshal(references.UploadInput{Name: "hidden.png", ContentType: "image/png", SizeBytes: 100, SHA256: strings.Repeat("a", 64), Validation: target})
		if out := sendTest(h, "POST", "/v1/references/uploads", string(raw), "", key); out.Code != 400 {
			t.Fatal("hidden model planned upload", id, out.Code, out.Body.String())
		}
	}
	if len(repo.items) != 0 || len(repo.sessions) != 0 || svc.calls != 0 {
		t.Fatal("invalid target retained inputs or submitted generation")
	}
}

func TestReferenceTargetDirectUploadRechecksPublicID(t *testing.T) {
	s, _, svc, local := templateFixture(t)
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	if err := policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "mock", Model: "dance"}}); err != nil {
		t.Fatal(err)
	}
	policy.models[0].PublicID = "studio/cinema"
	s.modelAccess = policy
	data := referencePNG(t)
	repo := &audioUploadRepository{referenceMemory: &referenceMemory{items: map[string]references.Image{}}, sessions: map[string]references.UploadSession{}}
	s.references = references.New(repo, &audioUploadStore{Local: local, data: data})
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"direct-public-identity"}`)
	target := &referencevalidation.Target{Provider: "mock", Model: "studio/cinema", Options: core.MediaOptions{ReferenceMode: "references"}}
	raw, _ := json.Marshal(references.UploadInput{Name: "synthetic.png", ContentType: "image/png", SizeBytes: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Validation: target})
	out := sendTest(h, "POST", "/v1/references/uploads", string(raw), "", key)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	var plan references.UploadPlan
	if err := json.Unmarshal(out.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	path := "/v1/references/uploads/" + plan.ID + "/complete"
	policy.models[0].EnabledByDefault = false
	if out := sendTest(h, "POST", path, "{}", "", key); out.Code != 400 {
		t.Fatal("hidden target completed", out.Code, out.Body.String())
	}
	policy.models[0].EnabledByDefault = true
	policy.models[0].PublicID = "renamed"
	if out := sendTest(h, "POST", path, "{}", "", key); out.Code != 400 {
		t.Fatal("retired alias completed", out.Code, out.Body.String())
	}
	if len(repo.items) != 0 {
		t.Fatal("rejected completion retained reference")
	}
	policy.models[0].PublicID = target.Model
	if out := sendTest(h, "POST", path, "{}", "", key); out.Code != 200 {
		t.Fatal("public target did not complete", out.Code, out.Body.String())
	}
	if len(repo.items) != 1 || svc.calls != 0 {
		t.Fatal("completion did not retain exactly one local reference without generation")
	}
}

func TestReferenceTargetAmbiguityRejectedBeforeRetention(t *testing.T) {
	for _, alias := range []string{"dance", "shared-public-id"} {
		t.Run(alias, func(t *testing.T) {
			s, _, svc, _ := templateFixture(t)
			first := svc.catalog[0].Models[0]
			second := first
			second.ID, second.PublicID = "another", alias
			if alias == "shared-public-id" {
				first.PublicID = alias
			}
			svc.catalog[0].Models = []core.MediaModel{first, second}
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"ambiguous-reference"}`)
			target := &referencevalidation.Target{Provider: "mock", Model: alias, Options: core.MediaOptions{ReferenceMode: "references"}}
			raw, _ := json.Marshal(map[string]any{"validation": target})
			if out := sendTest(h, "POST", "/v1/references/validate", string(raw), "", key); out.Code != 400 {
				t.Fatal("ambiguous validation", out.Code, out.Body.String())
			}
			if out := validationUpload(t, h, "/v1/references", key, "", "synthetic.png", referencePNG(t), target); out.Code != 400 {
				t.Fatal("ambiguous multipart upload", out.Code, out.Body.String())
			}
			raw, _ = json.Marshal(references.UploadInput{Name: "synthetic.png", ContentType: "image/png", SizeBytes: 100, SHA256: strings.Repeat("a", 64), Validation: target})
			if out := sendTest(h, "POST", "/v1/references/uploads", string(raw), "", key); out.Code != 400 {
				t.Fatal("ambiguous direct upload", out.Code, out.Body.String())
			}
			r := httptest.NewRequest("POST", "/v1/references/uploads", nil)
			if _, err := s.referenceUploadLimit(r, target); err == nil || svc.calls != 0 {
				t.Fatal("ambiguous target selected a limit or submitted generation")
			}
		})
	}
}
