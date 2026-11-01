package providerassets

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"net/http"
	"slices"
	"testing"
)

type mediaScopeNoNetwork struct{ t *testing.T }

func (p mediaScopeNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	p.t.Error("media catalog inspection attempted network")
	return nil, errors.New("network forbidden")
}
func TestEveryBuiltInExposesOnlyReviewedMediaGeneration(t *testing.T) {
	if len(All()) != 8 {
		t.Fatal("update scope coverage for registry change")
	}
	for _, d := range All() {
		t.Run(d.ID, func(t *testing.T) {
			credentials := providerkit.Credentials{APIKey: "offline-fixture", MusicAPIKey: "offline-fixture"}
			credentials = d.Defaults(credentials)
			a, err := d.Factory(credentials, nil, &http.Client{Transport: mediaScopeNoNetwork{t}})
			if err != nil {
				t.Fatal(err)
			}
			var models []core.MediaModel
			if reviewed, ok := a.(interface{ ReviewedCatalogModels() []core.MediaModel }); ok {
				models = reviewed.ReviewedCatalogModels()
			} else if d.Profiles != nil {
				models = d.Profiles()
			} else {
				models, err = a.(core.ModelCatalog).ListModels(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(models) == 0 {
				t.Fatal("no retained media models")
			}
			for _, m := range models {
				if !core.MediaGenerationModel(m) {
					t.Fatalf("nonmedia public model %s (%s)", m.ID, m.MediaType)
				}
			}
			if d.Profiles != nil {
				for _, m := range d.Profiles() {
					if !core.MediaGenerationModel(m) {
						t.Fatal("nonmedia manual profile", m.ID)
					}
				}
			}
		})
	}
}

func TestMiniMaxExactPublicMediaSetAndRetiredAdmission(t *testing.T) {
	a, err := minimax.New("offline-fixture", "", &http.Client{Transport: mediaScopeNoNetwork{t}})
	if err != nil {
		t.Fatal(err)
	}
	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02", "T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01", "image-01", "image-01-live", minimax.Regeneration, minimax.RegenerationVideo}
	for _, id := range []string{"392747428568649728", "393769180141805569", "397087679467597833", "393881433990066176", "393498001241890824", "393488336655310850", "394514820878671878", "393879757702918151", "397017167949312007", "394176968202485769", "393876118804459526", "393866076583718914", "394875727173492739", "394220989629177861", "393853165953970178", "401431836934868999"} {
		expected = append(expected, "video-agent-"+id)
	}
	actual := []string{}
	for _, m := range models {
		if !core.MediaGenerationModel(m) {
			t.Fatal("nonmedia descriptor", m.ID)
		}
		actual = append(actual, m.ID)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("media identities changed: actual=%v expected=%v", actual, expected)
	}
	service := core.NewTaskServiceWithProviders(nil, nil, map[string]core.MediaProvider{"minimax": a}, map[string]string{"minimax": "MiniMax-H3"}, "minimax")
	for _, id := range []string{minimax.ContextIR, minimax.ResourceUpload, minimax.ResourceOutputFile, minimax.ResourceSourceTask} {
		for _, media := range []string{"video", "text", "resource", ""} {
			input := core.CreateMediaInput{Provider: "minimax", Model: id, MediaType: media, Prompt: "fixture"}
			if _, err := service.CreateMediaRequest(context.Background(), input, "retired"); !errors.Is(err, core.ErrModelUnavailable) {
				t.Fatalf("%s submission: %v", id, err)
			}
			if _, err := service.Estimate(context.Background(), input); !errors.Is(err, core.ErrModelUnavailable) {
				t.Fatalf("%s estimate: %v", id, err)
			}
		}
	}
	for _, tc := range []struct{ consumer, helper, subtype string }{{"MiniMax-H3", minimax.ResourceUpload, "image"}, {"MiniMax-H3", minimax.ResourceUpload, "video"}, {"MiniMax-H3", minimax.ResourceUpload, "audio"}, {"MiniMax-H3", minimax.ResourceOutputFile, "video"}, {minimax.Regeneration, minimax.ResourceSourceTask, "regeneration_source"}} {
		helpers, err := service.PreparationModelsForWorkflow(context.Background(), "minimax", tc.consumer)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, helper := range helpers {
			if helper.ID == tc.helper {
				for _, consumer := range models {
					if consumer.ID == tc.consumer && core.PreparationSupportsSubtype(consumer, helper, tc.subtype) {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("required preparation removed: %+v", tc)
		}
	}
}
func TestKlingRetiredUtilitiesAndInternalMediaDependencies(t *testing.T) {
	a, err := kling.New("offline-fixture", "", &http.Client{Transport: mediaScopeNoNetwork{t}})
	if err != nil {
		t.Fatal(err)
	}
	models, _ := a.ListModels(context.Background())
	public := map[string]bool{}
	for _, m := range models {
		public[m.ID] = true
	}
	for _, id := range []string{kling.ImageRecognition, kling.FaceDetection, kling.ResourceCreateElement, kling.ResourceCreateVoice, kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear} {
		if public[id] {
			t.Fatal("standalone utility retained", id)
		}
	}
	for _, id := range []string{kling.LipSync, kling.MultiElementsEditing, kling.SessionPreview, kling.TextToSpeech, kling.AvatarImageToVideo, kling.MotionControl3, kling.OmniVideo4} {
		if !public[id] {
			t.Fatal("legitimate media removed", id)
		}
	}
	service := core.NewTaskServiceWithProviders(nil, nil, map[string]core.MediaProvider{"kling": a}, map[string]string{"kling": kling.TextToVideo4}, "kling")
	for _, tc := range []struct {
		consumer string
		helpers  []string
	}{{kling.LipSync, []string{kling.FaceDetection}}, {kling.MultiElementsEditing, []string{kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear}}, {kling.SessionPreview, []string{kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear}}, {kling.TextToSpeech, []string{kling.ResourceCreateVoice}}, {kling.OmniVideo4, []string{kling.ResourceCreateElement, kling.ResourceCreateVoice}}} {
		helpers, err := service.PreparationModelsForWorkflow(context.Background(), "kling", tc.consumer)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range tc.helpers {
			if !slices.ContainsFunc(helpers, func(m core.MediaModel) bool { return m.ID == id }) {
				t.Fatalf("required preparation %s removed from %s", id, tc.consumer)
			}
		}
	}
}
