package minimax

import (
	"bytes"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"net/http"
	"strings"
	"testing"
)

func TestH3NativeAudioAndReferenceAudioAreSeparateContracts(t *testing.T) {
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"} {
		t.Run(id, func(t *testing.T) {
			m, _ := modelFor(id)
			if !m.SupportsAudio || !m.AudioAlwaysOn || !m.SupportsAudioInput {
				t.Fatal("H3 native audio/reference metadata changed")
			}
			calls := 0
			a := fixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				p := body(t, r)
				for _, key := range []string{"generate_audio", "audio", "override_sound", "override_sound_id", "sound"} {
					if _, exists := p[key]; exists {
						t.Fatal("Gateway audio control leaked to native payload", key)
					}
				}
				content := p["content"].([]any)
				if len(content) != 2 || content[1].(map[string]any)["type"] != "audio_url" || content[1].(map[string]any)["role"] != "reference_audio" {
					t.Fatal("audio reference lost its input role")
				}
				return response(200, success("123")), nil
			})
			on, off := true, false
			r := core.MediaRequest{Model: id, Prompt: "Use reference audio1", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "16:9", AudioURLs: []string{"https://media.example/reference.mp3"}, GenerateAudio: &off}}
			if err := a.ValidateRequest(owned("alice"), r); err == nil {
				t.Fatal("Audio Off silently ignored")
			}
			if _, err := a.Generate(owned("alice"), r); err == nil || calls != 0 {
				t.Fatal("Audio Off reached upstream")
			}
			if _, err := core.NormalizeModelOptions(m, r.Options); err == nil {
				t.Fatal("shared admission allowed Audio Off")
			}
			r.Options.GenerateAudio = &on
			r.Options.OverrideSoundID = "Gateway-post-processing-only"
			if _, err := a.Generate(owned("alice"), r); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
	m, _ := modelFor(ContextIR)
	if m.SupportsAudio || m.AudioAlwaysOn || !m.SupportsAudioInput {
		t.Fatal("text context was advertised as generated audio")
	}
	a := fixture(t, nil)
	for _, value := range []bool{false, true} {
		r := core.MediaRequest{Model: ContextIR, Prompt: "scene", Options: core.MediaOptions{GenerateAudio: &value}}
		if err := a.ValidateRequest(owned("alice"), r); err == nil {
			t.Fatal("Context advertised a generated audio switch")
		}
	}
	for _, m := range catalog() {
		if strings.HasPrefix(m.ID, "MiniMax-H3") && m.ID != ContextIR && m.ID != Regeneration && m.ID != RegenerationVideo {
			continue
		}
		if m.AudioAlwaysOn || m.SupportsAudio {
			t.Fatal("undocumented output-audio control", m.ID)
		}
	}
}
func TestAdaptiveRatioIsScopedToDocumentedH3Modes(t *testing.T) {
	a := fixture(t, nil)
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"} {
		m, _ := modelFor(id)
		for _, mode := range m.ReferenceModes {
			r := requestFor(m, mode)
			r.Options.Ratio = "adaptive"
			err := a.ValidateRequest(owned("alice"), r)
			allowed := mode.ID != "none" && !(id == "MiniMax-H3-Lite" && mode.ID == "references")
			if (err == nil) != allowed {
				t.Fatal(id, mode.ID, allowed, err)
			}
		}
	}
}
func TestModelResolutionDurationCartesianBoundaries(t *testing.T) {
	a := fixture(t, nil)
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02"} {
		m, _ := modelFor(id)
		for _, res := range []string{"480p", "512p", "768p", "1080p", "2k"} {
			for seconds := 3; seconds <= 16; seconds++ {
				mode := m.ReferenceModes[0]
				if res == "512p" && id == "MiniMax-Hailuo-02" {
					mode = m.ReferenceModes[1]
				}
				r := requestFor(m, mode)
				r.Options.Resolution = res
				r.Options.Duration = seconds
				allowed := false
				if strings.HasPrefix(id, "MiniMax-H3") {
					min := 4
					if id == "MiniMax-H3-Max" {
						min = 5
					}
					allowed = seconds >= min && seconds <= 15 && (res == "768p" || id == "MiniMax-H3" && res == "2k" || id != "MiniMax-H3" && res == "480p")
				} else {
					allowed = (res == "768p" && (seconds == 6 || seconds == 10)) || (res == "1080p" && seconds == 6) || (id == "MiniMax-Hailuo-02" && res == "512p" && (seconds == 6 || seconds == 10))
				}
				err := a.ValidateRequest(owned("alice"), r)
				if (err == nil) != allowed {
					t.Fatal(id, res, seconds, allowed, err)
				}
			}
		}
	}
}

// The shared inspector exposes MP4 sample-entry identifiers, not marketing
// codec names. Exercise the same byte evidence used by public upload admission.
func TestH3InspectedVideoCodecAllowlist(t *testing.T) {
	for _, codec := range []string{"avc1", "avc3", "hvc1", "hev1", "vp09"} {
		t.Run(codec, func(t *testing.T) {
			data := bytes.Replace(testmedia.MP4(120, 24, 120), []byte("avc1"), []byte(codec), 1)
			info, err := mediafile.Inspect(data, "video/mp4")
			if err != nil {
				t.Fatal(err)
			}
			if info.Codec != codec {
				t.Fatalf("inspected codec %q, want %q", info.Codec, codec)
			}
			file := referencevalidation.File{Type: "video", Name: "inspected.mp4", Info: info}
			model, _ := modelFor("MiniMax-H3")
			upload := fileOperationModel(ResourceUpload)
			upload.ReferenceMediaLimits = upload.ResourceOperation.Variants[1].Limits
			for _, m := range []core.MediaModel{model, upload} {
				err := referencevalidation.ValidateFiles(m, []referencevalidation.File{file}, false)
				if (err == nil) != (codec != "vp09") {
					t.Fatalf("%s codec %s: %v", m.ID, codec, err)
				}
			}
		})
	}
}

// Official H3 content tables use inclusive per-file byte ceilings. The public
// resource-upload variants must apply the same limits as direct references.
func TestH3InspectedMediaByteCeilingsAreInclusive(t *testing.T) {
	for _, tc := range []struct {
		kind, contentType string
		size              int64
	}{
		{"image", "image/png", 30_000_000}, {"video", "video/mp4", 50_000_000}, {"audio", "audio/wav", 15_000_000},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			model, _ := modelFor("MiniMax-H3")
			upload := fileOperationModel(ResourceUpload)
			for _, variant := range upload.ResourceOperation.Variants {
				if variant.Subtype == tc.kind {
					upload.ReferenceMediaLimits = variant.Limits
				}
			}
			for _, m := range []core.MediaModel{model, upload} {
				for _, size := range []int64{tc.size - 1, tc.size, tc.size + 1} {
					zero := 0
					f := referencevalidation.File{Type: tc.kind, Info: mediafile.Info{AudioStreams: &zero, ContentType: tc.contentType, Codec: "avc1", Size: size, Width: 512, Height: 512, Duration: 5, FPS: 24}}
					err := referencevalidation.ValidateFiles(m, []referencevalidation.File{f}, false)
					if (err == nil) != (size <= tc.size) {
						t.Fatalf("%s %s size %d: %v", m.ID, tc.kind, size, err)
					}
				}
			}
		})
	}
}

func TestPublishedExclusiveImageByteCeilings(t *testing.T) {
	for _, tc := range []struct {
		id    string
		limit int64
	}{
		{"MiniMax-Hailuo-2.3", 20_000_000}, {"video-agent-392747428568649728", 20_000_000}, {"image-01", 10_000_000}, {"image-01-live", 10_000_000},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m, ok := modelFor(tc.id)
			if !ok {
				t.Fatal("missing model")
			}
			for _, size := range []int64{tc.limit - 1, tc.limit, tc.limit + 1} {
				f := referencevalidation.File{Type: "image", Info: mediafile.Info{ContentType: "image/png", Size: size, Width: 512, Height: 512}}
				err := referencevalidation.ValidateFiles(m, []referencevalidation.File{f}, false)
				if (err == nil) != (size < tc.limit) {
					t.Fatalf("size %d: %v", size, err)
				}
			}
		})
	}
}

func TestH3EmbeddedAudioConstraintsAreExactAndFailClosed(t *testing.T) {
	one, two, zero := 1, 2, 0
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", ContextIR} {
		t.Run(id, func(t *testing.T) {
			m, _ := modelFor(id)
			for _, tc := range []struct {
				name   string
				count  *int
				codecs []string
				valid  bool
			}{
				{"aac", &one, []string{"aac"}, true}, {"mp3", &one, []string{"mp3"}, true}, {"mixed", &two, []string{"aac", "mp3"}, true}, {"silent", &zero, nil, true},
				{"unsupported", &one, []string{"opus"}, false}, {"present unknown", &one, []string{""}, false}, {"unknown count", nil, nil, false}, {"missing codec", &one, nil, false}, {"mismatch", &two, []string{"aac"}, false}, {"silence mismatch", &zero, []string{"aac"}, false},
			} {
				f := referencevalidation.File{Type: "video", Info: mediafile.Info{ContentType: "video/mp4", Codec: "avc1", AudioStreams: tc.count, AudioCodecs: tc.codecs, Size: 1000, Width: 512, Height: 512, Duration: 5, FPS: 24}}
				err := referencevalidation.ValidateFiles(m, []referencevalidation.File{f}, false)
				if (err == nil) != tc.valid {
					t.Fatalf("%s: %v", tc.name, err)
				}
			}
		})
	}
	base := baseRegenerationModel()
	if len(base.ReferenceMediaLimits.Video.AllowedAudioCodecs) != 0 {
		t.Fatal("undocumented base-video codec constraint introduced")
	}
	for _, rule := range base.ReferenceRoleLimits {
		if rule.Role == "reference_video" && len(rule.Limits.AllowedAudioCodecs) != 2 {
			t.Fatal("original reference role lost embedded codec constraints")
		}
	}
	for _, op := range operations() {
		m := describe(op)
		if op.kind != "h3" && op.kind != "context" && op.kind != "base_regeneration" && op.kind != "resource" && m.ReferenceMediaLimits != nil && len(m.ReferenceMediaLimits.Video.AllowedAudioCodecs) > 0 {
			t.Fatal("unrelated model acquired codec constraints", m.ID)
		}
	}
}

func TestDirectNativeReferenceAudioProofRejectsBeforeHTTP(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid trusted codec evidence reached provider")
		return nil, nil
	})
	one, two, zero := 1, 2, 0
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max", ContextIR, RegenerationVideo} {
		t.Run(id, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				count  *int
				codecs []string
				valid  bool
			}{
				{"aac", &one, []string{"aac"}, true}, {"mp3", &one, []string{"mp3"}, true}, {"silent", &zero, nil, true}, {"unsupported", &one, []string{"opus"}, false}, {"present unknown", &one, []string{""}, false}, {"unknown count", nil, nil, false}, {"mismatch", &two, []string{"aac"}, false},
			} {
				r := core.MediaRequest{Model: id, Prompt: "Use the reference video", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "16:9"}}
				if id == RegenerationVideo {
					r = baseRequest()
				}
				url := "https://media.example/codec.mp4"
				r.Options.VideoURLs = append(r.Options.VideoURLs, url)
				if r.Options.ReferenceMediaEvidence == nil {
					r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
				}
				r.Options.ReferenceMediaEvidence[url] = core.InputMediaEvidence{Type: "video", Duration: 4, AudioStreams: tc.count, AudioCodecs: tc.codecs}
				err := a.ValidateRequest(owned("alice"), r)
				if (err == nil) != tc.valid {
					t.Fatalf("%s: %v", tc.name, err)
				}
				if !tc.valid {
					if _, err := a.Generate(owned("alice"), r); err == nil {
						t.Fatal("invalid trusted codec dispatched")
					}
				}
			}
			// Opaque external URLs have no local byte proof; their documented codec
			// requirements are checked upstream, never converted into invented evidence.
			r := core.MediaRequest{Model: id, Prompt: "Use reference", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "16:9", VideoURLs: []string{"https://media.example/opaque.mp4"}}}
			if id != RegenerationVideo {
				if err := a.ValidateRequest(owned("alice"), r); err != nil {
					t.Fatal("opaque native URL policy changed", err)
				}
			}
		})
	}
}
