package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/openai"
)

func TestOpenAIImage26HTTPCatalogueAndAdmissionUseReviewedContract(t *testing.T) {
	for _, id := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, variation := range []string{"valid", "size", "quality", "format", "complexity", "future snapshot"} {
			t.Run(id+"/"+variation, func(t *testing.T) {
				client := &http.Client{Transport: omniMockTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.URL.Path != "/v1/models" {
						t.Fatal("admission submitted generation", r.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"` + id + `"},{"id":"gpt-image-2.6-flare-2099-01-01"}]}`))}, nil
				})}
				adapter, err := openai.New("synthetic-fixture", "", client, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				repo, queue := &omniAdmissionRepository{}, &admissionQueue{}
				svc := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"openai": adapter}, map[string]string{"openai": openai.DefaultModel}, "openai")
				svc.EnableAccounting()
				h := New(svc, healthStub{}, "secret")
				catalog := sendTest(h, "GET", "/v1/models", "", "", "secret")
				if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), `"id":"openai/`+id+`"`) || strings.Contains(catalog.Body.String(), "2099") || !strings.Contains(catalog.Body.String(), `"key":"complexity"`) {
					t.Fatal(catalog.Code, catalog.Body)
				}
				body := `{"model":"openai/` + id + `","prompt":"Synthetic offline fixture","size":"1024x1024","quality":"medium","output_format":"png","options":{"parameters":{"complexity":"low"}}}`
				switch variation {
				case "size":
					body = strings.Replace(body, "1024x1024", "3840x2160", 1)
				case "quality":
					body = strings.Replace(body, `"quality":"medium"`, `"quality":"max"`, 1)
				case "format":
					body = strings.Replace(body, `"png"`, `"svg"`, 1)
				case "complexity":
					body = strings.Replace(body, `"complexity":"low"`, `"complexity":"max"`, 1)
				case "future snapshot":
					body = strings.Replace(body, id, "gpt-image-2.6-flare-2099-01-01", 1)
				}
				for _, path := range []string{"/v1/generations/estimate", "/v1/images/generations"} {
					w := openAIRequest(h, path, body)
					if variation != "valid" {
						if w.Code < 400 || w.Code >= 500 || repo.creates != 0 || len(queue.ids) != 0 {
							t.Fatal("invalid intent admitted", w.Code, w.Body)
						}
						continue
					}
					want := 202
					if path == "/v1/generations/estimate" {
						want = 200
					}
					if w.Code != want {
						t.Fatal(w.Code, w.Body)
					}
				}
				if variation == "valid" {
					if repo.creates != 1 || len(queue.ids) != 1 || repo.task.Cost == nil || repo.task.Cost.ExpectedUSD <= 0 || repo.task.Options.Parameters["complexity"] != "low" {
						t.Fatal(repo.task)
					}
					found := false
					for _, c := range repo.task.Cost.Pricing.Components {
						if c.Metric == "image_output_tokens" {
							found = c.EstimatedUnits == 1449
						}
					}
					if !found {
						t.Fatal("admitted quote differs from 2.6 calculator", repo.task.Cost)
					}
				}
			})
		}
	}
}
