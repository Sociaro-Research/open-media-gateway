package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/google/uuid"
)

type omniMockTransport func(*http.Request) (*http.Response, error)

func (f omniMockTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type omniAdmissionRepository struct{ admissionRepository }

func (r *omniAdmissionRepository) CreateOrGet(ctx context.Context, task core.Task, key, hash string) (core.Task, bool, error) {
	if r.task.ID != "" {
		return r.admissionRepository.CreateOrGet(ctx, task, key, hash)
	}
	if task.AdmissionError != nil {
		return core.Task{}, false, task.AdmissionError
	}
	if task.RequireCost && task.Cost == nil {
		return core.Task{}, false, core.ErrPricingUnavailable
	}
	return r.admissionRepository.CreateOrGet(ctx, task, key, hash)
}

func omniFixture(t *testing.T) (*Server, *templateMemory, *omniAdmissionRepository, core.MediaProvider) {
	t.Helper()
	s, templatesRepo, _, _ := templateFixture(t)
	client := &http.Client{Transport: omniMockTransport(func(*http.Request) (*http.Response, error) { t.Fatal("admission contacted Kling"); return nil, nil })}
	adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "synthetic-omni-key"}, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	repo := &omniAdmissionRepository{}
	svc := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"kling": adapter}, map[string]string{"kling": kling.OmniVideo4}, "kling")
	svc.EnableAccounting()
	s.service = svc
	return s, templatesRepo, repo, adapter
}
func omniContext(actor string) context.Context {
	return identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: actor}))
}
func omniPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 512, 512))
	draw.Draw(im, im.Bounds(), &image.Uniform{C: color.RGBA{R: 30, G: 90, B: 180, A: 255}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func omniVideo() []byte {
	b := testmedia.MP4(4000, 1000, 120)
	i := bytes.Index(b, []byte("tkhd")) + 4
	binary.BigEndian.PutUint32(b[i+76:], 1280<<16)
	binary.BigEndian.PutUint32(b[i+80:], 720<<16)
	return b
}
func omniRequest(h http.Handler, path, key string, body any, idempotency string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "https://gateway.example.test"+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if idempotency != "" {
		r.Header.Set("Idempotency-Key", idempotency)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestKlingOmniAuthenticatedAdmissionInspectsAndPinsVideoDuration(t *testing.T) {
	s, _, repo, _ := omniFixture(t)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Omni local fixture"}`)
	ctx := omniContext("alice")
	video, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(omniVideo()))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "https://gateway.example.test", nil).WithContext(ctx)
	videoURL := s.referenceAbsoluteURL(request, video)
	body := map[string]any{"model": "kling/" + kling.OmniVideo4, "prompt": "Follow @Video1", "seconds": "10", "options": map[string]any{"reference_mode": "references", "video_urls": []string{videoURL}, "reference_roles": map[string]string{"video_1": "source"}}}
	if w := omniRequest(h, "/v1/generations/estimate", "", body, ""); w.Code != 401 {
		t.Fatal("unauthenticated quote", w.Code, w.Body.String())
	}
	w := omniRequest(h, "/v1/generations/estimate", key, body, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"expected_usd":2.82`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = omniRequest(h, "/v1/videos", key, body, "omni-idempotent")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if repo.task.Options.ReferenceMediaEvidence[videoURL].Duration != 4 || repo.task.Cost.ExpectedUSD != 2820000 || repo.task.Prompt != "Follow @Video1" {
		t.Fatal("inspected intent not pinned", repo.task)
	}
	if strings.Contains(w.Body.String(), "_reference_media") || strings.Contains(w.Body.String(), "synthetic-omni-key") {
		t.Fatal("internal evidence or credential leaked", w.Body.String())
	}
	replay := omniRequest(h, "/v1/videos", key, body, "omni-idempotent")
	if replay.Code != 202 || repo.creates != 1 {
		t.Fatal("retry duplicated admission", replay.Code, replay.Body.String(), repo.creates)
	}
	options := body["options"].(map[string]any)
	options["reference_roles"] = map[string]string{"video_1": "reference"}
	changed := omniRequest(h, "/v1/videos", key, body, "omni-idempotent")
	if changed.Code != 409 {
		t.Fatal("changed role replayed old intent", changed.Code, changed.Body.String())
	}
}

func TestKlingOmniRejectsSpoofedEvidenceRolesAndForeignReferences(t *testing.T) {
	for _, kind := range []string{"evidence", "top-level evidence", "parameter evidence", "missing role input", "wire role", "foreign input", "invalid signature"} {
		t.Run(kind, func(t *testing.T) {
			s, _, repo, _ := omniFixture(t)
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"Omni spoof fixture"}`)
			owner := "alice"
			if kind == "foreign input" {
				owner = "bob"
			}
			ctx := omniContext(owner)
			ref, err := s.references.Upload(ctx, "private.mp4", bytes.NewReader(omniVideo()))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "https://gateway.example.test", nil).WithContext(ctx)
			rawURL := s.referenceAbsoluteURL(request, ref)
			options := map[string]any{"reference_mode": "references", "video_urls": []string{rawURL}}
			body := map[string]any{"model": "kling/" + kling.OmniVideo4, "prompt": "Synthetic", "seconds": "10", "options": options}
			expected := 400
			switch kind {
			case "evidence":
				options["_reference_media"] = map[string]any{rawURL: map[string]any{"type": "video", "duration": .01}}
			case "top-level evidence":
				body["reference_evidence"] = map[string]any{rawURL: map[string]any{"duration": .01}}
			case "parameter evidence":
				options["parameters"] = map[string]any{"input_video_duration": .01}
			case "missing role input":
				options["reference_roles"] = map[string]string{"video_2": "source"}
			case "wire role":
				options["reference_roles"] = map[string]string{"video_1": "base_video"}
			case "foreign input":
				expected = 404
			case "invalid signature":
				options["video_urls"] = []string{rawURL + "tampered"}
				expected = 422
			}
			w := omniRequest(h, "/v1/generations/estimate", key, body, "")
			if w.Code != expected || repo.creates != 0 {
				t.Fatal(kind, w.Code, w.Body.String(), repo.creates)
			}
			if strings.Contains(w.Body.String(), "private.mp4") {
				t.Fatal("foreign filename disclosed", w.Body.String())
			}
		})
	}
}

func TestKlingOmniTemplateFixedVideoAndTwoImagesUseActualQuote(t *testing.T) {
	s, library, repo, _ := omniFixture(t)
	h := s.handler()
	id := uuid.NewString()
	models := s.service.ListProviders(omniContext("alice"))[0].Models
	var model core.MediaModel
	for _, m := range models {
		if m.ID == kling.OmniVideo4 {
			model = m
		}
	}
	d := templates.Definition{Title: "Synthetic Omni template", Provider: "kling", Model: kling.OmniVideo4, MediaType: "video", Syntax: templates.UIReferenceSyntax, Prompt: "@Image1 and @Image2 follow @Video1", Options: core.MediaOptions{ReferenceMode: "references", Duration: 10}}
	var err error
	d.Slots, err = d.InferSlots(model)
	if err != nil {
		t.Fatal(err)
	}
	library.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	d.CoverID = templateCopy(t, s, id, omniPNG(t)).ID
	fixed := templateCopy(t, s, id, omniVideo())
	for i := range d.Slots {
		if d.Slots[i].Type == "video" {
			d.Slots[i].AssetID = fixed.ID
		}
	}
	data, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
	published := sendTest(h, "PUT", "/v1/system/templates/"+id, string(data), "alice", "")
	if published.Code != 200 {
		t.Fatal(published.Code, published.Body.String())
	}
	ctx := omniContext("alice")
	imageRef, err := s.references.Upload(ctx, "image.png", bytes.NewReader(omniPNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Omni template fixture"}`)
	detail := sendTest(h, "GET", "/v1/templates/"+id, "", "", key)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), kling.OmniVideo4) {
		t.Fatal("Omni excluded despite compatible slots", detail.Code, detail.Body.String())
	}
	body := map[string]any{"revision": 2, "provider": "kling", "model": kling.OmniVideo4, "inputs": map[string]string{"image:1": imageRef.ID, "image:2": imageRef.ID}, "max_expected_cost_usd": 2.82}
	quote := omniRequest(h, "/v1/templates/"+id+"/estimate", key, body, "")
	if quote.Code != 200 || !strings.Contains(quote.Body.String(), `"expected_usd":2.82`) {
		t.Fatal(quote.Code, quote.Body.String())
	}
	generated := omniRequest(h, "/v1/templates/"+id+"/generations", key, body, "template-omni-fixture")
	if generated.Code != 202 || repo.creates != 1 {
		t.Fatal(generated.Code, generated.Body.String())
	}
	total, known := repo.task.Options.InputVideoDuration()
	if !known || total != 4 || len(repo.task.Options.ImageURLs) != 2 || repo.task.Prompt != d.Prompt || len(repo.task.TemplateSnapshot) == 0 {
		t.Fatal("template evidence/intent changed", repo.task)
	}
	// Native mapping happens only at dispatch; accepted prompt remains byte-identical.
	client := &http.Client{Transport: omniMockTransport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		if !bytes.Contains(b, []byte("@image_1 and @image_2 follow @video_1")) {
			t.Fatal(string(b))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"id":"synthetic_dispatch","status":"submitted"}}`))}, nil
	})}
	adapter, err := kling.Definition().Factory(providerkit.Credentials{APIKey: "synthetic-dispatch"}, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Generate(ctx, core.MediaRequest{TaskID: repo.task.ID, Model: repo.task.Model, Prompt: repo.task.Prompt, Options: repo.task.Options.ProviderOptions()}); err != nil {
		t.Fatal(err)
	}
	if repo.task.Prompt != d.Prompt {
		t.Fatal("dispatch rewrote accepted prompt")
	}
}

func TestGW253QuoteAndAcceptedJobPinCanonicalEvidence(t *testing.T) {
	for _, variation := range []string{"canonical", "host", "query", "host and query"} {
		t.Run(variation, func(t *testing.T) {
			s, _, repo, _ := omniFixture(t)
			ctx := omniContext("alice")
			item, err := s.references.Upload(ctx, "synthetic.mp4", bytes.NewReader(omniVideo()))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "https://untrusted-request-host.invalid", nil).WithContext(ctx)
			canonical := s.assetPublicURL + s.referenceView(request, item).URL
			raw := canonical
			if strings.Contains(variation, "host") {
				raw = strings.Replace(raw, s.assetPublicURL, "https://substituted.invalid", 1)
			}
			if strings.Contains(variation, "query") {
				raw += "&source=different#fragment"
			}
			h := s.handler()
			_, key := issueTestKey(t, h, "alice", `{"name":"GW-253 synthetic fixture"}`)
			body := map[string]any{"model": "kling/" + kling.OmniVideo4, "prompt": "Synthetic", "seconds": "10", "options": map[string]any{"reference_mode": "references", "video_urls": []string{raw}}}
			quote := omniRequest(h, "/v1/generations/estimate", key, body, "")
			if quote.Code != 200 || !strings.Contains(quote.Body.String(), `"expected_usd":2.82`) {
				t.Fatal(quote.Code, quote.Body.String())
			}
			quoteBytes := quote.Body.String()
			accepted := omniRequest(h, "/v1/videos", key, body, "gw253-pinned")
			if accepted.Code != 202 {
				t.Fatal(accepted.Code, accepted.Body.String())
			}
			if repo.task.Options.VideoURLs[0] != canonical || len(repo.task.Options.ReferenceMediaEvidence) != 1 || repo.task.Options.ReferenceMediaEvidence[canonical].Duration != 4 || repo.task.Cost.ExpectedUSD != 2820000 {
				t.Fatalf("wrong accepted source/evidence/cost: %+v", repo.task)
			}
			// Dispatch derives a fresh URL without changing the accepted task or quote.
			resolved, err := references.NewResolver(s.references, "fixture-secret", s.assetPublicURL).ResolveDirect(ctx, repo.task.Options)
			if err != nil || resolved.VideoURLs[0] != canonical {
				t.Fatal(resolved.VideoURLs, err)
			}
			// Exercise the actual capability handler in memory: these are the
			// bytes a provider gets at the resolved canonical URL, with no HTTP.
			served := httptest.NewRecorder()
			h.ServeHTTP(served, httptest.NewRequest("GET", resolved.VideoURLs[0], nil))
			if served.Code != 200 || !bytes.Equal(served.Body.Bytes(), omniVideo()) {
				t.Fatal("canonical delivery differed from inspected bytes", served.Code)
			}
			resolved.VideoURLs[0] = "https://mutated.invalid/other"
			if repo.task.Options.VideoURLs[0] != canonical || repo.task.Options.ReferenceMediaEvidence[canonical].Duration != 4 || quote.Body.String() != quoteBytes {
				t.Fatal("dispatch mutated accepted evidence or prior quote")
			}
			replay := omniRequest(h, "/v1/videos", key, body, "gw253-pinned")
			if replay.Code != 202 || repo.creates != 1 || repo.task.Cost.ExpectedUSD != 2820000 {
				t.Fatal("accepted quote changed on retry", replay.Code, replay.Body.String())
			}
		})
	}
}
