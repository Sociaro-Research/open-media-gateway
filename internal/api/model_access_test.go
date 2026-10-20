package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

type modelAccessMemory struct {
	modelaccess.Store
	models []modelaccess.Model
	org    map[string]map[string]bool
	fail   bool
}

func (m *modelAccessMemory) Ensure(_ context.Context, refs []modelaccess.Ref) error {
	if m.fail {
		return errors.New("unavailable")
	}
	for _, ref := range refs {
		found := false
		for _, v := range m.models {
			if v.Provider == ref.Provider && v.Model == ref.Model {
				found = true
			}
		}
		if !found {
			m.models = append(m.models, modelaccess.Model{Ref: ref, ID: uuid.NewString(), PublicID: ref.Provider + "/" + ref.Model, EnabledByDefault: true, Version: 1})
		}
	}
	return nil
}
func (m *modelAccessMemory) List(_ context.Context, org string) ([]modelaccess.Model, error) {
	if m.fail {
		return nil, errors.New("unavailable")
	}
	result := append([]modelaccess.Model{}, m.models...)
	for i := range result {
		result[i].Granted = m.org[org][result[i].ID]
	}
	return result, nil
}

func TestModelPolicyCatalogAdmissionAndOrganizationGrants(t *testing.T) {
	svc := openAIFixture()
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	s := &Server{service: svc, modelAccess: policy}
	_ = policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "mock", Model: "video"}})
	policy.models[0].PublicID = "cinema-fast"
	policy.models[0].EnabledByDefault = false
	policy.org["allowed-company"] = map[string]bool{policy.models[0].ID: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.listModels)
	mux.HandleFunc("GET /v1/info", s.info)
	mux.HandleFunc("GET /v1/providers", s.listProviders)
	mux.HandleFunc("GET /v1/models/{model...}", s.getModel)
	for _, path := range []string{"/v1/videos", "/v1/generations", "/v1/generations/estimate"} {
		mux.HandleFunc("POST "+path, s.createOpenAIGeneration)
	}
	handler := s.modelCatalogPolicy(mux)
	send := func(org, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(identity.WithOrganization(r.Context(), org, false))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, org := range []string{"", "another-company"} {
		for _, path := range []string{"/v1/models", "/v1/providers", "/v1/info"} {
			w := send(org, "GET", path, "")
			if w.Code != 200 || strings.Contains(w.Body.String(), "cinema-fast") || strings.Contains(w.Body.String(), `"id":"video"`) || strings.Contains(w.Body.String(), `"model":"video"`) {
				t.Fatal("hidden model leaked", org, w.Code, w.Body)
			}
		}
		if w := send(org, "GET", "/v1/models/cinema-fast", ""); w.Code != 404 {
			t.Fatal(w.Code, w.Body)
		}
		for _, path := range []string{"/v1/videos", "/v1/generations", "/v1/generations/estimate"} {
			w := send(org, "POST", path, `{"model":"cinema-fast","prompt":"x"}`)
			if w.Code != 400 {
				t.Fatal("hidden model admitted", w.Code, w.Body)
			}
		}
	}
	if svc.calls != 0 {
		t.Fatal("disallowed request reached admission")
	}
	if w := send("allowed-company", "GET", "/v1/models/cinema-fast", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := send("allowed-company", "POST", "/v1/videos", `{"model":"cinema-fast","prompt":"x"}`)
	if w.Code != 202 || svc.input.Model != "video" || svc.input.Provider != "mock" || svc.input.PublicModel != "cinema-fast" {
		t.Fatal("public name was not mapped to native model", w.Code, w.Body, svc.input)
	}
	if w := send("allowed-company", "POST", "/v1/videos", `{"model":"mock/video","prompt":"x"}`); w.Code != 400 {
		t.Fatal("old public name remained an alias")
	}
	delete(policy.org["allowed-company"], policy.models[0].ID)
	if w := send("allowed-company", "POST", "/v1/videos", `{"model":"cinema-fast","prompt":"x"}`); w.Code != 400 {
		t.Fatal("revoked grant was cached")
	}
	policy.models[0].EnabledByDefault = true
	if w := send("another-company", "POST", "/v1/videos", `{"model":"cinema-fast","prompt":"x"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	policy.fail = true
	for _, path := range []string{"/v1/models", "/v1/providers", "/v1/info"} {
		if w := send("allowed-company", "GET", path, ""); w.Code != 503 {
			t.Fatal("policy failure exposed unfiltered catalog", w.Code)
		}
	}
	// The source provider catalog must never be mutated by caller-specific policy.
	if svc.catalog[0].Models[1].PublicID != "" {
		t.Fatal("mutated provider adapter catalog")
	}
}

func TestModelSettingsRequireSystemAdministrator(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"root": true}}
	s.modelAccess = &modelAccessMemory{}
	h := s.handler()
	for _, path := range []string{"/v1/system/model-settings", "/v1/organizations/" + fixtureOrg + "/model-access"} {
		for _, method := range []string{"GET", "PUT"} {
			p := path
			if method == "PUT" && path == "/v1/system/model-settings" {
				p += "/" + uuid.NewString()
			}
			w := sendTest(h, method, p, `{"model_ids":[],"version":1}`, "alice", "")
			if w.Code != 403 {
				t.Fatal("ordinary organization admin may grant models", method, path, w.Code, w.Body)
			}
		}
	}
}

func TestPublicModelSnapshot(t *testing.T) {
	for _, tc := range []struct{ name, provider, native, public, want string }{
		{"seedance", "bytedance", "dreamina-seedance-2-0-260128", "bytedance/dreamina-seedance-2-0-260128", "bytedance/dreamina-seedance-2-0-260128"},
		{"custom alias", "mock", "native", "studio-image", "studio-image"},
		{"namespaced native ID", "openai", "vendor/image", "studio-image", "studio-image"},
		{"legacy task", "mock", "native", "", "mock/native"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := core.Task{Provider: tc.provider, Model: tc.native, PublicModel: tc.public, MediaType: "video", Status: core.TaskFailed}
			r := httptest.NewRequest(http.MethodGet, "/v1/generations/job", nil)
			for name, view := range map[string]any{"core": core.GenerationView(task), "HTTP": (&Server{}).generationView(r, task)} {
				t.Run(name, func(t *testing.T) {
					w := httptest.NewRecorder()
					writeJSON(w, 200, view)
					var got struct {
						Model         string `json:"model"`
						ProviderModel string `json:"provider_model"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if got.Model != tc.want || got.ProviderModel != tc.native {
						t.Fatalf("public/native model IDs changed: %s", w.Body)
					}
				})
			}
		})
	}
}
