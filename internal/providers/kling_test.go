package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingConnectionEncryptionCatalogEstimatesAndPinnedPolling(t *testing.T) {
	m, repo, ctx := fixtureConnection(t)
	submissions, polls, checks := 0, 0, 0
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer kling-fixture-secret" || r.URL.Host != "api-singapore.klingai.com" {
			t.Fatal("wrong auth or destination")
		}
		body := `{"code":0,"data":{"id":"task_123","status":"submitted"}}`
		if r.URL.Path == "/account/costs" {
			checks++
			if r.Method != "GET" || r.URL.Query().Get("start_time") == "" || r.URL.Query().Get("end_time") == "" {
				t.Fatal("invalid account query")
			}
			body = `{"code":0,"message":"kling-fixture-secret private account details","data":{"code":0}}`
		} else if r.Method == "POST" {
			submissions++
			if r.URL.Path != "/image-to-video/kling-3.0" {
				t.Fatal(r.URL.Path)
			}
		} else {
			polls++
			if r.URL.Path != "/tasks" || r.URL.Query().Get("task_ids") != "task_123" {
				t.Fatal("unexpected task query", r.URL)
			}
			body = `{"code":0,"data":[{"id":"task_123","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/video.mp4","duration":"5"}]}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	connection, err := m.Create(ctx, CreateConnection{APIType: "kling", Name: "Kling fixture"})
	if err != nil {
		t.Fatal(err)
	}
	key := "kling-fixture-secret"
	p, err := m.Update(ctx, connection.ID, ProviderUpdate{APIKey: &key, Enabled: pointer(true), Connection: &providerkit.Connection{ManualCatalog: false}, Extra: map[string]string{"kling4_1080p_price": "0.200001"}})
	if err != nil || !p.Configured || p.Status != "unverified" || submissions+polls != 0 {
		t.Fatal(p, err)
	}
	assertKlingContracts(t, p.Models)
	record, err := repo.Get(ctx, connection.ID)
	if err != nil || strings.Contains(string(record.CredentialsCiphertext), key) {
		t.Fatal("unencrypted secret", err)
	}
	wire, _ := json.Marshal(p)
	if strings.Contains(string(wire), key) || p.Hints["api_key"] == "" {
		t.Fatal("secret exposed")
	}
	checked, err := m.Check(ctx, connection.ID)
	if err != nil || checked.Status != "authenticated" || !strings.Contains(checked.StatusMessage, "Resource-package details were absent or malformed") || strings.Contains(checked.StatusMessage, key) || strings.Contains(checked.StatusMessage, "private account details") || submissions+polls != 0 || checks != 1 {
		t.Fatal(checked, err)
	}
	listed, err := m.Get(ctx, connection.ID)
	if err != nil || listed.Status != "authenticated" || listed.LastCheckedAt == nil || listed.StatusMessage != checked.StatusMessage || checks != 1 {
		t.Fatal(listed, err)
	}
	cached, checkErr := m.Check(ctx, connection.ID)
	if checkErr != nil || cached.StatusMessage != checked.StatusMessage {
		t.Fatal("cached warning lost", cached, checkErr)
	}
	if checks != 1 {
		t.Fatal("cached check repeated account query")
	}
	all, listErr := m.List(ctx)
	if listErr != nil {
		t.Fatal(listErr)
	}
	found := false
	for _, item := range all {
		if item.ID == connection.ID {
			found = true
			if item.StatusMessage != checked.StatusMessage || item.Status != "authenticated" {
				t.Fatal("list lost warning", item)
			}
		}
	}
	if !found || checks != 1 {
		t.Fatal("list repeated check or omitted connection")
	}
	provider, err := m.Provider(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewTaskServiceWithProviders(nil, nil, map[string]core.MediaProvider{connection.ID: provider}, map[string]string{connection.ID: kling.TextToVideo}, connection.ID)
	service.EnableAccounting()
	catalog := service.ListProviders(ctx)
	if len(catalog) != 1 {
		t.Fatal(catalog)
	}
	assertKlingContracts(t, catalog[0].Models)
	for _, model := range catalog[0].Models {
		if !slices.Contains([]string{kling.TextToVideo, kling.ImageToVideo, kling.TextToVideo4, kling.ImageToVideo4, kling.TextToVideo3Turbo, kling.ImageToVideo3Turbo, kling.TextToVideo25Turbo, kling.ImageToVideo25Turbo, kling.OmniVideo4}, model.ID) {
			continue
		}
		input := core.CreateMediaInput{Provider: connection.ID, Model: model.ID, Prompt: "Synthetic fixture"}
		if model.RequiresImageInput {
			input.Options.ImageURLs = []string{"https://cdn.example/first.png"}
		}
		estimate, err := service.Estimate(ctx, input)
		want := int64(420000)
		if model.ID == kling.TextToVideo4 || model.ID == kling.ImageToVideo4 {
			want = 2000010
		}
		if model.ID == kling.TextToVideo3Turbo || model.ID == kling.ImageToVideo3Turbo {
			want = 560000
		}
		if model.ID == kling.TextToVideo25Turbo || model.ID == kling.ImageToVideo25Turbo {
			want = 210000
		}
		if model.ID == kling.OmniVideo4 {
			input.Options.Duration = 10
			estimate, err = service.Estimate(ctx, input)
			want = 2000010
		}
		if err != nil || int64(estimate.Cost.ExpectedUSD) != want || estimate.Cost.ActualUSD != nil {
			t.Fatal("shared API/Playground normalization or quote failed", estimate, err)
		}
	}
	r := core.MediaRequest{Model: kling.ImageToVideo, Prompt: "Synthetic fixture", Options: core.MediaOptions{ImageURLs: []string{"https://cdn.example/first.png"}}}
	cost, revision, err := provider.(core.CostQuoter).Quote(ctx, core.MediaModel{}, r)
	if err != nil || revision == "" || cost.ExpectedUSD != 420000 || cost.BillingRequired {
		t.Fatal(cost, revision, err)
	}
	task, err := provider.Generate(core.WithProviderRevision(ctx, revision), r)
	if err != nil || submissions != 1 {
		t.Fatal(task, err)
	}
	// Key rotation leaves accepted tasks pinned to their encrypted old revision.
	if _, err = m.Update(ctx, connection.ID, ProviderUpdate{APIKey: pointer("rotated-fixture-secret")}); err != nil {
		t.Fatal(err)
	}
	rotated, err := m.Get(ctx, connection.ID)
	if err != nil || rotated.Status != "unverified" {
		t.Fatal("rotated key reused check", rotated, err)
	}
	result, err := provider.GetTask(core.WithProviderRevision(ctx, revision), task.ID)
	if err != nil || result.Status != core.ProviderSucceeded || polls != 1 {
		t.Fatal(result, err)
	}
}

func TestKlingRejectsGenericManualModelSubstitution(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	_, err := m.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("fixture-secret"), Connection: &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{{Profile: kling.TextToVideo, Model: core.MediaModel{ID: "kling-4.0", MediaType: "video"}}}}})
	if err == nil {
		t.Fatal("generic body model substitution cannot select an unreviewed Kling route")
	}
}

func TestKlingAuthenticationCheckIgnoresPollOverridesAndReportsFailure(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	calls := 0
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/account/costs" || r.Header.Get("X-Poll-Only") != "" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Fatal("probe inherited polling configuration", r.URL)
		}
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":1103,"message":"fixture-key private account"}`))}, nil
	})
	p, err := m.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("fixture-key"), Connection: &providerkit.Connection{ManualCatalog: false, Endpoints: map[string]string{"poll": "/poll-override"}, Headers: []providerkit.Header{{Name: "X-Poll-Only", Value: pointer("fixture"), Operation: "poll"}}}})
	if err != nil || calls != 0 {
		t.Fatal(p, err)
	}
	p, err = m.Check(ctx, "kling")
	if err != nil || calls != 1 || p.Status != "error" || p.LastCheckedAt == nil || !strings.Contains(p.StatusMessage, "permission") || strings.Contains(p.StatusMessage, "fixture-key") || strings.Contains(p.StatusMessage, "private account") {
		t.Fatal(p, err)
	}
	p, err = m.Get(ctx, "kling")
	if err != nil || calls != 1 || p.Status != "error" {
		t.Fatal("probe failure was forgotten", p, err)
	}
}
