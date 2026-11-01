package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

type mediaCountTransport func(*http.Request) (*http.Response, error)

func (f mediaCountTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type mediaCountRepository struct {
	core.TaskRepository
	task      core.Task
	key, hash string
	creates   int
}

func (r *mediaCountRepository) CreateOrGet(_ context.Context, t core.Task, key, hash string) (core.Task, bool, error) {
	if r.task.ID != "" && key == r.key {
		if hash != r.hash {
			return core.Task{}, false, core.ErrIdempotencyConflict
		}
		return r.task, false, nil
	}
	if t.AdmissionError != nil {
		return core.Task{}, false, t.AdmissionError
	}
	if t.RequireCost && t.Cost == nil {
		return core.Task{}, false, core.ErrPricingUnavailable
	}
	r.task, r.key, r.hash = t, key, hash
	r.creates++
	return t, true, nil
}
func (*mediaCountRepository) MarkEnqueued(context.Context, string) error { return nil }

type mediaCountQueue struct {
	core.TaskQueue
	ids []string
}

func (q *mediaCountQueue) Enqueue(_ context.Context, id string) error {
	q.ids = append(q.ids, id)
	return nil
}

type mediaCountResolver struct {
	snapshots map[string]core.ResourceSnapshot
	fail      error
}

func (r *mediaCountResolver) ResolveResourceReferences(_ context.Context, _ string, refs []core.ResourceReference) ([]core.ResourceSnapshot, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	out := []core.ResourceSnapshot{}
	for _, ref := range refs {
		s, ok := r.snapshots[ref.BindingID]
		if !ok {
			return nil, core.ErrOptionsInvalid
		}
		s.ReferenceID = ref.ReferenceID
		out = append(out, s)
	}
	return out, nil
}
func mediaCountSnapshot(kind, subtype, alias string) core.ResourceSnapshot {
	return core.ResourceSnapshot{BindingID: uuid.NewString(), ReferenceID: alias, Kind: kind, Subtype: subtype, RemoteID: "trusted-remote", ConnectionIdentity: "count-revision"}
}
func mediaCountOptions(snapshots ...core.ResourceSnapshot) core.MediaOptions {
	o := core.MediaOptions{ResourceSnapshots: snapshots}
	for _, s := range snapshots {
		o.ResourceReferences = append(o.ResourceReferences, core.ResourceReference{BindingID: s.BindingID, ReferenceID: s.ReferenceID})
	}
	return o
}
func mediaCountModel() core.MediaModel {
	return core.MediaModel{ID: "reviewed-media", MediaType: "video", SupportsImageInput: true, RequiresImageInput: true, MinImageInputs: 2, MaxImageInputs: 2, ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "file_resource", MaxCount: 12, Subtypes: []string{"image", "video", "audio"}}}, ReferenceModes: []core.ReferenceMode{{ID: "first_last_frame", Label: "First and last", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}}}}
}

func TestResourceMediaInputFileKeyframeCardinality(t *testing.T) {
	m := mediaCountModel()
	for _, tc := range []struct {
		name    string
		urls    []string
		aliases []string
	}{{"two files", nil, []string{"image_1", "image_2"}}, {"URL then file", []string{"https://media.example/front.png"}, []string{"image_2"}}} {
		t.Run(tc.name, func(t *testing.T) {
			snapshots := []core.ResourceSnapshot{}
			for _, alias := range tc.aliases {
				snapshots = append(snapshots, mediaCountSnapshot("file_resource", "image", alias))
			}
			o := mediaCountOptions(snapshots...)
			o.ImageURLs = tc.urls
			o.ReferenceMode = "first_last_frame"
			if _, e := core.NormalizeModelOptions(m, o); e != nil {
				t.Fatalf("reviewed files must satisfy first/last cardinality: %v", e)
			}
			if e := core.ValidateReferenceMode(m, o); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestResourceMediaInputMiniMaxActualAdmissionAndWireRoles(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		url   bool
		files int
		roles []string
	}{{"first_frame", false, 1, []string{"first_frame"}}, {"last_frame", false, 1, []string{"last_frame"}}, {"first_last_frame", false, 2, []string{"first_frame", "last_frame"}}, {"first_last_frame", true, 1, []string{"first_frame", "last_frame"}}} {
		t.Run(fmt.Sprintf("%s/URL=%t", tc.mode, tc.url), func(t *testing.T) {
			ctx := core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "minimax-fixture", OwnerID: "fixture-provider-owner", Revision: "count-revision"})
			uploads, generations := 0, 0
			var wire map[string]any
			a, e := minimax.New("synthetic-count-key", "", &http.Client{Transport: mediaCountTransport(func(r *http.Request) (*http.Response, error) {
				var payload any
				switch r.URL.Path {
				case "/v1/files/upload":
					uploads++
					if e := r.ParseMultipartForm(1 << 20); e != nil {
						t.Fatal(e)
					}
					f, _, e := r.FormFile("file")
					if e != nil {
						t.Fatal(e)
					}
					defer f.Close()
					_, _ = io.Copy(io.Discard, f)
					payload = map[string]any{"file": map[string]any{"file_id": fmt.Sprint(450 + uploads), "purpose": "video_generation_input", "bytes": 7}, "base_resp": map[string]any{"status_code": 0}}
				case "/v2/video_generation":
					generations++
					if e := json.NewDecoder(r.Body).Decode(&wire); e != nil {
						t.Fatal(e)
					}
					payload = map[string]any{"task_id": "123", "base_resp": map[string]any{"status_code": 0}}
				default:
					t.Fatalf("unexpected mock route: %s", r.URL.Path)
				}
				raw, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})})
			if e != nil {
				t.Fatal(e)
			}
			o := core.MediaOptions{ReferenceMode: tc.mode, Ratio: "adaptive"}
			if tc.url {
				o.ImageURLs = []string{"https://media.example/front.png"}
			}
			resolver := &mediaCountResolver{snapshots: map[string]core.ResourceSnapshot{}}
			for i := 0; i < tc.files; i++ {
				f, e := a.UploadFile(ctx, fmt.Sprintf("image%d.png", i), "image/png", strings.NewReader("fixture"))
				if e != nil {
					t.Fatal(e)
				}
				s := mediaCountSnapshot("file_resource", "image", fmt.Sprintf("image_%d", len(o.ImageURLs)+i+1))
				s.RemoteID = f.Handle
				resolver.snapshots[s.BindingID] = s
				o.ResourceReferences = append(o.ResourceReferences, core.ResourceReference{BindingID: s.BindingID, ReferenceID: s.ReferenceID})
			}
			repo := &mediaCountRepository{}
			queue := &mediaCountQueue{}
			service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"minimax-fixture": a}, map[string]string{"minimax-fixture": "MiniMax-H3"}, "minimax-fixture")
			service.EnableAccounting()
			service.SetReferenceResourceResolver(resolver)
			input := core.CreateMediaInput{Prompt: "A synthetic scene", Options: o}
			task, e := service.CreateMediaRequest(ctx, input, "keyframes")
			if e != nil {
				t.Fatalf("actual reviewed file-bound keyframes rejected by shared admission: %v", e)
			}
			if repo.creates != 1 || len(queue.ids) != 1 || generations != 0 || uploads != tc.files {
				t.Fatal("admission performed provider generation or duplicated uploads")
			}
			if _, e = a.Generate(ctx, core.MediaRequest{TaskID: task.ID, Model: task.Model, Prompt: task.Prompt, Options: task.Options}); e != nil {
				t.Fatal(e)
			}
			content, ok := wire["content"].([]any)
			if !ok || len(content) != len(tc.roles)+1 {
				t.Fatalf("wrong native content: %#v", wire)
			}
			for i, role := range tc.roles {
				part := content[i+1].(map[string]any)
				if part["role"] != role {
					t.Fatalf("position%d role %#v want%s", i, part, role)
				}
				u := part["image_url"].(map[string]any)["url"].(string)
				if (!tc.url || i > 0) && !strings.HasPrefix(u, "mm_file://") {
					t.Fatalf("managed binding lost native file identity: %s", u)
				}
			}
			resolver.fail = errors.New("binding deleted after acceptance")
			replay, e := service.CreateMediaRequest(ctx, input, "keyframes")
			if e != nil || replay.ID != task.ID || repo.creates != 1 || generations != 1 {
				t.Fatal("accepted immutable replay depended on live binding", e)
			}
			if _, e = service.CreateMediaRequest(ctx, input, "new-key"); !errors.Is(e, resolver.fail) || repo.creates != 1 {
				t.Fatal("new unavailable binding admitted", e)
			}
		})
	}
}

func TestResourceMediaInputKlingKeyframesAndNonFileResources(t *testing.T) {
	a, e := kling.New("synthetic-count-kling", "", &http.Client{Transport: mediaCountTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("Kling cardinality must not contact provider")
		return nil, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	models, e := a.ListModels(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var m core.MediaModel
	for _, candidate := range models {
		if candidate.ID == kling.ImageToVideo4 {
			m = candidate
		}
	}
	for _, urls := range [][]string{{"https://media.example/first.png"}, {"https://media.example/first.png", "https://media.example/last.png"}} {
		mode := "first_frame"
		if len(urls) == 2 {
			mode = "first_last_frame"
		}
		if _, e := core.NormalizeModelOptions(m, core.MediaOptions{ReferenceMode: mode, ImageURLs: urls}); e != nil {
			t.Fatal("existing Kling URL keyframe behavior regressed", e)
		}
	}
	m = mediaCountModel()
	m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "element", MaxCount: 2, Subtypes: []string{"multi_image"}}, {Kind: "voice", MaxCount: 1}}
	for _, s := range []core.ResourceSnapshot{mediaCountSnapshot("element", "multi_image", "subject"), mediaCountSnapshot("voice", "custom", "speaker")} {
		o := mediaCountOptions(s)
		o.ReferenceMode = "first_last_frame"
		if _, e := core.NormalizeModelOptions(m, o); !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal("element or voice satisfied image cardinality", e)
		}
	}
}

func TestResourceMediaInputCountsRejectUnresolvedOrAmbiguousFiles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*core.MediaModel, *core.MediaOptions)
	}{
		{"missing snapshot", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceSnapshots = nil }},
		{"missing reference", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceReferences = nil }},
		{"different binding", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceSnapshots[0].BindingID = uuid.NewString() }},
		{"missing remote identity", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceSnapshots[0].RemoteID = "" }},
		{"missing connection revision", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceSnapshots[0].ConnectionIdentity = "" }},
		{"unreviewed model", func(m *core.MediaModel, _ *core.MediaOptions) { m.ResourceInputSchemas = nil }},
		{"wildcard subtype schema", func(m *core.MediaModel, _ *core.MediaOptions) { m.ResourceInputSchemas[0].Subtypes = nil }},
		{"unknown subtype", func(_ *core.MediaModel, o *core.MediaOptions) { o.ResourceSnapshots[0].Subtype = "caller-video" }},
		{"unordered alias", func(_ *core.MediaModel, o *core.MediaOptions) {
			o.ResourceReferences[0].ReferenceID = "image_2"
			o.ResourceSnapshots[0].ReferenceID = "image_2"
		}},
		{"duplicate binding", func(_ *core.MediaModel, o *core.MediaOptions) {
			o.ResourceReferences = append(o.ResourceReferences, o.ResourceReferences[0])
			o.ResourceSnapshots = append(o.ResourceSnapshots, o.ResourceSnapshots[0])
		}},
		{"duplicate alias", func(_ *core.MediaModel, o *core.MediaOptions) {
			other := mediaCountSnapshot("file_resource", "image", "image_1")
			o.ResourceReferences = append(o.ResourceReferences, core.ResourceReference{BindingID: other.BindingID, ReferenceID: other.ReferenceID})
			o.ResourceSnapshots = append(o.ResourceSnapshots, other)
		}},
		{"URL alias collision", func(_ *core.MediaModel, o *core.MediaOptions) {
			o.ImageURLs = []string{"https://media.example/first.png"}
		}},
		{"conflicting provenance", func(_ *core.MediaModel, o *core.MediaOptions) {
			o.ResourceSnapshots[0].Sources = []core.ResourceSourceEvidence{{MediaType: "video"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mediaCountModel()
			o := mediaCountOptions(mediaCountSnapshot("file_resource", "image", "image_1"))
			tc.change(&m, &o)
			if _, e := core.CountMediaInputs(m, o); !errors.Is(e, core.ErrOptionsInvalid) {
				t.Fatal("invalid file influenced counts", e)
			}
		})
	}
}

func TestResourceMediaInputCountsOneBindingOnceAndLimitsEveryKind(t *testing.T) {
	m := mediaCountModel()
	m.RequiresImageInput = false
	m.MinImageInputs = 0
	m.MaxImageInputs = 9
	m.SupportsVideoInput = true
	m.SupportsAudioInput = true
	m.ReferenceModes = []core.ReferenceMode{{ID: "references", MaxImages: 9, MaxVideos: 3, MaxAudios: 3}}
	image := mediaCountSnapshot("file_resource", "image", "image_1")
	image.Sources = []core.ResourceSourceEvidence{{MediaType: "image"}, {MediaType: "image"}, {MediaType: "image"}}
	o := mediaCountOptions(image, mediaCountSnapshot("file_resource", "video", "video_1"), mediaCountSnapshot("file_resource", "audio", "audio_1"))
	counts, e := core.CountMediaInputs(m, o)
	if e != nil || counts.Images != 1 || counts.Videos != 1 || counts.Audios != 1 {
		t.Fatal("bindings counted by provenance or wrong kind", counts, e)
	}
	if _, e = core.NormalizeModelOptions(m, o); e != nil {
		t.Fatal("reviewed mixed files were rejected", e)
	}
	for _, kind := range []string{"video", "audio"} {
		scoped := m
		scoped.RequiresVideoInput = kind == "video"
		scoped.RequiresAudioInput = kind == "audio"
		if _, e = core.NormalizeModelOptions(scoped, o); e != nil {
			t.Fatal("managed file did not satisfy required media", kind, e)
		}
		scoped.SupportsVideoInput = kind != "video"
		scoped.SupportsAudioInput = kind != "audio"
		if _, e = core.NormalizeModelOptions(scoped, o); !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal("unsupported managed file admitted", kind, e)
		}
	}
	m.ReferenceModes[0].MaxVisuals = 1
	if e = core.ValidateReferenceMode(m, o); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("managed visuals bypassed combined cap", e)
	}
	m.ReferenceModes[0].MaxVisuals = 0
	m.ReferenceModes[0].MaxDurationWithVideo = 8
	o.Duration = 9
	if e = core.ValidateReferenceMode(m, o); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("managed video bypassed duration restriction", e)
	}
	m = mediaCountModel()
	tooMany := mediaCountOptions(mediaCountSnapshot("file_resource", "image", "image_1"), mediaCountSnapshot("file_resource", "image", "image_2"), mediaCountSnapshot("file_resource", "image", "image_3"))
	if _, e = core.NormalizeModelOptions(m, tooMany); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("managed images bypassed maximum", e)
	}
	// Explicitly reviewed non-media file resources retain resource semantics.
	m.ResourceInputSchemas[0].Subtypes = append(m.ResourceInputSchemas[0].Subtypes, "regeneration_source")
	c, e := core.CountMediaInputs(m, mediaCountOptions(mediaCountSnapshot("file_resource", "regeneration_source", "source")))
	if e != nil || c.Images+c.Videos+c.Audios != 0 {
		t.Fatal("regeneration source converted to a media input", c, e)
	}
}

func TestResourceMediaInputRolesUseURLThenFilePositions(t *testing.T) {
	m := mediaCountModel()
	m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"source", "target"}, Default: "source"}}
	o := mediaCountOptions(mediaCountSnapshot("file_resource", "image", "image_2"))
	o.ImageURLs = []string{"https://media.example/front.png"}
	o.ReferenceRoles = map[string]string{"image_2": "target"}
	if e := core.ValidateReferenceRoles(m, o); e != nil {
		t.Fatal("reviewed file role lost positional identity", e)
	}
	if core.ReferenceRole(m, o, "image", 1) != "target" {
		t.Fatal("file role changed")
	}
	o.ReferenceRoles = map[string]string{"image_3": "target"}
	if e := core.ValidateReferenceRoles(m, o); !errors.Is(e, core.ErrOptionsInvalid) {
		t.Fatal("role addressed missing file", e)
	}
}

func TestResourceMediaInputMaterializedPairsAreConsumedAndCallerEvidenceCleared(t *testing.T) {
	m := mediaCountModel()
	m.SupportsAssetReferences = true
	o := core.MediaOptions{ReferenceMode: "first_last_frame", ImageURLs: []string{"asset://trusted_file_1", "asset://trusted_file_2"}}
	counts, e := core.CountMediaInputs(m, o)
	if e != nil || counts.Images != 2 {
		t.Fatal("materialized files counted twice", counts, e)
	}
	// The host resolves ResourceReferences afresh; caller-supplied snapshots cannot
	// satisfy cardinality even if they otherwise resemble a trusted file binding.
	a, e := minimax.New("synthetic-count-key", "", &http.Client{Transport: mediaCountTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("forged source contacted provider")
		return nil, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	repo := &mediaCountRepository{}
	q := &mediaCountQueue{}
	svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": a}, map[string]string{"minimax": "MiniMax-H3"}, "minimax")
	forged := mediaCountOptions(mediaCountSnapshot("file_resource", "image", "image_1"))
	forged.ResourceReferences = nil
	forged.ReferenceMode = "first_frame"
	forged.Ratio = "adaptive"
	if _, e = svc.CreateMediaRequest(context.Background(), core.CreateMediaInput{Prompt: "scene", Options: forged}, "forged"); !errors.Is(e, core.ErrOptionsInvalid) || repo.creates != 0 {
		t.Fatal("caller asserted private file snapshot", e)
	}
}
