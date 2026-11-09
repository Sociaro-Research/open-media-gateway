package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestReviewedDurationContractsAndNativeSerialization(t *testing.T) {
	for _, id := range []string{"seedance-1-5-pro-251215", "dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-5-260628", "seedance-1-0-pro-fast-251015"} {
		t.Run(id, func(t *testing.T) {
			model := describeModel(id)
			auto := !strings.HasPrefix(id, "seedance-1-0-")
			if model.DurationMayBeInferred != auto {
				t.Fatalf("inference = %v, want %v", model.DurationMayBeInferred, auto)
			}
			wantMin := 4
			if !auto {
				wantMin = 2
			}
			if model.MinDuration != wantMin {
				t.Fatalf("minimum = %d, want %d", model.MinDuration, wantMin)
			}
			calls := 0
			adapter := &Adapter{clock: beforeSeedanceRetirement, apiKey: "test-key", baseURL: "http://byte.test", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if auto {
					if body["duration"] != float64(-1) {
						t.Fatalf("native auto = %#v", body)
					}
				} else {
					if body["duration"] != nil || !strings.Contains(body["content"].([]any)[0].(map[string]any)["text"].(string), "--duration 5") {
						t.Fatalf("legacy default = %#v", body)
					}
				}
				return jsonResponse(http.StatusOK, `{"id":"duration-test"}`), nil
			})}}
			if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: "synthetic"}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			for _, seconds := range []int{-1, model.MinDuration - 1, model.MaxDuration + 1} {
				err := adapter.ValidateRequest(context.Background(), core.MediaRequest{Model: id, Prompt: "synthetic", Options: core.MediaOptions{Duration: seconds}})
				if !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatalf("duration %d accepted: %v", seconds, err)
				}
			}
			for _, seconds := range []int{model.MinDuration, model.MaxDuration} {
				if err := adapter.ValidateRequest(context.Background(), core.MediaRequest{Model: id, Prompt: "synthetic", Options: core.MediaOptions{Duration: seconds}}); err != nil {
					t.Fatalf("boundary %d: %v", seconds, err)
				}
			}
			if auto {
				for _, ratio := range []string{"16:9", "adaptive"} {
					_, err := quotePublic("https://ark.ap-southeast.bytepluses.com", model, core.MediaRequest{Model: id, Options: core.MediaOptions{Resolution: "480p", Ratio: ratio}}, time.Now())
					if !errors.Is(err, core.ErrPricingUnavailable) {
						t.Fatalf("unknown duration quoted: %v", err)
					}
				}
			}
		})
	}
}
