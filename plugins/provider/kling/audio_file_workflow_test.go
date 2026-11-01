package kling

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

// Golden pairs transcribed from the Voice ID / Voice Language columns of the
// legacy guide linked ONLY by https://kling.ai/document-api/api/video/avatar/text-to-speech.
// Source: https://docs.qingque.cn/s/home/eZQDvafJ4vXQkP8T9ZPvmye8S?identityId=2E1MlYrrPk4
// Reviewed 2026-10-31. Preview filenames and the separate 4.0 preset-list IDs
// are not aliases for these legacy TTS IDs.
const audioFileLegacyVoicePairs = `{"AOT":["en"],"PeppaPig_platform":["en"],"ai_chenjiahao_712":["zh","en"],"ai_huangyaoshi_712":["zh","en"],"ai_huangzhong_712":["en"],"ai_kaiya":["zh","en"],"ai_laoguowang_712":["zh","en"],"ai_shatang":["zh","en"],"ai_taiwan_man2_speech02":["zh"],"calm_story1":["en"],"cartoon-boy-07":["zh","en"],"cartoon-girl-01":["zh","en"],"chaoshandashu_speech02":["zh"],"chat1_female_new-3":["zh","en"],"chat_0407_5-1":["en"],"chengshu_jiejie":["zh","en"],"chongqingxiaohuo_speech02":["zh"],"chuanmeizi_speech02":["zh"],"commercial_lady_en_f-v1":["en"],"daopianyansang-v1":["zh"],"diyinnansang_DB_CN_M_04-v2":["zh"],"dongbeilaotie_speech02":["zh"],"genshin_kirara":["zh","en"],"genshin_klee2":["zh","en"],"genshin_vindi2":["zh","en"],"girlfriend_1_speech02":["zh"],"girlfriend_2_speech02":["zh"],"girlfriend_4_speech02":["en"],"guanxiaofang-v2":["zh"],"heainainai_speech02":["zh","en"],"laopopo_speech02":["zh","en"],"mengwa-v1":["zh"],"oversea_male1":["en"],"reader_en_m-v1":["en"],"tianjinjiejie_speech02":["zh"],"tianmeixuemei-v1":["zh"],"tiexin_nanyou":["zh"],"tiyuxi_xuedi":["zh"],"uk_boy1":["en"],"uk_man2":["en"],"uk_oldman3":["zh"],"xianzhanggui_speech02":["zh"],"yizhipiannan-v1":["zh"],"you_pingjing":["zh","en"],"zhinen_xuesheng":["zh","en"],"zhuxi_speech02":["zh"]}`

func TestAudioFileWorkflowLegacyVoiceGuidePairsAndNativeTTS(t *testing.T) {
	var golden map[string][]string
	if e := json.Unmarshal([]byte(audioFileLegacyVoicePairs), &golden); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(audioPresetVoiceLanguages, golden) {
		t.Fatal("legacy TTS catalogue differs from independently reviewed guide columns")
	}
	calls, pairs := 0, 0
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "POST" || req.URL.Path != "/v1/audio/tts" {
			t.Fatal("unexpected TTS route", req.URL)
		}
		var p map[string]any
		if e := json.NewDecoder(req.Body).Decode(&p); e != nil {
			t.Fatal(e)
		}
		voice, voiceOK := p["voice_id"].(string)
		language, languageOK := p["voice_language"].(string)
		valid := false
		for _, candidate := range golden[voice] {
			valid = valid || candidate == language
		}
		if !voiceOK || !languageOK || !valid || p["text"] != "Synthetic existing TTS" || len(p) != 3 {
			t.Fatal("TTS controls/ID-language mapping changed", p)
		}
		return response(200, `{"code":0,"data":{"task_id":"tts-fixture","task_status":"succeed","task_result":{"audios":[{"id":"private-tts-output","url":"https://cdn.example/tts.wav","duration":"4"}]}}}`), nil
	})
	for voice, languages := range golden {
		for _, language := range languages {
			pairs++
			r := core.MediaRequest{Model: TextToSpeech, Prompt: "Synthetic existing TTS", Options: core.MediaOptions{Voice: voice, Language: language}}
			task, e := a.Generate(audioFixtureContext(), r)
			if e != nil || task.ID != "" || len(task.Artifacts) != 1 || task.Artifacts[0].MediaType != "audio" || task.Artifacts[0].URL != "https://cdn.example/tts.wav" {
				t.Fatal(voice, language, task, e)
			}
			if len(languages) == 1 {
				opposite := "en"
				if language == "en" {
					opposite = "zh"
				}
				r.Options.Language = opposite
				if _, e = a.Generate(audioFixtureContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
					t.Fatal("wrong voice language dispatched", voice, opposite, e)
				}
			}
		}
	}
	if len(golden) != 46 || pairs != 62 || calls != 62 {
		t.Fatal("reviewed legacy voice coverage", len(golden), pairs, calls)
	}
	// These opaque numeric IDs appear in separate voice-library examples; no
	// reviewed source maps them to a legacy TTS voice/language pair.
	for _, unreviewed := range []string{"829826751244537879", "850087542757535834", "string", "Oversea_male1"} {
		r := core.MediaRequest{Model: TextToSpeech, Prompt: "Synthetic existing TTS", Options: core.MediaOptions{Voice: unreviewed, Language: "en"}}
		if _, e := a.Generate(audioFixtureContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal("unmapped library ID accepted as legacy preset", unreviewed, e)
		}
	}
	if calls != 62 {
		t.Fatal("invalid legacy voice selection reached provider", calls)
	}
}

func TestAudioFileWorkflowExistingTTSSpeedAndTextControls(t *testing.T) {
	calls := 0
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body["voice_id"] != "oversea_male1" || body["voice_language"] != "en" {
			t.Fatal(body)
		}
		if _, exists := body["model_name"]; exists {
			t.Fatal("TTS invented a model_name")
		}
		return response(200, `{"code":0,"data":{"task_id":"tts","task_status":"succeed","task_result":{"audios":[{"id":"audio","url":"https://cdn.example/output.wav","duration":"4"}]}}}`), nil
	})
	for _, speed := range []float64{.8, 1, 1.2, 2} {
		r := core.MediaRequest{Model: TextToSpeech, Prompt: strings.Repeat("a", 1000), Options: core.MediaOptions{Voice: "oversea_male1", Language: "en", Parameters: map[string]any{"voice_speed": speed}}}
		if _, e := a.Generate(audioFixtureContext(), r); e != nil {
			t.Fatal(speed, e)
		}
	}
	for _, speed := range []float64{.7, 2.1, 1.25, math.NaN()} {
		r := core.MediaRequest{Model: TextToSpeech, Prompt: "Synthetic", Options: core.MediaOptions{Voice: "oversea_male1", Language: "en", Parameters: map[string]any{"voice_speed": speed}}}
		if _, e := a.Generate(audioFixtureContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal("unsupported speed dispatched", speed, e)
		}
	}
	r := core.MediaRequest{Model: TextToSpeech, Prompt: strings.Repeat("a", 1001), Options: core.MediaOptions{Voice: "oversea_male1", Language: "en"}}
	if _, e := a.Generate(audioFixtureContext(), r); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("oversized script dispatched", e)
	}
	if calls != 4 {
		t.Fatal("invalid TTS options reached provider", calls)
	}
}

type audioFileOwnedRepository struct{ items map[string]references.Image }

func audioFileOwnedKey(ctx context.Context, id string) string {
	return identity.OwnerID(ctx) + "/" + id
}
func (r *audioFileOwnedRepository) Get(ctx context.Context, id string) (references.Image, error) {
	v, ok := r.items[audioFileOwnedKey(ctx, id)]
	if !ok {
		return v, storage.ErrObjectNotFound
	}
	return v, nil
}
func (r *audioFileOwnedRepository) Insert(ctx context.Context, v references.Image) error {
	r.items[audioFileOwnedKey(ctx, v.ID)] = v
	return nil
}
func (r *audioFileOwnedRepository) Delete(ctx context.Context, id string) error {
	delete(r.items, audioFileOwnedKey(ctx, id))
	return nil
}
func (r *audioFileOwnedRepository) List(ctx context.Context, limit, offset int) ([]references.Image, error) {
	out := []references.Image{}
	for key, v := range r.items {
		if strings.HasPrefix(key, identity.OwnerID(ctx)+"/") {
			out = append(out, v)
		}
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *audioFileOwnedRepository) FindByHash(ctx context.Context, hash string) (references.Image, error) {
	for key, v := range r.items {
		if strings.HasPrefix(key, identity.OwnerID(ctx)+"/") && v.SHA256 == hash {
			return v, nil
		}
	}
	return references.Image{}, storage.ErrObjectNotFound
}
func (r *audioFileOwnedRepository) UnhashedBySize(context.Context, int64) ([]references.Image, error) {
	return nil, nil
}
func (r *audioFileOwnedRepository) SetHash(ctx context.Context, id, hash string) error {
	v, e := r.Get(ctx, id)
	if e != nil {
		return e
	}
	v.SHA256 = hash
	return r.Insert(ctx, v)
}
func audioFileFourSecondWAV() []byte {
	data := make([]byte, 44+32000)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 36+32000)
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 8000)
	binary.LittleEndian.PutUint16(data[32:], 1)
	binary.LittleEndian.PutUint16(data[34:], 8)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 32000)
	for i := 44; i < len(data); i++ {
		data[i] = 128
	}
	return data
}

// This is the existing file workflow: synthesize, explicitly download the audio,
// reupload it into the owner's library, then pass the signed file URL as sound_file.
// It makes no native-audio-ID binding, preset discovery or unsupported polling call.
func TestAudioFileWorkflowTTSDownloadOwnedUploadToAvatarAndLipSoundFile(t *testing.T) {
	ctx := legacyScopeFixture(uuid.NewString(), "fixture-provider-owner", "kling--fixture", "fixture-revision")
	wave := audioFileFourSecondWAV()
	info, e := mediafile.Inspect(wave, "audio/wav")
	if e != nil || info.Duration != 4 {
		t.Fatal("synthetic WAV", info, e)
	}
	ttsCalls, consumerCalls, downloads := 0, 0, 0
	var ownedURL string
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Fatal("provider authorization changed")
		}
		var p map[string]any
		if e := json.NewDecoder(req.Body).Decode(&p); e != nil {
			t.Fatal(e)
		}
		switch req.URL.Path {
		case "/v1/audio/tts":
			ttsCalls++
			if p["voice_id"] != "oversea_male1" || p["voice_language"] != "en" {
				t.Fatal(p)
			}
			return response(200, `{"code":0,"data":{"task_id":"tts","task_status":"succeed","task_result":{"audios":[{"id":"native-tts-output-not-reused","url":"https://cdn.example/tts-output.wav","duration":"4"}]}}}`), nil
		case "/v1/videos/avatar/image2video":
			consumerCalls++
			if p["sound_file"] != ownedURL || p["audio_id"] != nil || p["image"] != "https://cdn.example/synthetic.png" {
				t.Fatal("Avatar must use uploaded file", p)
			}
		case "/v1/videos/advanced-lip-sync":
			consumerCalls++
			faces := p["face_choose"].([]any)
			f := faces[0].(map[string]any)
			if len(faces) != 1 || f["sound_file"] != ownedURL || f["audio_id"] != nil || f["sound_start_time"] != float64(0) || f["sound_end_time"] != float64(3000) || f["sound_insert_time"] != float64(1000) || f["sound_volume"] != float64(0) || f["original_audio_volume"] != float64(0) {
				t.Fatal("Lip-sync file/crop/volumes changed", p)
			}
		default:
			t.Fatal("unexpected provider route", req.URL.Path)
		}
		raw, _ := json.Marshal(p)
		if bytes.Contains(raw, []byte("native-tts-output-not-reused")) {
			t.Fatal("downloaded TTS ID leaked into file request")
		}
		return response(200, `{"code":0,"data":{"task_id":"video","task_status":"submitted"}}`), nil
	})
	tts, e := a.Generate(ctx, core.MediaRequest{Model: TextToSpeech, Prompt: "Synthetic TTS for the existing file route", Options: core.MediaOptions{Voice: "oversea_male1", Language: "en"}})
	if e != nil {
		t.Fatal(e)
	}
	cdn := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		downloads++
		if req.URL.String() != tts.ResultURL || req.Header.Get("Authorization") != "" {
			t.Fatal("download destination or credential leak")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/wav"}}, ContentLength: int64(len(wave)), Body: io.NopCloser(bytes.NewReader(wave))}, nil
	})}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tts.ResultURL, nil)
	download, e := cdn.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(io.LimitReader(download.Body, 5<<20+1))
	download.Body.Close()
	if e != nil || !bytes.Equal(data, wave) {
		t.Fatal("download changed bytes", e)
	}
	local, e := storage.NewLocal(t.TempDir(), cdn, 5<<20)
	if e != nil {
		t.Fatal(e)
	}
	library := references.New(&audioFileOwnedRepository{items: map[string]references.Image{}}, local)
	uploaded, e := library.Upload(ctx, "Downloaded TTS.wav", bytes.NewReader(data))
	if e != nil {
		t.Fatal("owned upload", e)
	}
	ownedURL, e = references.CanonicalReferenceURL("https://gateway.fixture", []byte("file-workflow-signing"), identity.OwnerID(ctx), uploaded.ID)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(ownedURL)
	if !strings.HasPrefix(u.Path, "/references/"+identity.OwnerID(ctx)+"/") || u.Query().Get("signature") == "" {
		t.Fatal("owned file URL not canonical", ownedURL)
	}
	resolver := references.NewResolver(library, "file-workflow-signing", "https://gateway.fixture")
	if _, e = resolver.ResolveDirect(identity.WithOwnerID(ctx, uuid.NewString()), core.MediaOptions{AudioURLs: []string{ownedURL}}); e == nil {
		t.Fatal("foreign owner reused signed input")
	}
	_, object, e := library.Open(ctx, uploaded.ID)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := io.ReadAll(object.Reader)
	object.Reader.Close()
	if e != nil || !bytes.Equal(stored, wave) {
		t.Fatal("owned upload bytes changed", e)
	}
	for _, model := range []string{AvatarImageToVideo, LipSync} {
		r := audioFixtureRequest(AvatarImageToVideo)
		if model == LipSync {
			r = auxiliaryFixture(LipSync)
			r.Options.ReferenceMode = "audio"
			r.Options.Parameters["sound_volume"] = 0.0
			r.Options.Parameters["original_audio_volume"] = 0.0
		}
		r.Options.AudioURLs = []string{ownedURL}
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{ownedURL: {ReferenceID: uploaded.ID, Type: "audio", SHA256: uploaded.SHA256, ContentType: uploaded.ContentType, SizeBytes: uploaded.SizeBytes, Duration: info.Duration}}
		r.Options, e = resolver.ResolveDirect(ctx, r.Options)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.Generate(ctx, r); e != nil {
			t.Fatal(model, e)
		}
	}
	if ttsCalls != 1 || consumerCalls != 2 || downloads != 1 {
		t.Fatal("file workflow used unexpected API/download calls", ttsCalls, consumerCalls, downloads)
	}
}
