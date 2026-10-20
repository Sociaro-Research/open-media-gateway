package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestSoniloEncryptedConfigurationServiceCheckAndTariffs(t *testing.T) {
	m, repo, ctx := fixtureConnection(t)
	calls := 0
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer sonilo-fixture-secret" || r.Method != "GET" || r.URL.Path != "/v1/account/services" {
			t.Fatal("credential check must only read authenticated account services", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"available_services":["text_to_music","text_to_sfx","video_to_sound"]}`))}, nil
	})
	key, enabled, price := "sonilo-fixture-secret", true, "0.018"
	p, err := m.Update(ctx, "sonilo", ProviderUpdate{APIKey: &key, Enabled: &enabled, Extra: map[string]string{"sound_price": price}})
	if err != nil || !p.Configured || len(p.Models) != 3 {
		t.Fatal(p, err)
	}
	record, err := repo.Get(ctx, "sonilo")
	if err != nil || strings.Contains(string(record.CredentialsCiphertext), key) {
		t.Fatal("Sonilo credentials were not encrypted", err)
	}
	wire, _ := json.Marshal(p)
	if strings.Contains(string(wire), key) || p.Hints["api_key"] == "" || p.Values["sound_price"] != price {
		t.Fatal("unsafe secret output or lost tariff")
	}
	checked, err := m.Check(ctx, "sonilo")
	if err != nil || checked.Status != "connected" || calls < 1 {
		t.Fatal(checked, err, calls)
	}
	service := core.NewTaskServiceWithProviders(nil, nil, map[string]core.MediaProvider{"sonilo": m.ManagedProviders()["sonilo"]}, map[string]string{"sonilo": "text-to-music"}, "sonilo")
	catalog := service.ListProviders(ctx)
	if len(catalog) != 1 || catalog[0].IconURL != p.IconURL || !strings.HasPrefix(catalog[0].IconURL, "/provider-assets/sonilo/icon.png?v=") || catalog[0].IconMonochrome {
		t.Fatalf("generation catalog lost original provider artwork: %+v", catalog)
	}
	for _, kind := range m.Types() {
		if kind.ID == "sonilo" && kind.IconURL != catalog[0].IconURL {
			t.Fatal("API profile picker lost provider artwork")
		}
	}
	provider, err := m.Provider(ctx, "sonilo")
	if err != nil {
		t.Fatal(err)
	}
	cost, revision, err := provider.(core.CostQuoter).Quote(ctx, core.MediaModel{}, core.MediaRequest{Model: "video-to-sound", Options: core.MediaOptions{VideoURL: "https://cdn.example/video.mp4"}})
	if err != nil || revision == "" || cost.ExpectedUSD.String() != "8.640000" || cost.ActualUSD != nil {
		t.Fatal(cost, revision, err)
	}
}
