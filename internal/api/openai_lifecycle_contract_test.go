package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestLifecycleSchemaDeclaresLocationAndFailureShape(t *testing.T) {
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/generations", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations"} {
		response := imageContractValue(spec, "paths", path, "post", "responses", "202")
		location := imageContractValue(response, "headers", "Location", "schema")
		if imageContractValue(location, "type") != "string" || imageContractValue(location, "format") != "uri-reference" {
			t.Error(path, "must declare its relative Location status route")
		}
		if imageContractValue(response, "content", "application/json", "schema", "$ref") != "#/components/schemas/Generation" {
			t.Error(path, "must retain the Generation response")
		}
	}
	for _, path := range []string{"/v1/generations/{id}/content", "/v1/videos/{id}/content"} {
		for _, method := range []string{"get", "head"} {
			responses := imageContractValue(spec, "paths", path, method, "responses")
			if imageContractValue(responses, "404") == nil || imageContractValue(responses, "409", "description") == "Not ready" {
				t.Error(path, method, "unavailable content returns 404, not a 409 readiness response")
			}
		}
	}
	failure := imageContractValue(spec, "components", "schemas", "Generation", "properties", "error")
	if !imageContractRequires(failure, "code") || !imageContractRequires(failure, "message") || imageContractValue(failure, "additionalProperties") != false || imageContractValue(failure, "properties", "code", "const") != "generation_failed" {
		t.Error("non-null generation errors must describe exactly the safe code/message envelope")
	}
	types, _ := imageContractValue(failure, "type").([]any)
	if len(types) != 2 || types[0] != "object" || types[1] != "null" {
		t.Error("error must remain nullable even for tasks without a recorded failure")
	}
}

func TestLifecycleAdmissionStatusLinksAndArchivalTime(t *testing.T) {
	stored, later := time.Unix(200, 0).UTC(), time.Unix(900, 0).UTC()
	for _, media := range []string{"video", "image"} {
		for _, status := range []core.TaskStatus{core.TaskQueued, core.TaskRunning, core.TaskSucceeded, core.TaskFailed} {
			for _, deleted := range []bool{false, true} {
				t.Run(media+"/"+string(status)+"/deleted="+map[bool]string{false: "no", true: "yes"}[deleted], func(t *testing.T) {
					task := core.Task{ID: "job", Provider: "mock", Model: "mock-" + media + "-v1", MediaType: media, Status: status, CreatedAt: time.Unix(100, 0), UpdatedAt: later, StoredAt: &stored}
					if deleted {
						task.ResultDeletedAt = &later
					}
					h := New(serviceStub{task: task}, healthStub{}, "secret")
					out := openAIRequest(h, "/v1/generations", `{"model":"mock/`+task.Model+`","prompt":"fixture"}`)
					var view map[string]any
					if json.Unmarshal(out.Body.Bytes(), &view) != nil || out.Code != 202 {
						t.Fatal(out.Code, out.Body)
					}
					wantStatus := map[core.TaskStatus]string{core.TaskQueued: "queued", core.TaskRunning: "in_progress", core.TaskSucceeded: "completed", core.TaskFailed: "failed"}[status]
					base := "/v1/generations/job"
					if media == "video" {
						base = "/v1/videos/job"
					}
					parsed, err := url.Parse(out.Header().Get("Location"))
					if err != nil || parsed.IsAbs() || out.Header().Get("Location") != base || view["status_url"] != base || view["content_url"] != base+"/content" || view["status"] != wantStatus {
						t.Fatal("acceptance must retain returned task status and derived links", out.Header(), view)
					}
					var completed any
					if status == core.TaskSucceeded {
						completed = float64(200)
					}
					if view["created_at"] != float64(100) || view["updated_at"] != float64(900) || view["completed_at"] != completed || view["error"] != nil {
						t.Fatal("timestamps or nullable error changed", view)
					}
					polled := sendTest(h, "GET", base, "", "", "secret")
					if polled.Code != 200 || polled.Body.String() != out.Body.String() {
						t.Fatal("status response differs from the same accepted task", polled.Code, polled.Body)
					}
				})
			}
		}
	}
	view := core.GenerationView(core.Task{Status: core.TaskSucceeded, UpdatedAt: later})
	if view.CompletedAt != nil {
		t.Fatal("unknown archival time must not use a later update time")
	}
}

func TestLifecycleHTTPViewAndWebhookBaseDiffer(t *testing.T) {
	stored := time.Unix(200, 0).UTC()
	private := "private-provider-body https://private.invalid/?token=secret"
	task := core.Task{ID: "job", OwnerID: "company", OrganizationID: "company", ActorID: "author", MediaType: "image", Status: core.TaskSucceeded, StoredAt: &stored, Error: &private, ProjectID: "private-project"}
	base, err := json.Marshal(core.GenerationView(task))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	_ = json.Unmarshal(base, &snapshot)
	failure, ok := snapshot["error"].(map[string]any)
	if !ok || len(failure) != 2 || failure["code"] != "generation_failed" || strings.Contains(string(base), "private-provider-body") {
		t.Fatal("unsafe generation failure envelope", string(base))
	}
	if _, ok := snapshot["content_access"]; ok {
		t.Fatal("webhook core snapshot gained HTTP-only permissions")
	}
	for _, role := range []string{org.Manager, org.ContentOfficer, org.MemberRole} {
		r := httptest.NewRequest("GET", "/v1/generations/job", nil)
		r = r.WithContext(identity.WithOrganizationRole(r.Context(), "company", role))
		raw, err := json.Marshal((&Server{}).generationView(r, task))
		if err != nil {
			t.Fatal(err)
		}
		var viewer map[string]any
		_ = json.Unmarshal(raw, &viewer)
		if viewer["id"] != snapshot["id"] || viewer["status"] != snapshot["status"] || viewer["status_url"] != snapshot["status_url"] {
			t.Fatal("shared lifecycle fields changed", viewer)
		}
		if role == org.Manager {
			if viewer["content_access"] != false {
				t.Fatal("restricted view gained content", viewer)
			}
			for _, field := range []string{"content_url", "completed_at", "error", "options"} {
				if _, ok := viewer[field]; ok {
					t.Error("restricted view must omit", field)
				}
			}
		} else if viewer["content_access"] != true || viewer["content_url"] != snapshot["content_url"] || viewer["completed_at"] != snapshot["completed_at"] {
			t.Fatal("content-authorized lifecycle view changed", viewer)
		}
	}
}

type lifecycleUnavailableStore struct {
	storage.Store
	opens int
}

func (s *lifecycleUnavailableStore) Open(context.Context, string) (storage.Object, error) {
	s.opens++
	return storage.Object{}, storage.ErrObjectNotFound
}

func TestLifecycleContentLinkDoesNotProveAvailability(t *testing.T) {
	key, deleted := "synthetic-archive", time.Unix(900, 0)
	for _, tc := range []struct {
		name   string
		status core.TaskStatus
		key    *string
		gone   *time.Time
		opens  int
	}{
		{"queued", core.TaskQueued, &key, nil, 0},
		{"running", core.TaskRunning, &key, nil, 0},
		{"failed", core.TaskFailed, &key, nil, 0},
		{"no_archive", core.TaskSucceeded, nil, nil, 0},
		{"deleted", core.TaskSucceeded, &key, &deleted, 0},
		{"missing_file", core.TaskSucceeded, &key, nil, 1},
	} {
		for _, media := range []string{"image", "video"} {
			for _, method := range []string{"GET", "HEAD"} {
				t.Run(tc.name+"/"+media+"/"+method, func(t *testing.T) {
					task := core.Task{ID: "job", MediaType: media, Status: tc.status, StorageKey: tc.key, ResultDeletedAt: tc.gone}
					store := &lifecycleUnavailableStore{}
					s := &Server{service: serviceStub{task: task}, storage: store}
					r := httptest.NewRequest(method, core.GenerationView(task).ContentURL, nil)
					r.SetPathValue("id", "job")
					out := httptest.NewRecorder()
					s.generationContent(out, r)
					if out.Code != 404 || store.opens != tc.opens || core.GenerationView(task).ContentURL == "" {
						t.Fatal("unavailable archive must not be inferred from content_url", out.Code, store.opens, out.Body)
					}
				})
			}
		}
	}
}
