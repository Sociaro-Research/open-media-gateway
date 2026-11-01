package templates_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	providers "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

// Catalogue discovery success is not a completeness check for operational
// contracts. Exercise the actual descriptors consumed by authoring/admission.
func TestNativeTemplateContracts(t *testing.T) {
	for _, tc := range []struct {
		provider, id, mode, prompt, syntax string
		counts                             map[string]int
		slots                              int
	}{
		{"bytedance", "dreamina-seedance-2-5-260628", "references", "Image 1 follows Video 1 to Audio 1", "numbered", nil, 3},
		{"alibaba", "wan3.0-video", "references", "Image 1 follows Video 1 to Audio 1", "numbered", nil, 3},
		{"alibaba", "wan3.0-video-prime", "references", "Image 1 follows Video 1 to Audio 1", "numbered", nil, 3},
		{"alibaba", "wan2.7-r2v", "references", "Image 1 follows Video 1", "numbered", nil, 2},
		{"alibaba", "wan2.7-r2v-2026-06-12", "first_frame_references", "Image 1 follows Video 1", "numbered", nil, 3},
		{"alibaba", "happyhorse-1.1-r2v", "references", "[Image 1] beside [Image 2]", "bracket", nil, 2},
		{"kling", "kling-4.0-image-to-video", "keyframes", "@image_1 then @image_2 then @image_3", "kling-id", nil, 3},
		{"agnes", "agnes-video-2.5", "first_last_frame", "Animate these frames", "", nil, 2},
		{"agnes", "agnes-video-2.5", "references", "<Picture 1> follows <Video 1> with <Audio 1>", "picture", nil, 3},
		{"agnes", "agnes-video-2.5-flash", "references", "<Picture 1> follows <Audio 1>", "picture", nil, 2},
		{"openai", "gpt-image-2.5-flare", "references", "Combine image 1 with image 2, preserving their subjects", "", map[string]int{"image": 2}, 2},
		{"bytedance", "seedream-4-5-251128", "references", "Combine image 1 with image 2, preserving their subjects", "", map[string]int{"image": 2}, 2},
		{"alibaba", "wan2.7-image", "references", "Combine image 1 with image 2, preserving their subjects", "", map[string]int{"image": 2}, 2},
		{"alibaba", "qwen-image-2.1-pro", "references", "Combine image 1 with image 2, preserving their subjects", "", map[string]int{"image": 2}, 2},
	} {
		t.Run(tc.provider+"/"+tc.id+"/"+tc.mode, func(t *testing.T) {
			definition, ok := providers.Find(tc.provider)
			if !ok {
				t.Fatal(tc.provider)
			}
			var models []core.MediaModel
			if definition.Profiles != nil {
				models = definition.Profiles()
			} else {
				a, err := definition.Factory(definition.Defaults(providerkit.Credentials{APIKey: "synthetic"}), nil, &http.Client{Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				models, err = a.(interface {
					ListModels(context.Context) ([]core.MediaModel, error)
				}).ListModels(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			}
			var model core.MediaModel
			for _, m := range models {
				if m.ID == tc.id {
					model = m
				}
			}
			if model.ID == "" {
				t.Fatal("required reviewed native descriptor missing")
			}
			d := templates.Definition{Title: "Fixture", MediaType: model.MediaType, Prompt: tc.prompt, Syntax: tc.syntax, ReferenceCounts: tc.counts, Options: core.MediaOptions{ReferenceMode: tc.mode}}
			var err error
			d.Slots, err = d.InferSlots(model)
			if tc.mode != "references" {
				if err == nil {
					t.Fatal("frame workflow allowed in Templates")
				}
				// The same native mode remains valid for ordinary generation.
				options := d.Options
				count := 2
				if tc.mode == "keyframes" {
					count = 3
				}
				for i := 0; i < count; i++ {
					options.ImageURLs = append(options.ImageURLs, "https://fixture.invalid/image")
				}
				normalized, ordinaryErr := core.NormalizeModelOptions(model, options)
				if ordinaryErr != nil || core.ValidateReferenceMode(model, normalized) != nil {
					t.Fatal("ordinary native workflow changed", ordinaryErr)
				}
				return
			}
			if err != nil || len(d.Slots) != tc.slots {
				t.Fatalf("slots=%+v err=%v", d.Slots, err)
			}
			if err = d.Validate(model); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "keyframes" {
				for i, role := range []string{"first_frame", "middle_frame", "last_frame"} {
					if d.Slots[i].Role != role {
						t.Fatal(d.Slots)
					}
				}
			}
			if tc.id == "wan2.7-r2v" {
				imageOnly := d
				imageOnly.Prompt, imageOnly.Options.Duration = "Image 1", 15
				imageOnly.Slots, err = imageOnly.InferSlots(model)
				if err != nil || imageOnly.Compatible(model) != nil {
					t.Fatal("documented image-only duration rejected", err)
				}
				imageOnly.Prompt = "Image 1 follows Video 1"
				imageOnly.Slots, err = imageOnly.InferSlots(model)
				if err != nil {
					t.Fatal(err)
				}
				if imageOnly.Compatible(model) == nil {
					t.Fatal("video-dependent output duration cap bypassed")
				}
				d.Prompt += " Audio 1"
				if _, err := d.InferSlots(model); err == nil {
					t.Fatal("per-asset voice was treated as a free audio namespace")
				}
			}
			if tc.id == "wan3.0-video" {
				for _, file := range []templates.InputMedia{
					{Type: "image", Info: mediafile.Info{ContentType: "image/png", Size: 100, Width: 640, Height: 480, AlphaChannel: true}},
					{Type: "video", Info: mediafile.Info{ContentType: "video/mp4", Size: 100, Width: 640, Height: 480, Duration: 16, FPS: 24}},
					{Type: "audio", Info: mediafile.Info{ContentType: "audio/mpeg", Size: 100, Duration: 16}},
				} {
					if templates.ValidateFiles(model, []templates.InputMedia{file}, true) == nil {
						t.Fatal("known provider constraint ignored", file)
					}
				}
			}
			if tc.id == "happyhorse-1.1-r2v" {
				if model.ReferenceMediaLimits == nil || model.ReferenceMediaLimits.Image.MinDimension != 400 {
					t.Fatal("reviewed file constraints lost")
				}
			}
			if tc.id == "dreamina-seedance-2-5-260628" {
				if model.ReferenceMediaLimits == nil || model.ReferenceMediaLimits.Video.MaxTotalDuration != 30 {
					t.Fatal("2.5 inherited 2.0 limits")
				}
			}
		})
	}
}

func TestDeclaredInputsCannotOverrideMarkerBindings(t *testing.T) {
	m := core.MediaModel{MediaType: "image", SupportsImageInput: true, MaxImageInputs: 2, ReferenceModes: []core.ReferenceMode{{ID: "references", MaxImages: 2}}}
	d := templates.Definition{Title: "Fixture", MediaType: "image", Prompt: "Use supplied images", ReferenceCounts: map[string]int{"image": 3}, Options: core.MediaOptions{ReferenceMode: "references"}}
	if _, err := d.InferSlots(m); err == nil {
		t.Fatal("declared input count bypassed native maximum")
	}
	d.ReferenceCounts = map[string]int{"image": 2}
	d.Slots, _ = d.InferSlots(m)
	d.Slots[1].Role = "first_frame"
	if d.Validate(m) == nil {
		t.Fatal("invented role accepted")
	}
	m.ReferencePromptSyntax = "numbered"
	d.Syntax = "numbered"
	d.Prompt = "Image 1"
	if _, err := d.InferSlots(m); err == nil {
		t.Fatal("declared counts overrode prompt bindings")
	}
}
