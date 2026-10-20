package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/openai"
)

type openAITransport func(*http.Request) (*http.Response, error)

func (f openAITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAIImmediateResultsArchiveAndDownload(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-flare", "tts-1", "gpt-audio-1.5"} {
		t.Run(model, func(t *testing.T) {
			calls := 0
			adapter, err := openai.New("fixture", openai.DefaultBaseURL, &http.Client{Transport: openAITransport(func(r *http.Request) (*http.Response, error) {
				calls++
				kind, body := "application/json", `{"data":[{"b64_json":"aW1hZ2U="}]}`
				if model == "tts-1" {
					kind, body = "audio/mpeg", "audio"
				}
				if model == "gpt-audio-1.5" {
					body = `{"choices":[{"finish_reason":"stop","message":{"audio":{"data":"YXVkaW8="}}}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			dest, err := storage.NewLocal(t.TempDir(), &http.Client{}, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			repo := &repoStub{task: core.Task{ID: "openai-task", Provider: "openai", Model: model, Prompt: "hello", Status: core.TaskQueued}}
			w := New(repo, nil, adapter, dest, time.Millisecond, time.Minute, 1, time.Second)
			if err = w.process(context.Background(), repo.task.ID); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || repo.task.Status != core.TaskSucceeded || repo.task.StorageKey == nil {
				t.Fatal(repo.task, calls)
			}
			file, err := dest.Open(context.Background(), *repo.task.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Reader.Close()
			data, err := io.ReadAll(file.Reader)
			if err != nil || len(data) == 0 {
				t.Fatal(err)
			}
			expected := "audio"
			if model == "gpt-image-2.5-flare" {
				expected = "image"
			}
			if string(data) != expected {
				t.Fatal(string(data))
			}
		})
	}
}
