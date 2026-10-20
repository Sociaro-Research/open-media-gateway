package core

import (
	"errors"
	"reflect"
	"testing"
)

func TestRegisteredReferenceAdmission(t *testing.T) {
	service := &TaskService{}
	model := MediaModel{MediaType: "video", SupportsAssetReferences: true, SupportsImageInput: true, MaxImageInputs: 3, SupportsVideoInput: true, SupportsAudioInput: true}
	inputs := []MediaOptions{
		{ImageURLs: []string{"https://example.test/scene.png", "asset://asset-character"}},
		{VideoURLs: []string{"asset://asset-video"}},
		{AudioURLs: []string{"asset://asset-audio"}},
		{VideoURL: "asset://asset-video", AudioURL: "asset://asset-audio"},
	}
	for _, options := range inputs {
		got, err := service.normalizeOptions(model, options)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.ImageURLs, options.ImageURLs) || !reflect.DeepEqual(got.Videos(), options.Videos()) || !reflect.DeepEqual(got.Audios(), options.Audios()) {
			t.Fatalf("references changed: %+v", got)
		}
		unsupported := model
		unsupported.SupportsAssetReferences = false
		if _, err = service.normalizeOptions(unsupported, options); !errors.Is(err, ErrOptionsInvalid) {
			t.Fatalf("unsupported assets accepted: %v", err)
		}
	}
	for _, ref := range []string{"asset://", "asset:asset-one", "asset:///asset-one", "asset://asset-one/path", "asset://asset-one?x=1", "asset://asset-one#fragment", "asset://user@asset-one", "asset://asset-one:80", "asset://asset%2Done", "asset://asset one", "asset://asset-one\n", "file:///tmp/image"} {
		for _, options := range []MediaOptions{{ImageURLs: []string{ref}}, {VideoURLs: []string{ref}}, {AudioURLs: []string{ref}}, {VideoURL: ref}, {AudioURL: ref}} {
			if _, err := service.normalizeOptions(model, options); !errors.Is(err, ErrOptionsInvalid) {
				t.Fatalf("accepted %q: %v", ref, err)
			}
		}
	}
	if _, err := service.normalizeOptions(model, MediaOptions{VideoURLs: []string{"asset://asset-one"}, VideoURL: "asset://asset-two"}); !errors.Is(err, ErrOptionsInvalid) {
		t.Fatal("conflicting aliases accepted")
	}
}
