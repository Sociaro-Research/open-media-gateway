package worker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func TestDashScopeMalformedPollPreservesAcceptedTask(t *testing.T) {
	const success = `{"output":{"task_status":"SUCCEEDED","choices":[{"message":{"content":[{"image":"https://media.example.test/result.png"}]}}]}}`
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/tasks/accepted-task" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, success)
		if calls == 1 {
			_, _ = io.WriteString(w, `{"error":`)
		}
	}))
	defer server.Close()
	provider, err := alibaba.New("synthetic-key", server.URL, "", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	providerID := "accepted-task"
	started := time.Now()
	repo := &repoStub{task: core.Task{ID: "poll", Status: core.TaskRunning, Provider: "alibaba", ProviderTaskID: &providerID, WorkerStartedAt: &started}}
	before := repo.task
	mediaStorage := &storageStub{}
	w := New(repo, nil, provider, mediaStorage, time.Second, time.Minute, 3, time.Second)
	err = w.process(context.Background(), repo.task.ID)
	var pending *pollLater
	if !errors.As(err, &pending) || pending.errors != 1 || !reflect.DeepEqual(repo.task, before) || mediaStorage.archivedURL != "" {
		t.Fatalf("malformed poll changed accepted task: task=%+v archive=%q error=%v", repo.task, mediaStorage.archivedURL, err)
	}
	// Simulate the existing scheduler persisting its poll-error count.
	repo.task.WorkerPollErrors = pending.errors
	if err := w.process(context.Background(), repo.task.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || repo.task.Status != core.TaskSucceeded || repo.task.ProviderTaskID == nil || *repo.task.ProviderTaskID != providerID || mediaStorage.archivedURL != "https://media.example.test/result.png" {
		t.Fatalf("task did not recover through the same provider ID: calls=%d task=%+v archive=%q", calls, repo.task, mediaStorage.archivedURL)
	}
}
