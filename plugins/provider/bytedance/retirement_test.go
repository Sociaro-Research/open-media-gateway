package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestSeedanceRetirementFiltersStaleDiscoveryAndCachedModels(t *testing.T) {
	const retired = "seedance-1-5-pro-251215"
	const active = "dreamina-seedance-2-0-260128"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	now := cutoff.Add(-time.Nanosecond)
	gets := 0
	a := &Adapter{
		apiKey: "fixture", baseURL: "https://byte.test",
		clock: func() time.Time { return now },
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != "/models" {
				t.Fatal("unexpected provider call", r.Method, r.URL)
			}
			gets++
			return jsonResponse(200, `{"data":[{"id":"seedance-1-5-pro-251215","shutdown_date":"2030-01-01"},{"id":"dreamina-seedance-2-0-260128"}]}`), nil
		})},
	}
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 2 {
		t.Fatal(models, err)
	}
	if gets != 1 {
		t.Fatal("expected one initial discovery", gets)
	}
	for _, instant := range []time.Time{cutoff, cutoff.Add(time.Nanosecond), cutoff.In(time.FixedZone("UTC+8", 8*3600))} {
		now = instant
		cached, err := a.ListModels(context.Background())
		if err != nil || len(cached) != 1 || cached[0].ID != active {
			t.Fatal("cached descriptor crossed reviewed cutoff", cached, err)
		}
		if gets != 1 {
			t.Fatal("cache crossing unexpectedly refetched", gets)
		}
	}
	// Force a fresh response; an unchanged provider list must not reactivate it.
	a.modelsAt = cutoff.Add(-11 * time.Minute)
	if models, err = a.ListModels(context.Background()); err != nil || len(models) != 1 || models[0].ID != active {
		t.Fatal("stale fresh discovery reactivated retired model", models, err)
	}
	ids, err := a.DiscoverModelIDs(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{active}) {
		t.Fatal("discovery IDs retained retired model", ids, err)
	}
	if !knownModel(retired) || describeModel(retired).Pricing.USDPerMillionTokens <= 0 {
		t.Fatal("historical serializer or pricing was removed")
	}
}

func TestSeedanceRetirementBlocksNewSubmissionButPreservesAcceptedPollingAndDownload(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	now := cutoff.Add(-time.Nanosecond)
	posts, polls, downloads := 0, 0, 0
	a := &Adapter{apiKey: "fixture", baseURL: "https://byte.test", clock: func() time.Time { return now }}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == tasksPath:
			posts++
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Model != id {
				t.Fatal("model was automatically replaced", body.Model)
			}
			return jsonResponse(200, `{"id":"accepted-before-cutoff"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == tasksPath+"/accepted-before-cutoff":
			polls++
			return jsonResponse(200, `{"status":"succeeded","content":{"video_url":"https://cdn.example/accepted.mp4"},"usage":{"completion_tokens":38800}}`), nil
		case r.Method == http.MethodGet && r.URL.String() == "https://cdn.example/accepted.mp4":
			downloads++
			return jsonResponse(200, "preserved-video-bytes"), nil
		default:
			t.Fatal("unexpected provider request", r.Method, r.URL)
			return nil, errors.New("unexpected request")
		}
	})}
	request := core.MediaRequest{Model: id, Prompt: "sunrise", Options: core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9"}}
	snapshot, _ := json.Marshal(request)
	task, err := a.Generate(context.Background(), request)
	if err != nil || task.ID != "accepted-before-cutoff" {
		t.Fatal(task, err)
	}
	for _, instant := range []time.Time{cutoff, cutoff.Add(time.Nanosecond)} {
		now = instant
		if err := a.ValidateRequest(context.Background(), request); !errors.Is(err, core.ErrModelUnavailable) {
			t.Fatal("new admission survived retirement", err)
		}
		if _, err := a.Generate(context.Background(), request); !errors.Is(err, core.ErrModelUnavailable) {
			t.Fatal("new generation reached retired provider model", err)
		}
	}
	result, err := a.GetTask(context.Background(), task.ID)
	if err != nil || result.Status != core.ProviderSucceeded || result.Usage == nil {
		t.Fatal("accepted polling/usage changed", result, err)
	}
	response, err := a.client.Get(result.ResultURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil || string(content) != "preserved-video-bytes" {
		t.Fatal(string(content), err)
	}
	after, _ := json.Marshal(request)
	if string(after) != string(snapshot) || posts != 1 || polls != 1 || downloads != 1 {
		t.Fatal("accepted intent changed or was resubmitted", posts, polls, downloads)
	}
}

func TestSeedanceDeprecationNoticeIsAnUnacknowledgedCodeDefault(t *testing.T) {
	const notice = "https://ai.byteplus.com/ark/region:ap-southeast-1/docs/model-deprecation-notice"
	definition := Definition()
	count := 0
	for _, source := range definition.CatalogSources {
		if source.URL == notice {
			count++
			if source.ID != "deprecations" || source.Parser != "document" {
				t.Fatal("notice must remain document evidence, not an executable manifest", source)
			}
		}
	}
	if count != 1 || definition.CatalogReviews[notice].Hash != "" {
		t.Fatal("notice missing, duplicated or fingerprint acknowledged", count)
	}
}

func TestSeedanceHistoricalUsageLookupIsPureAndNeverEnablesExecution(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := &Adapter{baseURL: "https://ark.ap-southeast.bytepluses.com/api/v3", clock: func() time.Time { return cutoff },
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Fatal("historical usage lookup contacted provider", r.URL)
			return nil, errors.New("unexpected call")
		})}}
	model, found, err := a.HistoricalUsageModel(context.Background(), id)
	if err != nil || !found || model.ID != id || !model.Pricing.Known || model.Pricing.USDPerMillionTokens != 1.2 {
		t.Fatal("historical model tariff lost", model, found, err)
	}
	if err := a.ValidateRequest(context.Background(), core.MediaRequest{Model: id}); !errors.Is(err, core.ErrModelUnavailable) {
		t.Fatal("historical lookup enabled new generation", err)
	}
	if _, found, err := a.HistoricalUsageModel(context.Background(), "dreamina-seedance-2-0-mini-260615"); err != nil || found {
		t.Fatal("historical lookup substituted a recommended replacement", found, err)
	}
}

// Serializer/validation fixtures describe the protocol before retirement. Keep
// those assertions stable after the wall clock passes the admission deadline.
func beforeSeedanceRetirement() time.Time {
	return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
}

func beforeLiteRetirement() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
