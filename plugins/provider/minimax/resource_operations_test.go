package minimax

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
	"os"
)

type sourceOpenerFunc func(context.Context, core.ResourceSourceInput) (core.ArtifactSource, error)

func (f sourceOpenerFunc) OpenResourceSource(c context.Context, s core.ResourceSourceInput) (core.ArtifactSource, error) {
	return f(c, s)
}
func uploadRequest(data []byte) core.MediaRequest {
	id := uuid.NewString()
	sum := sha256.Sum256(data)
	source := core.ResourceSourceInput{ReferenceID: id, URL: "https://caller-host-must-not-be-used.invalid/source", MediaType: "image", ContentType: "image/png", SizeBytes: int64(len(data)), Width: 300, Height: 300, SHA256: hex.EncodeToString(sum[:])}
	intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "image", Name: "Owned input", ReferenceIDs: []string{id}}
	return core.MediaRequest{TaskID: uuid.NewString(), Model: ResourceUpload, Options: core.MediaOptions{ResourceIntent: intent, ResourceCreation: &core.ResourceCreateRequest{Kind: intent.Kind, Subtype: intent.Subtype, Name: intent.Name, FileSources: []core.ResourceSourceInput{source}}}}
}
func TestFileResourceUsesOnlyTrustedStreamAndExplicitPrice(t *testing.T) {
	data := []byte("known staged fixture bytes")
	calls, opens := 0, 0
	a := fixture(t, func(h *http.Request) (*http.Response, error) {
		calls++
		if h.URL.Path != "/v1/files/upload" {
			t.Fatal(h.URL.Path)
		}
		reader, err := h.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		p, err := reader.NextPart()
		if err != nil || p.FormName() != "purpose" {
			t.Fatal(err)
		}
		v, _ := io.ReadAll(p)
		if string(v) != "video_generation_input" {
			t.Fatal("purpose")
		}
		p, err = reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		v, _ = io.ReadAll(p)
		if !bytes.Equal(v, data) {
			t.Fatal("provider received wrong bytes")
		}
		return response(200, map[string]any{"file": map[string]any{"file_id": "900719925474099312345", "bytes": len(data), "purpose": "video_generation_input", "created_at": time.Now().Unix()}, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	r := uploadRequest(data)
	ctx := core.WithResourceSourceOpener(owned("alice"), sourceOpenerFunc(func(_ context.Context, s core.ResourceSourceInput) (core.ArtifactSource, error) {
		opens++
		if s.ReferenceID != r.Options.ResourceIntent.ReferenceIDs[0] {
			t.Fatal("untrusted source identity")
		}
		return core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: "image/png", SizeBytes: int64(len(data))}, nil
	}))
	m := fileOperationModel(ResourceUpload)
	if _, _, err := a.Quote(ctx, m, r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("undocumented fee fabricated", err)
	}
	price := money.USD(1000)
	a.fileOperationPrice = &price
	quote, _, err := a.Quote(ctx, m, r)
	if err != nil || quote.ExpectedUSD != price || !quote.Pricing.AccountSpecific || quote.Pricing.Source != "operator_confirmed" {
		t.Fatal("confirmed fee not retained", err)
	}
	result, err := a.Generate(ctx, r)
	if err != nil || len(result.Resources) != 1 || result.Resources[0].Kind != "file_resource" || calls != 1 || opens != 1 {
		t.Fatal("trusted upload failed", err, calls, opens)
	}
	h, err := a.open(ctx, result.ID)
	if err != nil || h.ExpiresAt <= time.Now().Unix() || h.MediaKind != "image" {
		t.Fatal("input capability lacks expiry/media kind", err)
	}
	if _, err = a.Generate(owned("alice"), r); err == nil || calls != 1 {
		t.Fatal("missing source opener reached provider")
	}
}
func TestManagedFileReferenceMappingAndIsolation(t *testing.T) {
	ctx := owned("alice")
	calls := 0
	a := fixture(t, func(h *http.Request) (*http.Response, error) {
		calls++
		p := body(t, h)
		content := p["content"].([]any)
		if content[1].(map[string]any)["image_url"].(map[string]any)["url"] != "mm_file://456" {
			t.Fatal("managed file mapping")
		}
		return response(200, success("123")), nil
	})
	f := a.fileView(ctx, File{ID: json.Number("456"), Purpose: "video_generation_input", MediaKind: "image"})
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	binding := uuid.NewString()
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "Use @image1", Options: core.MediaOptions{ReferenceMode: "references", ResourceReferences: []core.ResourceReference{{BindingID: binding, ReferenceID: "image_1"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "image_1", Kind: "file_resource", Subtype: "image", RemoteID: f.Handle, ConnectionIdentity: scope.Revision}}}}
	if _, err := a.Generate(ctx, r); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, err := a.Generate(owned("bob"), r); err == nil || calls != 1 {
		t.Fatal("foreign file resource submitted")
	}
	r.Options.ResourceSnapshots[0].ConnectionIdentity = "changed"
	if _, err := a.Generate(ctx, r); err == nil || calls != 1 {
		t.Fatal("changed resource revision submitted")
	}
}
func TestFileCapabilitiesExpireAndCannotRenewOnRead(t *testing.T) {
	ctx := owned("alice")
	calls := 0
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation_input", "bytes": 5}, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	expired := a.seal(ctx, handle{ID: "456", Kind: "file", Purpose: "video_generation_input", MediaKind: "image", ExpiresAt: time.Now().Unix() - 1})
	if _, err := a.GetFile(ctx, expired); err == nil || calls != 0 {
		t.Fatal("expired input file read")
	}
	valid := a.seal(ctx, handle{ID: "456", Kind: "file", Purpose: "video_generation_input", MediaKind: "image", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	f, err := a.GetFile(ctx, valid)
	if err != nil || f.Handle != valid {
		t.Fatal("metadata read renewed capability", err)
	}
	if _, err = a.DeleteResource(ctx, core.ResourceSnapshot{Kind: "file_resource", Subtype: "image", RemoteID: valid}); err == nil || calls != 1 {
		t.Fatal("undocumented input deletion called provider")
	}
}
func TestFileOperationConfigurationRejectsAmbiguousRates(t *testing.T) {
	d := Definition()
	for _, v := range []string{"-0.001", "NaN", "0.0000001"} {
		if _, err := d.Factory(providerkit.Credentials{APIKey: "fixture-key", Extra: map[string]string{"file_operation_price_usd": v}}, nil, nil); err == nil {
			t.Fatal("invalid operator fee accepted")
		}
	}
	p, err := d.Factory(providerkit.Credentials{APIKey: "fixture-key", Extra: map[string]string{"file_operation_price_usd": "0.001"}}, nil, nil)
	if err != nil || p.(*Adapter).fileOperationPrice == nil || *p.(*Adapter).fileOperationPrice != 1000 {
		t.Fatal(err)
	}
	if strings.Contains(d.BillingHelp, "file operations are free") {
		t.Fatal("unsupported free claim")
	}
}

func TestOwnedGeneratedVideoFileReuseAndPurposeIsolation(t *testing.T) {
	ctx := owned("alice")
	calls := 0
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		p := body(t, r)
		content := p["content"].([]any)
		if content[1].(map[string]any)["video_url"].(map[string]any)["url"] != "mm_file://456" {
			t.Fatal("owned output file was not reused")
		}
		return response(200, success("123")), nil
	})
	file := a.fileView(ctx, File{ID: json.Number("456"), Purpose: "video_generation", MediaKind: "video"})
	if file.URI == "" {
		t.Fatal("owned output video URI missing")
	}
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	binding := uuid.NewString()
	duration := 6.0
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "Continue @video1", Options: core.MediaOptions{ReferenceMode: "references", ResourceReferences: []core.ResourceReference{{BindingID: binding, ReferenceID: "video_1"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "video_1", Kind: "file_resource", Subtype: "video", RemoteID: file.Handle, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{{ReferenceID: uuid.NewString(), MediaType: "video", Duration: &duration, SHA256: strings.Repeat("a", 64)}}}}}}
	source := &r.Options.ResourceSnapshots[0].Sources[0]
	one := 1
	source.Inspection = &core.InputMediaEvidence{ReferenceID: source.ReferenceID, Type: "video", SHA256: source.SHA256, Duration: duration, AudioStreams: &one, AudioCodecs: []string{"aac"}}
	if _, err := a.Generate(ctx, r); err != nil || calls != 1 {
		t.Fatal("owned output reference failed", err)
	}
	q, _, err := a.Quote(ctx, describe(operation{id: r.Model, kind: "h3"}), r)
	if err != nil || len(q.Pricing.Components) < 2 || q.Pricing.Components[1].EstimatedUnits != 6 {
		t.Fatal("owned output input billing lost duration", err)
	}
	for _, kind := range []string{"image", "audio"} {
		if _, err := a.resolveFileInput(ctx, file.URI, kind); err == nil {
			t.Fatal("output purpose replayed as", kind)
		}
	}
	if _, err := a.resolveFileInput(owned("bob"), file.URI, "video"); err == nil {
		t.Fatal("foreign output reused")
	}
	r.Options.ResourceSnapshots[0].ConnectionIdentity = "changed"
	if _, err := a.Generate(ctx, r); err == nil || calls != 1 {
		t.Fatal("changed output binding reused")
	}
}

func TestDirectResourceCreationRejectsMislabeledGeneratedFilesBeforeHTTP(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("mislabeled file reached HTTP"); return nil, nil })
	ctx := owned("alice")
	token := a.seal(ctx, handle{ID: "123", Model: "MiniMax-Hailuo-2.3", Kind: "legacy"})
	for _, subtype := range []string{"image", "audio", "unknown", "video_generation"} {
		request := core.ResourceCreateRequest{Kind: "file_resource", Subtype: subtype, Name: "Owned output", GeneratedVideo: &core.GeneratedResourceSource{TaskID: uuid.NewString(), TaskHandle: token, Model: "MiniMax-Hailuo-2.3"}}
		if _, err := a.CreateResource(ctx, request); err == nil {
			t.Fatal("mislabeled generated file", subtype)
		}
	}
	request := uploadRequest([]byte("fixture")).Options.ResourceCreation
	request.Subtype = "unknown"
	ctx = core.WithResourceSourceOpener(ctx, sourceOpenerFunc(func(context.Context, core.ResourceSourceInput) (core.ArtifactSource, error) {
		t.Fatal("unknown subtype opened trusted source")
		return core.ArtifactSource{}, nil
	}))
	if _, err := a.CreateResource(ctx, *request); err == nil {
		t.Fatal("unknown upload subtype")
	}
}

func TestResourceDeletionPreflightIsPureAndOperationScoped(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("deletion preflight reached HTTP")
		return nil, nil
	})
	ctx := owned("alice")
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	for _, tc := range []struct {
		label, subtype string
		h              handle
		allowed        bool
	}{
		{"uploaded image", "image", handle{ID: "123", Kind: "file", Purpose: "video_generation_input", MediaKind: "image"}, false},
		{"uploaded video", "video", handle{ID: "123", Kind: "file", Purpose: "video_generation_input", MediaKind: "video"}, false},
		{"uploaded audio", "audio", handle{ID: "123", Kind: "file", Purpose: "video_generation_input", MediaKind: "audio"}, false},
		{"generated video", "video", handle{ID: "123", Kind: "file", Purpose: "video_generation", MediaKind: "video"}, true},
		{"source binding", "regeneration_source", handle{ID: "123", Kind: "source_binding", Model: "MiniMax-H3"}, true},
		{"wrong family", "video", handle{ID: "123", Kind: "h3", Model: "MiniMax-H3"}, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := core.ResourceSnapshot{BindingID: uuid.NewString(), Kind: "file_resource", Subtype: tc.subtype, RemoteID: a.seal(ctx, tc.h), ConnectionIdentity: scope.Revision}
			if err := a.ValidateResourceDeletion(ctx, r); (err == nil) != tc.allowed {
				t.Fatal("wrong deletion capability", err)
			}
			if err := a.ValidateResourceDeletion(owned("bob"), r); err == nil {
				t.Fatal("foreign deletion preflight")
			}
			r.ConnectionIdentity = "changed"
			if err := a.ValidateResourceDeletion(ctx, r); err == nil {
				t.Fatal("changed revision deletion preflight")
			}
		})
	}
}

func TestResourceOperationsAreManagementCapabilitiesNotGenerationTariffs(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("management eligibility reached provider HTTP")
		return nil, nil
	})
	count := 0
	for _, model := range catalog() {
		if model.ResourceOperation == nil {
			continue
		}
		count++
		if model.MediaType != "resource" || !model.NoPrompt || model.Pricing.Known || model.SupportsImageInput || model.SupportsVideoInput || model.SupportsAudioInput {
			t.Fatal("resource operation advertised as a generation model", model.ID)
		}
		if _, err := templates.AuthoringMode(model, "none"); err == nil {
			t.Fatal("resource operation admitted to Templates", model.ID)
		}
		if _, err := a.CatalogRateRequest(model, catalogsync.Rate{Unit: "request", Per: 1, Price: 1}); err == nil {
			t.Fatal("automatic generation tariff synthesized for resource", model.ID)
		}
		if _, err := a.Generate(owned("alice"), core.MediaRequest{Model: model.ID, Prompt: "image or video generation"}); err == nil {
			t.Fatal("resource accepted as generation without a managed intent", model.ID)
		}
	}
	if count != 3 {
		t.Fatal("management capability count changed", count)
	}
	b, err := os.ReadFile("testdata/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := interpretPrices(catalogsync.Source{URL: priceSource + ".md"}, string(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		m, ok := modelFor(entry.ID)
		if !ok || m.ResourceOperation != nil || m.MediaType == "resource" {
			t.Fatal("resource entered automatic price updates", entry.ID)
		}
	}
}

func TestManagedBaseConsumesOnlyMatchingInspectedProof(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("inspection validation reached HTTP")
		return nil, nil
	})
	ctx := owned("alice")
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	original := baseRequest()
	e := original.Options.ReferenceMediaEvidence[original.Options.Videos()[0]]
	duration := e.Duration
	binding := uuid.NewString()
	file := a.fileView(ctx, File{ID: json.Number("456"), Purpose: "video_generation_input", MediaKind: "video"})
	original.Options.VideoURLs = nil
	original.Options.ReferenceMediaEvidence = nil
	original.Options.ResourceReferences = []core.ResourceReference{{BindingID: binding, ReferenceID: "video_1"}}
	original.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "video_1", Kind: "file_resource", Subtype: "video", RemoteID: file.Handle, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{{ReferenceID: e.ReferenceID, MediaType: "video", SHA256: e.SHA256, Duration: &duration, Inspection: &e}}}}
	if err := a.ValidateRequest(ctx, original); err != nil {
		t.Fatal("trusted persisted base proof rejected", err)
	}
	expanded, err := a.expandManagedFiles(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	proof := expanded.Options.ReferenceMediaEvidence[expanded.Options.Videos()[0]]
	*proof.AudioStreams = 99
	if *original.Options.ResourceSnapshots[0].Sources[0].Inspection.AudioStreams != 1 {
		t.Fatal("expanded request mutated accepted immutable proof")
	}
	for _, mutate := range []func(*core.ResourceSourceEvidence){
		func(s *core.ResourceSourceEvidence) { s.Inspection = nil },
		func(s *core.ResourceSourceEvidence) { s.Inspection.AudioStreams = nil },
		func(s *core.ResourceSourceEvidence) { zero := 0; s.Inspection.AudioStreams = &zero },
		func(s *core.ResourceSourceEvidence) { s.Inspection.SHA256 = strings.Repeat("b", 64) },
		func(s *core.ResourceSourceEvidence) { s.Inspection.ReferenceID = uuid.NewString() },
		func(s *core.ResourceSourceEvidence) { s.Duration = nil },
	} {
		r := original
		r.Options.ResourceSnapshots = append([]core.ResourceSnapshot(nil), original.Options.ResourceSnapshots...)
		r.Options.ResourceSnapshots[0].Sources = core.CloneResourceSources(original.Options.ResourceSnapshots[0].Sources)
		mutate(&r.Options.ResourceSnapshots[0].Sources[0])
		if err := a.ValidateRequest(ctx, r); err == nil {
			t.Fatal("missing/changed/silent/unknown base evidence admitted")
		}
	}
}

func TestStandaloneRegenerationAllowsThirteenManagedFiles(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("thirteen-file validation reached HTTP")
		return nil, nil
	})
	ctx := owned("alice")
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	base := baseRequest()
	e := base.Options.ReferenceMediaEvidence[base.Options.Videos()[0]]
	r := core.MediaRequest{Model: RegenerationVideo, Prompt: "Original inputs and inspected base", Options: core.MediaOptions{ReferenceMode: "references", ReferenceRoles: map[string]string{"video_1": "base_video"}}}
	for _, group := range []struct {
		kind  string
		count int
	}{{"image", 9}, {"video", 3}, {"audio", 1}} {
		for i := 1; i <= group.count; i++ {
			binding := uuid.NewString()
			alias := fmt.Sprintf("%s_%d", group.kind, i)
			file := a.fileView(ctx, File{ID: json.Number(strconv.Itoa(len(r.Options.ResourceReferences) + 100)), Purpose: "video_generation_input", MediaKind: group.kind})
			source := core.ResourceSourceEvidence{ReferenceID: uuid.NewString(), MediaType: group.kind, SHA256: strings.Repeat("a", 64)}
			if group.kind != "image" {
				duration := 2.0
				source.Duration = &duration
			}
			if group.kind == "video" && i > 1 {
				one := 1
				source.Inspection = &core.InputMediaEvidence{ReferenceID: source.ReferenceID, Type: "video", SHA256: source.SHA256, Duration: *source.Duration, AudioStreams: &one, AudioCodecs: []string{"aac"}}
			}
			if group.kind == "video" && i == 1 {
				duration := e.Duration
				source = core.ResourceSourceEvidence{ReferenceID: e.ReferenceID, MediaType: "video", SHA256: e.SHA256, Duration: &duration, Inspection: &e}
			}
			r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: alias})
			r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, core.ResourceSnapshot{BindingID: binding, ReferenceID: alias, Kind: "file_resource", Subtype: group.kind, RemoteID: file.Handle, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{source}})
		}
	}
	if len(r.Options.ResourceReferences) != 13 {
		t.Fatal("fixture file count")
	}
	if err := core.ValidateResourceReferences(baseRegenerationModel(), r.Options); err != nil {
		t.Fatal("documented base+12original input schema rejected", err)
	}
	if err := a.ValidateRequest(ctx, r); err != nil {
		t.Fatal("documented thirteen-file request rejected", err)
	}
}

func TestTrustedWAVAliasesNormalizeToDocumentedUploadType(t *testing.T) {
	for _, admitted := range []string{"audio/wav", "audio/wave", "audio/x-wav"} {
		t.Run(admitted, func(t *testing.T) {
			data := []byte("trusted inspected WAV fixture")
			a := fixture(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/files/upload" {
					t.Fatal("wrong WAV endpoint")
				}
				mr, err := r.MultipartReader()
				if err != nil {
					t.Fatal(err)
				}
				_, _ = mr.NextPart()
				part, err := mr.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(part)
				if !strings.HasSuffix(part.FileName(), ".wav") || !bytes.Equal(got, data) {
					t.Fatal("WAV MIME translation changed trusted bytes/name")
				}
				return response(200, map[string]any{"file": map[string]any{"file_id": "456", "bytes": len(data), "purpose": "video_generation_input"}, "base_resp": map[string]int{"status_code": 0}}), nil
			})
			r := uploadRequest(data)
			r.Options.ResourceCreation.Subtype = "audio"
			r.Options.ResourceCreation.FileSources[0].MediaType = "audio"
			r.Options.ResourceCreation.FileSources[0].ContentType = admitted
			ctx := core.WithResourceSourceOpener(owned("alice"), sourceOpenerFunc(func(context.Context, core.ResourceSourceInput) (core.ArtifactSource, error) {
				return core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: "audio/wave", SizeBytes: int64(len(data))}, nil
			}))
			job, err := a.CreateResource(ctx, *r.Options.ResourceCreation)
			if err != nil || len(job.Resources) != 1 || job.Resources[0].Subtype != "audio" {
				t.Fatal("canonical WAV upload failed", err)
			}
		})
	}
}

func TestManagedReferenceAudioCodecProofIsRequiredClonedAndOwnerBound(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid managed codec input reached provider")
		return nil, nil
	})
	ctx := owned("alice")
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	one, zero, two := 1, 0, 2
	for _, tc := range []struct {
		name   string
		count  *int
		codecs []string
		valid  bool
	}{
		{"aac", &one, []string{"aac"}, true}, {"mp3", &one, []string{"mp3"}, true}, {"silent", &zero, nil, true},
		{"unsupported", &one, []string{"opus"}, false}, {"unknown", &one, []string{""}, false}, {"missing", &one, nil, false}, {"unknown count", nil, []string{"aac"}, false}, {"mismatch", &two, []string{"aac"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.NewString()
			ref := uuid.NewString()
			duration := 5.
			e := core.InputMediaEvidence{ReferenceID: ref, Type: "video", SHA256: strings.Repeat("a", 64), Duration: duration, AudioStreams: tc.count, AudioCodecs: core.CloneAudioCodecs(tc.codecs)}
			f := a.fileView(ctx, File{ID: json.Number("456"), Purpose: "video_generation_input", MediaKind: "video"})
			r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "Follow @video1", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "16:9", ResourceReferences: []core.ResourceReference{{BindingID: id, ReferenceID: "video_1"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: id, ReferenceID: "video_1", Kind: "file_resource", Subtype: "video", RemoteID: f.Handle, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{{ReferenceID: ref, MediaType: "video", SHA256: e.SHA256, Duration: &duration, Inspection: &e}}}}}}
			err := a.ValidateRequest(ctx, r)
			if (err == nil) != tc.valid {
				t.Fatal("admission", err)
			}
			if !tc.valid {
				if _, err := a.Generate(ctx, r); err == nil {
					t.Fatal("invalid codec dispatched")
				}
				return
			}
			expanded, err := a.expandManagedFiles(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			proof := expanded.Options.ReferenceMediaEvidence[expanded.Options.Videos()[0]]
			if len(proof.AudioCodecs) > 0 {
				proof.AudioCodecs[0] = "caller-mutation"
				if e.AudioCodecs[0] == "caller-mutation" {
					t.Fatal("accepted codec proof mutated")
				}
			}
			if err := a.ValidateRequest(owned("bob"), r); err == nil {
				t.Fatal("foreign owner accepted codec binding")
			}
		})
	}
}
