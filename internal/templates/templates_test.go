package templates

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
)

func danceModel() core.MediaModel {
	return core.MediaModel{MediaType: "video", ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 12, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4"}}, Audio: core.MediaFileLimits{ContentTypes: []string{"audio/wav"}}}, ReferencePromptSyntax: "numbered", ReferencePromptTypes: []string{"image", "video", "audio"}, SupportsImageInput: true, SupportsVideoInput: true, SupportsAudioInput: true, MaxImageInputs: 9, ReferenceModes: []core.ReferenceMode{{ID: "references", MaxImages: 9, MaxVideos: 3, MaxAudios: 3, MaxVisuals: 12}}, Resolutions: []string{"720p"}, Ratios: []string{"16:9"}, MinDuration: 4, MaxDuration: 15, DefaultDuration: 5}
}
func TestInferenceAndStableTypedBinding(t *testing.T) {
	for _, tc := range []struct {
		syntax, prompt string
		types          []string
		want           int
		bad            bool
	}{
		{"numbered", "Image 1 and Image 2 follow Video 1; repeat Image 1", []string{"image", "video"}, 3, false},
		{"picture", "<Picture 1> <Video 1> <Audio 1>", []string{"image", "video", "audio"}, 3, false},
		{"bracket", "[Image 1], [Image 1]", []string{"image"}, 1, false},
		{"kling-id", "@image_1 @image_2", []string{"image"}, 2, false},
		{"numbered", "An image of a landscape and a video montage", []string{"image"}, 0, false},
		{"numbered", "Image 2", []string{"image"}, 0, true},
		{"numbered", "Image 0", []string{"image"}, 0, true},
		{"numbered", "Image -1", []string{"image"}, 0, true},
		{"numbered", "Image 1.5", []string{"image"}, 0, true},
		{"numbered", "Image 1e3", []string{"image"}, 0, true},
		{"numbered", "@Image NaN", []string{"image"}, 0, true},
		{"numbered", "Image 01", []string{"image"}, 0, true},
		{"numbered", "Image 9007199254740993", []string{"image"}, 0, true},
		{"numbered", "Image 9999999999999999999999999999999", []string{"image"}, 0, true},
		{"picture", "<Picture 1", []string{"image"}, 0, true},
		{"picture", "<Picture NaN>", []string{"image"}, 0, true},
		{"bracket", "[Video 1]", []string{"image"}, 0, true},
		{"kling-id", "@Image_1", []string{"image"}, 0, true},
		{"", "Image 1", nil, 0, true},
	} {
		t.Run(tc.syntax+tc.prompt, func(t *testing.T) {
			slots, e := Infer(tc.prompt, tc.syntax, tc.types, core.ReferenceMode{ID: "references"})
			if (e != nil) != tc.bad || e == nil && len(slots) != tc.want {
				t.Fatalf("slots=%+v err=%v", slots, e)
			}
		})
	}
	model := danceModel()
	d := Definition{Title: "Dance", MediaType: "video", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
	d.Slots, _ = Infer(d.Prompt, d.Syntax, model.ReferencePromptTypes, model.ReferenceModes[0])
	d.Slots[2].AssetID = "fixed-dance"
	if e := d.Validate(model); e != nil {
		t.Fatal(e)
	}
	if d.Slots[0].ID != "image:1" || d.Slots[1].ID != "image:2" || d.Slots[2].ID != "video:1" {
		t.Fatal(d.Slots)
	}
	d.Slots[1] = d.Slots[0]
	if e := d.Validate(model); e == nil {
		t.Fatal("duplicate binding accepted")
	}
}
func TestCompatibilityUsesCompleteContract(t *testing.T) {
	model := danceModel()
	d := Definition{Title: "Dance", MediaType: "video", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5}}
	d.Slots, _ = Infer(d.Prompt, d.Syntax, model.ReferencePromptTypes, model.ReferenceModes[0])
	if e := d.Compatible(model); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*core.MediaModel){
		"output": func(m *core.MediaModel) { m.MediaType = "image" }, "grammar": func(m *core.MediaModel) { m.ReferencePromptSyntax = "picture" },
		"unsupported type": func(m *core.MediaModel) { m.ReferencePromptTypes = []string{"image"} },
		"workflow": func(m *core.MediaModel) {
			m.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", MinImages: 1, MaxImages: 1}}
		},
		"image counts":          func(m *core.MediaModel) { m.ReferenceModes[0].MaxImages = 1 },
		"combined limit":        func(m *core.MediaModel) { m.ReferenceModes[0].MaxVisuals = 2 },
		"role":                  func(m *core.MediaModel) { m.ReferenceModes[0].ImageRoles = []string{"first_frame"} },
		"video output duration": func(m *core.MediaModel) { m.ReferenceModes[0].MaxDurationWithVideo = 4 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := model
			candidate.ReferenceModes = append([]core.ReferenceMode(nil), model.ReferenceModes...)
			change(&candidate)
			if e := d.Compatible(candidate); e == nil {
				t.Fatal("incompatible model accepted")
			}
		})
	}
	d.Options.ImageURLs = []string{"https://private.example.test"}
	if e := d.Validate(model); e == nil {
		t.Fatal("implicit references accepted")
	}
	d.Options.ImageURLs = nil
	d.Prompt = " " + d.Prompt
	if e := d.Validate(model); e == nil {
		t.Fatal("prompt would be silently trimmed")
	}
}
func TestFirstFrameReferenceOffset(t *testing.T) {
	mode := core.ReferenceMode{ID: "first_frame_references", ImageRoles: []string{"first_frame"}}
	slots, e := Infer("Image 1 and Image 2", "numbered", []string{"image"}, mode)
	if e != nil || len(slots) != 3 || slots[0].Role != "first_frame" || slots[1].ID != "image:2" {
		t.Fatal(slots, e)
	}
}
func FuzzInferNeverPanics(f *testing.F) {
	for _, s := range []string{"Image 1", "<Picture 1>", strings.Repeat("9", 100), "@image_1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		for _, grammar := range []string{"numbered", "picture", "bracket", "kling-id", ""} {
			_, _ = Infer(value, grammar, []string{"image", "video", "audio"}, core.ReferenceMode{ID: "references"})
		}
	})
}

func TestMediaLimitsIncludeFixedAndUserFiles(t *testing.T) {
	model := danceModel()
	model.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 3, Image: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 1000, MinDimension: 256, MaxDimension: 6000}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4"}, MinDuration: 2, MaxDuration: 12, MinFPS: 24, MaxFPS: 60, MaxTotalDuration: 12}}
	files := []InputMedia{{Type: "image", Info: mediafile.Info{ContentType: "image/png", Size: 99, Width: 256, Height: 256}}, {Type: "video", Info: mediafile.Info{ContentType: "video/mp4", Size: 500, Width: 640, Height: 480, Duration: 5, FPS: 30}}}
	if e := ValidateFiles(model, files, true); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func([]InputMedia){"image dimension": func(f []InputMedia) { f[0].Info.Width = 255 }, "image size": func(f []InputMedia) { f[0].Info.Size = 1000 }, "video duration": func(f []InputMedia) { f[1].Info.Duration = 13 }, "video FPS": func(f []InputMedia) { f[1].Info.FPS = 23 }, "content type": func(f []InputMedia) { f[1].Info.ContentType = "image/png" }} {
		t.Run(name, func(t *testing.T) {
			copy := append([]InputMedia(nil), files...)
			change(copy)
			if e := ValidateFiles(model, copy, true); e == nil {
				t.Fatal("invalid actual file admitted")
			}
		})
	}
}

func TestTextOnlyTemplatesAreRejectedWithoutChangingNativeGrammar(t *testing.T) {
	model := danceModel()
	model.ReferencePromptSyntax = "picture"
	model.ReferenceModes = append(model.ReferenceModes, core.ReferenceMode{ID: "none"})
	d := Definition{Title: "Text only", Prompt: "A calm landscape", MediaType: "video", Syntax: PromptSyntax(model, "none"), Slots: []Slot{}, Options: core.MediaOptions{ReferenceMode: "none"}}
	if e := d.Validate(model); e == nil {
		t.Fatal("text-only template accepted")
	}
	if _, e := core.NormalizeModelOptions(model, d.Options); e != nil {
		t.Fatal("ordinary text-only generation changed", e)
	}
	if syntax := PromptSyntax(model, "references"); syntax != "picture" {
		t.Fatal("reference grammar lost", syntax)
	}
}

func TestContractMatchingDoesNotDependOnModelIdentity(t *testing.T) {
	source := danceModel()
	d := Definition{Title: "Dance", MediaType: "video", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5}}
	d.Slots, _ = Infer(d.Prompt, d.Syntax, source.ReferencePromptTypes, source.ReferenceModes[0])
	for _, identity := range []string{"operator-private-alias", "entirely-new-native-model", "another-connection/model"} {
		candidate := source
		candidate.ID = identity
		candidate.DisplayName = "Unrelated label " + identity
		if err := d.Compatible(candidate); err != nil {
			t.Fatal("identical contract rejected by identity", identity, err)
		}
	}
	candidate := source
	candidate.ID = "same-modality-without-file-contract"
	candidate.ReferenceMediaLimits = nil
	if err := d.Compatible(candidate); err != nil {
		t.Fatal("optional file metadata must not erase the native contract", err)
	}
}

func TestZeroReferenceTemplatesCannotMapToAnotherWorkflow(t *testing.T) {
	for _, output := range []string{"image", "video"} {
		t.Run(output, func(t *testing.T) {
			d := Definition{Title: "Landscape", MediaType: output, Prompt: "A calm landscape", Syntax: "numbered", Slots: []Slot{}, Options: core.MediaOptions{ReferenceMode: "none"}}
			for _, grammar := range []string{"", "numbered", "picture", "bracket", "kling-id"} {
				for _, workflow := range []string{"none", "references", ""} {
					candidate := core.MediaModel{ID: "arbitrary-" + grammar + workflow, MediaType: output, ReferencePromptSyntax: grammar, ReferenceModes: []core.ReferenceMode{{ID: workflow}}, ReferenceMediaLimits: nil}
					if err := d.Compatible(candidate); err == nil {
						t.Fatal("zero-reference recipe was mapped to another contract")
					}
					options, err := d.OptionsFor(candidate)
					if err == nil || len(options.ImageURLs)+len(options.Videos())+len(options.Audios()) != 0 {
						t.Fatal(options, err)
					}
				}
			}
			required := core.MediaModel{MediaType: output, SupportsImageInput: true, RequiresImageInput: true, ReferenceModes: []core.ReferenceMode{{ID: "first_frame", MinImages: 1, MaxImages: 1}}}
			if err := d.Compatible(required); err == nil {
				t.Fatal("required reference model accepted zero inputs")
			}
			bad := core.MediaModel{MediaType: output, NoPrompt: true}
			if err := d.Compatible(bad); err == nil {
				t.Fatal("prompt-less model accepted immutable prompt")
			}
			bad.NoPrompt = false
			bad.MediaType = "speech"
			if err := d.Compatible(bad); err == nil {
				t.Fatal("wrong output accepted")
			}
		})
	}
	// A candidate grammar may not reinterpret literal text into new bindings.
	d := Definition{Title: "Literal marker", MediaType: "video", Prompt: "Image 1", Syntax: "picture", Slots: []Slot{}, Options: core.MediaOptions{ReferenceMode: "references"}}
	candidate := core.MediaModel{MediaType: "video", ReferencePromptSyntax: "numbered", ReferencePromptTypes: []string{"image"}}
	if err := d.Compatible(candidate); err == nil {
		t.Fatal("zero-reference switch silently introduced a marker")
	}
}

func TestGenerationSettingsLegacyAndModelCapabilities(t *testing.T) {
	model := danceModel()
	model.DefaultDuration = 9
	d := Definition{MediaType: "video", Slots: []Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference"}}, Options: core.MediaOptions{ReferenceMode: "references"}}
	options, err := d.OptionsFor(model)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := core.NormalizeModelOptions(model, options)
	if err != nil || normalized.Duration != 9 {
		t.Fatal("legacy must use model default, not five seconds", normalized, err)
	}
	model.DurationStep = 2
	d.Options.Duration = 5
	if d.ValidateGenerationOptions(model) == nil {
		t.Fatal("off-step duration accepted")
	}
	d.Options.Duration = 6
	if err := d.ValidateGenerationOptions(model); err != nil {
		t.Fatal(err)
	}
	model.DurationFromInput = true
	if d.ValidateGenerationOptions(model) == nil {
		t.Fatal("input-derived duration overridden")
	}
	d.Options.Duration = 0
	model.SupportsAudio, model.AudioAlwaysOn = true, true
	disabled := false
	d.Options.GenerateAudio = &disabled
	if d.ValidateGenerationOptions(model) == nil {
		t.Fatal("mandatory audio disabled")
	}
	enabled := true
	d.Options.GenerateAudio = &enabled
	if err := d.ValidateGenerationOptions(model); err != nil {
		t.Fatal(err)
	}
	model.ReferenceModes[0].Ratios = []string{"9:16"}
	d.Options.Ratio = "16:9"
	if d.ValidateGenerationOptions(model) == nil {
		t.Fatal("workflow ratio restriction ignored")
	}
	d.Options.Ratio = "9:16"
	if err := d.ValidateGenerationOptions(model); err != nil {
		t.Fatal(err)
	}
}
