package minimax

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func TestActualTemplateReferenceContracts(t *testing.T) {
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", "image-01", "image-01-live", "S2V-01"} {
		t.Run(id, func(t *testing.T) {
			m, _ := modelFor(id)
			prompt := "Use @image1 in a cinematic scene"
			expected := 1
			if strings.HasPrefix(id, "MiniMax-H3") {
				prompt = "Use @image1 following @video1 with @audio1"
				expected = 3
			}
			ratio := "16:9"
			if id == "S2V-01" {
				ratio = ""
			}
			d := templates.Definition{Title: "MiniMax fixture", MediaType: m.MediaType, Model: id, Prompt: prompt, Syntax: templates.UIReferenceSyntax, Options: core.MediaOptions{ReferenceMode: "references", Ratio: ratio}}
			var e error
			d.Slots, e = d.InferSlots(m)
			if e != nil || len(d.Slots) != expected {
				t.Fatal(d.Slots, e)
			}
			if e = d.Validate(m); e != nil {
				t.Fatal(e)
			}
			r := core.MediaRequest{Model: id, Prompt: prompt, Options: d.Options}
			r.Options.ImageURLs = []string{"https://media.example/image.png"}
			if expected == 3 {
				r.Options.VideoURLs = []string{"https://media.example/video.mp4"}
				r.Options.AudioURLs = []string{"https://media.example/audio.wav"}
			}
			// S2V has no ratio control; image/H3 support this concrete ratio.
			if id == "S2V-01" {
				d.Options.Ratio = ""
				r.Options.Ratio = ""
			}
			a := fixture(t, func(h *http.Request) (*http.Response, error) {
				p := body(t, h)
				if expected == 3 {
					native := p["content"].([]any)[0].(map[string]any)["text"].(string)
					if native != "Use reference image 1 following reference video 1 with reference audio 1" {
						t.Fatal(native)
					}
				}
				if m.MediaType == "image" {
					return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/out.jpg"}}, "metadata": map[string]int{"success_count": 1, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
				}
				return response(200, success("123")), nil
			})
			normalized, e := core.NormalizeModelOptions(m, r.Options)
			if e != nil {
				t.Fatal(e)
			}
			r.Options = normalized
			if _, e = a.Generate(owned("alice"), r); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestNativeMentionTranslation(t *testing.T) {
	prompt := "@image1 follows @video_2 with @audio3 [Pan left] and @director; native reference image 1"
	want := "reference image 1 follows reference video 2 with reference audio 3 [Pan left] and @director; native reference image 1"
	if got := minimaxPrompt(prompt); got != want {
		t.Fatal(got)
	}
}

// Every Templates-eligible reference workflow must survive actual slot
// inference, validation, normalization and provider dispatch. Frame workflows
// are deliberately excluded by the shared authoring gate, not by this adapter.
func TestEveryEligibleReferenceWorkflowThroughTemplates(t *testing.T) {
	for _, op := range operations() {
		m := describe(op)
		if m.NoPrompt || m.MediaType != "image" && m.MediaType != "video" {
			continue
		}
		for _, mode := range core.ReferenceModesFor(m) {
			if _, err := templates.AuthoringMode(m, mode.ID); err != nil {
				continue
			}
			if mode.MaxImages == 0 && mode.MaxVideos == 0 && mode.MaxAudios == 0 {
				continue
			}
			t.Run(m.ID+"/"+mode.ID, func(t *testing.T) {
				o := core.MediaOptions{ReferenceMode: mode.ID}
				if len(mode.Ratios) > 0 {
					o.Ratio = mode.Ratios[0]
				} else if len(m.Ratios) > 0 {
					o.Ratio = m.Ratios[0]
				}
				prompt := "Scene"
				n := mode.MinImages
				if n == 0 && mode.MaxImages > 0 {
					n = 1
				}
				for i := 0; i < n; i++ {
					prompt += " @image" + strconv.Itoa(i+1)
					o.ImageURLs = append(o.ImageURLs, "https://media.example/image"+strconv.Itoa(i+1)+".png")
				}
				if mode.MaxVideos > 0 {
					prompt += " @video1"
					o.VideoURLs = []string{"https://media.example/video.mp4"}
				}
				if mode.MaxAudios > 0 {
					prompt += " @audio1"
					o.AudioURLs = []string{"https://media.example/audio.wav"}
				}
				if m.ID == RegenerationVideo {
					evidence := baseRequest().Options.ReferenceMediaEvidence[baseRequest().Options.Videos()[0]]
					o.ReferenceRoles = map[string]string{"video_1": "base_video"}
					o.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{o.VideoURLs[0]: evidence}
				}
				d := templates.Definition{Title: "MiniMax reference workflow", MediaType: m.MediaType, Model: m.ID, Prompt: prompt, Syntax: templates.UIReferenceSyntax, Options: core.MediaOptions{ReferenceMode: mode.ID, Ratio: o.Ratio, ReferenceRoles: o.ReferenceRoles}}
				var err error
				d.Slots, err = d.InferSlots(m)
				if err != nil {
					t.Fatal(err)
				}
				if len(d.Slots) != len(o.ImageURLs)+len(o.VideoURLs)+len(o.AudioURLs) {
					t.Fatal("missing template reference slots", d.Slots)
				}
				if err = d.Validate(m); err != nil {
					t.Fatal(err)
				}
				o, err = core.NormalizeModelOptions(m, o)
				if err != nil {
					t.Fatal(err)
				}
				a := fixture(t, func(r *http.Request) (*http.Response, error) {
					if m.MediaType == "image" {
						return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/result.png"}}, "metadata": map[string]int{"success_count": 1, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
					}
					return response(200, success("123")), nil
				})
				if _, err = a.Generate(owned("alice"), core.MediaRequest{Model: m.ID, Prompt: prompt, Options: o}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
