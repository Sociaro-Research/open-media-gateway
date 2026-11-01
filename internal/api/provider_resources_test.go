package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/google/uuid"
)

const resourceAPIRevision = "private-resource-revision"

type resourceAPITransport struct{ t *testing.T }

func (p resourceAPITransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.t.Fatal("resource admission must not contact a provider")
	return nil, nil
}

type resourceAPIProvider struct{ core.MediaProvider }

func (p *resourceAPIProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	return p.MediaProvider.(core.ModelCatalog).ListModels(ctx)
}
func (p *resourceAPIProvider) PreparationModels(ctx context.Context) ([]core.MediaModel, error) {
	return p.MediaProvider.(core.MediaPreparationCatalog).PreparationModels(ctx)
}
func resourceAPIScope(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
	return core.ProviderConnectionScope{ID: "kling", OwnerID: identity.ProviderOwnerID(ctx), Revision: resourceAPIRevision}, nil
}
func resourceAPIPinned(ctx context.Context) context.Context {
	scope, _ := resourceAPIScope(ctx, "kling")
	return core.WithProviderConnectionScope(ctx, scope)
}
func (p *resourceAPIProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	return p.MediaProvider.(core.RequestValidator).ValidateRequest(resourceAPIPinned(ctx), r)
}
func (p *resourceAPIProvider) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	q, _, e := p.MediaProvider.(interface {
		Quote(context.Context, core.MediaModel, core.MediaRequest) (core.TaskCost, string, error)
	}).Quote(resourceAPIPinned(ctx), m, r)
	return q, resourceAPIRevision, e
}

type resourceAPITasks struct {
	core.TaskRepository
	tasks    map[string]core.Task
	accepted map[string]core.Task
	hashes   map[string]string
	creates  int
	budget   bool
}

func (r *resourceAPITasks) CreateOrGet(ctx context.Context, t core.Task, key, hash string) (core.Task, bool, error) {
	k := identity.OwnerID(ctx) + "/" + key
	if old, ok := r.accepted[k]; ok {
		if r.hashes[k] != hash {
			return core.Task{}, false, core.ErrIdempotencyConflict
		}
		return old, false, nil
	}
	if t.AdmissionError != nil {
		return core.Task{}, false, t.AdmissionError
	}
	if t.RequireCost && t.Cost == nil {
		return core.Task{}, false, core.ErrPricingUnavailable
	}
	if t.Cost != nil && t.MaxExpectedUSD != nil && t.Cost.ExpectedUSD > *t.MaxExpectedUSD {
		return core.Task{}, false, core.ErrEstimateChanged
	}
	if r.budget {
		return core.Task{}, false, core.ErrBudgetExceeded
	}
	r.accepted[k] = t
	r.hashes[k] = hash
	r.tasks[t.ID] = t
	r.creates++
	return t, true, nil
}
func (r *resourceAPITasks) MarkEnqueued(context.Context, string) error { return nil }
func (r *resourceAPITasks) Get(ctx context.Context, id string) (core.Task, error) {
	t, ok := r.tasks[id]
	if !ok || t.OwnerID != identity.OwnerID(ctx) {
		return core.Task{}, core.ErrTaskNotFound
	}
	return t, nil
}

type resourceAPIRepository struct {
	providerresources.Repository
	bindings map[string]providerresources.Binding
	jobs     map[string]providerresources.Job
	deletes  map[string]string
	inUse    bool
}

func (r *resourceAPIRepository) GetBinding(ctx context.Context, id string) (providerresources.Binding, error) {
	b, ok := r.bindings[id]
	if !ok || b.OwnerID != identity.OwnerID(ctx) || b.ProviderOwnerID != identity.ProviderOwnerID(ctx) {
		return b, providerresources.ErrNotFound
	}
	return b, nil
}
func (r *resourceAPIRepository) ListBindings(ctx context.Context, p string, limit, offset int) ([]providerresources.Binding, error) {
	out := []providerresources.Binding{}
	for _, b := range r.bindings {
		if b.ProviderID == p && b.OwnerID == identity.OwnerID(ctx) && b.ProviderOwnerID == identity.ProviderOwnerID(ctx) {
			out = append(out, b)
		}
	}
	if offset >= len(out) {
		return []providerresources.Binding{}, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *resourceAPIRepository) GetJob(ctx context.Context, id string) (providerresources.Job, error) {
	j, ok := r.jobs[id]
	if !ok || j.OwnerID != identity.OwnerID(ctx) || j.ProviderOwnerID != identity.ProviderOwnerID(ctx) {
		return j, providerresources.ErrNotFound
	}
	return j, nil
}
func (r *resourceAPIRepository) BeginDelete(ctx context.Context, id, key string) (providerresources.Job, bool, error) {
	b, e := r.GetBinding(ctx, id)
	if e != nil {
		return providerresources.Job{}, false, e
	}
	if r.inUse {
		return providerresources.Job{}, false, providerresources.ErrInUse
	}
	k := identity.OwnerID(ctx) + "/" + id + "/" + key
	if jid, ok := r.deletes[k]; ok {
		return r.jobs[jid], false, nil
	}
	if b.Status != providerresources.Succeeded {
		return providerresources.Job{}, false, providerresources.ErrConflict
	}
	j := providerresources.Job{ID: uuid.NewString(), BindingID: id, OwnerID: b.OwnerID, ActorID: b.ActorID, ProviderID: b.ProviderID, ConnectionID: b.ConnectionID, ProviderOwnerID: b.ProviderOwnerID, Revision: b.Revision, Kind: b.Kind, Operation: providerresources.Delete, Status: providerresources.Queued, Intent: providerresources.Intent{Kind: b.Kind}}
	b.Status = providerresources.Deleting
	r.bindings[id] = b
	r.jobs[j.ID] = j
	r.deletes[k] = j.ID
	return j, true, nil
}
func (r *resourceAPIRepository) StartJob(ctx context.Context, id string) (providerresources.Job, bool, error) {
	j, e := r.GetJob(ctx, id)
	if e != nil {
		return j, false, e
	}
	if j.Status != providerresources.Queued {
		return j, false, nil
	}
	j.Status = providerresources.Running
	r.jobs[id] = j
	return j, true, nil
}
func (r *resourceAPIRepository) SaveHandle(ctx context.Context, id, h, w string) error {
	j, e := r.GetJob(ctx, id)
	if e != nil {
		return e
	}
	j.Handle = h
	j.Warning = w
	r.jobs[id] = j
	return nil
}
func (r *resourceAPIRepository) RecordUncertain(ctx context.Context, id, w string) error {
	j, e := r.GetJob(ctx, id)
	if e != nil {
		return e
	}
	j.Warning = w
	r.jobs[id] = j
	return nil
}
func (r *resourceAPIRepository) FailJob(ctx context.Context, id, s, w string) error {
	j, e := r.GetJob(ctx, id)
	if e != nil {
		return e
	}
	j.Status = providerresources.Failed
	j.SafeError = s
	j.Warning = w
	r.jobs[id] = j
	return nil
}
func (r *resourceAPIRepository) CompleteDelete(ctx context.Context, id string) error {
	j, e := r.GetJob(ctx, id)
	if e != nil {
		return e
	}
	j.Status = providerresources.Succeeded
	r.jobs[id] = j
	b := r.bindings[j.BindingID]
	b.Status = providerresources.Deleted
	r.bindings[b.ID] = b
	return nil
}

type resourceAPIDirectory struct {
	core.ProviderDirectory
	p core.MediaProvider
}

func (d resourceAPIDirectory) Provider(context.Context, string) (core.MediaProvider, error) {
	return d.p, nil
}

type resourceAPIDeleter struct {
	core.MediaProvider
	core.ResourceProvider
	t     *testing.T
	calls int
	polls int
}

func (p *resourceAPIDeleter) DeleteResource(ctx context.Context, s core.ResourceSnapshot) (core.ResourceJob, error) {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope.Revision != resourceAPIRevision || core.ProviderRevision(ctx) != resourceAPIRevision || s.RemoteID != "private-native-resource" {
		p.t.Fatal("deletion lost trusted provider scope", scope, s)
	}
	p.calls++
	return core.ResourceJob{Handle: "private-delete-handle", Status: core.ProviderQueued, Warning: "private-native-resource private-provider-warning"}, nil
}
func (p *resourceAPIDeleter) GetResourceJob(_ context.Context, kind, h string) (core.ResourceJob, error) {
	if kind != "voice" || h != "private-delete-handle" {
		p.t.Fatal(kind, h)
	}
	p.polls++
	return core.ResourceJob{Handle: h, Status: core.ProviderRunning, Warning: "private-provider-warning"}, nil
}

type resourceAPIFixture struct {
	s             *Server
	h             http.Handler
	tasks         *resourceAPITasks
	resources     *resourceAPIRepository
	queue         *admissionQueue
	alice, bob    string
	deleter       *resourceAPIDeleter
	providerOwner string
}

func newResourceAPIFixture(t *testing.T) *resourceAPIFixture {
	return newResourceAPIFixtureWithPrices(t, nil)
}
func newResourceAPIFixtureWithPrices(t *testing.T, prices map[string]string) *resourceAPIFixture {
	t.Helper()
	s, _, _, _ := templateFixture(t)
	native, e := kling.Definition().Factory(providerkit.Credentials{APIKey: "synthetic-resource-key", Extra: prices}, nil, &http.Client{Transport: resourceAPITransport{t}})
	if e != nil {
		t.Fatal(e)
	}
	p := &resourceAPIProvider{native}
	tasks := &resourceAPITasks{tasks: map[string]core.Task{}, accepted: map[string]core.Task{}, hashes: map[string]string{}}
	queue := &admissionQueue{}
	svc := core.NewTaskServiceWithProviders(tasks, queue, map[string]core.MediaProvider{"kling": p}, map[string]string{"kling": kling.ResourceCreateVoice}, "kling")
	svc.EnableAccounting()
	s.service = svc
	repo := &resourceAPIRepository{bindings: map[string]providerresources.Binding{}, jobs: map[string]providerresources.Job{}, deletes: map[string]string{}}
	deleter := &resourceAPIDeleter{t: t}
	s.resourceRepository = repo
	svc.SetReferenceResourceResolver(providerresources.New(repo, resourceAPIScope))
	s.resourceScopes = resourceAPIScope
	s.resourceLifecycle = providerresources.NewLifecycle(repo, resourceAPIScope, resourceAPIDirectory{p: deleter})
	h := s.handler()
	key, a := issueTestKey(t, h, "alice", `{"name":"resource-a","scopes":["generation"]}`)
	_, b := issueTestKey(t, h, "bob", `{"name":"resource-b","scopes":["generation"]}`)
	po := "alice"
	if key.ResourceMode == "shared" {
		po = identity.SharedOwnerID
	}
	return &resourceAPIFixture{s: s, h: h, tasks: tasks, resources: repo, queue: queue, alice: a, bob: b, deleter: deleter, providerOwner: po}
}
func (f *resourceAPIFixture) context(actor string) context.Context {
	po := actor
	if f.providerOwner == identity.SharedOwnerID {
		po = identity.SharedOwnerID
	}
	return identity.WithResourceOwners(gw253Context(actor), po, po)
}
func (f *resourceAPIFixture) binding(actor, kind string) providerresources.Binding {
	return providerresources.Binding{ID: uuid.NewString(), OwnerID: identity.OwnerID(f.context(actor)), ActorID: actor, ProviderID: "kling", ConnectionID: "kling", ProviderOwnerID: identity.ProviderOwnerID(f.context(actor)), Revision: resourceAPIRevision, RemoteID: "private-native-resource", Kind: kind, Subtype: "custom", Name: "my local resource", Status: providerresources.Succeeded}
}
func resourceAPIRequest(h http.Handler, method, path, key, idem string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
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
func resourceAPIWAV(seconds int) []byte {
	out := make([]byte, 44+8000*seconds)
	copy(out, testmedia.WAV()[:44])
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	binary.LittleEndian.PutUint32(out[40:], uint32(len(out)-44))
	return out
}
func resourceAPIPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 512, 512))
	im.Set(0, 0, color.RGBA{R: shade, A: 255})
	var b bytes.Buffer
	if e := png.Encode(&b, im); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func (f *resourceAPIFixture) upload(t *testing.T, actor, name string, data []byte) references.Image {
	t.Helper()
	ref, e := f.s.references.Upload(f.context(actor), name, bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	return ref
}
func resourceAPIVoice(ref string) map[string]any {
	return map[string]any{"provider": "kling", "workflow_model": kling.TextToSpeech, "kind": "voice", "name": "My voice", "reference_ids": []string{ref}}
}
func resourceAPIAssertStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
	}
}
func resourceAPIAssertPrivate(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, v := range values {
		if v != "" && strings.Contains(body, v) {
			t.Fatalf("private provider evidence %q leaked: %s", v, body)
		}
	}
}

func TestProviderResourcesVoiceAdmissionCanonicalEvidenceAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"voice.wav", resourceAPIWAV(6)}, {"voice.mp4", testmedia.MP4(6000, 1000, 180)}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceAPIFixture(t)
			ref := f.upload(t, "alice", tc.name, tc.data)
			body := resourceAPIVoice(ref.ID)
			w := resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "voice-create", body)
			resourceAPIAssertStatus(t, w, 202)
			if f.tasks.creates != 1 || len(f.queue.ids) != 1 {
				t.Fatal("resource creation must admit exactly one queued task")
			}
			task := f.tasks.tasks[f.queue.ids[0]]
			if task.PreparationConsumer != kling.TextToSpeech {
				t.Fatalf("trusted consumer missing from actual admission: got %q", task.PreparationConsumer)
			}
			if task.Cost == nil || task.Cost.ExpectedUSD != 7000 || task.Cost.Pricing.Unit != "request" || task.MediaType != "resource" || task.Model != kling.ResourceCreateVoice || task.ProviderIdentity != resourceAPIRevision {
				t.Fatalf("incorrect paid resource admission: %#v", task)
			}
			intent := task.Options.ResourceIntent
			if intent == nil || intent.Kind != "voice" || intent.Subtype != "custom" || intent.Name != "My voice" || len(intent.ReferenceIDs) != 1 || intent.ReferenceIDs[0] != ref.ID {
				t.Fatalf("immutable public intent lost: %#v", intent)
			}
			creation := task.Options.ResourceCreation
			if creation == nil || len(creation.VoiceSources) != 1 {
				t.Fatal("missing host-resolved source", creation)
			}
			src := creation.VoiceSources[0]
			digest := sha256.Sum256(tc.data)
			if src.ReferenceID != ref.ID || src.SHA256 != hex.EncodeToString(digest[:]) || src.Duration == nil || *src.Duration != 6 || src.SizeBytes != int64(len(tc.data)) {
				t.Fatalf("source was not inspected: %#v", src)
			}
			want, e := references.CanonicalReferenceURL(f.s.assetPublicURL, f.s.mediaSigner.secret, identity.OwnerID(f.context("alice")), ref.ID)
			if e != nil {
				t.Fatal(e)
			}
			if src.URL != want {
				t.Fatalf("source URL is not canonical: %s", src.URL)
			}
			u, _ := url.Parse(src.URL)
			for _, q := range []string{"expires", "X-Amz-Expires", "X-Amz-Credential"} {
				if u.Query().Has(q) {
					t.Fatal("source admission sealed expiring storage URL")
				}
			}
			resourceAPIAssertPrivate(t, w.Body.String(), src.URL, src.SHA256, resourceAPIRevision, "synthetic-resource-key", "_resource_creation", "VoiceSources")
			var public map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &public); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(w.Body.String(), ref.ID) {
				t.Fatal("public immutable local intent missing")
			}
			if e = f.s.references.Delete(f.context("alice"), ref.ID); e != nil {
				t.Fatal(e)
			}
			replay := resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "voice-create", body)
			resourceAPIAssertStatus(t, replay, 202)
			var again map[string]any
			_ = json.Unmarshal(replay.Body.Bytes(), &again)
			if again["id"] != public["id"] || f.tasks.creates != 1 || len(f.queue.ids) != 2 || f.queue.ids[0] != f.queue.ids[1] {
				t.Fatal("accepted replay revalidated deleted input or resubmitted", replay.Body)
			}
			body["name"] = "changed"
			resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "voice-create", body), 409)
		})
	}
}

func TestProviderResourcesRejectClientEvidenceAndForeignSources(t *testing.T) {
	f := newResourceAPIFixture(t)
	own := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
	foreign := f.upload(t, "bob", "foreign.wav", resourceAPIWAV(7))
	for _, field := range []string{"fakeSnapshot", "remote_id", "url", "voice_url", "resource_creation", "_resource_creation", "duration"} {
		t.Run(field, func(t *testing.T) {
			body := resourceAPIVoice(own.ID)
			body[field] = "client-forged"
			resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, field, body), 400)
		})
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "foreign-source", resourceAPIVoice(foreign.ID)), 404)
	// A generated task must be read through the same owner-scoped service, even if its UUID is known.
	foreignTask := uuid.NewString()
	f.tasks.tasks[foreignTask] = core.Task{ID: foreignTask, OwnerID: identity.OwnerID(f.context("bob"))}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "foreign-task", map[string]any{"provider": "kling", "workflow_model": kling.TextToSpeech, "kind": "voice", "name": "voice", "generated_task_id": foreignTask}), 404)
	voice := f.binding("bob", "voice")
	f.resources.bindings[voice.ID] = voice
	a := f.upload(t, "alice", "front.png", resourceAPIPNG(t, 10))
	b := f.upload(t, "alice", "other.png", resourceAPIPNG(t, 20))
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "foreign-binding", map[string]any{"provider": "kling", "workflow_model": kling.ImageOmni3, "kind": "element", "subtype": "multi_image", "name": "person", "description": "person", "reference_ids": []string{a.ID, b.ID}, "bound_voice_binding_id": voice.ID}), 404)
	if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
		t.Fatal("untrusted input reached task admission")
	}
}

func TestProviderResourcesVisibilityAuthenticationPricingAndBudget(t *testing.T) {
	t.Run("API key required", func(t *testing.T) {
		f := newResourceAPIFixture(t)
		for _, path := range []string{"/v1/provider-resources?provider=kling", "/v1/provider-resources/" + uuid.NewString(), "/v1/provider-resource-jobs/" + uuid.NewString()} {
			resourceAPIAssertStatus(t, sendTest(f.h, "GET", path, "", "alice", ""), 401)
		}
		resourceAPIAssertStatus(t, sendTest(f.h, "POST", "/v1/provider-resources", `{"provider":"kling","kind":"voice","name":"voice"}`, "alice", ""), 401)
	})
	t.Run("hidden model", func(t *testing.T) {
		f := newResourceAPIFixture(t)
		ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
		policy := &modelAccessMemory{org: map[string]map[string]bool{}}
		_ = policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "kling", Model: kling.TextToSpeech}})
		policy.models[0].EnabledByDefault = false
		f.s.modelAccess = policy
		f.h = f.s.handler()
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "hidden", resourceAPIVoice(ref.ID)), 400)
		if f.tasks.creates != 0 {
			t.Fatal("hidden resource model admitted")
		}
	})
	t.Run("unknown element tariff", func(t *testing.T) {
		f := newResourceAPIFixture(t)
		a := f.upload(t, "alice", "front.png", resourceAPIPNG(t, 30))
		b := f.upload(t, "alice", "other.png", resourceAPIPNG(t, 40))
		w := resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "element", map[string]any{"provider": "kling", "workflow_model": kling.ImageOmni3, "kind": "element", "subtype": "multi_image", "name": "person", "description": "person", "reference_ids": []string{a.ID, b.ID}})
		resourceAPIAssertStatus(t, w, 422)
		if !strings.Contains(w.Body.String(), "pricing_unavailable") || f.tasks.creates != 0 {
			t.Fatal("unknown tariff was admitted", w.Body)
		}
	})
	t.Run("budget", func(t *testing.T) {
		f := newResourceAPIFixture(t)
		ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
		f.tasks.budget = true
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "budget", resourceAPIVoice(ref.ID)), 402)
		if f.tasks.creates != 0 {
			t.Fatal("unfunded creation admitted")
		}
	})
	t.Run("inspected duration limits", func(t *testing.T) {
		for _, seconds := range []int{4, 31} {
			f := newResourceAPIFixture(t)
			ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(seconds))
			resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "duration", resourceAPIVoice(ref.ID)), 400)
			if f.tasks.creates != 0 {
				t.Fatal("out-of-contract source admitted")
			}
		}
	})
}

func TestProviderResourcesListGetPrivacyAndLocalFaceSelectors(t *testing.T) {
	f := newResourceAPIFixture(t)
	b := f.binding("alice", "face_session")
	b.OriginTaskID = uuid.NewString()
	b.Session = &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 6, SourceSHA256: strings.Repeat("c", 64), Faces: []core.SessionFace{{ID: "private-native-face-id", StartMS: 100, EndMS: 5800, PreviewArtifactID: "2"}}}
	f.resources.bindings[b.ID] = b
	foreign := f.binding("bob", "voice")
	f.resources.bindings[foreign.ID] = foreign
	for _, path := range []string{"/v1/provider-resources?provider=kling", "/v1/provider-resources/" + b.ID} {
		w := resourceAPIRequest(f.h, "GET", path, f.alice, "", nil)
		resourceAPIAssertStatus(t, w, 200)
		body := w.Body.String()
		resourceAPIAssertPrivate(t, body, "private-native-resource", resourceAPIRevision, "private-native-face-id", strings.Repeat("c", 64), foreign.ID, "connection_id", "provider_owner_id", "source_sha256")
		for _, want := range []string{`"index":1`, `"start_ms":100`, `"end_ms":5800`, `"preview_artifact_id":"2"`, `"generation_id":"` + b.OriginTaskID + `"`, `"usable":true`} {
			if !strings.Contains(body, want) {
				t.Fatalf("local face selector missing %s: %s", want, body)
			}
		}
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resources/"+b.ID, f.bob, "", nil), 404)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resources/"+foreign.ID, f.alice, "", nil), 404)
}

func TestProviderResourcesDeletePendingReplayAndJobOwner(t *testing.T) {
	f := newResourceAPIFixture(t)
	b := f.binding("alice", "voice")
	f.resources.bindings[b.ID] = b
	path := "/v1/provider-resources/" + b.ID
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "DELETE", path, f.alice, "blocked", nil), 405)
	job, e := f.s.resourceLifecycle.Delete(f.context("alice"), b.ID, "historical-delete")
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	saferequest := httptest.NewRequest("GET", path, nil).WithContext(f.context("alice"))
	f.s.writeViewerJSON(w, saferequest, 202, job)
	if job.Status != providerresources.Running || f.resources.bindings[b.ID].Status != providerresources.Deleting || f.deleter.calls != 1 {
		t.Fatal("submission was incorrectly terminal", w.Body)
	}
	resourceAPIAssertPrivate(t, w.Body.String(), "private-native-resource", "private-delete-handle", "private-provider-warning", resourceAPIRevision)
	again, e := f.s.resourceLifecycle.Delete(f.context("alice"), b.ID, "historical-delete")
	if e != nil {
		t.Fatal(e)
	}
	if again.ID != job.ID || f.deleter.calls != 1 {
		t.Fatal("delete replay resubmitted")
	}
	poll := resourceAPIRequest(f.h, "GET", "/v1/provider-resource-jobs/"+job.ID, f.alice, "", nil)
	resourceAPIAssertStatus(t, poll, 200)
	if f.deleter.calls != 1 || f.deleter.polls != 1 {
		t.Fatal("poll submitted deletion")
	}
	resourceAPIAssertPrivate(t, poll.Body.String(), "private-delete-handle", "private-provider-warning", resourceAPIRevision)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-jobs/"+job.ID, f.bob, "", nil), 404)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "DELETE", path, f.bob, "foreign", nil), 405)
	if f.deleter.calls != 1 || f.deleter.polls != 1 {
		t.Fatal("foreign lifecycle call contacted provider")
	}
}

func TestProviderResourcesPriceIncreaseIsAnAdmissionConflict(t *testing.T) {
	f := newResourceAPIFixture(t)
	ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
	body := resourceAPIVoice(ref.ID)
	body["max_expected_usd"] = 0.006
	w := resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "price-cap", body)
	resourceAPIAssertStatus(t, w, 409)
	if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
		t.Fatal("price increase admitted paid work")
	}
}

func TestProviderResourcesGeneratedVoiceUsesOwnedArchivedOutput(t *testing.T) {
	f := newResourceAPIFixture(t)
	handle := "private-native-task-26"
	audio := true
	source := generatedVideoArchivedTask(t, f, 6)
	id := source.ID
	source.Model = kling.TextToVideo26
	source.ProviderTaskID = &handle
	source.Options.GenerateAudio = &audio
	source.StoredArtifacts[0].ID = "local-output"
	source.StoredArtifacts[0].ProviderID = "private-native-output-video"
	f.tasks.tasks[id] = source
	body := map[string]any{"provider": "kling", "workflow_model": kling.TextToSpeech, "kind": "voice", "name": "Archived voice", "generated_task_id": id}
	w := resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "generated-voice", body)
	resourceAPIAssertStatus(t, w, 202)
	if f.tasks.creates != 1 || len(f.queue.ids) != 1 {
		t.Fatal("generated voice was not admitted once")
	}
	admitted := f.tasks.tasks[f.queue.ids[0]]
	got := admitted.Options.ResourceCreation.GeneratedVideo
	if got == nil || got.TaskID != id || got.TaskHandle != handle || got.OutputID != "private-native-output-video" || got.RemoteID != "" || got.SHA256 != source.StoredArtifacts[0].SHA256 || got.Duration == nil || *got.Duration != 6 || !got.NativeAudio {
		t.Fatalf("host did not retain exact archived generation evidence: %#v", got)
	}
	resourceAPIAssertPrivate(t, w.Body.String(), handle, "private-native-output-video", source.StoredArtifacts[0].SHA256, resourceAPIRevision)
	// Same-owner stale credential revision must still be unavailable.
	source.ProviderIdentity = "retired-private-revision"
	f.tasks.tasks[id] = source
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "stale-generated", body), 404)
	if f.tasks.creates != 1 {
		t.Fatal("stale generated source admitted")
	}
}

func TestProviderResourcesDeleteInUseIsConflict(t *testing.T) {
	f := newResourceAPIFixture(t)
	b := f.binding("alice", "voice")
	f.resources.bindings[b.ID] = b
	f.resources.inUse = true
	w := resourceAPIRequest(f.h, "DELETE", "/v1/provider-resources/"+b.ID, f.alice, "in-use", nil)
	resourceAPIAssertStatus(t, w, 405)
	if f.deleter.calls != 0 || len(f.resources.jobs) != 0 || f.resources.bindings[b.ID].Status != providerresources.Succeeded {
		t.Fatal("retained resource deletion reached provider")
	}
}
