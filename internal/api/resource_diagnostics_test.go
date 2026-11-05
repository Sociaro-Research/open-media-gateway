package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

func TestResourceIntentDiagnosticsReachPreparationAPI(t *testing.T) {
	for _, path := range []string{"/v1/provider-resources", "/v1/provider-resources/estimate"} {
		for _, tc := range []struct {
			field  string
			change func(map[string]any)
			hint   string
		}{
			{"name", func(body map[string]any) { body["name"] = " " }, "nonempty"},
			{"reference_ids.0", func(body map[string]any) { body["reference_ids"] = []string{"private-value"} }, "UUID"},
			{"reference_ids", func(body map[string]any) { body["reference_ids"] = []string{} }, "source"},
		} {
			t.Run(path+tc.field, func(t *testing.T) {
				f := newResourceAPIFixture(t)
				ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
				body := resourceAPIVoice(ref.ID)
				tc.change(body)
				w := resourceAPIRequest(f.h, "POST", path, f.alice, "diagnostic", body)
				var got struct {
					Error struct{ Code, Param, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if w.Code != 400 || got.Error.Code != "invalid_resource" || got.Error.Param != tc.field || !strings.Contains(got.Error.Message, tc.hint) {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				if strings.Contains(w.Body.String(), "private-value") || f.tasks.creates != 0 || len(f.queue.ids) != 0 {
					t.Fatal("reject reflected value or admitted task", w.Body)
				}
			})
		}
	}
}

func TestResourceInspectedSourceDiagnosisAndPrivateMetadata(t *testing.T) {
	for _, path := range []string{"/v1/provider-resources", "/v1/provider-resources/estimate"} {
		f := newResourceAPIFixture(t)
		ref := f.upload(t, "alice", "private-filename.wav", resourceAPIWAV(2))
		w := resourceAPIRequest(f.h, "POST", path, f.alice, "bad-duration", resourceAPIVoice(ref.ID))
		var got struct {
			Error struct{ Param, Message, Code string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || got.Error.Code != "invalid_resource" || got.Error.Param != "reference_ids.0" || !strings.Contains(got.Error.Message, "duration 2 seconds") || !strings.Contains(got.Error.Message, "5–30 seconds") || strings.Contains(w.Body.String(), "private-filename") || f.tasks.creates != 0 || len(f.queue.ids) != 0 {
			t.Fatal(w.Code, w.Body)
		}
		// Corrected source follows the existing estimate/admission contract.
		ref = f.upload(t, "alice", "valid.wav", resourceAPIWAV(5))
		w = resourceAPIRequest(f.h, "POST", path, f.alice, "corrected-duration", resourceAPIVoice(ref.ID))
		status, creates := 202, 1
		if strings.HasSuffix(path, "/estimate") {
			status, creates = 200, 0
		}
		if w.Code != status || f.tasks.creates != creates || len(f.queue.ids) != creates {
			t.Fatal(w.Code, w.Body)
		}
	}
	model := core.MediaModel{ID: "private-model", DisplayName: "private-model", ResourceOperation: &core.ResourceOperationSchema{Variants: []core.ResourceVariantSchema{{Subtype: "custom", SourceKinds: []string{"audio"}, Limits: &core.ReferenceMediaLimits{Audio: core.MediaFileLimits{ContentTypes: []string{"audio/wav"}, Codecs: []string{"pcm"}, MinDuration: 5}}}}}}
	for _, tc := range []struct {
		info mediafile.Info
		hint string
	}{
		{mediafile.Info{ContentType: "private-mime", Codec: "private-codec", Duration: 6}, "audio/wav"},
		{mediafile.Info{ContentType: "audio/wav", Codec: "private-codec", Duration: 6}, "pcm"},
		{mediafile.Info{ContentType: "audio/wav", Codec: "pcm", Duration: 2}, "at least 5"},
	} {
		err := validateResourceSourceFile(model, "custom", referencevalidation.File{Name: "private-file", Type: "audio", Info: tc.info})
		if !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		(&Server{}).resourceError(w, fmt.Errorf("private-wrapper: %w", err))
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.hint) || strings.Contains(w.Body.String(), "private-") {
			t.Fatal(w.Body)
		}
	}
	// Malicious and overlong schema strings cannot become diagnostics either.
	for _, choice := range []string{"https://private-secret.example/token", strings.Repeat("a", 4096)} {
		model.ResourceOperation.Variants[0].Limits.Audio.ContentTypes = []string{choice}
		err := validateResourceSourceFile(model, "custom", referencevalidation.File{Type: "audio", Info: mediafile.Info{ContentType: "audio/wav"}})
		w := httptest.NewRecorder()
		(&Server{}).resourceError(w, err)
		if w.Code != 400 || strings.Contains(w.Body.String(), choice) || len(w.Body.Bytes()) > 2500 {
			t.Fatal(w.Body)
		}
	}
}

func TestResourceDiagnosticProjectionAndGenerationFieldMapping(t *testing.T) {
	model := core.MediaModel{ResourceOperation: &core.ResourceOperationSchema{Kind: "voice"}}
	err := core.ValidateResourceCreationIntent(model, core.MediaOptions{ResourceIntent: &core.ResourceIntent{Kind: "voice"}}, true)
	w := httptest.NewRecorder()
	generationValidationError(w, core.CreateMediaInput{}, err)
	if !strings.Contains(w.Body.String(), `"param":"options.resource_intent.name"`) {
		t.Fatal(w.Body)
	}
	// Synchronous validation hints are not a newly allowed persisted failure.
	if strings.Contains(core.PublicGenerationFailure(err.Error()), "nonempty") {
		t.Fatal("historical read boundary broadened")
	}
	for _, hidden := range []error{providerresources.ErrNotFound, core.ErrTaskNotFound} {
		w := httptest.NewRecorder()
		(&Server{}).resourceError(w, fmt.Errorf("private-value: %w", hidden))
		if w.Code != 404 || strings.Contains(w.Body.String(), "private-value") {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestResourceMapperRejectsUntrustedDiagnostics(t *testing.T) {
	s := &Server{}
	for _, err := range []error{fmt.Errorf("%w: private-value", core.ErrOptionsInvalid), fmt.Errorf("%w: private-value", providerresources.ErrInvalid), &core.OptionValidationError{Field: "name", Hint: "private-value"}} {
		for index, mapper := range []func(*httptest.ResponseRecorder){func(w *httptest.ResponseRecorder) { s.resourceError(w, err) }, func(w *httptest.ResponseRecorder) { s.resourceAdmissionError(w, err) }} {
			w := httptest.NewRecorder()
			mapper(w)
			status := 400
			if index == 1 && errors.Is(err, providerresources.ErrInvalid) {
				status = 503
			}
			if w.Code != status || strings.Contains(w.Body.String(), "private-value") {
				t.Fatal(w.Code, w.Body)
			}
		}
	}
}
