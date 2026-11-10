package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

const pixverseFixtureEndpoint = "https://workspace-fixture.ap-southeast-1.maas.aliyuncs.com/api/v1"

type pixverseFixtureCase struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
}
type pixverseFixtureCorpus struct {
	SizeTiers map[string][]string   `json:"size_tiers"`
	Valid     []pixverseFixtureCase `json:"valid_payloads"`
	Invalid   []pixverseFixtureCase `json:"invalid_payloads"`
	Billing   []struct {
		ID, Model, Region, Resolution string
		Audio                         bool
		Requested                     int    `json:"requested_seconds"`
		Reported                      int    `json:"reported_seconds"`
		Expected                      string `json:"expected_reservation_usd"`
		Metered                       string `json:"expected_metered_usd"`
	} `json:"billing_cases"`
	Scenarios []struct {
		ID, Model string
		Request   json.RawMessage
		Outcome   string `json:"expected_outcome"`
		Events    []struct {
			Method, Scheme, Host, Path string
			Status                     int               `json:"status_code"`
			Headers                    map[string]string `json:"expected_headers"`
			Body                       json.RawMessage
			BodyText                   string `json:"body_text"`
			TransportError             string `json:"transport_error"`
		}
	} `json:"fake_http_scenarios"`
	Connections []struct {
		ID         string
		BaseURL    string `json:"base_url"`
		Activation string `json:"activation_evidence"`
		KeyRegion  string `json:"key_region"`
		Expected   string
	} `json:"connection_guard_cases"`
	Media []struct {
		ID            string
		ContentType   string `json:"content_type"`
		Width, Height int
		Size          int64 `json:"size_bytes"`
		Expected      string
	} `json:"media_evidence_cases"`
}

func readPixverseFixtures(t *testing.T) pixverseFixtureCorpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pixverse-v6", "contracts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus pixverseFixtureCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Valid) != 119 || len(corpus.Invalid) != 113 || len(corpus.Billing) != 32 || len(corpus.Scenarios) != 32 {
		t.Fatal("incomplete reviewed corpus")
	}
	return corpus
}

// Convert the independent wire specimens to Gateway input. Invalid JSON option
// types exercise encoding/json's typed boundary, not an invented adapter API.
// Geometry comes from fixture evidence, not the implementation's lookup table.
func pixverseFixtureRequest(raw json.RawMessage, corpus pixverseFixtureCorpus) (core.MediaRequest, error) {
	var wire struct {
		Model      string
		Input      map[string]json.RawMessage
		Parameters map[string]any
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return core.MediaRequest{}, err
	}
	r := core.MediaRequest{Model: wire.Model}
	if err := json.Unmarshal(wire.Input["prompt"], &r.Prompt); len(wire.Input["prompt"]) > 0 && err != nil {
		return r, err
	}
	options := map[string]any{}
	extras := map[string]any{}
	for key, value := range wire.Parameters {
		switch key {
		case "duration", "seed", "watermark":
			options[key] = value
		case "audio":
			options["generate_audio"] = value
		case "shot_type":
			extras[key] = value
		case "size", "resolution":
		default:
			extras[key] = value
		}
	}
	resolution, hasResolution := wire.Parameters["resolution"]
	size, hasSize := wire.Parameters["size"]
	if hasSize {
		options["resolution"] = "unreviewed-size"
		if s, ok := size.(string); ok {
			ratios := []string{"16:9", "4:3", "1:1", "3:4", "9:16", "3:2", "2:3", "21:9"}
			for tier, sizes := range corpus.SizeTiers {
				for i, candidate := range sizes {
					if candidate == s {
						options["resolution"], options["ratio"] = strings.ToLower(tier), ratios[i]
					}
				}
			}
		}
		if hasResolution {
			extras["resolution"] = resolution
		}
	} else if hasResolution {
		if s, ok := resolution.(string); ok {
			options["resolution"] = strings.ToLower(s)
		} else {
			options["resolution"] = resolution
		}
	}
	var media []map[string]any
	if len(wire.Input["media"]) > 0 {
		if err := json.Unmarshal(wire.Input["media"], &media); err != nil {
			return r, err
		}
	}
	var images, videos, audios []any
	var names []any
	named := false
	roles := map[string]string{}
	for _, item := range media {
		kind, _ := item["type"].(string)
		switch kind {
		case "audio_url":
			audios = append(audios, item["url"])
		case "video_url":
			videos = append(videos, item["url"])
		default:
			images = append(images, item["url"])
			if wire.Model == pixverseKF2V {
				roles[fmt.Sprintf("image_%d", len(images))] = kind
			} else if kind != "image_url" {
				extras["unsupported_media_type"] = kind
			}
			if name, ok := item["ref_name"]; ok {
				named = true
				names = append(names, name)
			} else {
				names = append(names, "")
			}
		}
	}
	if named {
		extras["reference_names"] = names
	}
	if len(images) > 0 {
		options["image_urls"] = images
	}
	if len(videos) > 0 {
		options["video_urls"] = videos
	}
	if len(audios) > 0 {
		options["audio_urls"] = audios
	}
	if len(roles) > 0 {
		options["reference_roles"] = roles
	}
	for key, value := range wire.Input {
		if key == "prompt" || key == "media" {
			continue
		}
		if key == "negative_prompt" {
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return r, err
			}
			options[key] = s
		} else {
			extras["unsupported_input_"+key] = string(value)
		}
	}
	options["parameters"] = extras
	encoded, err := json.Marshal(options)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(encoded, &r.Options); err != nil {
		return r, err
	}
	return r, nil
}

func pixverseGolden(raw []byte) map[string]any {
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	parameters := result["parameters"].(map[string]any)
	if _, present := parameters["audio"]; !present {
		parameters["audio"] = false
	}
	return result
}

func pixverseFixtureAdapter(t *testing.T, client *http.Client) *Adapter {
	t.Helper()
	a, err := New("fixture-only", pixverseFixtureEndpoint, "", "", client)
	if err != nil {
		t.Fatal(err)
	}
	// Test-only attestation. This is not a check of any real provider account.
	a.pixverseAccountReviewed = true
	return a
}

func TestPixverseExactPayloadCorpus(t *testing.T) {
	corpus := readPixverseFixtures(t)
	for _, test := range corpus.Valid {
		t.Run(test.ID, func(t *testing.T) {
			r, err := pixverseFixtureRequest(test.Payload, corpus)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := pixversePayload(r)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pixverseGolden(actual), pixverseGolden(test.Payload)) {
				t.Fatalf("wire mismatch:\n%s\nwant:\n%s", actual, test.Payload)
			}
		})
	}
	for _, test := range corpus.Invalid {
		t.Run(test.ID, func(t *testing.T) {
			posts := 0
			a := pixverseFixtureAdapter(t, &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) { posts++; return nil, errors.New("must not submit") })})
			r, err := pixverseFixtureRequest(test.Payload, corpus)
			if err == nil {
				_, err = a.Generate(context.Background(), r)
			}
			if err == nil || posts != 0 {
				t.Fatalf("invalid request accepted or submitted: posts=%d err=%v", posts, err)
			}
		})
	}
}

func TestPixverseGatewayOptionsAreNotSilentlyDropped(t *testing.T) {
	corpus := readPixverseFixtures(t)
	mutations := []func(*core.MediaOptions){
		func(o *core.MediaOptions) { o.AudioURL = "https://media.example.invalid/a.wav" },
		func(o *core.MediaOptions) { o.VideoURL = "https://media.example.invalid/v.mp4" },
		func(o *core.MediaOptions) { o.ReferenceVoices = map[string]string{"source": "voice"} },
		func(o *core.MediaOptions) { b := false; o.Draft = &b },
		func(o *core.MediaOptions) { b := false; o.CameraFixed = &b },
		func(o *core.MediaOptions) { b := false; o.ReturnLastFrame = &b },
		func(o *core.MediaOptions) { o.NegativePrompt = "do not discard" },
		func(o *core.MediaOptions) { o.Parameters = map[string]any{"unknown": true} },
		func(o *core.MediaOptions) { n := int64(-1); o.Seed = &n },
		func(o *core.MediaOptions) { o.OutputCount = 2 },
	}
	for _, id := range pixverseIDs {
		for index, mutate := range mutations {
			r := pixverseRequestForModel(t, corpus, id)
			mutate(&r.Options)
			if _, err := pixversePayload(r); err == nil {
				t.Fatalf("unsupported option accepted: %s %d", id, index)
			}
		}
	}
}

func pixverseRequestForModel(t *testing.T, corpus pixverseFixtureCorpus, id string) core.MediaRequest {
	t.Helper()
	for _, test := range corpus.Valid {
		r, err := pixverseFixtureRequest(test.Payload, corpus)
		if err != nil {
			t.Fatal(err)
		}
		if r.Model == id && r.Options.Resolution == "720p" && (r.Options.GenerateAudio == nil || !*r.Options.GenerateAudio) {
			return r
		}
	}
	t.Fatal("missing representative model")
	return core.MediaRequest{}
}

func TestPixverseExactRegionalBillingCorpus(t *testing.T) {
	corpus := readPixverseFixtures(t)
	valid := map[string]json.RawMessage{}
	for _, test := range corpus.Valid {
		valid[test.ID] = test.Payload
	}
	for _, test := range corpus.Billing {
		t.Run(test.ID, func(t *testing.T) {
			r, err := pixverseFixtureRequest(valid[test.ID], corpus)
			if err != nil {
				t.Fatal(err)
			}
			model, _ := pixverseModel(test.Model)
			cost, err := quotePublic(pixverseFixtureEndpoint, model, r, time.Unix(0, 0))
			if err != nil || cost.ExpectedUSD.String() != test.Expected || cost.Pricing.Region != "Singapore" {
				t.Fatalf("wrong quote: %+v %v", cost, err)
			}
			size, _ := pixverseSize(r.Options.Resolution, "16:9")
			if r.Model == pixverseI2V && r.Options.Resolution == "720p" {
				size = "992*944"
			}
			if r.Model == pixverseKF2V && r.Options.Resolution == "720p" {
				size = "1280*960"
			}
			raw := map[string]any{"duration": float64(test.Reported), "video_count": float64(1), "fps": float64(24), "audio": test.Audio, "SR": strings.TrimSuffix(test.Resolution, "P"), "size": size}
			usage := pixverseUsage(raw, r)
			if usage.CostStatus != "metered" || !cost.MeterUsage(*usage) || cost.MeteredUSD == nil || cost.MeteredUSD.String() != test.Metered {
				t.Fatalf("wrong reported-duration metering: %+v %+v", usage, cost)
			}
			for _, endpoint := range []string{"https://dashscope-intl.aliyuncs.com/api/v1", "https://dashscope.aliyuncs.com/api/v1", "https://workspace-fixture.ap-southeast-1.maas.aliyuncs.com.evil.invalid/api/v1"} {
				if _, err := quotePublic(endpoint, model, r, time.Unix(0, 0)); !errors.Is(err, core.ErrPricingUnavailable) {
					t.Fatal("unproved endpoint enabled a tariff", endpoint, err)
				}
			}
		})
	}
}

func TestPixverseConnectionAndUploadGuards(t *testing.T) {
	corpus := readPixverseFixtures(t)
	for _, test := range corpus.Connections {
		t.Run(test.ID, func(t *testing.T) {
			a, err := New("fixture-only", test.BaseURL, "", "", &http.Client{})
			if err != nil {
				t.Fatal(err)
			}
			a.pixverseAccountReviewed = test.Activation != "unverified" && test.KeyRegion != "Beijing"
			err = a.validatePixverseConnection()
			if (err == nil) != (test.Expected == "allow_fake_transport_only") {
				t.Fatalf("guard mismatch: %v", err)
			}
		})
	}
	// The same explicit file contract guards owned uploads and trusted inspected
	// evidence at native submission. No client URL is fetched during validation.
	for _, id := range []string{pixverseI2V, pixverseKF2V, pixverseR2V} {
		model, _ := pixverseModel(id)
		for _, test := range corpus.Media {
			r := pixverseRequestForModel(t, corpus, id)
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.ImageURLs[0]: {Type: "image", ContentType: test.ContentType, Width: test.Width, Height: test.Height, SizeBytes: test.Size}}
			_, nativeErr := pixversePayload(r)
			file := referencevalidation.File{Type: "image", Name: "synthetic", Info: mediafile.Info{ContentType: test.ContentType, Width: test.Width, Height: test.Height, Size: test.Size}}
			uploadErr := referencevalidation.ValidateFiles(model, []referencevalidation.File{file}, false)
			want := test.Expected == "accept"
			if (nativeErr == nil) != want || (uploadErr == nil) != want {
				t.Fatalf("upload/dispatch mismatch %s %s: %v %v", id, test.ID, uploadErr, nativeErr)
			}
		}
	}
	a := pixverseFixtureAdapter(t, &http.Client{})
	a.pixverseAccountReviewed = false
	r := pixverseRequestForModel(t, corpus, pixverseT2V)
	if _, err := a.Generate(context.Background(), r); !errors.Is(err, core.ErrProviderUnavailable) {
		t.Fatal("missing account guard", err)
	}
	model, _ := pixverseModel(r.Model)
	if _, _, err := a.Quote(context.Background(), model, r); !errors.Is(err, core.ErrProviderUnavailable) {
		t.Fatal("account guard bypassed by quote", err)
	}
}

// These assert the HTTP legs of prepared transcripts. Actual archive/restart
// behavior is tested by the worker suite, not simulated as an adapter success.
func TestPixverseHTTPTranscriptWireLegs(t *testing.T) {
	corpus := readPixverseFixtures(t)
	for _, test := range corpus.Scenarios {
		t.Run(test.ID, func(t *testing.T) {
			r, err := pixverseFixtureRequest(test.Request, corpus)
			if err != nil {
				t.Fatal(err)
			}
			index, posts := 0, 0
			client := &http.Client{Transport: dashRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if index >= len(test.Events) {
					t.Fatal("unexpected extra HTTP request")
				}
				e := test.Events[index]
				index++
				if request.Method == "POST" {
					posts++
				}
				if request.Method != e.Method || request.URL.Scheme != e.Scheme || request.URL.Host != e.Host || request.URL.Path != e.Path {
					t.Fatalf("wrong endpoint: %s %s", request.Method, request.URL)
				}
				for key, expected := range e.Headers {
					if key == "Authorization" {
						expected = "Bearer fixture-only"
					}
					if request.Header.Get(key) != expected {
						t.Fatalf("wrong required header %s", key)
					}
				}
				if request.Method == "POST" {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(pixverseGolden(body), pixverseGolden(test.Request)) {
						t.Fatal("HTTP body differs from golden contract")
					}
				}
				if e.TransportError != "" {
					return nil, errors.New("synthetic transport uncertainty")
				}
				body := string(e.Body)
				if e.BodyText != "" {
					body = e.BodyText
				}
				return dashJSONResponse(e.Status, body), nil
			})}
			a := pixverseFixtureAdapter(t, client)
			task, submitErr := a.Generate(context.Background(), r)
			var result core.ProviderResult
			var pollErr error
			if submitErr == nil {
				if task.ID != "fixture-task-335" {
					t.Fatal("native task ID was replaced")
				}
				for index < len(test.Events) {
					// New adapter instance simulates loss of any in-memory cache.
					a = pixverseFixtureAdapter(t, client)
					ctx := core.WithProviderPollRequest(context.Background(), r)
					result, pollErr = a.GetTask(ctx, task.ID)
				}
			}
			if posts != 1 || index != len(test.Events) {
				t.Fatalf("unexpected replay or unconsumed events: posts=%d events=%d/%d", posts, index, len(test.Events))
			}
			switch test.Outcome {
			case "acceptance_uncertain_reconcile", "submit_failure_no_automatic_replay", "invalid_submission_receipt_no_automatic_replay":
				if submitErr == nil {
					t.Fatal("invalid/uncertain submission accepted")
				}
			case "invalid_poll_response_without_resubmission":
				if pollErr == nil {
					t.Fatal("invalid poll accepted")
				}
			case "terminal_without_resubmission":
				last := string(test.Events[len(test.Events)-1].Body)
				if strings.Contains(last, "UNKNOWN") {
					if pollErr == nil || result.Status == core.ProviderFailed {
						t.Fatal("unknown/expired lookup claimed zero-charge terminal failure")
					}
				} else if pollErr != nil || result.Status != core.ProviderFailed {
					t.Fatal("terminal status lost", pollErr)
				}
			case "preserve_receipt_and_guard_unproved_metering":
				if pollErr != nil || result.Status != core.ProviderSucceeded || result.ResultURL == "" || result.Usage == nil || result.Usage.CostStatus != "unavailable" || len(result.Usage.Metrics) != 0 {
					t.Fatalf("unsafe metering or discarded result: %+v %v", result, pollErr)
				}
			default:
				if submitErr != nil || pollErr != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://media.example.invalid/video.mp4" {
					t.Fatalf("successful poll did not preserve the video: %+v %v %v", result, submitErr, pollErr)
				}
			}
		})
	}
}

func TestPixverseGuardDoesNotChangeLegacyWanSubmission(t *testing.T) {
	posts := 0
	a, err := New("fixture-only", "https://dashscope-intl.aliyuncs.com/api/v1", "", "", &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		posts++
		if r.URL.Path != "/api/v1/services/aigc/video-generation/video-synthesis" {
			t.Fatal("legacy route changed")
		}
		// Existing generic contracts do not require a creation task_status.
		return dashJSONResponse(200, `{"output":{"task_id":"legacy-task"}}`), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Generate(context.Background(), core.MediaRequest{Model: "wan2.6-t2v", Prompt: "A bird.", Options: core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9"}})
	if err != nil || task.ID != "legacy-task" || posts != 1 {
		t.Fatalf("legacy generation regressed: %+v %v", task, err)
	}
}

func TestPixverseAccountReviewIsBoundToExactConnection(t *testing.T) {
	d := Definition()
	credentials := Credentials{APIKey: "fixture-only", BaseURL: pixverseFixtureEndpoint}
	reviewedIdentity := d.Identity(credentials)
	cases := []struct {
		name        string
		credentials Credentials
		attestation string
		want        bool
	}{
		{"matching", credentials, reviewedIdentity, true},
		{"missing", credentials, "", false},
		{"global-boolean-is-not-review", credentials, "true", false},
		{"rotated-key", Credentials{APIKey: "rotated-fixture", BaseURL: credentials.BaseURL}, reviewedIdentity, false},
		{"other-workspace", Credentials{APIKey: credentials.APIKey, BaseURL: "https://other-workspace.ap-southeast-1.maas.aliyuncs.com/api/v1"}, reviewedIdentity, false},
		{"second-account", Credentials{APIKey: "second-fixture", BaseURL: "https://second-workspace.ap-southeast-1.maas.aliyuncs.com/api/v1"}, reviewedIdentity, false},
		{"normalized-trailing-slash", Credentials{APIKey: credentials.APIKey, BaseURL: credentials.BaseURL + "/"}, reviewedIdentity, true},
		{"encoded-endpoint", Credentials{APIKey: credentials.APIKey, BaseURL: strings.Replace(credentials.BaseURL, "/api/v1", "/api/%761", 1)}, reviewedIdentity, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p, err := d.Factory(test.credentials, map[string]string{pixverseAccountReviewSetting: test.attestation}, &http.Client{})
			if err != nil {
				t.Fatal(err)
			}
			if got := p.(*Adapter).pixverseReady(); got != test.want {
				t.Fatalf("unscoped account review: got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestPixversePollingRejectsContradictorySuccessWithoutResubmission(t *testing.T) {
	calls := 0
	a := pixverseFixtureAdapter(t, &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/tasks/accepted-pixverse" {
			t.Fatal("unexpected generation replay")
		}
		return dashJSONResponse(200, `{"output":{"task_id":"accepted-pixverse","task_status":"SUCCEEDED","code":"InternalError","video_url":"https://media.example.test/video.mp4"}}`), nil
	})})
	ctx := core.WithProviderPollRequest(context.Background(), core.MediaRequest{Model: pixverseT2V})
	if result, err := a.GetTask(ctx, "accepted-pixverse"); err == nil || result.Status == core.ProviderSucceeded || calls != 1 {
		t.Fatal("contradictory completion accepted", result, err)
	}
}

func TestPixverseUsageCannotMeterUncheckedAliases(t *testing.T) {
	corpus := readPixverseFixtures(t)
	r := pixverseRequestForModel(t, corpus, pixverseT2V)
	model, _ := pixverseModel(r.Model)
	for _, extra := range []map[string]any{
		{"output_video_duration": float64(100)},
		{"video_duration": float64(100), "input_video_duration": float64(100)},
		{"nested": map[string]any{"duration": float64(100), "output_video_duration": float64(100)}},
	} {
		raw := map[string]any{"duration": float64(3), "size": "1280*720", "fps": float64(24), "video_count": float64(1), "audio": false, "SR": "720"}
		for key, value := range extra {
			raw[key] = value
		}
		usage := pixverseUsage(raw, r)
		if len(usage.Metrics) != 1 || usage.Metrics[0].Name != "duration" || usage.Metrics[0].Quantity != 3 {
			t.Fatal("unchecked alias entered metering", usage)
		}
		cost, err := quotePublic(pixverseFixtureEndpoint, model, r, time.Now())
		if err != nil || !cost.MeterUsage(*usage) || cost.MeteredUSD == nil || cost.MeteredUSD.String() != "0.120000" {
			t.Fatal("alias altered accepted tariff metering", cost, err)
		}
		var retained map[string]any
		if err := json.Unmarshal(usage.Raw, &retained); err != nil {
			t.Fatal(err)
		}
		for key := range extra {
			if retained[key] == nil {
				t.Fatal("raw conflicting evidence was discarded")
			}
		}
	}
	raw := map[string]any{"nested": map[string]any{"duration": float64(3)}, "size": "1280*720", "fps": float64(24), "video_count": float64(1), "audio": false, "SR": "720"}
	if usage := pixverseUsage(raw, r); len(usage.Metrics) != 0 || usage.CostStatus != "unavailable" {
		t.Fatal("nested duration replaced missing documented top-level usage")
	}
}
