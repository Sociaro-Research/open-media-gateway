package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestAuthenticatedTaskResponsesIncludePrompt(t *testing.T) {
	task := core.Task{ID: "task-1", Prompt: "A mountain lake", OwnerID: "private-owner"}
	handler := New(serviceStub{task: task}, healthStub{}, "secret")
	for _, endpoint := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/generations", ""},
		{http.MethodGet, "/v1/generations/task-1", ""},
		{http.MethodPost, "/v1/generations", `{"prompt":"A mountain lake","model":"mock/mock-image-v1"}`},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), task.Prompt) {
				t.Fatalf("unauthenticated task response: %d %s", response.Code, response.Body.String())
			}
			request = httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
			request.Header.Set("Authorization", "Bearer "+"secret")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), `"prompt":"A mountain lake"`) || strings.Contains(response.Body.String(), task.OwnerID) {
				t.Fatalf("unexpected task response: %s", response.Body.String())
			}
		})
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), task.Prompt) {
		t.Fatal("prompt must remain excluded from core/webhook serialization")
	}
}
