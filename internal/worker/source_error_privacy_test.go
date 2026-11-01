package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type sourcePrivacyProvider struct{ multiResultProvider }

const sourcePrivacySignedURL = "https://cdn.example.test/preview.mp4?signature=private-signed-value"

func (sourcePrivacyProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	return core.ProviderTask{ID: "private-native-task", ResultURL: sourcePrivacySignedURL, Artifacts: []core.ProviderArtifact{{ID: "private-native-output", MediaType: "video", Format: "mp4", URL: sourcePrivacySignedURL}}}, nil
}

type sourcePrivacyWorkerTransport struct{ calls int }

func (p *sourcePrivacyWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.calls++
	if r.URL.String() != sourcePrivacySignedURL {
		return nil, errors.New("unexpected synthetic source")
	}
	return nil, errors.New("private-transport-detail")
}

func TestSignedProviderSourceFailureRemainsPrivateInPublicGeneration(t *testing.T) {
	transport := &sourcePrivacyWorkerTransport{}
	local, err := storage.NewLocal(t.TempDir(), &http.Client{Transport: transport}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	repo := &repoStub{task: core.Task{ID: "preview-source-error", Provider: "multi", OwnerID: "accepted-owner", MediaType: "video", Status: core.TaskQueued}}
	w := New(repo, nil, sourcePrivacyProvider{}, local, time.Millisecond, time.Second, 2, time.Second)
	if err := w.process(context.Background(), repo.task.ID); err != nil {
		t.Fatal(err)
	}
	if transport.calls != 1 || repo.task.Status != core.TaskFailed || repo.task.Error == nil {
		t.Fatal("actual archive failure did not terminalize", transport.calls, repo.task.Status)
	}
	public, err := json.Marshal(core.GenerationView(repo.task))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"cdn.example.test", "signature", "private-signed-value", "private-transport-detail", "private-native-task", "private-native-output"} {
		if strings.Contains(*repo.task.Error, private) || strings.Contains(string(public), private) {
			t.Errorf("private signed source escaped through task/public error: %s", private)
		}
	}
	if !strings.Contains(string(public), "store provider result") {
		t.Fatal("public response lost useful safe failure context")
	}
}
