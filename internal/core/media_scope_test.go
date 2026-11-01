package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type mediaScopeProvider struct {
	catalogProvider
	preparations []MediaModel
}

func (p mediaScopeProvider) PreparationModels(context.Context) ([]MediaModel, error) {
	return p.preparations, nil
}

func TestMediaOnlyScopeDoesNotConfuseArtifactsWithOperationPurpose(t *testing.T) {
	for _, media := range []string{"image", "video", "audio", "speech", "music"} {
		if !MediaGenerationModel(MediaModel{MediaType: media}) {
			t.Fatal(media)
		}
	}
	for _, media := range []string{"text", "json", "resource", "binary", "", "Video"} {
		if MediaGenerationModel(MediaModel{MediaType: media}) {
			t.Fatal(media)
		}
	}
	for _, m := range []MediaModel{{MediaType: "video", ResourceOperation: &ResourceOperationSchema{Kind: "editing_session"}}, {MediaType: "video", SessionOperation: &SessionOperationSchema{Action: "clear", Mutates: true}}, {MediaType: "video", SessionOperation: &SessionOperationSchema{Action: "add"}}} {
		if MediaGenerationModel(m) {
			t.Fatal("utility admitted", m)
		}
	}
	if !MediaGenerationModel(MediaModel{MediaType: "video", SessionOperation: &SessionOperationSchema{Action: "preview"}}) {
		t.Fatal("media preview removed")
	}
}
func TestMediaOnlyAdmissionAndEstimateRejectRelabelledUtilities(t *testing.T) {
	retired := []string{"kling-image-recognition", "kling-lip-sync-face-detection", "kling-create-element", "kling-create-voice", "kling-selection-init", "kling-selection-add", "kling-selection-delete", "kling-selection-clear", "MiniMax-H3-Context-IR", "MiniMax-Upload-File", "MiniMax-Bind-Output-File", "MiniMax-Bind-H3-Source"}
	for _, id := range retired {
		t.Run(id, func(t *testing.T) {
			repo := &memoryRepository{}
			s := NewTaskService(repo, &memoryQueue{}, "fixture", id)
			s.providers["fixture"] = catalogProvider{models: []MediaModel{{ID: id, MediaType: "json", NoPrompt: true}, {ID: "video", MediaType: "video"}}}
			for _, media := range []string{"json", "video", ""} {
				input := CreateMediaInput{Provider: "fixture", Model: id, MediaType: media, Prompt: "test"}
				if _, err := s.CreateMediaRequest(context.Background(), input, "key"); !errors.Is(err, ErrModelUnavailable) {
					t.Fatalf("submission: %v", err)
				}
				if _, err := s.Estimate(context.Background(), input); !errors.Is(err, ErrModelUnavailable) {
					t.Fatalf("estimate: %v", err)
				}
			}
			if repo.createCount != 0 {
				t.Fatal("retired utility persisted")
			}
			listed := s.ListProviders(context.Background())
			if len(listed) != 1 || len(listed[0].Models) != 1 || listed[0].Models[0].ID != "video" {
				t.Fatal("utility remains in public catalog")
			}
		})
	}
}
func TestPreparationScopeRequiresExactConsumerHelperProviderAndSubtype(t *testing.T) {
	media := MediaModel{ID: "movie", MediaType: "video", ResourceInputSchemas: []ResourceInputSchema{{Kind: "file_resource", MaxCount: 1, Subtypes: []string{"video"}}}}
	prep := MediaModel{ID: "upload", MediaType: "resource", ResourceOperation: &ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"image", "video"}}, ResourceOutputs: []ResourceOutputSchema{{Kind: "file_resource"}}}
	s := NewTaskService(&memoryRepository{}, &memoryQueue{}, "fixture", "movie")
	s.providers["fixture"] = mediaScopeProvider{catalogProvider: catalogProvider{models: []MediaModel{media}}, preparations: []MediaModel{prep, {ID: "context", MediaType: "text"}}}
	for _, tc := range []struct {
		provider, consumer, operation, subtype string
		want                                   bool
	}{{"fixture", "movie", "upload", "video", true}, {"fixture", "movie", "upload", "image", false}, {"other", "movie", "upload", "video", false}, {"fixture", "missing", "upload", "video", false}, {"fixture", "movie", "context", "", false}, {"fixture", "upload", "upload", "video", false}} {
		ctx := WithMediaPreparationWorkflow(context.Background(), tc.provider, tc.consumer, tc.operation, tc.subtype)
		if _, ok := s.findModel(ctx, "fixture", tc.operation); ok != tc.want {
			t.Fatalf("%+v: got %v", tc, ok)
		}
	}
	if _, ok := s.findModel(context.Background(), "fixture", "upload"); ok {
		t.Fatal("unscoped internal helper executable")
	}
}

func TestPreparationGenerationDoesNotExportInspectedInputURLs(t *testing.T) {
	for _, mediaType := range []string{"json", "resource", "text"} {
		original := Task{ID: "prepared-input", MediaType: mediaType, Options: MediaOptions{ImageURLs: []string{"https://private.invalid/image"}, VideoURLs: []string{"https://private.invalid/video"}, AudioURLs: []string{"https://private.invalid/audio"}}}
		data, err := json.Marshal(GenerationView(original))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private.invalid") {
			t.Fatal("private preparation input leaked")
		}
		if len(original.Options.VideoURLs) != 1 || len(original.Options.ImageURLs) != 1 || len(original.Options.AudioURLs) != 1 {
			t.Fatal("dispatch inputs changed")
		}
	}
}
