package kling

import (
	"context"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestTTSLanguageChoicesMatchExistingValidatedRequestValues(t *testing.T) {
	var tts core.MediaModel
	for _, m := range audioModels() {
		if m.ID == TextToSpeech {
			tts = m
		}
	}
	if !reflect.DeepEqual(tts.Languages, []string{"en", "zh"}) || tts.NoLanguage || len(tts.Voices) == 0 || tts.Voices[0] != "oversea_male1" {
		t.Fatal("existing voice/language control cannot select the reviewed default", tts.Languages, tts.Voices)
	}
	a, err := New("synthetic-language-key", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range tts.Languages {
		voice := "oversea_male1"
		if language == "zh" {
			voice = "girlfriend_1_speech02"
		}
		request := core.MediaRequest{Model: TextToSpeech, Prompt: "Reviewed language choice", Options: core.MediaOptions{Voice: voice, Language: language}}
		if err := a.ValidateRequest(context.Background(), request); err != nil {
			t.Fatal("advertised language cannot be admitted", language, err)
		}
	}
	for _, language := range []string{"Auto", "English", "Chinese", "Russian", ""} {
		if err := a.ValidateRequest(context.Background(), core.MediaRequest{Model: TextToSpeech, Prompt: "Reviewed choice", Options: core.MediaOptions{Voice: "oversea_male1", Language: language}}); err == nil {
			t.Fatal("unadvertised legacy UI value accepted", language)
		}
	}
}
