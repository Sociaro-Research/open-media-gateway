package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

type presetBrowserAuth struct{ browserAuthStub }

func (a presetBrowserAuth) Authenticate(r *http.Request) (identity.User, error) {
	cookie, err := r.Cookie("test_session")
	if err == nil {
		if id, e := uuid.Parse(cookie.Value); e == nil {
			return identity.User{ID: id.String(), Name: "Preset fixture"}, nil
		}
	}
	return a.browserAuthStub.Authenticate(r)
}

type presetAPIService struct {
	Service
	models []core.MediaModel
}

func (p *presetAPIService) ListProviders(context.Context) []core.ProviderDescriptor {
	return []core.ProviderDescriptor{{ID: "kling", Models: p.models}}
}

type presetAPIProvider struct {
	core.MediaProvider
	core.ResourceProvider
	calls  int
	mutate func()
	result []core.ProviderResource
}

func (p *presetAPIProvider) ListResources(ctx context.Context, kind string, preset bool, page, size int) (core.ResourcePage, error) {
	p.calls++
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope.ID != "kling" || scope.Revision != core.ProviderRevision(ctx) || !preset {
		panic("unscoped preset query")
	}
	if p.mutate != nil {
		p.mutate()
	}
	return core.ResourcePage{Page: page, PageSize: size, Resources: p.result}, nil
}

type presetAPIRepository struct {
	*resourceAPIRepository
	mu              sync.Mutex
	imports, writes int
}

func (r *presetAPIRepository) ImportPreset(ctx context.Context, b providerresources.Binding) (providerresources.Binding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.imports++
	if err := providerresources.ValidatePresetBinding(ctx, b); err != nil {
		return b, err
	}
	for _, prior := range r.bindings {
		if prior.OwnerID == b.OwnerID && prior.ProviderOwnerID == b.ProviderOwnerID && prior.ProviderID == b.ProviderID && prior.ConnectionID == b.ConnectionID && prior.Revision == b.Revision && prior.Kind == b.Kind && prior.RemoteID == b.RemoteID {
			if !prior.IsPreset || prior.Name != b.Name {
				return b, providerresources.ErrConflict
			}
			return prior, nil
		}
	}
	b.CreatedAt = time.Now().UTC()
	b.UpdatedAt = b.CreatedAt
	r.bindings[b.ID] = b
	r.writes++
	return b, nil
}

type presetAPIFixture struct {
	*resourceAPIFixture
	catalog              *presetAPIService
	native               *presetAPIProvider
	repo                 *presetAPIRepository
	revision, connection string
}

func newPresetAPIFixture(t *testing.T) *presetAPIFixture {
	f := &presetAPIFixture{resourceAPIFixture: newResourceAPIFixture(t), revision: resourceAPIRevision, connection: "kling"}
	f.s.auth = presetBrowserAuth{}
	_, f.alice = issueTestKey(t, f.resourceAPIFixture.h, uuid.NewString(), `{"name":"preset-a","scopes":["generation"]}`)
	_, f.bob = issueTestKey(t, f.resourceAPIFixture.h, uuid.NewString(), `{"name":"preset-b","scopes":["generation"]}`)
	f.catalog = &presetAPIService{Service: f.s.service, models: []core.MediaModel{{ID: "preset-consumer", MediaType: "video", ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "voice", MaxCount: 1, SupportsPresets: true}, {Kind: "element", MaxCount: 1, SupportsPresets: true, Subtypes: []string{"multi_image", "video_character"}}}}}}
	f.native = &presetAPIProvider{result: []core.ProviderResource{{RemoteID: "private-large-native-preset", Name: "Official fixed voice", Kind: "voice", Subtype: "preset", Status: core.ProviderSucceeded, IsPreset: true, PreviewURL: "https://cdn.example/private-preview?signature=private-source", Sources: []core.ResourceSourceInput{{URL: "https://cdn.example/private-source", SHA256: strings.Repeat("a", 64)}}}}}
	f.repo = &presetAPIRepository{resourceAPIRepository: f.resources}
	f.s.service = f.catalog
	f.s.resourceRepository = f.repo
	f.s.resourceScopes = func(ctx context.Context, _ string) (core.ProviderConnectionScope, error) {
		return core.ProviderConnectionScope{ID: f.connection, OwnerID: identity.ProviderOwnerID(ctx), Revision: f.revision}, nil
	}
	f.s.resourceLifecycle = providerresources.NewLifecycle(f.repo, f.s.resourceScopes, resourceAPIDirectory{p: f.native})
	// Exercise the actual shared handler registration and complete authentication,
	// catalogue, organization and installation policies.
	f.h = f.s.handler()
	return f
}
func (f *presetAPIFixture) discover(t *testing.T, kind string) []resourcePresetView {
	t.Helper()
	w := resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?provider=kling&workflow_model=preset-consumer&kind="+kind, f.alice, "", nil)
	resourceAPIAssertStatus(t, w, 200)
	resourceAPIAssertPrivate(t, w.Body.String(), "private-large-native-preset", "private-preview", "private-source", strings.Repeat("a", 64), resourceAPIRevision, "remote_id", "connection_id", "provider_owner_id")
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("tickets cacheable")
	}
	var body struct {
		Data []resourcePresetView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}
func TestResourcePresetDiscoveryImportOpaqueAndIdempotent(t *testing.T) {
	f := newPresetAPIFixture(t)
	data := f.discover(t, "voice")
	if len(data) != 1 || !data[0].Importable || data[0].ImportTicket == "" || data[0].TicketExpiresAt == nil || data[0].Name != "Official fixed voice" {
		t.Fatal(data)
	}
	raw, err := base64.RawURLEncoding.DecodeString(data[0].ImportTicket)
	if err != nil || json.Valid(raw) || strings.Contains(string(raw), "private-large-native-preset") {
		t.Fatal("ticket is plaintext")
	}
	body := map[string]any{"provider": "kling", "workflow_model": "preset-consumer", "ticket": data[0].ImportTicket}
	first := resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body)
	resourceAPIAssertStatus(t, first, 200)
	var binding resourceBindingView
	if err = json.Unmarshal(first.Body.Bytes(), &binding); err != nil || !binding.IsPreset || !binding.Usable || binding.Name != data[0].Name || binding.ID == "" {
		t.Fatal(binding, err)
	}
	again := resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body)
	resourceAPIAssertStatus(t, again, 200)
	var second resourceBindingView
	_ = json.Unmarshal(again.Body.Bytes(), &second)
	if second.ID != binding.ID || f.repo.writes != 1 || f.native.calls != 1 || f.tasks.creates != 0 || len(f.queue.ids) != 0 || len(f.repo.jobs) != 0 {
		t.Fatal("import created duplicate, native mutation or paid task")
	}
	resourceAPIAssertPrivate(t, first.Body.String(), "private-large-native-preset", "private-source", resourceAPIRevision, "remote_id", "connection_id", "SourceReference")
	f.native.result = []core.ProviderResource{{RemoteID: "official-image-element", Name: "Official element", Kind: "element", Subtype: "multi_image", Status: core.ProviderSucceeded, IsPreset: true}}
	image := f.discover(t, "element")
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", map[string]any{"provider": "kling", "workflow_model": "preset-consumer", "ticket": image[0].ImportTicket}), 200)
}
func TestResourcePresetTicketsRejectTamperingForeignScopeExpiryAndPermissionLoss(t *testing.T) {
	f := newPresetAPIFixture(t)
	ticket := f.discover(t, "voice")[0].ImportTicket
	body := func(token, provider string) map[string]any {
		return map[string]any{"provider": provider, "workflow_model": "preset-consumer", "ticket": token}
	}
	for _, token := range []string{"", strings.Repeat("x", presetTicketLimit+1), "private-native-id", ticket[:len(ticket)/2] + "!" + ticket[len(ticket)/2+1:]} {
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(token, "kling")), 400)
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.bob, "", body(ticket, "kling")), 400)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(ticket, "other-connection")), 400)
	payload, err := f.s.openPresetTicket(ticket, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload.ExpiresAt = time.Now().Add(-time.Second).Unix()
	expired, err := f.s.sealPresetTicket(payload)
	if err != nil {
		t.Fatal(err)
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(expired, "kling")), 400)
	f.revision = "rotated"
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(ticket, "kling")), 404)
	f.revision = resourceAPIRevision
	f.connection = "duplicate-key-other-connection"
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(ticket, "kling")), 404)
	f.connection = "kling"
	f.catalog.models[0].ResourceInputSchemas[0].SupportsPresets = false
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", body(ticket, "kling")), 400)
	for _, field := range []string{"remote_id", "source_url", "name", "duration", "is_preset"} {
		request := body(ticket, "kling")
		request[field] = "forged"
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", request), 400)
	}
	if f.repo.imports != 0 {
		t.Fatal("invalid ticket reached persistence")
	}
}
func TestResourcePresetCatalogueAuthorizationVideoGapAndRotation(t *testing.T) {
	f := newPresetAPIFixture(t)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?provider=kling&workflow_model=preset-consumer&kind=voice", "", "", nil), 401)
	for _, q := range []string{"provider=other&kind=voice", "provider=kling&workflow_model=preset-consumer&kind=file_resource", "provider=kling&workflow_model=preset-consumer&kind=voice&page=0", "provider=kling&workflow_model=preset-consumer&kind=voice&size=101", "provider=kling&workflow_model=preset-consumer&kind=voice&kind=element", "provider=kling&workflow_model=preset-consumer&kind=voice&remote_id=forged"} {
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?"+q, f.alice, "", nil), 400)
	}
	f.catalog.models[0].ResourceInputSchemas[0].SupportsPresets = false
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?provider=kling&workflow_model=preset-consumer&kind=voice", f.alice, "", nil), 400)
	if f.native.calls != 0 {
		t.Fatal("unauthorized catalogue request reached native provider")
	}
	f.native.result = []core.ProviderResource{{RemoteID: "video-character-id", Name: "Official video character", Kind: "element", Subtype: "video_character", Status: core.ProviderSucceeded, IsPreset: true}}
	data := f.discover(t, "element")
	if len(data) != 1 || data[0].Importable || data[0].ImportTicket != "" || data[0].UnavailableReason != "video_source_inspection_unavailable" {
		t.Fatal("invented video duration", data)
	}
	f.native.mutate = func() { f.revision = "rotated-during-query" }
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?provider=kling&workflow_model=preset-consumer&kind=element", f.alice, "", nil), 404)
}

func TestResourcePresetTicketEncryptionDomainExpiryAndSecretRotation(t *testing.T) {
	s := &Server{mediaSigner: newMediaURLSigner("synthetic-preset-secret", time.Hour)}
	now := time.Unix(1800000000, 0)
	payload := resourcePresetTicket{Version: 1, WorkflowModel: "preset-consumer", ExpiresAt: now.Add(presetTicketTTL).Unix(), OwnerID: "private-owner", ProviderOwnerID: "private-provider-owner", ProviderID: "actual-connection", ConnectionID: "actual-connection", Revision: "private-revision", Kind: "voice", Subtype: "preset", Name: "Official", RemoteID: "private-native-id"}
	first, err := s.sealPresetTicket(payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.sealPresetTicket(payload)
	if err != nil || second == first {
		t.Fatal("nonce reused", err)
	}
	if got, err := s.openPresetTicket(first, now); err != nil || got != payload {
		t.Fatal("ticket did not roundtrip", got, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if _, err = s.openPresetTicket(base64.RawURLEncoding.EncodeToString(raw), now); err == nil {
		t.Fatal("ciphertext modification accepted")
	}
	if _, err = s.openPresetTicket(first, time.Unix(payload.ExpiresAt, 0)); err == nil {
		t.Fatal("expired ticket accepted")
	}
	if _, err = s.openPresetTicket(first, now.Add(-time.Second)); err == nil {
		t.Fatal("excessive future validity accepted")
	}
	rotated := &Server{mediaSigner: newMediaURLSigner("different-preset-secret", time.Hour)}
	if _, err = rotated.openPresetTicket(first, now); err == nil {
		t.Fatal("rotated server key accepted ticket")
	}
	aead, err := s.presetCipher()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, _ := json.Marshal(payload)
	nonce := make([]byte, aead.NonceSize())
	otherDomain := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plaintext, []byte("different-protocol")))
	if _, err = s.openPresetTicket(otherDomain, now); err == nil {
		t.Fatal("different protocol ticket accepted")
	}
}

func TestResourcePresetSelectedWorkflowBinding(t *testing.T) {
	f := newPresetAPIFixture(t)
	// A different visible media model must not lend eligibility to this request.
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", "/v1/provider-resource-presets?provider=kling&kind=voice", f.alice, "", nil), 400)
	if f.native.calls != 0 {
		t.Fatal("missing selected workflow reached provider")
	}
	f.catalog.models = append(f.catalog.models, core.MediaModel{ID: "other-consumer", MediaType: "video", ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "voice", MaxCount: 1, SupportsPresets: true}}})
	ticket := f.discover(t, "voice")[0].ImportTicket
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", map[string]any{"provider": "kling", "workflow_model": "other-consumer", "ticket": ticket}), 400)
	if f.repo.imports != 0 {
		t.Fatal("cross-workflow ticket reached persistence")
	}
	f.catalog.models[0].PublicID = "public-consumer"
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resource-presets/import", f.alice, "", map[string]any{"provider": "kling", "workflow_model": "public-consumer", "ticket": ticket}), 200)
}
