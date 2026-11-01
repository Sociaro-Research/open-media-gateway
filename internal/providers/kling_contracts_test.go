package providers

import (
	"slices"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func assertKlingContracts(t *testing.T, models []core.MediaModel) {
	t.Helper()
	type contract struct {
		images                 int
		video                  bool
		modes                  []string
		min, max, step, prompt int
	}
	expected := map[string]contract{
		kling.TextToVideo:         {0, false, []string{"none"}, 3, 15, 1, 3072},
		kling.ImageToVideo:        {2, false, []string{"first_frame", "first_last_frame"}, 3, 15, 1, 3072},
		kling.TextToVideo4:        {0, false, []string{"none"}, 3, 30, 1, 10000},
		kling.ImageToVideo4:       {10, false, []string{"first_frame", "first_last_frame", "keyframes"}, 3, 30, 1, 10000},
		kling.TextToVideo3Turbo:   {0, false, []string{"none"}, 3, 15, 1, 3072},
		kling.ImageToVideo3Turbo:  {1, false, []string{"first_frame"}, 3, 15, 1, 2500},
		kling.TextToVideo25Turbo:  {0, false, []string{"none"}, 5, 10, 5, 2500},
		kling.ImageToVideo25Turbo: {2, false, []string{"first_frame", "first_last_frame"}, 5, 10, 5, 2500},
		kling.OmniVideo4:          {10, true, []string{"references"}, 3, 30, 1, 26000},
	}

	additional := map[string]string{kling.SessionPreview: "video", kling.OmniVideo3: "video", kling.OmniVideoO1: "video", kling.LipSync: "video", kling.MultiElementsEditing: "video",
		kling.ImageGeneration3:   "image",
		kling.ImageGeneration21:  "image",
		kling.ImageOmni3:         "image",
		kling.ImageO1:            "image",
		kling.MultiImage21:       "image",
		kling.ImageOutpainting:   "image",
		kling.ImageMultiShot:     "image",
		kling.TextToAudio:        "audio",
		kling.VideoToAudio:       "audio",
		kling.TextToSpeech:       "speech",
		kling.AvatarImageToVideo: "video",
		kling.TextToVideo26:      "video",
		kling.ImageToVideo26:     "video",
		kling.MotionControl3:     "video",
		kling.MotionControl26:    "video",
		kling.KlingEffects:       "video",
		kling.ApparelReplicator:  "video",
		kling.GoodsStudio:        "video",
		kling.VideoCommerce:      "video",
		kling.VirtualTryOn:       "image",
	}
	seen := map[string]bool{}
	for _, m := range models {
		e, ok := expected[m.ID]
		if media, additionalModel := additional[m.ID]; additionalModel {
			if seen[m.ID] || m.MediaType != media || len(m.ReferenceModes) == 0 {
				t.Fatalf("wrong reviewed family descriptor: %+v", m)
			}
			if media == "image" && m.MaxOutputs < 1 {
				t.Fatalf("missing image result count contract: %+v", m)
			}
			if m.ID == kling.TextToSpeech && (len(m.Voices) != 46 || !m.RequiresVoice || len(m.ResourceInputSchemas) != 1) {
				t.Fatalf("missing speech voice contract: %+v", m)
			}
			if (m.ID == kling.MotionControl3 || m.ID == kling.MotionControl26) && (!m.DurationFromInput || !m.RequiresVideoInput || m.ReferenceMediaLimits.Video.MaxDuration != 30) {
				t.Fatalf("missing motion video contract: %+v", m)
			}
			if m.ID == kling.SessionInit && (m.ResourceOperation == nil || m.ResourceOperation.Kind != "editing_session" || len(m.ResourceOutputs) != 1 || !m.ResourceOutputs[0].ProviderExpiryKnown) {
				t.Fatal("missing initialized session contract", m)
			}
			if m.ID == kling.SessionAdd || m.ID == kling.SessionDelete || m.ID == kling.SessionClear || m.ID == kling.SessionPreview {
				action := map[string]string{kling.SessionAdd: "add", kling.SessionDelete: "delete", kling.SessionClear: "clear", kling.SessionPreview: "preview"}[m.ID]
				if m.SessionOperation == nil || m.SessionOperation.Kind != "editing_session" || m.SessionOperation.Action != action || m.SessionOperation.Mutates != (action != "preview") || m.SessionOperation.MaxFrames != 10 || m.SessionOperation.MaxPoints != 10 || m.SessionOperation.FrameIndexBase != 0 {
					t.Fatal("wrong exact selection action contract", m)
				}
			}
			seen[m.ID] = true
			continue
		}
		if !ok || seen[m.ID] {
			t.Fatalf("unexpected or duplicate Kling operation: %s", m.ID)
		}
		seen[m.ID] = true
		modes := []string{}
		for _, mode := range m.ReferenceModes {
			modes = append(modes, mode.ID)
		}
		if m.MediaType != "video" || m.MaxImageInputs != e.images || m.SupportsVideoInput != e.video || !slices.Equal(modes, e.modes) || m.MinDuration != e.min || m.MaxDuration != e.max || m.DurationStep != e.step || m.MaxPromptCharacters != e.prompt {
			t.Fatalf("wrong exact contract for %s: %+v", m.ID, m)
		}
		if m.ID == kling.OmniVideo4 && (m.ReferenceMediaLimits.Video.MaxTotalDuration != 30 || m.ReferenceModes[0].MaxVideos != 5 || m.PromptPolicy == nil || !m.DurationMayBeInferred || !m.RatioMayBeInferred) {
			t.Fatal("Omni capability omitted", m)
		}
	}
	for id := range additional {
		if !seen[id] {
			t.Errorf("reviewed family missing: %s", id)
		}
	}
	for id := range expected {
		if !seen[id] {
			t.Errorf("reviewed operation missing: %s", id)
		}
	}
}
