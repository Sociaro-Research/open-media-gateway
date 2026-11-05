package worker

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type archiveDiagnosticProvider struct{}

func (archiveDiagnosticProvider) Name() string { return "archive-fixture" }
func (archiveDiagnosticProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	return core.ProviderTask{}, nil
}
func (archiveDiagnosticProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://fixture.invalid/result"}, nil
}

func TestFilesystemArchiveFailureRemainsPrivateThroughPublicReads(t *testing.T) {
	client := &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: 3, Body: io.NopCloser(strings.NewReader("png")), Request: r}, nil
	})}
	root := t.TempDir()
	store, err := storage.NewLocal(root, client, 1024)
	if err != nil {
		t.Fatal(err)
	}
	taskID := "11111111-1111-4111-8111-111111111111"
	if err = os.WriteFile(filepath.Join(root, taskID), []byte("obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id := "private-native-task"
	repo := &repoStub{task: core.Task{ID: taskID, Provider: "archive-fixture", Status: core.TaskRunning, ProviderTaskID: &id, WorkerStartedAt: &now}}
	w := New(repo, nil, archiveDiagnosticProvider{}, store, time.Second, time.Minute, 1, time.Second)
	if err = w.process(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	if repo.task.Error == nil || strings.Contains(*repo.task.Error, root) {
		t.Fatal("filesystem path exposed")
	}
	message := publicDiagnostic(t, repo.task)
	if strings.Contains(message, root) || !strings.Contains(message, "check result storage") || !strings.Contains(message, "original task before creating another paid generation") {
		t.Fatal("unsafe/unhelpful archive diagnostic")
	}
}
