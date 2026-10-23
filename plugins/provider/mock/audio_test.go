package mock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestAudioSample(t *testing.T) {
	if len(audioSample) != 24076 {
		t.Fatalf("sample size = %d", len(audioSample))
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(audioSample)); got != "ca109c0ee4b9fc90f5701ee0e075af338d5c461e8e2f58144e6e3d41a55c3b90" {
		t.Fatalf("sample changed: %s", got)
	}
	if got := http.DetectContentType(audioSample); got != "audio/mpeg" {
		t.Fatalf("sample MIME type = %s", got)
	}
}

func TestAudioResultsAndStorage(t *testing.T) {
	ctx := context.Background()
	for _, base := range []string{"", "embedded"} {
		for _, kind := range []string{"speech", "music"} {
			t.Run(base+"/"+kind, func(t *testing.T) {
				adapter := New(0, base)
				models, err := adapter.ListModels(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var model core.MediaModel
				for _, candidate := range models {
					if candidate.MediaType == kind {
						model = candidate
					}
				}
				if model.ID != "mock-"+kind+"-v1" || len(model.Formats) != 1 || model.Formats[0] != "mp3" || !model.Pricing.Known {
					t.Fatalf("unexpected audio descriptor: %+v", model)
				}
				task, err := adapter.Generate(ctx, core.MediaRequest{TaskID: "audio-task", Model: model.ID, Options: core.MediaOptions{Format: "mp3"}})
				if err != nil {
					t.Fatal(err)
				}
				result, err := adapter.GetTask(ctx, task.ID)
				if err != nil || result.Status != core.ProviderSucceeded {
					t.Fatalf("result: %+v, %v", result, err)
				}
				payload, found := strings.CutPrefix(result.ResultURL, "data:audio/mpeg;base64,")
				if !found {
					t.Fatal("mock audio must declare MP3 MIME type")
				}
				decoded, err := base64.StdEncoding.DecodeString(payload)
				if err != nil || !bytes.Equal(decoded, audioSample) {
					t.Fatalf("result changed the supplied sample: %v", err)
				}
				repeated, err := adapter.GetTask(ctx, task.ID)
				if err != nil || repeated.ResultURL != result.ResultURL {
					t.Fatal("audio result must be deterministic")
				}
				store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				media, err := store.Archive(ctx, "audio-task", result.ResultURL)
				if err != nil {
					t.Fatal(err)
				}
				if media.ContentType != "audio/mpeg" || media.SizeBytes != int64(len(audioSample)) || media.StorageKey != "audio-task/audio.mp3" {
					t.Fatalf("archived audio: %+v", media)
				}
				object, err := store.Open(ctx, media.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				defer object.Reader.Close()
				stored, err := io.ReadAll(object.Reader)
				if err != nil || !bytes.Equal(stored, audioSample) || object.ContentType != "audio/mpeg" {
					t.Fatalf("stored sample changed: %v", err)
				}
			})
		}
	}
}

func TestResultFixturesKeepMediaTypes(t *testing.T) {
	ctx := context.Background()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio-task.mp3" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audioSample)
	}))
	defer fixture.Close()
	for _, tc := range []struct{ kind, data, extension string }{
		{"speech", embeddedAudioDataURL, ".mp3"},
		{"music", embeddedAudioDataURL, ".mp3"},
		{"image", embeddedImageDataURL, ".png"},
		{"video", embeddedVideoDataURL, ".mp4"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			for _, base := range []string{"embedded", fixture.URL + "/"} {
				adapter := New(0, base)
				task, err := adapter.Generate(ctx, core.MediaRequest{TaskID: "audio-task", Model: "mock-" + tc.kind + "-v1"})
				if err != nil {
					t.Fatal(err)
				}
				result, err := adapter.GetTask(ctx, task.ID)
				want := tc.data
				if base != "embedded" {
					want = fixture.URL + "/audio-task" + tc.extension
				}
				if err != nil || result.ResultURL != want {
					t.Fatalf("%s result URL did not match media format: %v", base, err)
				}
				if base != "embedded" && (tc.kind == "speech" || tc.kind == "music") {
					store, err := storage.NewLocal(t.TempDir(), fixture.Client(), 1<<20)
					if err != nil {
						t.Fatal(err)
					}
					media, err := store.Archive(ctx, "fixture-task", result.ResultURL)
					if err != nil || media.ContentType != "audio/mpeg" || media.SizeBytes != int64(len(audioSample)) {
						t.Fatalf("external MP3 fixture download: %+v, %v", media, err)
					}
				}
			}
		})
	}
}
