package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/bytedance"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticService struct{ task core.Task }

func (s diagnosticService) ValidateMediaRequest(context.Context, core.CreateMediaInput) error {
	return nil
}
func (s diagnosticService) CreateMediaRequest(context.Context, core.CreateMediaInput, string) (core.Task, error) {
	return s.task, nil
}
func (s diagnosticService) GetTask(context.Context, string) (core.Task, error) { return s.task, nil }
func (s diagnosticService) ListTasks(context.Context, int) ([]core.Task, error) {
	return []core.Task{s.task}, nil
}
func (s diagnosticService) ListProviders(context.Context) []core.ProviderDescriptor { return nil }
func (s diagnosticService) Info() core.ServiceInfo                                  { return core.ServiceInfo{} }

type diagnosticHealth struct{}

func (diagnosticHealth) Check(context.Context) error { return nil }

func publicDiagnostic(t *testing.T, task core.Task) string {
	t.Helper()
	h := api.New(diagnosticService{task}, diagnosticHealth{}, "fixture")
	for _, path := range []string{"/v1/generations/" + task.ID, "/v1/generations"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("public read status=%d", w.Code)
		}
		for _, secret := range []string{"private-body", "private-code", "private.invalid", "private-token", "private-native-task", "fixture-key"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("private diagnostic escaped in %s", path)
			}
		}
		if !strings.Contains(w.Body.String(), "\"generation_failed\"") {
			t.Fatal("public failure code changed")
		}
	}
	view := core.GenerationView(task)
	// Webhook serialization uses this same projection; PostgreSQL delivery is
	// exercised by its separate integration suite.
	payload, err := core.CustomerJSON(map[string]any{"data": view})
	if err != nil || strings.Contains(string(payload), "private-body") {
		t.Fatal("unsafe webhook projection")
	}
	return view.Error.(map[string]string)["message"]
}

func TestProviderFailureDiagnosticsSurviveWorkerAndPublicReads(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 503} {
		for _, queued := range []bool{false, true} {
			calls := 0
			client := &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("{\"code\":\"private-code\",\"message\":\"private-body https://private.invalid/?token=private-token\"}")), Request: r}, nil
			})}
			bd, _ := bytedance.New("fixture-key", "https://fixture.invalid", client)
			al, _ := alibaba.New("fixture-key", "https://fixture.invalid", "", "", client)
			for _, p := range []core.MediaProvider{bd, al} {
				calls = 0
				now := time.Now()
				id := "private-native-task"
				model := "seedance-model"
				if p.Name() == "alibaba" {
					model = "qwen-image-3.0"
				}
				task := core.Task{ID: "11111111-1111-4111-8111-111111111111", Provider: p.Name(), Model: model, Prompt: "fixture", Status: core.TaskRunning, ProviderTaskID: &id, WorkerStartedAt: &now}
				if queued {
					task.Status = core.TaskQueued
					task.ProviderTaskID = nil
				}
				repo := &repoStub{task: task}
				w := New(repo, nil, p, nil, time.Second, time.Minute, 1, time.Second)
				if err := w.process(context.Background(), task.ID); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || repo.task.Status != core.TaskFailed || repo.task.Error == nil {
					t.Fatal("unexpected provider submission/poll count or outcome", calls)
				}
				message := publicDiagnostic(t, repo.task)
				action := map[int]string{400: "inputs", 401: "API key", 403: "permissions", 429: "frequency", 503: "provider status"}[status]
				if !strings.Contains(message, action) {
					t.Fatal("safe action lost", message)
				}
				if queued && !strings.Contains(message, "before submitting again") {
					t.Fatal("uncertain acceptance guidance lost")
				}
			}
		}
	}
}

func TestNativeTaskMessagesRemainPrivateThroughPublicProjection(t *testing.T) {
	for _, pname := range []string{"bytedance", "alibaba"} {
		calls := 0
		client := &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			body := "{\"status\":\"failed\",\"error\":{\"code\":\"AuthenticationError\",\"message\":\"private-body\"}}"
			if pname == "alibaba" {
				body = "{\"output\":{\"task_status\":\"FAILED\",\"code\":\"InvalidApiKey\",\"message\":\"private-body\"}}"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
		var p core.MediaProvider
		if pname == "bytedance" {
			p, _ = bytedance.New("fixture-key", "https://fixture.invalid", client)
		} else {
			p, _ = alibaba.New("fixture-key", "https://fixture.invalid", "", "", client)
		}
		now := time.Now()
		id := "private-native-task"
		repo := &repoStub{task: core.Task{ID: "11111111-1111-4111-8111-111111111111", Provider: pname, Status: core.TaskRunning, ProviderTaskID: &id, WorkerStartedAt: &now}}
		w := New(repo, nil, p, nil, time.Second, time.Minute, 1, time.Second)
		if err := w.process(context.Background(), repo.task.ID); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatal("native task was retried")
		}
		if !strings.Contains(publicDiagnostic(t, repo.task), "API key") {
			t.Fatal("native safe action lost")
		}
	}
}

// Assert the public error schema stays unchanged even though the internal
// classification is richer.
func TestPublicDiagnosticEnvelopeStaysCompatible(t *testing.T) {
	message := "safe failure"
	data, _ := json.Marshal(core.GenerationView(core.Task{Error: &message}))
	var v struct{ Error map[string]string }
	if json.Unmarshal(data, &v) != nil || len(v.Error) != 2 || v.Error["code"] != "generation_failed" || v.Error["message"] != message {
		t.Fatal("generation error contract changed")
	}
}
