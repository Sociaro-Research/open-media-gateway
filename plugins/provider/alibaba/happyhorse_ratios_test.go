package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Source: HappyHorse reference-to-video API, reviewed 2026-10-26.
// These are offline contract/serialization checks, not live generation results.
func TestHappyHorse11ReferenceRatios(t *testing.T) {
	m, ok := describeDashModel("happyhorse-1.1-r2v", "")
	if !ok {
		t.Fatal("native descriptor missing")
	}
	want := []string{"16:9", "9:16", "3:4", "4:3", "4:5", "5:4", "1:1", "9:21", "21:9"}
	if !reflect.DeepEqual(m.Ratios, want) {
		t.Fatalf("documented ratio choices: got %v want %v", m.Ratios, want)
	}
	for _, ratio := range want {
		t.Run(ratio, func(t *testing.T) {
			options, err := normalizeRatioThroughAdmission(t, m, happyHorseRatioOptions(ratio))
			if err != nil || options.Ratio != ratio {
				t.Fatal("documented ratio changed or rejected", options, err)
			}
			assertVideoRatioPayload(t, m.ID, options, &ratio)
		})
	}
}

func TestHappyHorse11OmittedRatioUsesDocumentedDefault(t *testing.T) {
	m, _ := describeDashModel("happyhorse-1.1-r2v", "")
	options := happyHorseRatioOptions("")
	// Direct adapter calls must omit this optional field, rather than sending
	// an empty string or silently requesting adaptive.
	assertVideoRatioPayload(t, m.ID, options, nil)
	normalized, err := normalizeRatioThroughAdmission(t, m, options)
	if err != nil || normalized.Ratio != "16:9" {
		t.Fatal(normalized, err)
	}
	want := "16:9"
	assertVideoRatioPayload(t, m.ID, normalized, &want)
}

func TestHappyHorse11UnsupportedRatioRejectedBeforeTransport(t *testing.T) {
	m, _ := describeDashModel("happyhorse-1.1-r2v", "")
	for _, ratio := range []string{"adaptive", "3:2", "auto", "16:9 "} {
		t.Run(ratio, func(t *testing.T) {
			options := happyHorseRatioOptions(ratio)
			if _, err := normalizeRatioThroughAdmission(t, m, options); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("core accepted unsupported ratio", err)
			}
			calls := 0
			a := &Adapter{baseURL: "https://fixture.invalid", client: &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return dashJSONResponse(200, `{"output":{"task_id":"offline-fixture"}}`), nil
			})}}
			request := core.MediaRequest{Model: m.ID, Prompt: "Preserve [Image 1]", Options: options}
			if err := a.ValidateRequest(context.Background(), request); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("native validation accepted unsupported ratio", err)
			}
			if _, err := a.Generate(context.Background(), request); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
				t.Fatal("unsupported ratio reached transport", err, calls)
			}
		})
	}
}

func TestWan3AdaptiveRatioUnaffected(t *testing.T) {
	for _, id := range []string{"wan3.0-video", "wan3.0-video-prime"} {
		t.Run(id, func(t *testing.T) {
			m, ok := describeDashModel(id, "")
			if !ok {
				t.Fatal("native descriptor missing")
			}
			options, err := normalizeRatioThroughAdmission(t, m, happyHorseRatioOptions("adaptive"))
			if err != nil || options.Ratio != "adaptive" {
				t.Fatal("Wan 3 adaptive contract changed", options, err)
			}
			want := "adaptive"
			assertVideoRatioPayload(t, id, options, &want)
		})
	}
}

func happyHorseRatioOptions(ratio string) core.MediaOptions {
	return core.MediaOptions{ReferenceMode: "references", Ratio: ratio, Resolution: "720p", Duration: 5, ImageURLs: []string{"https://fixture.invalid/first.png", "https://fixture.invalid/second.png"}}
}

type ratioAdmissionProvider struct {
	*Adapter
	model      core.MediaModel
	normalized core.MediaOptions
}

func (p *ratioAdmissionProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{p.model}, nil
}
func (p *ratioAdmissionProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	p.normalized = r.Options
	return p.Adapter.ValidateRequest(ctx, r)
}
func (p *ratioAdmissionProvider) Quote(context.Context, core.MediaModel, core.MediaRequest) (core.TaskCost, string, error) {
	return core.FreeCost(time.Now()), "offline-fixture", nil
}

// Exercise the existing public admission API rather than depending on the
// normalization helper exported by the separate Templates implementation.
func normalizeRatioThroughAdmission(t *testing.T, m core.MediaModel, options core.MediaOptions) (core.MediaOptions, error) {
	t.Helper()
	provider := &ratioAdmissionProvider{Adapter: &Adapter{}, model: m}
	service := core.NewTaskServiceWithProviders(nil, nil, map[string]core.MediaProvider{"fixture": provider}, map[string]string{"fixture": m.ID}, "fixture")
	service.EnableAccounting()
	_, err := service.Estimate(context.Background(), core.CreateMediaInput{Provider: "fixture", Model: m.ID, MediaType: m.MediaType, Prompt: "Preserve the supplied references", Options: options})
	return provider.normalized, err
}

func assertVideoRatioPayload(t *testing.T, id string, options core.MediaOptions, want *string) {
	t.Helper()
	calls := 0
	const prompt = "Preserve [Image 1] and [Image 2]"
	a := &Adapter{apiKey: "synthetic", baseURL: "https://fixture.invalid", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != videoPath {
			t.Fatal("unexpected request", r.Method, r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Input struct {
				Prompt string                       `json:"prompt"`
				Media  []struct{ Type, URL string } `json:"media"`
			} `json:"input"`
			Parameters map[string]json.RawMessage `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != id || body.Input.Prompt != prompt || len(body.Input.Media) != 2 {
			t.Fatal("unrelated payload changed", body)
		}
		for index, media := range body.Input.Media {
			if media.Type != "reference_image" || media.URL != options.ImageURLs[index] {
				t.Fatal("reference mapping changed", body.Input.Media)
			}
		}
		raw, present := body.Parameters["ratio"]
		if want == nil {
			if present {
				t.Fatal("omitted ratio must use native default", string(raw))
			}
		} else {
			var actual string
			if err := json.Unmarshal(raw, &actual); err != nil || actual != *want {
				t.Fatal("ratio changed in transport", string(raw), want, err)
			}
		}
		return dashJSONResponse(200, `{"output":{"task_id":"offline-fixture"}}`), nil
	})}}
	if _, err := a.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: prompt, Options: options}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("expected one offline dispatch", calls)
	}
}
