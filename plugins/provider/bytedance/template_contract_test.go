package bytedance

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestSeedance25ReferenceWorkflowSendsExplicitTaskHint(t *testing.T) {
	for _, mode := range []string{"references", "first_frame", "none"} {
		t.Run(mode, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if mode == "references" {
					if body["omni_reference_task_type"] != "reference" {
						t.Fatal(body)
					}
				} else if body["omni_reference_task_type"] != nil {
					t.Fatal("task hint leaked into another workflow", body)
				}
				return jsonResponse(200, `{"id":"offline-fixture"}`), nil
			})}
			a, err := New("synthetic", "https://api.example.test", client, WithImageTimeout(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			o := core.MediaOptions{ReferenceMode: mode, Ratio: "adaptive"}
			if mode != "none" {
				o.ImageURLs = []string{"https://cdn.example.test/reference.png"}
			}
			if _, err = a.Generate(context.Background(), core.MediaRequest{Model: "dreamina-seedance-2-5-260628", Prompt: "Use Image 1 as a visual reference", Options: o}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSeedance25KeyframeRatioUsesNativeAdaptiveConstraint(t *testing.T) {
	m := describeModel("dreamina-seedance-2-5-260628")
	for _, mode := range []string{"first_frame", "first_last_frames"} {
		o := core.MediaOptions{ReferenceMode: mode, ImageURLs: []string{"https://cdn.example.test/first.png"}}
		if mode == "first_last_frames" {
			o.ImageURLs = append(o.ImageURLs, "https://cdn.example.test/last.png")
		}
		normalized, err := core.NormalizeModelOptions(m, o)
		if err != nil || normalized.Ratio != "adaptive" {
			t.Fatal(normalized, err)
		}
		o.Ratio = "16:9"
		if _, err = core.NormalizeModelOptions(m, o); err == nil {
			t.Fatal("keyframe ratio was silently changed")
		}
		if (&Adapter{}).ValidateRequest(context.Background(), core.MediaRequest{Model: m.ID, Options: o}) == nil {
			t.Fatal("direct native request bypassed keyframe ratio")
		}
	}
}
