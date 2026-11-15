package bytedance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const flashID = "dreamina-seedance-2-5-flash-261115"

func TestSeedance25FlashDiscoveryKeepsExactID(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Fatal("discovery sent", r.Method, r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"data":[{"id":"dreamina-seedance-2-5-flash-261115"},{"id":"dreamina-seedance-2-5-flash-future"},{"id":"dreamina-seedance-2-5-260628"}]}`), nil
	})}
	a := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "dreamina-seedance-2-5-260628" || models[1].ID != flashID {
		t.Fatal(models, err)
	}
	ids, err := a.DiscoverModelIDs(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{flashID, "dreamina-seedance-2-5-260628"}) {
		t.Fatal("reviewed discovery", ids, err)
	}
	if !slices.ContainsFunc(Definition().Profiles(), func(m core.MediaModel) bool { return m.ID == flashID }) || len(Definition().CatalogVerificationSources[flashID]) != 1 {
		t.Fatal("Flash profile or operational source missing")
	}
}

func TestSeedance25FlashProfileUsesOwnLimits(t *testing.T) {
	m, base := describeModel(flashID), describeModel("dreamina-seedance-2-5-260628")
	l := m.ReferenceMediaLimits
	if !reflect.DeepEqual(m.Resolutions, []string{"480p", "720p"}) || len(m.Ratios) != 7 || !slices.Contains(m.Ratios, "adaptive") || m.MinDuration != 4 || m.MaxDuration != 30 || !m.DurationMayBeInferred ||
		m.MaxImageInputs != 10 || m.SupportsDraft || !m.SupportsAudio || !m.SupportsLastFrame || m.Pricing.USDPerMillionTokens != 7 ||
		l.MaxFiles != 20 || l.Video.MinDuration != 2 || l.Video.MaxDuration != 30 || l.Video.MaxTotalDuration != 30 || l.Audio.MinDuration != 2 || l.Audio.MaxDuration != 30 || l.Audio.MaxTotalDuration != 30 {
		t.Fatalf("Flash contract: %+v %+v", m, l)
	}
	if !slices.Contains(base.Resolutions, "1080p") || base.MaxImageInputs != 30 || base.ReferenceMediaLimits.MaxFiles != 50 || base.Pricing.USDPerMillionTokens != 10.7 {
		t.Fatal("base 2.5 contract changed", base)
	}
	var refs core.ReferenceMode
	for _, mode := range m.ReferenceModes {
		if mode.ID == "references" {
			refs = mode
		}
	}
	if refs.MaxImages != 10 || refs.MaxVideos != 5 || refs.MaxAudios != 5 || refs.RequiresVisual {
		t.Fatal("reference counts", refs)
	}
	a := &Adapter{}
	urls := func(kind string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("https://cdn.example.test/%s%d", kind, i)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		o    core.MediaOptions
		ok   bool
	}{
		{"limits", core.MediaOptions{ReferenceMode: "references", ImageURLs: urls("i", 10), VideoURLs: urls("v", 5), AudioURLs: urls("a", 5), Duration: 30}, true},
		{"audio only", core.MediaOptions{ReferenceMode: "references", AudioURLs: urls("a", 1)}, true},
		{"images", core.MediaOptions{ReferenceMode: "references", ImageURLs: urls("i", 11)}, false},
		{"videos", core.MediaOptions{ReferenceMode: "references", VideoURLs: urls("v", 6)}, false},
		{"audios", core.MediaOptions{ReferenceMode: "references", AudioURLs: urls("a", 6)}, false},
		{"long", core.MediaOptions{Duration: 31}, false},
		{"short", core.MediaOptions{Duration: 3}, false},
		{"native sentinel", core.MediaOptions{Duration: -1}, false},
	} {
		if err := a.ValidateRequest(context.Background(), core.MediaRequest{Model: flashID, Options: tc.o}); (err == nil) != tc.ok {
			t.Fatal(tc.name, err)
		}
	}
	for _, o := range []core.MediaOptions{{Resolution: "1080p"}, {Resolution: "4k"}, {Draft: new(true)}} {
		if _, err := core.NormalizeModelOptions(m, o); err == nil {
			t.Fatal("unsupported Flash option admitted", o)
		}
	}
	for _, mode := range []string{"first_frame", "first_last_frames"} {
		o := core.MediaOptions{ReferenceMode: mode, ImageURLs: urls("f", 1)}
		if mode == "first_last_frames" {
			o.ImageURLs = urls("f", 2)
		}
		if normalized, err := core.NormalizeModelOptions(m, o); err != nil || normalized.Ratio != "adaptive" {
			t.Fatal(normalized, err)
		}
		o.Ratio = "16:9"
		if !errors.Is(a.ValidateRequest(context.Background(), core.MediaRequest{Model: flashID, Options: o}), core.ErrOptionsInvalid) {
			t.Fatal("keyframe ratio accepted", mode)
		}
	}
}

func TestSeedance25FlashSerializerAndPolling(t *testing.T) {
	for _, mode := range []string{"references", "first_frame", "none"} {
		t.Run(mode, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, `{"status":"succeeded","content":{"video_url":"https://cdn.example.test/out.mp4"},"usage":{"completion_tokens":108000,"total_tokens":108000}}`), nil
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != flashID || body["duration"] != float64(-1) || body["resolution"] != "720p" || body["draft"] != nil {
					t.Fatal(body)
				}
				if (body["omni_reference_task_type"] == "reference") != (mode == "references") || mode != "references" && body["omni_reference_task_type"] != nil {
					t.Fatal("task intent", body)
				}
				return jsonResponse(http.StatusOK, `{"id":"flash-task"}`), nil
			})}
			a, err := New("synthetic", "https://api.example.test", client)
			if err != nil {
				t.Fatal(err)
			}
			o := core.MediaOptions{ReferenceMode: mode, Resolution: "720p", Ratio: "adaptive"}
			if mode != "none" {
				o.ImageURLs = []string{"https://cdn.example.test/reference.png"}
			}
			task, err := a.Generate(context.Background(), core.MediaRequest{Model: flashID, Prompt: "Use Image 1", Options: o})
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.GetTask(context.Background(), task.ID)
			if err != nil || result.Status != core.ProviderSucceeded || result.Usage == nil || !slices.ContainsFunc(result.Usage.Metrics, func(m core.UsageMetric) bool { return m.Name == "completion_tokens" && m.Quantity == 108000 }) {
				t.Fatal(result, err)
			}
		})
	}
}

func TestSeedance25FlashPricingByInputModeAndResolution(t *testing.T) {
	m := describeModel(flashID)
	quote := func(o core.MediaOptions) (core.TaskCost, error) {
		return quotePublic("https://ark.ap-southeast.bytepluses.com", m, core.MediaRequest{Model: flashID, Options: o}, time.Now())
	}
	// Published Flash examples for 5 s 16:9: USD 0.34 / 0.76 without video input and
	// USD 1.45 / 3.25 with 30 s of input video (the reserve assumes 30 s).
	for _, tc := range []struct {
		resolution     string
		video          bool
		rate, expected string
	}{
		{"480p", false, "7.000000", "0.336263"},
		{"720p", false, "7.000000", "0.756000"},
		{"480p", true, "4.300000", "1.445929"},
		{"720p", true, "4.300000", "3.250800"},
	} {
		o := core.MediaOptions{Resolution: tc.resolution, Ratio: "16:9", Duration: 5}
		if tc.video {
			o.VideoURLs = []string{"https://cdn.example.test/ref.mp4"}
		}
		cost, err := quote(o)
		if err != nil || cost.Pricing.Price.String() != tc.rate || cost.ExpectedUSD.String() != tc.expected || cost.Pricing.Unit != "token" || cost.Pricing.Version != "2026-11-15.1" {
			t.Fatal(tc, cost, err)
		}
		if tc.video != strings.Contains(cost.Pricing.EstimateDescription, "30 seconds") {
			t.Fatal("reserve explanation", cost.Pricing.EstimateDescription)
		}
		if !cost.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 100_000}}}) || cost.BilledUnits == nil || *cost.BilledUnits != 100_000 {
			t.Fatal("actual completion_tokens not metered", cost)
		}
	}
	// Published lowest Flash prices with 2-4 s input video (USD 0.37 / 0.84) are the token floors.
	if minimumVideoTokens(5, 854*480*24/1024.0) != 86468 || minimumVideoTokens(5, 1280*720*24/1024.0) != 194400 {
		t.Fatal("2.5 series floor changed")
	}
	video := []string{"https://cdn.example.test/ref.mp4"}
	for _, o := range []core.MediaOptions{
		{Resolution: "1080p", Ratio: "16:9", Duration: 5}, {Resolution: "1080p", Ratio: "16:9", Duration: 5, VideoURLs: video},
		{Resolution: "4k", Ratio: "16:9", Duration: 5}, {Resolution: "720p", Ratio: "adaptive", Duration: 5}, {Resolution: "720p", Ratio: "16:9"},
		{Resolution: "720p", Ratio: "16:9", Duration: 31}, {Resolution: "480p", Ratio: "16:9", Duration: 5, Draft: new(true)},
	} {
		if _, err := quote(o); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("unknown Flash price admitted", o, err)
		}
	}
	if _, err := quotePublic("https://ark.eu-west-1.example.test", m, core.MediaRequest{Model: flashID, Options: core.MediaOptions{Resolution: "720p", Ratio: "16:9", Duration: 5}}, time.Now()); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unreviewed region priced", err)
	}
}

func TestSeedance25SeriesLayoutKeepsModelLimitsApart(t *testing.T) {
	body, err := os.ReadFile("testdata/video-api/seedance-2-5-series-parameters.txt")
	if err != nil {
		t.Fatal(err)
	}
	type limits struct {
		resolutions                   string
		images, videos, audios, files int
	}
	read := func(document string) map[string]limits {
		entries, err := interpretVideoAPI(videoAPISource, document)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]limits{}
		for _, e := range entries {
			f := e.OperationalFields
			var resolutions []string
			var images, videos, audios, duration, videoDuration, audioDuration map[string]int
			var files int
			json.Unmarshal(f["output.resolutions"], &resolutions)
			json.Unmarshal(f["workflow.references.image.count"], &images)
			json.Unmarshal(f["workflow.references.video.count"], &videos)
			json.Unmarshal(f["workflow.references.audio.count"], &audios)
			json.Unmarshal(f["reference_media_limits.max_files"], &files)
			json.Unmarshal(f["output.duration"], &duration)
			json.Unmarshal(f["workflow.references.video.duration"], &videoDuration)
			json.Unmarshal(f["workflow.references.audio.duration"], &audioDuration)
			if duration["min"] != 4 || duration["max"] != 30 || duration["default"] != -1 || videoDuration["min"] != 2 || videoDuration["max"] != 30 || videoDuration["max_total"] != 30 || audioDuration["max"] != 30 || audioDuration["max_total"] != 30 || string(f["workflow.first_frame.ratios"]) != `["adaptive"]` {
				t.Fatal("series fact missing", e.ID, duration, videoDuration, audioDuration)
			}
			got[e.ID] = limits{strings.Join(resolutions, ","), images["max"], videos["max"], audios["max"], files}
		}
		return got
	}
	base := limits{"1080p,480p,720p", 30, 10, 10, 50}
	if got := read(string(body)); !reflect.DeepEqual(got, map[string]limits{"dreamina-seedance-2-5-260628": base, flashID: {"480p,720p", 10, 5, 5, 20}}) {
		t.Fatal(got)
	}
	// Flash never borrows a base 2.5 limit when its own line disappears.
	noCounts := strings.Replace(string(body), "0-10 reference images, 0-5 reference videos, and 0-5 reference audio clips", "reference images, videos and audio clips", 1)
	if got := read(noCounts); got["dreamina-seedance-2-5-260628"] != base || got[flashID] != (limits{"480p,720p", 0, 0, 0, 0}) {
		t.Fatal("limits crossed between models", got)
	}
	// Shared series text alone never verifies an unnamed snapshot.
	if got := read(strings.ReplaceAll(string(body), "Dreamina Seedance 2.5 flash", "Dreamina Seedance 2.5 flash-future")); len(got) != 1 || got["dreamina-seedance-2-5-260628"] != base {
		t.Fatal("unreviewed snapshot mapped", got)
	}
	// The current published contract fully covers both native descriptors.
	_, text, err := catalogsync.Parse(videoAPISource, body)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := interpretVideoAPI(videoAPISource, text)
	if err != nil {
		t.Fatal(err)
	}
	ob := catalogsync.Observation{Source: videoAPISource, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Text: text, Entries: entries}
	c := catalogsync.AssessCompleteness(catalogsync.State{}, catalogsync.Capture{Models: []core.MediaModel{describeModel("dreamina-seedance-2-5-260628"), describeModel(flashID)}, VerificationSources: Definition().CatalogVerificationSources}, []catalogsync.Observation{ob}, nil)
	for _, model := range c.Models {
		if model.Status != "complete" {
			t.Fatalf("%s: %s %+v", model.Model, model.Status, model.Fields)
		}
	}
}
