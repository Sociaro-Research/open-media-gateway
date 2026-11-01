package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
	"strings"
	"testing"
)

type templateUploadContextService struct{ *openAIServiceFixture }

func (s *templateUploadContextService) ListProviders(ctx context.Context) []core.ProviderDescriptor {
	if identity.ProviderOwnerID(ctx) != identity.SharedOwnerID {
		return nil
	}
	return s.openAIServiceFixture.ListProviders(ctx)
}
func TestTemplateUploadErrorParityWithSharedCatalog(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	s.service = &templateUploadContextService{svc}
	s.modelAccess = &modelAccessMemory{org: map[string]map[string]bool{}}
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	h := s.handler()
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references"}}
	bad := testmedia.MP4(125956, 12288, 244)
	library := validationUpload(t, h, "/v1/system/template-references", "", "alice", "timing.mp4", bad, target)
	direct := validationUpload(t, h, "/v1/system/templates/"+id+"/assets", "", "alice", "timing.mp4", bad, target)
	t.Logf("library %d %s", library.Code, library.Body.String())
	t.Logf("direct %d %s", direct.Code, direct.Body.String())
	if library.Code != 400 || direct.Code != 400 {
		t.Fatal("expected both uploads rejected")
	}
	if !strings.Contains(library.Body.String(), "24–60 FPS") || !strings.Contains(direct.Body.String(), "24–60 FPS") {
		t.Fatal("upload paths do not preserve the same file constraint explanation")
	}
	paths := []string{"/v1/system/template-references", "/v1/system/templates/" + id + "/assets"}
	for _, path := range paths {
		out := validationUpload(t, h, path, "", "bob", "timing.mp4", bad, target)
		if out.Code != 403 {
			t.Fatal("consumer upload permission changed", path, out.Code)
		}
	}
	good := testmedia.MP4(5000, 1000, 150)
	for _, path := range paths {
		out := validationUpload(t, h, path, "", "alice", "valid.mp4", good, target)
		if out.Code != 201 {
			t.Fatal("valid retry rejected", path, out.Code, out.Body.String())
		}
	}
	policy := s.modelAccess.(*modelAccessMemory)
	for i := range policy.models {
		policy.models[i].EnabledByDefault = false
	}
	for _, path := range paths {
		out := validationUpload(t, h, path, "", "alice", "timing.mp4", bad, target)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "model is unavailable") {
			t.Fatal("hidden model bypassed", path, out.Code, out.Body.String())
		}
	}
	generic := validationUpload(t, h, paths[0], "", "alice", "generic.mp4", bad, nil)
	if generic.Code != 201 {
		t.Fatal("generic upload invented model-specific minimum", generic.Code, generic.Body.String())
	}
	if svc.calls != 0 {
		t.Fatal("validation contacted generation", svc.calls)
	}
}

func TestTemplateUploadsAcceptConstantSamplesDespiteLongerHeader(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	h := s.handler()
	data := testmedia.MP4(124928, 12288, 244)
	at := bytes.Index(data, []byte("mdhd"))
	if at < 0 {
		t.Fatal("missing header")
	}
	binary.BigEndian.PutUint32(data[at+20:at+24], 125952)
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references"}}
	for _, path := range []string{"/v1/system/template-references", "/v1/system/templates/" + id + "/assets"} {
		out := validationUpload(t, h, path, "", "alice", "constant24.mp4", data, target)
		if out.Code != 201 {
			t.Fatal(path, out.Code, out.Body.String())
		}
	}
}
