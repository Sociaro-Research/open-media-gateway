package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestGW253InspectionEvidenceUsesCanonicalSource(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	ctx := gw253Context("alice")
	item, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(testmedia.MP4(4000, 1000, 120)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://substituted.invalid/v1/videos", nil).WithContext(ctx)
	canonical := s.assetPublicURL + s.referenceView(request, item).URL
	altered := strings.Replace(canonical, s.assetPublicURL, "https://substituted.invalid", 1) + "&source=other-bytes"
	input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", Duration: 10, VideoURLs: []string{altered}}}
	if err := s.prepareGenerationReferences(request, &input); err != nil {
		t.Fatal(err)
	}
	if input.Options.VideoURLs[0] != canonical || input.ReferenceEvidence[canonical].Duration != 4 {
		t.Fatalf("inspected evidence did not bind to canonical source: urls=%v evidence=%v", input.Options.VideoURLs, input.ReferenceEvidence)
	}
	if _, ok := input.ReferenceEvidence[altered]; ok {
		t.Fatal("caller-controlled source retained trusted evidence")
	}
}

func TestGW253UnverifiedSourcesCannotInheritSpoofedInspectionEvidence(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	ctx := gw253Context("alice")
	item, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(testmedia.MP4(4000, 1000, 120)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://untrusted-request-host.invalid", nil).WithContext(ctx)
	canonical := s.assetPublicURL + s.referenceView(request, item).URL
	for _, raw := range []string{"https://vendor.example/video.mp4", canonical + "invalid", canonical + "&signature=other", strings.Replace(canonical, item.ID, "469eda6a-8f12-4b5b-b5f7-b505e3cea208", 1), strings.Replace(canonical, "/references/", "/%72eferences/", 1)} {
		input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", Duration: 10, VideoURLs: []string{raw}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{raw: {Type: "video", Duration: .01}}}}
		if err := s.prepareGenerationReferences(request, &input); err != nil {
			t.Fatal(err)
		}
		if input.Options.VideoURLs[0] != raw || len(input.ReferenceEvidence) != 0 || len(input.Options.ReferenceMediaEvidence) != 0 {
			t.Fatal("unverified URL inherited trusted evidence", input)
		}
	}
}

func TestGW253InspectionDoesNotMutateCallerContainers(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	ctx := gw253Context("alice")
	item, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(testmedia.MP4(4000, 1000, 120)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://untrusted-request-host.invalid", nil).WithContext(ctx)
	canonical := s.assetPublicURL + s.referenceView(request, item).URL
	altered := strings.Replace(canonical, s.assetPublicURL, "https://substituted.invalid", 1)
	callerURLs := []string{altered}
	callerEvidence := map[string]core.InputMediaEvidence{altered: {Type: "video", Duration: .01}}
	input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", Duration: 10, VideoURLs: callerURLs}, ReferenceEvidence: callerEvidence}
	if err := s.prepareGenerationReferences(request, &input); err != nil {
		t.Fatal(err)
	}
	callerURLs[0] = "https://mutated.invalid/video.mp4"
	callerEvidence[altered] = core.InputMediaEvidence{Type: "video", Duration: 99}
	if input.Options.VideoURLs[0] != canonical || input.ReferenceEvidence[canonical].Duration != 4 {
		t.Fatal("caller containers changed inspected input")
	}
}

func TestGW253MissingCanonicalOriginFailsInspectionClosed(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	ctx := gw253Context("alice")
	item, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(testmedia.MP4(4000, 1000, 120)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://untrusted-request-host.invalid", nil).WithContext(ctx)
	raw := s.assetPublicURL + s.referenceView(request, item).URL
	s.assetPublicURL = ""
	input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", Duration: 10, VideoURLs: []string{raw}}}
	if err := s.prepareGenerationReferences(request, &input); err == nil || len(input.ReferenceEvidence) != 0 {
		t.Fatal("incoming request Host replaced missing configured origin")
	}
}

func TestGW253ReviewAPIVoiceAliasesHaveDeterministicIntent(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	svc.catalog[0].Models[0].ReferenceModes[0].SupportsReferenceVoice = true
	ctx := gw253Context("alice")
	request := httptest.NewRequest("POST", "https://untrusted-request-host.invalid", nil).WithContext(ctx)
	visual, err := s.references.Upload(ctx, "visual.png", bytes.NewReader(referencePNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	voice1, err := s.references.Upload(ctx, "voice-1.wav", bytes.NewReader(testmedia.WAV()))
	if err != nil {
		t.Fatal(err)
	}
	audio2 := testmedia.WAV()
	audio2[len(audio2)-1] ^= 1
	voice2, err := s.references.Upload(ctx, "voice-2.wav", bytes.NewReader(audio2))
	if err != nil {
		t.Fatal(err)
	}
	visualURL := s.assetPublicURL + s.referenceView(request, visual).URL
	voiceURL := s.assetPublicURL + s.referenceView(request, voice1).URL
	otherVoiceURL := s.assetPublicURL + s.referenceView(request, voice2).URL
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"GW-253 voice aliases"}`)
	random := rand.New(rand.NewSource(253))
	for _, conflict := range []bool{false, true} {
		expectedError := ""
		expectedResponse := ""
		for attempt := 0; attempt < 32; attempt++ {
			secondVoice := strings.Replace(voiceURL, s.assetPublicURL, "https://voice-alias.invalid", 1) + "&alias=second"
			if conflict {
				secondVoice = otherVoiceURL
			}
			entries := [][2]string{{strings.Replace(visualURL, s.assetPublicURL, "https://visual-alias.invalid", 1), voiceURL}, {visualURL + "&alias=second", secondVoice}}
			voices := map[string]string{}
			for _, i := range random.Perm(len(entries)) {
				voices[entries[i][0]] = entries[i][1]
			}
			before, _ := json.Marshal(voices)
			input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{visualURL}, ReferenceVoices: voices}}
			err := s.prepareGenerationReferences(request, &input)
			after, _ := json.Marshal(voices)
			if !bytes.Equal(before, after) {
				t.Fatal("API changed caller voice aliases")
			}
			if conflict {
				if !errors.Is(err, referencevalidation.ErrInvalid) || !strings.Contains(err.Error(), "Conflicting voices") {
					t.Fatalf("conflicting aliases accepted: %v %+v", err, input.Options.ReferenceVoices)
				}
				if expectedError == "" {
					expectedError = err.Error()
				}
				if err.Error() != expectedError {
					t.Fatal("map order changed rejection", err)
				}
				body := map[string]any{"model": "mock/dance", "prompt": "Synthetic", "input_reference": visualURL, "options": map[string]any{"reference_mode": "references", "reference_voices": voices}}
				response := gw253Request(h, "/v1/generations/estimate", key, body, "")
				if response.Code != 400 || !strings.Contains(response.Body.String(), "Conflicting voices") {
					t.Fatal(response.Code, response.Body.String())
				}
				if expectedResponse == "" {
					expectedResponse = response.Body.String()
				}
				if response.Body.String() != expectedResponse {
					t.Fatal("map order changed public rejection")
				}
			} else {
				if err != nil || len(input.Options.ReferenceVoices) != 1 || input.Options.ReferenceVoices[visualURL] != voiceURL {
					t.Fatal("identical aliases changed intent", err, input.Options.ReferenceVoices)
				}
				input.Options.ReferenceVoices[visualURL] = "changed"
				unchanged, _ := json.Marshal(voices)
				if !bytes.Equal(before, unchanged) {
					t.Fatal("prepared voice map shares caller storage")
				}
			}
		}
	}
}

func gw253Context(actor string) context.Context {
	return identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: actor}))
}
func gw253Request(h http.Handler, path, key string, body any, idem string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "https://gateway.example.test"+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if idem != "" {
		r.Header.Set("Idempotency-Key", idem)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
