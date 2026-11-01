package core_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
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

func TestResourceMediaInputMaterializedPairsAreConsumed(t *testing.T) {
	m := mediaCountModel()
	m.SupportsAssetReferences = true
	o := core.MediaOptions{ReferenceMode: "first_last_frame", ImageURLs: []string{"asset://trusted_file_1", "asset://trusted_file_2"}}
	counts, e := core.CountMediaInputs(m, o)
	if e != nil || counts.Images != 2 {
		t.Fatal("materialized files counted twice", counts, e)
	}

}
