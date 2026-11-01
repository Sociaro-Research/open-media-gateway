package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

// Match the repository boundary: authenticated reads are owner-filtered; a
// verified signed delivery capability resolves the task without a user session.
type artifactBoundaryService struct {
	*openAIServiceFixture
	task core.Task
}

func (s *artifactBoundaryService) GetTask(ctx context.Context, id string) (core.Task, error) {
	if id != s.task.ID {
		return core.Task{}, core.ErrTaskNotFound
	}
	if _, authenticated := identity.UserFromContext(ctx); authenticated && identity.OwnerID(ctx) != s.task.OwnerID {
		return core.Task{}, core.ErrTaskNotFound
	}
	return s.task, nil
}
func (s *artifactBoundaryService) ListTasks(ctx context.Context, _ int) ([]core.Task, error) {
	task, err := s.GetTask(ctx, s.task.ID)
	if err != nil {
		return []core.Task{}, nil
	}
	return []core.Task{task}, nil
}

type artifactBoundaryStore struct {
	*storage.Local
	t      *testing.T
	owner  string
	opened []string
}

func (s *artifactBoundaryStore) Open(ctx context.Context, key string) (storage.Object, error) {
	s.opened = append(s.opened, key)
	if identity.OwnerID(ctx) != s.owner {
		s.t.Fatalf("artifact storage opened under a different owner: %q", identity.OwnerID(ctx))
	}
	return s.Local.Open(ctx, key)
}

type artifactBoundaryFixture struct {
	server               *Server
	service              *artifactBoundaryService
	store                *artifactBoundaryStore
	handler              http.Handler
	ownerKey, foreignKey string
	bytes                map[string][]byte
}

func newArtifactBoundaryFixture(t *testing.T) *artifactBoundaryFixture {
	t.Helper()
	s, _, svc, local := templateFixture(t)
	ctx := gw253Context("alice")
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	privateTask, privateSource := "upstream-task-private-fixture", "opaque-provider-source-handle-private-fixture"
	sourceURL := "https://upstream.invalid/private-result?source=" + privateSource
	task := core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(ctx), ActorID: "alice", Status: core.TaskSucceeded, Provider: "mock", Model: "dance", MediaType: "video", Prompt: "Synthetic archived outputs", ProviderTaskID: &privateTask, ProviderResultURL: &sourceURL, ProviderIdentity: privateSource, HasStoredResult: true, CreatedAt: now, UpdatedAt: now, StoredAt: &now}
	payloads := [][]byte{referencePNG(t), testmedia.WAV()}
	kinds := []string{"image", "audio"}
	types := []string{"image/png", "audio/wav"}
	formats := []string{"png", "wav"}
	contents := map[string][]byte{}
	group := uuid.NewString()
	for i, data := range payloads {
		localID := uuid.NewString()
		// data: sources guarantee archival cannot make a provider/network call.
		archived, err := local.Archive(ctx, task.ID+"/artifacts/"+localID, "data:"+types[i]+";base64,"+base64.StdEncoding.EncodeToString(data))
		if err != nil {
			t.Fatal(err)
		}
		task.StoredArtifacts = append(task.StoredArtifacts, core.StoredArtifact{ID: localID, ProviderID: "upstream-artifact-private-" + strconv.Itoa(i), MediaType: kinds[i], Format: formats[i], GroupID: group, StorageKey: archived.StorageKey, ContentType: archived.ContentType, SizeBytes: archived.SizeBytes, SHA256: archived.SHA256})
		contents[localID] = data
	}
	first := task.StoredArtifacts[0]
	task.StorageKey, task.ResultType, task.ResultSize, task.ResultSHA256 = &first.StorageKey, &first.ContentType, &first.SizeBytes, &first.SHA256
	service := &artifactBoundaryService{openAIServiceFixture: svc, task: task}
	store := &artifactBoundaryStore{Local: local, t: t, owner: task.OwnerID}
	s.service, s.storage = service, store
	s.mediaSigner.now = func() time.Time { return now }
	h := s.handler()
	_, ownerKey := issueTestKey(t, h, "alice", `{"name":"Artifact owner fixture"}`)
	_, foreignKey := issueTestKey(t, h, "bob", `{"name":"Artifact foreign fixture"}`)
	return &artifactBoundaryFixture{server: s, service: service, store: store, handler: h, ownerKey: ownerKey, foreignKey: foreignKey, bytes: contents}
}
func (f *artifactBoundaryFixture) contentURL(id string) string {
	return "/v1/generations/" + f.service.task.ID + "/artifacts/" + url.PathEscape(id) + "/content"
}

type artifactBoundaryAccess struct {
	Preview   string `json:"preview_url"`
	Artifacts []struct {
		ID       string `json:"id"`
		Preview  string `json:"preview_url"`
		Download string `json:"download_url"`
	} `json:"artifacts"`
}

func (f *artifactBoundaryFixture) access(t *testing.T) artifactBoundaryAccess {
	t.Helper()
	response := sendTest(f.handler, "GET", "/v1/generations/"+f.service.task.ID+"/media-access", "", "", f.ownerKey)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var access artifactBoundaryAccess
	if err := json.Unmarshal(response.Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	if len(access.Artifacts) != 2 {
		t.Fatal("archived artifacts missing from signed access", response.Body.String())
	}
	return access
}
func artifactBoundaryAssertBytes(t *testing.T, response *httptest.ResponseRecorder, method string, artifact core.StoredArtifact, expected []byte) {
	t.Helper()
	if response.Code != 200 {
		t.Fatal(method, response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != artifact.ContentType || response.Header().Get("Content-Length") != strconv.Itoa(len(expected)) || response.Header().Get("ETag") != `"sha256-`+artifact.SHA256+`"` {
		t.Fatal("wrong artifact metadata", response.Header())
	}
	if method == "HEAD" {
		if response.Body.Len() != 0 {
			t.Fatal("HEAD returned artifact bytes")
		}
	} else if !bytes.Equal(response.Body.Bytes(), expected) {
		t.Fatal("selected artifact bytes differ from archive")
	}
}

func TestArtifactContentAuthenticatedGETAndHEADSelectLocalArtifacts(t *testing.T) {
	f := newArtifactBoundaryFixture(t)
	for _, artifact := range f.service.task.StoredArtifacts {
		for _, method := range []string{"GET", "HEAD"} {
			response := sendTest(f.handler, method, f.contentURL(artifact.ID), "", "", f.ownerKey)
			artifactBoundaryAssertBytes(t, response, method, artifact, f.bytes[artifact.ID])
			if f.store.opened[len(f.store.opened)-1] != artifact.StorageKey {
				t.Fatal("content route selected another archived object")
			}
		}
	}
}

func TestArtifactContentRejectsUnauthorizedForeignUnknownAndDeleted(t *testing.T) {
	f := newArtifactBoundaryFixture(t)
	artifact := f.service.task.StoredArtifacts[1]
	for _, method := range []string{"GET", "HEAD"} {
		for _, tc := range []struct {
			name, key, id string
			status        int
		}{
			{"unauthenticated", "", artifact.ID, 401},
			{"invalid key", "invalid-synthetic-key", artifact.ID, 401},
			{"foreign owner", f.foreignKey, artifact.ID, 404},
			{"unknown local id", f.ownerKey, uuid.NewString(), 404},
			{"provider id", f.ownerKey, artifact.ProviderID, 404},
			{"source handle", f.ownerKey, f.service.task.ProviderIdentity, 404},
			{"storage key", f.ownerKey, artifact.StorageKey, 404},
		} {
			opened := len(f.store.opened)
			response := sendTest(f.handler, method, f.contentURL(tc.id), "", "", tc.key)
			if response.Code != tc.status || len(f.store.opened) != opened {
				t.Fatalf("%s %s: status=%d storage reads=%d", method, tc.name, response.Code, len(f.store.opened)-opened)
			}
			if method == "HEAD" && response.Body.Len() != 0 {
				t.Fatal("failed HEAD returned a body")
			}
		}
	}
	access := f.access(t)
	deleted := time.Date(2026, 10, 31, 12, 1, 0, 0, time.UTC)
	f.service.task.ResultDeletedAt = &deleted
	for _, artifact := range f.service.task.StoredArtifacts {
		for _, method := range []string{"GET", "HEAD"} {
			response := sendTest(f.handler, method, f.contentURL(artifact.ID), "", "", f.ownerKey)
			if response.Code != 404 {
				t.Fatal("deleted artifact remained readable", method, response.Code)
			}
		}
	}
	for _, artifact := range access.Artifacts {
		for _, method := range []string{"GET", "HEAD"} {
			response := sendTest(f.handler, method, artifact.Preview, "", "", "")
			if response.Code != 404 {
				t.Fatal("issued capability survived result deletion", method, response.Code)
			}
		}
	}
	if response := sendTest(f.handler, "GET", "/v1/generations/"+f.service.task.ID+"/media-access", "", "", f.ownerKey); response.Code != 404 {
		t.Fatal("deleted result minted new media access", response.Code)
	}
	response := sendTest(f.handler, "GET", "/v1/generations/"+f.service.task.ID, "", "", f.ownerKey)
	if response.Code != 200 || strings.Contains(response.Body.String(), `"artifacts"`) {
		t.Fatal("deleted result exposed artifact links", response.Code, response.Body.String())
	}
}

func TestArtifactMediaAccessUsesSignedLocalIDsAndRejectsPrivateSelectors(t *testing.T) {
	f := newArtifactBoundaryFixture(t)
	access := f.access(t)
	for i, granted := range access.Artifacts {
		artifact := f.service.task.StoredArtifacts[i]
		for _, raw := range []string{granted.Preview, granted.Download} {
			parsed, err := url.Parse(raw)
			if err != nil || parsed.Path != "/media/"+f.service.task.ID || parsed.Query().Get("artifact") != artifact.ID || granted.ID != artifact.ID {
				t.Fatal("signed access did not use the local artifact ID", raw, err)
			}
			for _, method := range []string{"GET", "HEAD"} {
				response := sendTest(f.handler, method, raw, "", "", "")
				artifactBoundaryAssertBytes(t, response, method, artifact, f.bytes[artifact.ID])
				if raw == granted.Download && !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") {
					t.Fatal("download lost attachment disposition")
				}
			}
		}
		for _, privateID := range []string{artifact.ProviderID, artifact.StorageKey, f.service.task.ProviderIdentity, uuid.NewString()} {
			parsed, _ := url.Parse(granted.Preview)
			query := parsed.Query()
			query.Set("artifact", privateID)
			parsed.RawQuery = query.Encode()
			opened := len(f.store.opened)
			response := sendTest(f.handler, "GET", parsed.String(), "", "", "")
			if response.Code != 404 || len(f.store.opened) != opened {
				t.Fatal("signed route accepted a nonlocal selector", response.Code)
			}
		}
		parsed, _ := url.Parse(granted.Preview)
		query := parsed.Query()
		query.Set("signature", "invalid")
		parsed.RawQuery = query.Encode()
		if response := sendTest(f.handler, "GET", parsed.String(), "", "", ""); response.Code != 403 {
			t.Fatal("tampered artifact capability accepted", response.Code)
		}
	}
	for _, key := range []string{"", f.foreignKey} {
		expected := 401
		if key != "" {
			expected = 404
		}
		response := sendTest(f.handler, "GET", "/v1/generations/"+f.service.task.ID+"/media-access", "", "", key)
		if response.Code != expected {
			t.Fatal("foreign or anonymous caller minted artifact capabilities", response.Code)
		}
	}
}

func TestArtifactPublicJSONRedactsPrivateProviderAndStorageData(t *testing.T) {
	f := newArtifactBoundaryFixture(t)
	paths := []string{"/v1/generations/" + f.service.task.ID, "/v1/generations", "/v1/generations/" + f.service.task.ID + "/media-access"}
	private := []string{*f.service.task.ProviderTaskID, *f.service.task.ProviderResultURL, f.service.task.ProviderIdentity, `"provider_task_id"`, `"provider_id"`, `"source_reference"`, `"storage_key"`, `"stored_artifacts"`}
	for _, artifact := range f.service.task.StoredArtifacts {
		private = append(private, artifact.ProviderID, artifact.StorageKey)
	}
	for _, path := range paths {
		response := sendTest(f.handler, "GET", path, "", "", f.ownerKey)
		if response.Code != 200 {
			t.Fatal(path, response.Code, response.Body.String())
		}
		for _, value := range private {
			if strings.Contains(response.Body.String(), value) {
				t.Errorf("public %s exposed private field/value %q", path, value)
			}
		}
		if path == paths[0] {
			var generation struct {
				Artifacts []core.GenerationArtifact `json:"artifacts"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &generation); err != nil {
				t.Fatal(err)
			}
			if len(generation.Artifacts) != 2 {
				t.Fatal("public local artifacts missing")
			}
			for i, artifact := range generation.Artifacts {
				if artifact.ID != f.service.task.StoredArtifacts[i].ID || artifact.ContentURL != f.contentURL(artifact.ID) {
					t.Fatal("public artifact URL did not use its local identifier", artifact)
				}
			}
		}
	}
}
