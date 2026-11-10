package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const qwen31TestEndpoint = "https://fixture-workspace.ap-southeast-1.maas.aliyuncs.com/api/v1"

func qwen31TestRequest(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, Prompt: "Fixture hello.", Options: core.MediaOptions{Format: "wav"}}
	if id != qwen31Next {
		r.Options.Voice = "longanhuan_v3.1"
	}
	return r
}

func qwen31TestEvidence() core.InputMediaEvidence {
	return core.InputMediaEvidence{Type: "audio", ContentType: "audio/wav", SizeBytes: 100, Duration: 1, SHA256: strings.Repeat("a", 64)}
}

func qwen31TestResponse() qwen31Response {
	var response qwen31Response
	response.RequestID = "fixture-request"
	response.Output.FinishReason = "stop"
	response.Output.Audio.ID = "fixture-audio"
	response.Output.Audio.URL = "https://media.example.test/result.wav?signature=fixture"
	response.Output.Audio.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
	return response
}

func TestQwen31ExactProfiles(t *testing.T) {
	for _, m := range qwen31Models() {
		got, ok := describeDashModel(m.ID, m.ID)
		if !ok || got.MediaType != "speech" || !reflect.DeepEqual(got.Formats, []string{"wav", "mp3"}) {
			t.Fatalf("profile missing: %+v %v", got, ok)
		}
		if m.ID == qwen31Next && (got.RequiresVoice || got.SupportsVoice || !got.NoLanguage || !got.NoInstructions || len(got.Voices) != 0 || got.MaxPromptCharacters != 3000 || len(got.ReferenceModes) != 1 || got.ReferenceModes[0].MaxAudios != 3) {
			t.Fatalf("Next inherited a voice contract: %+v", got)
		}
	}
	for _, id := range []string{"qwen-audio-3.1-tts-next-latest", "qwen-audio-3.1-tts-flash-preview", "QWEN-AUDIO-3.1-TTS-PLUS", "qwen-audio-3.1-realtime-flash"} {
		if isQwen31(id) {
			t.Fatal("unreviewed alias accepted", id)
		}
		if _, ok := describeDashModel(id, id); ok {
			t.Fatal("unreviewed profile inferred", id)
		}
	}
}

func TestQwen31NativeGoldenPayloads(t *testing.T) {
	for _, tc := range []struct {
		id, prompt, want string
		references       bool
	}{
		{"qwen-audio-3.1-tts-flash", "Fixture hello.", `{"model":"qwen-audio-3.1-tts-flash","input":{"text":"Fixture hello.","voice":"longanhuan_v3.1","format":"wav","sample_rate":24000,"instruction":"Speak gently.","language_hints":["en"]}}`, false},
		{"qwen-audio-3.1-tts-plus", "Fixture hello.", `{"model":"qwen-audio-3.1-tts-plus","input":{"text":"Fixture hello.","voice":"longanhuan_v3.1","format":"wav","sample_rate":24000,"instruction":"Speak gently.","language_hints":["en"]}}`, false},
		{qwen31Next, "Synthetic rain.", `{"model":"qwen-audio-3.1-tts-next","input":{"text_prompt":"Synthetic rain.","format":"wav","sample_rate":48000,"channels":2}}`, false},
		{qwen31Next, "@voice2 answers @voice1.", `{"model":"qwen-audio-3.1-tts-next","input":{"text_prompt":"@voice2 answers @voice1.","references":[{"audio_url":"https://media.example.test/first.wav"},{"audio_url":"https://media.example.test/second.wav"}],"format":"wav","sample_rate":48000,"channels":2}}`, true},
	} {
		t.Run(tc.id+tc.prompt, func(t *testing.T) {
			request := qwen31TestRequest(tc.id)
			request.Prompt = tc.prompt
			if tc.id != qwen31Next {
				request.Options.Instructions, request.Options.Language = "Speak gently.", "en"
			}
			if tc.references {
				request.Options.AudioURLs = []string{"https://media.example.test/first.wav", "https://media.example.test/second.wav"}
				request.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
				for _, raw := range request.Options.AudioURLs {
					request.Options.ReferenceMediaEvidence[raw] = qwen31TestEvidence()
				}
			}
			calls := 0
			client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.String() != qwen31TestEndpoint+qwen31Path || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("X-DashScope-Async") != "" || r.Header.Get("X-DashScope-SSE") != "" {
					t.Fatalf("unexpected native request: %s %s", r.Method, r.URL)
				}
				var got, want any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("wire contract changed: got=%v want=%v error=%v", got, want, err)
				}
				body, _ := json.Marshal(qwen31TestResponse())
				return dashJSONResponse(200, string(body)), nil
			})}
			adapter, err := New("fixture-key", qwen31TestEndpoint, "", "", client)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Generate(context.Background(), request)
			if err != nil || calls != 1 || len(result.Artifacts) != 1 || result.Artifacts[0].Format != "wav" || result.Artifacts[0].SourceReference == "" || result.Artifacts[0].URL != "" || result.ResultURL != "" {
				t.Fatalf("invalid immediate result: %+v calls=%d err=%v", result, calls, err)
			}
		})
	}
}

func TestQwen31RejectsUnsupportedInputsBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*core.MediaRequest)
	}{
		{"empty", func(r *core.MediaRequest) { r.Prompt = " " }},
		{"over-prompt-limit", func(r *core.MediaRequest) { r.Prompt = strings.Repeat("界", 3001) }},
		{"voice-alias", func(r *core.MediaRequest) { r.Options.Voice = "Cherry" }},
		{"instructions", func(r *core.MediaRequest) { r.Options.Instructions = "old voice instructions" }},
		{"raw-reference-override", func(r *core.MediaRequest) {
			r.Options.Parameters = map[string]any{"references": []any{map[string]any{"audio_url": "https://media.example.test/a.wav", "audio_data": "data:audio/wav;base64,AAAA"}}}
		}},
		{"data-uri", func(r *core.MediaRequest) { r.Options.AudioURL = "data:audio/wav;base64,AAAA" }},
		{"two-reference-sources", func(r *core.MediaRequest) {
			r.Options.AudioURL = "https://media.example.test/one.wav"
			r.Options.AudioURLs = []string{"https://media.example.test/two.wav"}
		}},
		{"four-references", func(r *core.MediaRequest) { r.Options.AudioURLs = []string{"a", "b", "c", "d"} }},
		{"missing-slot", func(r *core.MediaRequest) { r.Prompt = "@voice1 says hello." }},
		{"invalid-slot", func(r *core.MediaRequest) { r.Prompt = "@voice01 @voice4" }},
		{"output-opus", func(r *core.MediaRequest) { r.Options.Format = "opus" }},
		{"output-pcm", func(r *core.MediaRequest) { r.Options.Format = "pcm" }},
		{"next-seed-range-unknown", func(r *core.MediaRequest) { n := int64(1); r.Options.Seed = &n }},
		{"unpreserved-setting", func(r *core.MediaRequest) { b := true; r.Options.CameraFixed = &b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := qwen31TestRequest(qwen31Next)
			tc.edit(&r)
			calls := 0
			a := &Adapter{baseURL: qwen31TestEndpoint, client: &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })}}
			if _, err := a.Generate(context.Background(), r); err == nil || calls != 0 {
				t.Fatalf("invalid input submitted: calls=%d err=%v", calls, err)
			}
		})
	}
	for _, ref := range []qwen31Reference{{}, {AudioURL: "https://media.example.test/a.wav", AudioData: "data:audio/wav;base64,AAAA"}, {AudioData: "data:audio/wav;base64,AAAA"}} {
		if ref.validURLOnly() {
			t.Fatal("unpreserved data reference accepted", ref)
		}
	}
}

func TestQwen31InspectedReferenceLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*core.InputMediaEvidence)
		ok   bool
	}{
		{"maximum", func(e *core.InputMediaEvidence) { e.Duration = 30; e.SizeBytes = 10_000_000 }, true},
		{"too-long", func(e *core.InputMediaEvidence) { e.Duration = 30.001 }, false},
		{"unknown-duration", func(e *core.InputMediaEvidence) { e.Duration = 0 }, false},
		{"too-large", func(e *core.InputMediaEvidence) { e.SizeBytes = 10_000_001 }, false},
		{"raw-pcm", func(e *core.InputMediaEvidence) { e.ContentType = "audio/pcm" }, false},
		{"ogg-opus", func(e *core.InputMediaEvidence) { e.ContentType = "audio/ogg"; e.AudioCodecs = []string{"opus"} }, true},
		{"ogg-vorbis", func(e *core.InputMediaEvidence) { e.ContentType = "audio/ogg"; e.AudioCodecs = []string{"vorbis"} }, false},
		{"unbound-bytes", func(e *core.InputMediaEvidence) { e.SHA256 = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := qwen31TestRequest(qwen31Next)
			r.Prompt = "@voice1 says hello."
			r.Options.AudioURL = "https://media.example.test/a.wav"
			e := qwen31TestEvidence()
			tc.edit(&e)
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.AudioURL: e}
			if err := qwen31Validate(qwen31TestEndpoint, r); (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
}

func TestQwen31RegionalAndVoiceGuards(t *testing.T) {
	for _, endpoint := range []string{"https://dashscope-intl.aliyuncs.com/api/v1", "https://fixture.eu-central-1.maas.aliyuncs.com/api/v1", "https://fixture.cn-beijing.maas.aliyuncs.com.evil.test/api/v1", "https://user@fixture.cn-beijing.maas.aliyuncs.com/api/v1", qwen31TestEndpoint + "?override=1"} {
		if qwen31Validate(endpoint, qwen31TestRequest(qwen31Next)) == nil {
			t.Fatal("unreviewed endpoint accepted", endpoint)
		}
	}
	for _, endpoint := range []string{qwen31TestEndpoint, "https://fixture.cn-beijing.maas.aliyuncs.com/api/v1"} {
		for _, id := range []string{"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash"} {
			r := qwen31TestRequest(id)
			if err := qwen31Validate(endpoint, r); err != nil {
				t.Fatal(err)
			}
			for _, voice := range []string{"Cherry", "longanhuan", "yuxiaoyun_v3.1"} {
				r.Options.Voice = voice
				if qwen31Validate(endpoint, r) == nil {
					t.Fatal("voice outside reviewed common subset accepted", voice)
				}
			}
		}
	}
}

func TestQwen31ResponseAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*qwen31Response)
	}{
		{"not-complete", func(r *qwen31Response) { r.Output.FinishReason = "" }},
		{"empty-url", func(r *qwen31Response) { r.Output.Audio.URL = "" }},
		{"credentials-url", func(r *qwen31Response) { r.Output.Audio.URL = "https://key@media.example.test/a.wav" }},
		{"private-url", func(r *qwen31Response) { r.Output.Audio.URL = "https://127.0.0.1/a.wav" }},
		{"expired", func(r *qwen31Response) { r.Output.Audio.ExpiresAt = 1 }},
		{"expiry-missing", func(r *qwen31Response) { r.Output.Audio.ExpiresAt = 0 }},
		{"unexpected-inline", func(r *qwen31Response) { r.Output.Audio.Data = "AAAA" }},
		{"native-error-with-url", func(r *qwen31Response) { r.Code = "AccessDenied" }},
		{"missing-identity", func(r *qwen31Response) { r.RequestID = ""; r.Output.Audio.ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := qwen31TestResponse()
			tc.edit(&response)
			if _, err := qwen31Result(qwen31TestRequest(qwen31Next), response, time.Now()); err == nil || !strings.Contains(err.Error(), "not retried") {
				t.Fatal("accepted malformed response or lost uncertain-acceptance guidance", err)
			}
		})
	}
	for _, status := range []int{400, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			a := &Adapter{baseURL: qwen31TestEndpoint, client: &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return dashJSONResponse(status, `{"code":"CLIENT_ERROR","message":"private-token private-url"}`), nil
			})}}
			_, err := a.Generate(context.Background(), qwen31TestRequest(qwen31Next))
			if err == nil || calls != 1 || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-url") {
				t.Fatal(calls, err)
			}
		})
	}
	for _, body := range []string{`{"output":`, `{} {}`, strings.Repeat(" ", (2<<20)+1)} {
		calls := 0
		a := &Adapter{baseURL: qwen31TestEndpoint, client: &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return dashJSONResponse(200, body), nil })}}
		if _, err := a.Generate(context.Background(), qwen31TestRequest(qwen31Next)); err == nil || calls != 1 {
			t.Fatal("malformed response replayed or accepted", calls, err)
		}
	}
	calls := 0
	a := &Adapter{baseURL: qwen31TestEndpoint, client: &http.Client{Transport: dashRoundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })}}
	if _, err := a.Generate(context.Background(), qwen31TestRequest(qwen31Next)); err == nil || calls != 1 {
		t.Fatal("uncertain transport outcome replayed", calls, err)
	}
}

func TestQwen31ImmutableBillingDimensions(t *testing.T) {
	for _, tc := range []struct {
		id, endpoint string
		want         money.USD
	}{
		{"qwen-audio-3.1-tts-flash", qwen31TestEndpoint, 60}, // Components independently round upward to USD millionths.
		{"qwen-audio-3.1-tts-flash", "https://fixture.cn-beijing.maas.aliyuncs.com/api/v1", 54},
		{"qwen-audio-3.1-tts-plus", qwen31TestEndpoint, 120},
		{"qwen-audio-3.1-tts-plus", "https://fixture.cn-beijing.maas.aliyuncs.com/api/v1", 80},
	} {
		t.Run(tc.id+tc.endpoint, func(t *testing.T) {
			r := qwen31TestRequest(tc.id)
			r.Prompt = "你好"
			m, _ := describeDashModel(r.Model, r.Model)
			cost, err := quotePublic(tc.endpoint, m, r, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			usage := qwen31Usage(tc.id, map[string]any{"input_tokens": float64(12), "output_tokens": float64(30), "total_tokens": float64(42), "characters": float64(4), "duration": float64(120)})
			if !cost.MeterUsage(*usage) || cost.MeteredUSD == nil || *cost.MeteredUSD != tc.want || cost.ActualUSD != nil {
				t.Fatal("wrong immutable billing dimension or rounding", cost)
			}
			before := *cost.MeteredUSD
			if !cost.MeterUsage(*usage) || *cost.MeteredUSD != before {
				t.Fatal("usage replay added a second charge")
			}
		})
	}
	for _, id := range []string{"qwen-audio-3.1-tts-flash", qwen31Next} {
		r := qwen31TestRequest(id)
		m, _ := describeDashModel(id, id)
		cost, err := quotePublic(qwen31TestEndpoint, m, r, time.Now())
		if err != nil || !cost.QuantityUnknown || !core.HasVerifiedQuantityUnknownTariff(cost) || len(cost.Pricing.Components) != 2 {
			t.Fatal("lost verified unknown-quantity tariff", cost, err)
		}
		for _, raw := range []map[string]any{{"duration": float64(120)}, {"input_tokens": float64(1)}, {"input_tokens": float64(-1), "output_tokens": float64(2)}, {"input_tokens": 1.5, "output_tokens": float64(2)}} {
			usage := qwen31Usage(id, raw)
			if cost.MeterUsage(*usage) || cost.MeteredUSD != nil || len(usage.Raw) == 0 {
				t.Fatal("invented token charge from incomplete evidence", cost, usage)
			}
		}
	}
	plus := qwen31Usage("qwen-audio-3.1-tts-plus", map[string]any{"input_tokens": float64(1), "output_tokens": float64(2)})
	if len(plus.Metrics) != 0 {
		t.Fatal("Plus token counts substituted for characters")
	}
	for _, endpoint := range []string{"https://dashscope-intl.aliyuncs.com/api/v1", "https://fixture.us-east-1.maas.aliyuncs.com/api/v1"} {
		r := qwen31TestRequest(qwen31Next)
		m, _ := describeDashModel(r.Model, r.Model)
		if _, err := quotePublic(endpoint, m, r, time.Now()); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("unknown regional price guessed", err)
		}
	}
}

func TestQwen31AcceptedReceiptPreservesUsageAndSource(t *testing.T) {
	for _, id := range []string{"qwen-audio-3.1-tts-flash", "qwen-audio-3.1-tts-plus", qwen31Next} {
		r := qwen31TestRequest(id)
		response := qwen31TestResponse()
		response.Usage = map[string]any{"input_tokens": float64(12), "output_tokens": float64(30), "characters": float64(4), "duration": float64(1)}
		accepted, err := qwen31Result(r, response, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		task := core.Task{ID: "receipt-task", OwnerID: "owner", ProviderOwnerID: "owner", StorageOwnerID: "owner", Provider: "alibaba", ProviderIdentity: "connection-revision", Model: id, MediaType: "speech"}
		raw, digest, err := core.EncodeAcceptedReceipt(task, accepted)
		if err != nil {
			t.Fatal(err)
		}
		recovered, err := core.DecodeAcceptedReceipt(task, raw, digest)
		if err != nil || !reflect.DeepEqual(recovered, &accepted) {
			t.Fatal("receipt changed source or accounting", recovered, err)
		}
		task.ProviderIdentity = "another-revision"
		if _, err := core.DecodeAcceptedReceipt(task, raw, digest); err == nil {
			t.Fatal("receipt accepted across connection revisions")
		}
	}
}

func TestQwen31DoesNotChangeLegacySpeechRoute(t *testing.T) {
	for _, tc := range []struct{ id, path string }{{"qwen3-tts-flash", ttsPath}, {"qwen-audio-3.0-tts-plus", qwen31Path}, {"cosyvoice-v3-flash", qwen31Path}} {
		calls := 0
		a := &Adapter{baseURL: "https://legacy.example.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != tc.path {
				t.Fatal("legacy endpoint changed", tc.id, r.URL.Path)
			}
			return dashJSONResponse(200, `{"request_id":"legacy-request","output":{"audio":{"id":"legacy-audio","url":"https://media.example.test/legacy.wav"}}}`), nil
		})}}
		if _, err := a.Generate(context.Background(), core.MediaRequest{Model: tc.id, Prompt: "hello", Options: core.MediaOptions{Voice: "Cherry"}}); err != nil || calls != 1 {
			t.Fatal(tc.id, calls, err)
		}
	}
}
