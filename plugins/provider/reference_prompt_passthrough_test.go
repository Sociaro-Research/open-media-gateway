package providerassets

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/agnes"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/bytedance"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/openai"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/sonilo"
	"net/http"
	"strings"
	"testing"
	"time"
)

type promptCaptureTransport func(*http.Request) (*http.Response, error)

func (f promptCaptureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReferencePromptLiteralNativeTransport(t *testing.T) {
	const prompt = "Keep <Picture1>, [IMAGE_1], {Image 1}, @Image1 and @Image_1. Bare Image 1, @Image 1 and character1 remain literal. <tag> stays text."
	for _, p := range []string{"bytedance", "alibaba", "agnes", "kling", "openai", "sonilo"} {
		t.Run(p, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: promptCaptureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
					if e := r.ParseMultipartForm(1 << 20); e != nil {
						t.Fatal(e)
					}
					if r.FormValue("prompt") != prompt {
						t.Fatal(r.MultipartForm.Value)
					}
				} else {
					var body any
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
						t.Fatal(e)
					}
					var contains func(any) bool
					contains = func(v any) bool {
						switch v := v.(type) {
						case string:
							return v == prompt
						case map[string]any:
							for _, x := range v {
								if contains(x) {
									return true
								}
							}
						case []any:
							for _, x := range v {
								if contains(x) {
									return true
								}
							}
						}
						return false
					}
					if !contains(body) {
						t.Fatal("literal prompt changed", body)
					}
				}
				return nil, errors.New("offline fixture stops before network")
			})}
			var a core.MediaProvider
			var e error
			model := ""
			o := core.MediaOptions{ImageURLs: []string{"https://fixture.invalid/reference.png"}, ReferenceMode: "references"}
			switch p {
			case "bytedance":
				a, e = bytedance.New("synthetic", "https://ark.ap-southeast.bytepluses.com/api/v3", client)
				model = "dreamina-seedance-2-5-260628"
			case "alibaba":
				a, e = alibaba.New("synthetic", "https://fixture.invalid", "", "", client)
				model = "wan3.0-video"
			case "agnes":
				a, e = agnes.New("synthetic", agnes.DefaultBaseURL, client, time.Second)
				model = "agnes-video-2.5"
			case "kling":
				a, e = kling.New("synthetic", kling.DefaultBaseURL, client)
				model = kling.ImageToVideo4
				o.ReferenceMode = "first_frame"
			case "openai":
				a, e = openai.New("synthetic", openai.DefaultBaseURL, client, time.Second)
				model = "gpt-image-2"
			case "sonilo":
				a, e = sonilo.New("synthetic", sonilo.DefaultBaseURL, client)
				model = "text-to-music"
				o = core.MediaOptions{Duration: 10}
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = a.Generate(context.Background(), core.MediaRequest{Model: model, Prompt: prompt, Options: o})
			if e == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, e)
			}
		})
	}
}
