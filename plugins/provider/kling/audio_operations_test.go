package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func audioFixtureContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
}

func audioFixtureRequest(id string) core.MediaRequest {
	r := core.MediaRequest{TaskID: "gateway_fixture", ProviderIdentity: "fixture-owner-connection", Model: id, Prompt: "Synthetic audio fixture"}
	switch id {
	case TextToAudio:
		r.Options.Parameters = map[string]any{"duration": 3.1}
	case VideoToAudio:
		r.Prompt = ""
		r.Options.VideoURLs = []string{"https://cdn.example/synthetic.mov"}
		r.Options.Parameters = map[string]any{"sound_effect_prompt": "Synthetic wind", "bgm_prompt": "Synthetic rhythm", "asmr_mode": false}
	case TextToSpeech:
		r.Options.Voice = "oversea_male1"
		r.Options.Language = "en"
		r.Options.Parameters = map[string]any{"voice_speed": 1.2}
	case AvatarImageToVideo:
		r.Options.ImageURLs = []string{"https://cdn.example/synthetic.png"}
		r.Options.AudioURLs = []string{"https://cdn.example/synthetic.wav"}
		r.Options.Parameters = map[string]any{"mode": "pro"}
		r.Options.Watermark = ptr(false)
	}
	return r
}

func TestAudioOperationNativeRequests(t *testing.T) {
	tests := []struct {
		id, route string
		want      map[string]any
	}{
		{TextToAudio, "/v1/audio/text-to-audio", map[string]any{"prompt": "Synthetic audio fixture", "duration": 3.1, "external_task_id": "gateway_fixture"}},
		{VideoToAudio, "/v1/audio/video-to-audio", map[string]any{"video_url": "https://cdn.example/synthetic.mov", "sound_effect_prompt": "Synthetic wind", "bgm_prompt": "Synthetic rhythm", "asmr_mode": false, "external_task_id": "gateway_fixture"}},
		{TextToSpeech, "/v1/audio/tts", map[string]any{"text": "Synthetic audio fixture", "voice_id": "oversea_male1", "voice_language": "en", "voice_speed": 1.2}},
		{AvatarImageToVideo, "/v1/videos/avatar/image2video", map[string]any{"image": "https://cdn.example/synthetic.png", "sound_file": "https://cdn.example/synthetic.wav", "prompt": "Synthetic audio fixture", "mode": "pro", "watermark_info": map[string]any{"enabled": false}, "external_task_id": "gateway_fixture"}},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			calls := 0
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "POST" || req.URL.Path != tc.route {
					t.Fatalf("wrong route %s %s", req.Method, req.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(body, tc.want) {
					t.Fatalf("request %#v; want %#v", body, tc.want)
				}
				if tc.id == TextToSpeech {
					return response(200, `{"code":0,"data":{"task_id":"tts_fixture","task_status":"succeed","task_result":{"audios":[{"id":"audio_fixture","url":"https://cdn.example/synthetic.mp3","duration":"3.2"}]}}}`), nil
				}
				return response(200, `{"code":0,"data":{"task_id":"async_fixture","task_status":"submitted"}}`), nil
			}))
			task, err := a.generateAudio(audioFixtureContext(), audioFixtureRequest(tc.id))
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
			if tc.id == TextToSpeech {
				if task.ID != "" || task.ResultURL != "https://cdn.example/synthetic.mp3" || len(task.Artifacts) != 1 || task.Artifacts[0].Format != "mp3" {
					t.Fatalf("synchronous result %#v", task)
				}
			} else if task.ID == "" || task.ID == "async_fixture" {
				t.Fatalf("async result lacks signed operation handle: %#v", task)
			}
		})
	}
}

func TestAudioOperationOptionalFieldsAndDefaults(t *testing.T) {
	for _, id := range []string{VideoToAudio, TextToSpeech, AvatarImageToVideo} {
		t.Run(id, func(t *testing.T) {
			r := audioFixtureRequest(id)
			r.TaskID = ""
			r.Options.Parameters = nil
			r.Options.Watermark = nil
			if id == AvatarImageToVideo {
				r.Prompt = ""
			}
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"external_task_id", "callback_url", "model_name", "duration", "watermark_info", "voice_speed", "sound_effect_prompt", "bgm_prompt", "asmr_mode"} {
					if _, ok := body[key]; ok {
						t.Fatalf("unexpected optional %s: %#v", key, body)
					}
				}
				if id == AvatarImageToVideo && body["mode"] != "std" {
					t.Fatal(body)
				}
				if id == TextToSpeech {
					return response(200, `{"code":0,"data":{"task_id":"tts_fixture","task_status":"succeed","task_result":{"audios":[{"id":"a","url":"https://cdn.example/audio","duration":"2"}]}}}`), nil
				}
				return response(200, `{"code":0,"data":{"task_id":"async_fixture","task_status":"submitted"}}`), nil
			}))
			if _, err := a.generateAudio(audioFixtureContext(), r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAudioOperationAdmission(t *testing.T) {
	tests := []struct {
		name, id string
		edit     func(*core.MediaRequest)
	}{
		{"fractional duration precision", TextToAudio, func(r *core.MediaRequest) { r.Options.Parameters["duration"] = 3.15 }},
		{"duration lower bound", TextToAudio, func(r *core.MediaRequest) { r.Options.Parameters["duration"] = 2.9 }},
		{"duration upper bound", TextToAudio, func(r *core.MediaRequest) { r.Options.Parameters["duration"] = 10.1 }},
		{"missing duration", TextToAudio, func(r *core.MediaRequest) { r.Options.Parameters = nil }},
		{"nonfinite duration", TextToAudio, func(r *core.MediaRequest) { r.Options.Parameters["duration"] = math.NaN() }},
		{"overlong unicode prompt", TextToAudio, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("雨", 201) }},
		{"empty text", TextToSpeech, func(r *core.MediaRequest) { r.Prompt = " " }},
		{"overlong text", TextToSpeech, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("雨", 1001) }},
		{"missing voice", TextToSpeech, func(r *core.MediaRequest) { r.Options.Voice = "" }},
		{"unsupported language", TextToSpeech, func(r *core.MediaRequest) { r.Options.Language = "fr" }},
		{"missing voice language", TextToSpeech, func(r *core.MediaRequest) { r.Options.Language = "" }},
		{"speed precision", TextToSpeech, func(r *core.MediaRequest) { r.Options.Parameters["voice_speed"] = 1.25 }},
		{"speed lower bound", TextToSpeech, func(r *core.MediaRequest) { r.Options.Parameters["voice_speed"] = 0.7 }},
		{"video prompt ambiguity", VideoToAudio, func(r *core.MediaRequest) { r.Prompt = "unsupported text field" }},
		{"video absent", VideoToAudio, func(r *core.MediaRequest) { r.Options.VideoURLs = nil }},
		{"video count", VideoToAudio, func(r *core.MediaRequest) {
			r.Options.VideoURLs = append(r.Options.VideoURLs, "https://cdn.example/second.mp4")
		}},
		{"raw video ID", VideoToAudio, func(r *core.MediaRequest) { r.Options.Parameters["video_id"] = "remote-video" }},
		{"oversized sound prompt", VideoToAudio, func(r *core.MediaRequest) { r.Options.Parameters["sound_effect_prompt"] = strings.Repeat("風", 201) }},
		{"wrong ASMR type", VideoToAudio, func(r *core.MediaRequest) { r.Options.Parameters["asmr_mode"] = "false" }},
		{"video too short", VideoToAudio, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: 2.9}}
		}},
		{"video too long", VideoToAudio, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: 20.1}}
		}},
		{"nonfinite inspected duration", VideoToAudio, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: math.NaN()}}
		}},
		{"avatar missing image", AvatarImageToVideo, func(r *core.MediaRequest) { r.Options.ImageURLs = nil }},
		{"avatar extra audio", AvatarImageToVideo, func(r *core.MediaRequest) {
			r.Options.AudioURLs = append(r.Options.AudioURLs, "https://cdn.example/second.wav")
		}},
		{"raw audio ID", AvatarImageToVideo, func(r *core.MediaRequest) { r.Options.Parameters["audio_id"] = "remote-audio" }},
		{"avatar mode", AvatarImageToVideo, func(r *core.MediaRequest) { r.Options.Parameters["mode"] = "ultra" }},
		{"avatar audio too short", AvatarImageToVideo, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.AudioURLs[0]: {Type: "audio", Duration: 1.9}}
		}},
		{"avatar audio too long", AvatarImageToVideo, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.AudioURLs[0]: {Type: "audio", Duration: 300.1}}
		}},
		{"conflicting singular audio", AvatarImageToVideo, func(r *core.MediaRequest) { r.Options.AudioURL = "https://cdn.example/other.wav" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid request made network call")
				return nil, nil
			}))
			r := audioFixtureRequest(tc.id)
			tc.edit(&r)
			if _, err := a.generateAudio(audioFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("error %v", err)
			}
		})
	}
	for _, id := range []string{TextToAudio, VideoToAudio, TextToSpeech, AvatarImageToVideo} {
		t.Run(id+" rejects ignored controls", func(t *testing.T) {
			for _, edit := range []func(*core.MediaRequest){
				func(r *core.MediaRequest) { r.Options.Duration = 3 }, func(r *core.MediaRequest) { r.Options.Format = "wav" }, func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) }, func(r *core.MediaRequest) { r.Options.Resolution = "720p" }, func(r *core.MediaRequest) { r.Options.Parameters["raw_json"] = map[string]any{} }, func(r *core.MediaRequest) { r.Options.Seed = ptr(int64(1)) },
			} {
				r := audioFixtureRequest(id)
				edit(&r)
				if err := new(Adapter).validateAudioRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatalf("accepted ignored control: %#v, %v", r.Options, err)
				}
			}
		})
	}
}

func TestAudioOperationCapabilities(t *testing.T) {
	for _, m := range audioModels() {
		if !isAudioOperation(m.ID) || len(m.Resolutions) > 0 || len(m.Formats) > 0 || m.SupportsAudio || !m.NoSeed {
			t.Fatalf("invented controls %#v", m)
		}
		r := audioFixtureRequest(m.ID)
		normalized, err := core.NormalizeModelOptions(m, r.Options)
		if err != nil {
			t.Fatalf("%s normalization: %v", m.ID, err)
		}
		r.Options = normalized
		if err := new(Adapter).validateAudioRequest(r); err != nil {
			t.Fatalf("%s normalized admission: %v", m.ID, err)
		}
	}
	video, _ := audioModel(VideoToAudio)
	avatar, _ := audioModel(AvatarImageToVideo)
	if video.ReferenceMediaLimits.Video.MaxBytes != 100<<20 || video.ReferenceMediaLimits.Video.MinDuration != 3 || video.ReferenceMediaLimits.Video.MaxDuration != 20 {
		t.Fatal(video.ReferenceMediaLimits)
	}
	if avatar.ReferenceMediaLimits.Image.MaxBytes != 10<<20 || avatar.ReferenceMediaLimits.Audio.MaxBytes != 5<<20 || avatar.ReferenceMediaLimits.Audio.MaxDuration != 300 {
		t.Fatal(avatar.ReferenceMediaLimits)
	}
	for _, kind := range []string{"audio/mpeg", "audio/wav", "audio/mp4", "audio/aac"} {
		if !slices.Contains(avatar.ReferenceMediaLimits.Audio.ContentTypes, kind) {
			t.Fatalf("documented Avatar sound format missing: %s", kind)
		}
	}
}

func TestAudioOperationPollingAndArtifacts(t *testing.T) {
	for _, id := range []string{TextToAudio, VideoToAudio, AvatarImageToVideo} {
		t.Run(id, func(t *testing.T) {
			for _, status := range []string{"submitted", "processing", "failed", "succeed"} {
				body := `{"task_id":"fixture_remote","task_status":` + fmt.Sprintf("%q", status) + `,"task_status_msg":"unsafe provider content","task_result":`
				switch id {
				case TextToAudio:
					body += `{"audios":[{"id":"a1","url_mp3":"https://cdn.example/a1.mp3","url_wav":"https://cdn.example/a1.wav","duration_mp3":"3.1","duration_wav":"3.12"},{"id":"a2","url_mp3":"https://cdn.example/a2.mp3","url_wav":"https://cdn.example/a2.wav","duration_mp3":"4","duration_wav":"4"}]}`
				case VideoToAudio:
					body += `{"audios":[{"id":"a1","url_mp3":"https://cdn.example/a.mp3","url_wav":"https://cdn.example/a.wav","duration_mp3":"5","duration_wav":"5"}],"videos":[{"id":"v1","url":"https://cdn.example/v.mp4","duration":"5"}]}`
				case AvatarImageToVideo:
					body += `{"videos":[{"id":"v1","url":"https://cdn.example/v.mp4","watermark_url":"https://cdn.example/w.mp4","duration":"8.5"}]}`
				}
				body += `,"final_unit_deduction":"0.25","final_balance_deduction":{"quota":"0.035","list_price":"0.035"}}`
				calls := 0
				a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != "GET" || req.URL.Path != audioPath(id)+"/fixture_remote" || req.URL.RawQuery != "" {
						t.Fatal(req.URL)
					}
					return response(200, `{"code":0,"data":`+body+`}`), nil
				}))
				result, err := a.getAudioTask(audioFixtureContext(), id, "fixture_remote")
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal(calls)
				}
				want := map[string]core.ProviderStatus{"submitted": core.ProviderQueued, "processing": core.ProviderRunning, "failed": core.ProviderFailed, "succeed": core.ProviderSucceeded}[status]
				if result.Status != want {
					t.Fatal(result)
				}
				if status == "failed" && strings.Contains(result.Error, "unsafe") {
					t.Fatal(result.Error)
				}
				if status != "succeed" {
					continue
				}
				count := map[string]int{TextToAudio: 4, VideoToAudio: 3, AvatarImageToVideo: 2}[id]
				if len(result.Artifacts) != count {
					t.Fatalf("artifacts %#v", result.Artifacts)
				}
				if result.Usage == nil || result.Usage.EstimatedCost != nil || result.Usage.Currency != "" || !strings.Contains(string(result.Usage.Raw), `"quota":"0.035"`) {
					t.Fatalf("usage %#v", result.Usage)
				}
				if id == TextToAudio {
					if result.Artifacts[0].GroupID != result.Artifacts[1].GroupID || result.Artifacts[0].Format != "mp3" || result.Artifacts[1].Format != "wav" || result.Artifacts[0].URL == result.Artifacts[1].URL {
						t.Fatal(result.Artifacts)
					}
					for _, metric := range result.Usage.Metrics {
						if metric.Name == "audio_duration" && metric.Quantity != 7.1 {
							t.Fatal(metric)
						}
					}
				}
			}
		})
	}
}

func TestAudioOperationMalformedResults(t *testing.T) {
	tests := []string{
		`null`,
		`{"id":"fixture_remote","status":"succeeded","outputs":[]}`,
		`{"task_id":"other","task_status":"submitted"}`,
		`{"task_id":"fixture_remote","task_status":"succeeded"}`,
		`{"task_id":"fixture_remote","task_status":"succeed","task_result":{}}`,
		`{"task_id":"fixture_remote","task_status":"succeed","task_result":{"audios":[{"id":"a","url":"https://cdn.example/a.mp3","duration":"3"}]}}`,
		`{"task_id":"fixture_remote","task_status":"succeed","task_result":{"audios":[{"id":"a","url_mp3":"https://cdn.example/a.mp3","duration_mp3":"NaN"}]}}`,
		`{"task_id":"fixture_remote","task_status":"succeed","task_result":{"audios":[{"id":"a","url_mp3":"http://cdn.example/a.mp3","duration_mp3":"3"}]}}`,
		`{"task_id":"fixture_remote","task_status":"succeed","task_result":{"audios":[{"id":"a","url_mp3":"https://cdn.example/a.mp3","duration_mp3":"3"},{"id":"a","url_mp3":"https://cdn.example/b.mp3","duration_mp3":"3"}]}}`,
	}
	for i, body := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := normalizeAudioTask(TextToAudio, "fixture_remote", json.RawMessage(body)); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
	for _, status := range []string{"submitted", "processing", "failed"} {
		t.Run("TTS "+status, func(t *testing.T) {
			a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
				return response(200, `{"code":0,"data":{"task_id":"tts_fixture","task_status":`+fmt.Sprintf("%q", status)+`}}`), nil
			}))
			if _, err := a.generateAudio(audioFixtureContext(), audioFixtureRequest(TextToSpeech)); err == nil {
				t.Fatal("accepted unfinished synchronous TTS")
			}
		})
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"code":1201,"message":"unsafe content"}`), nil
	}))
	if _, err := a.generateAudio(audioFixtureContext(), audioFixtureRequest(TextToAudio)); err == nil || strings.Contains(err.Error(), "unsafe") {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if _, err := a.getAudioTask(audioFixtureContext(), TextToSpeech, "tts_fixture"); err == nil {
		t.Fatal("invented TTS polling endpoint")
	}
}

func TestAudioOperationQuotes(t *testing.T) {
	for _, id := range []string{TextToAudio, VideoToAudio, TextToSpeech, AvatarImageToVideo} {
		t.Run(id, func(t *testing.T) {
			r := audioFixtureRequest(id)
			cost, _, err := new(Adapter).quoteAudio(r)
			if id == AvatarImageToVideo {
				if !errors.Is(err, core.ErrPricingUnavailable) {
					t.Fatal(err)
				}
				r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.AudioURLs[0]: {Type: "audio", Duration: 10}}
				if _, _, err := new(Adapter).quoteAudio(r); !errors.Is(err, core.ErrPricingUnavailable) {
					t.Fatal("invented std/pro tariff", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := money.USD(35000)
			if id == TextToSpeech {
				want = 7000
			}
			if cost.ExpectedUSD != want || cost.EstimatedUnits != 1 || cost.Pricing.Unit != "request" || cost.Status != "estimated" || cost.ActualUSD != nil {
				t.Fatal(cost)
			}
		})
	}
}

func audioFixtureVoice(r *core.MediaRequest) {
	r.Options.Voice = ""
	r.Options.ResourceReferences = []core.ResourceReference{{BindingID: "b8c27fe8-a688-407a-8ccd-b89dcc9cb3d5", ReferenceID: "speaker"}}
	r.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: r.Options.ResourceReferences[0].BindingID, ReferenceID: "speaker", Kind: "voice", RemoteID: "90071992547409931", ConnectionIdentity: "fixture-connection-revision"}}
}

func TestAudioOperationManagedVoice(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			r := audioFixtureRequest(TextToSpeech)
			audioFixtureVoice(&r)
			r.Options.Language = language
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["voice_id"] != "90071992547409931" || body["voice_language"] != language {
					t.Fatalf("managed voice corrupted: %#v", body)
				}
				if body["resource_references"] != nil || body["resource_snapshots"] != nil {
					t.Fatal(body)
				}
				return response(200, `{"code":0,"data":{"task_id":"tts_fixture","task_status":"succeed","task_result":{"audios":[{"id":"a","url":"https://cdn.example/a.mp3","duration":"3"}]}}}`), nil
			}))
			task, err := a.generateAudio(audioFixtureContext(), r)
			if err != nil {
				t.Fatal(err)
			}
			if task.ID != "" || len(task.Artifacts) != 1 {
				t.Fatal(task)
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*core.MediaRequest)
	}{
		{"raw custom voice", func(r *core.MediaRequest) {
			r.Options.ResourceReferences = nil
			r.Options.ResourceSnapshots = nil
			r.Options.Voice = "custom_remote_voice"
		}},
		{"wrong preset language", func(r *core.MediaRequest) {
			r.Options.ResourceReferences = nil
			r.Options.ResourceSnapshots = nil
			r.Options.Voice = "oversea_male1"
			r.Options.Language = "zh"
		}},
		{"unresolved binding", func(r *core.MediaRequest) { r.Options.ResourceSnapshots = nil }},
		{"snapshot only", func(r *core.MediaRequest) { r.Options.ResourceReferences = nil }},
		{"wrong resource kind", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Kind = "element" }},
		{"different binding", func(r *core.MediaRequest) {
			r.Options.ResourceSnapshots[0].BindingID = "36deff26-af8d-4ac2-86e1-e6e7b6591dda"
		}},
		{"different alias", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].ReferenceID = "other" }},
		{"missing connection identity", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].ConnectionIdentity = "" }},
		{"raw and managed voice", func(r *core.MediaRequest) { r.Options.Voice = "oversea_male1" }},
		{"missing language", func(r *core.MediaRequest) { r.Options.Language = "" }},
		{"unsupported language", func(r *core.MediaRequest) { r.Options.Language = "fr" }},
		{"extra voice", func(r *core.MediaRequest) {
			r.Options.ResourceReferences = append(r.Options.ResourceReferences, r.Options.ResourceReferences[0])
			r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, r.Options.ResourceSnapshots[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := audioFixtureRequest(TextToSpeech)
			audioFixtureVoice(&r)
			tc.edit(&r)
			if err := new(Adapter).validateAudioRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
	for _, id := range []string{TextToAudio, VideoToAudio, AvatarImageToVideo} {
		t.Run(id+" rejects unrelated managed voice", func(t *testing.T) {
			r := audioFixtureRequest(id)
			audioFixtureVoice(&r)
			if err := new(Adapter).validateAudioRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestAudioOperationReviewedPresets(t *testing.T) {
	pairs := 0
	m, _ := audioModel(TextToSpeech)
	for voice, languages := range audioPresetVoiceLanguages {
		if !slices.Contains(m.Voices, voice) {
			t.Fatalf("preset missing from catalog: %s", voice)
		}
		for _, language := range languages {
			pairs++
			r := audioFixtureRequest(TextToSpeech)
			r.Options.Voice = voice
			r.Options.Language = language
			if err := new(Adapter).validateAudioRequest(r); err != nil {
				t.Fatalf("%s/%s: %v", voice, language, err)
			}
		}
	}
	if pairs != 62 || len(m.Voices) != len(audioPresetVoiceLanguages) {
		t.Fatalf("reviewed presets: %d pairs; %d catalog IDs; %d source IDs", pairs, len(m.Voices), len(audioPresetVoiceLanguages))
	}
	for _, voice := range []string{"guanxiaofang-v2", "tianmeixuemei-v1", "diyinnansang_DB_CN_M_04-v2"} {
		r := audioFixtureRequest(TextToSpeech)
		r.Options.Voice = voice
		r.Options.Language = "zh"
		if err := new(Adapter).validateAudioRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	r := audioFixtureRequest(TextToSpeech)
	r.Options.Voice = "AOT"
	r.Options.Language = "zh"
	if !errors.Is(new(Adapter).validateAudioRequest(r), core.ErrOptionsInvalid) {
		t.Fatal("accepted unreviewed preset language")
	}
}
