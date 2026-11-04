package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingFlashManagedPollingKeepsAcceptedRevision(t *testing.T) {
	for _, tc := range []struct{ id, path string }{
		{kling.TextToVideo4Flash, "/text-to-video/kling-4.0-flash"},
		{kling.ImageToVideo4Flash, "/image-to-video/kling-4.0-flash"},
		{kling.OmniVideo4Flash, "/omni-video/kling-4.0-flash"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			manager, _, ctx := fixtureConnection(t)
			posts, gets := 0, 0
			manager.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Authorization") != "Bearer flash-fixture-key" {
					t.Fatal("accepted Flash task used another credential revision")
				}
				body := `{"code":0,"data":{"id":"flash_accepted","status":"submitted"}}`
				if req.Method == "POST" {
					posts++
					if req.URL.Path != tc.path {
						t.Fatal("wrong Flash operation", req.URL.Path)
					}
				} else {
					gets++
					if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "flash_accepted" {
						t.Fatal("wrong pinned task query", req.URL.Path)
					}
					body = `{"code":0,"data":[{"id":"flash_accepted","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/flash.mp4","duration":"5.125"}],"usage":{"output_duration":"5"}}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			if _, err := manager.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("flash-fixture-key"), Enabled: pointer(true), Connection: &providerkit.Connection{ManualCatalog: false}}); err != nil {
				t.Fatal(err)
			}
			provider, err := manager.Provider(ctx, "kling")
			if err != nil {
				t.Fatal(err)
			}
			request := core.MediaRequest{Model: tc.id, Prompt: "Synthetic offline fixture", Options: core.MediaOptions{Duration: 10}}
			if tc.id == kling.ImageToVideo4Flash {
				request.Options.ImageURLs = []string{"https://cdn.example/first.png"}
			}
			cost, revision, err := provider.(core.CostQuoter).Quote(ctx, core.MediaModel{}, request)
			if err != nil || revision == "" || cost.ExpectedUSD != 940000 || cost.ActualUSD != nil || posts+gets != 0 {
				t.Fatal("managed Flash estimate failed", cost, err)
			}
			task, err := provider.Generate(core.WithProviderRevision(ctx, revision), request)
			if err != nil || posts != 1 || !strings.HasPrefix(task.ID, "kling-v1.") {
				t.Fatal("managed Flash submission lost scoped handle", err)
			}
			if _, err = manager.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("rotated-flash-fixture-key")}); err != nil {
				t.Fatal(err)
			}
			result, err := provider.GetTask(core.WithProviderRevision(ctx, revision), task.ID)
			if err != nil || result.Status != core.ProviderSucceeded || gets != 1 || len(result.Artifacts) != 1 || result.Artifacts[0].Duration != 5.125 || result.Usage == nil || !strings.Contains(string(result.Usage.Raw), `"output_video_duration":5`) {
				t.Fatal("accepted Flash task could not poll its pinned revision", result, err)
			}
		})
	}
}
