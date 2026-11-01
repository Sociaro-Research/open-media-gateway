package api

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type logoAdminFixture struct {
	providerAdminStub
	logos  map[string][]byte
	fail   bool
	writes int
}

func (f *logoAdminFixture) RemoveLogo(ctx context.Context, id string) error {
	if f.fail {
		return errors.New("storage unavailable; retry")
	}
	f.writes++
	delete(f.logos, identity.ProviderOwnerID(ctx)+":"+id)
	return nil
}

func TestProviderLogoRemovalAuthorizationScopeFailureAndIdempotency(t *testing.T) {
	s, _ := newPersonalTestServer()
	settings := system.Defaults()
	settings.SharedResources = true
	s.system = &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}
	f := &logoAdminFixture{logos: map[string][]byte{"alice:mock": {1}, "alice:openai": {2}, identity.SharedOwnerID + ":mock": {3}}}
	s.providerAdmin = f
	h := s.handler()
	for _, prefix := range []string{"/v1/admin/providers", "/v1/system/providers"} {
		for _, actor := range []string{"", "bob"} {
			w := sendTest(h, "DELETE", prefix+"/mock/logo", "", actor, "")
			if w.Code != 401 && w.Code != 403 {
				t.Fatal("unauthorized removal", w.Code, w.Body)
			}
		}
	}
	if f.writes != 0 {
		t.Fatal("unauthorized delete reached repository")
	}
	f.fail = true
	if w := sendTest(h, "DELETE", "/v1/admin/providers/mock/logo", "", "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	f.fail = false
	if len(f.logos) != 3 {
		t.Fatal("failed removal changed persisted artwork")
	}
	for i := 0; i < 2; i++ {
		if w := sendTest(h, "DELETE", "/v1/admin/providers/mock/logo?owner="+identity.SharedOwnerID, "", "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if _, ok := f.logos["alice:mock"]; ok {
		t.Fatal("private logo retained")
	}
	if len(f.logos) != 2 {
		t.Fatal("other owner/provider changed")
	}
	if w := sendTest(h, "GET", "/provider-assets/alice/mock/icon.png?v=old", "", "alice", ""); w.Code != 404 {
		t.Fatal("old image remains visible", w.Code)
	}
	if w := sendTest(h, "DELETE", "/v1/system/providers/mock/logo", "", "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if len(f.logos) != 1 || f.logos["alice:openai"][0] != 2 {
		t.Fatal("shared deletion changed other provider")
	}
}

func TestProviderLogoReadResourceModesAndRevokedVisibility(t *testing.T) {
	s, keys := newPersonalTestServer()
	settings := system.Defaults()
	settings.SharedResources = true
	sys := &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}
	s.system = sys
	s.providerAdmin = &logoAdminFixture{logos: map[string][]byte{"bob:mock": {1}, identity.SharedOwnerID + ":mock": {2}}}
	h := s.handler()
	_, personal := issueTestKey(t, h, "bob", `{"name":"Personal","resource_mode":"personal","scopes":["generation"]}`)
	_, shared := issueTestKey(t, h, "bob", `{"name":"Shared","resource_mode":"shared","scopes":["generation"]}`)
	privateURL := "/provider-assets/bob/mock/icon.png?v=one"
	sharedURL := "/provider-assets/" + identity.SharedOwnerID + "/mock/icon.png?v=two"
	for _, tc := range []struct {
		url, key string
		status   int
	}{{privateURL, personal, 200}, {sharedURL, shared, 200}, {privateURL, shared, 404}, {sharedURL, personal, 404}} {
		if w := sendTest(h, "GET", tc.url, "", "", tc.key); w.Code != tc.status {
			t.Fatal(tc.url, w.Code, w.Body)
		}
	}
	sys.settings.PersonalProviders = false
	if w := sendTest(h, "GET", privateURL, "", "", personal); w.Code != 404 {
		t.Fatal("disabled personal logo exposed", w.Code)
	}
	sys.settings.PersonalProviders = true
	sys.settings.SharedResources = false
	if w := sendTest(h, "GET", sharedURL, "", "", shared); w.Code != 404 {
		t.Fatal("disabled shared logo exposed", w.Code)
	}
	sys.settings.SharedResources = true
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	s.modelAccess = policy
	models := s.service.ListProviders(context.Background())[0].Models
	refs := []modelaccess.Ref{}
	for _, m := range models {
		refs = append(refs, modelaccess.Ref{Provider: "mock", Model: m.ID})
	}
	_ = policy.Ensure(context.Background(), refs)
	for i := range policy.models {
		policy.models[i].EnabledByDefault = false
	}
	if w := sendTest(h, "GET", sharedURL, "", "", shared); w.Code != 404 {
		t.Fatal("model-hidden provider logo exposed", w.Code)
	}
	// A simultaneous personal browser session must not discard an explicit
	// organization key's model grants when its catalog logo is loaded.
	company := "11111111-1111-4111-8111-111111111111"
	policy.org[company] = map[string]bool{policy.models[0].ID: true}
	secret := "gw_" + strings.Repeat("a", 43)
	keys.records = append(keys.records, storedTestKey{owner: "bob", key: apikey.Key{ID: "org-logo-key", ResourceMode: "shared", OrganizationID: company, OrganizationRole: "member", Scopes: []string{apikey.Generation}}, hash: apikey.Hash(secret)})
	if w := sendTest(h, "GET", sharedURL+"&generation=1", "", "bob", secret); w.Code != 200 {
		t.Fatal("organization grant lost during logo read", w.Code, w.Body)
	}
	delete(policy.org[company], policy.models[0].ID)
	if w := sendTest(h, "GET", sharedURL+"&generation=1", "", "bob", secret); w.Code != 404 {
		t.Fatal("revoked organization grant retained logo", w.Code, w.Body)
	}
}

func TestCommercialProviderLogoReadRequiresCurrentGrant(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &permissionFixture{salesFixture: &salesFixture{clientManagerFixture: &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true, assigned: "11111111-1111-4111-8111-111111111111"}, role: system.ClientManager}, granted: map[string]bool{}}
	s.system = f
	s.providerAdmin = &logoAdminFixture{logos: map[string][]byte{identity.SharedOwnerID + ":mock": {1}}}
	h := s.handler()
	url := "/provider-assets/" + identity.SharedOwnerID + "/mock/icon.png?v=fixture&organization=" + f.assigned
	if w := sendTest(h, "GET", url, "", "bob", ""); w.Code != 404 {
		t.Fatal("ungranted commercial logo", w.Code)
	}
	f.granted["models"] = true
	if w := sendTest(h, "GET", url, "", "bob", ""); w.Code != 200 {
		t.Fatal("granted commercial picker lost logo", w.Code, w.Body)
	}
	f.granted["models"] = false
	f.granted["pricing"] = true
	if w := sendTest(h, "GET", url, "", "bob", ""); w.Code != 200 {
		t.Fatal("granted markup picker lost logo", w.Code, w.Body)
	}
	f.granted["pricing"] = false
	if w := sendTest(h, "GET", url, "", "bob", ""); w.Code != 404 {
		t.Fatal("revoked grant retained logo", w.Code)
	}
}

func (f *logoAdminFixture) Logo(ctx context.Context, id string) ([]byte, error) {
	b, ok := f.logos[identity.ProviderOwnerID(ctx)+":"+id]
	if !ok {
		return nil, providerconfig.ErrNotFound
	}
	return b, nil
}
func (f *logoAdminFixture) SaveLogo(ctx context.Context, id string, b []byte) (string, error) {
	if f.fail {
		return "", errors.New("storage unavailable; retry")
	}
	f.writes++
	f.logos[identity.ProviderOwnerID(ctx)+":"+id] = bytes.Clone(b)
	return "/provider-assets/" + identity.ProviderOwnerID(ctx) + "/" + id + "/icon.png?v=saved", nil
}

type brokenLogoBody struct{}

func (brokenLogoBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenLogoBody) Close() error             { return nil }

func TestProviderLogoAuthorizationValidationAndAtomicFailure(t *testing.T) {
	s, _ := newPersonalTestServer()
	settings := system.Defaults()
	settings.PersonalProviders = true
	settings.SharedResources = true
	s.system = &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}
	f := &logoAdminFixture{logos: map[string][]byte{}}
	s.providerAdmin = f
	h := s.handler()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	img.SetNRGBA(1, 0, color.NRGBA{255, 255, 255, 127})
	img.SetNRGBA(2, 0, color.NRGBA{255, 255, 255, 255})
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, img)
	valid := encoded.Bytes()
	put := func(path, actor, mime string, body io.ReadCloser) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, body)
		r.Header.Set("Content-Type", mime)
		if actor != "" {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, prefix := range []string{"/v1/admin/providers/mock/logo", "/v1/system/providers/mock/logo"} {
		for _, actor := range []string{"", "bob"} {
			if w := put(prefix, actor, "image/png", io.NopCloser(bytes.NewReader(valid))); w.Code != 401 && w.Code != 403 {
				t.Fatal("unauthorized upload", w.Code)
			}
		}
		if f.writes != 0 {
			t.Fatal("unauthorized mutation")
		}
	}
	if w := put("/v1/admin/providers/mock/logo", "alice", "image/png", io.NopCloser(bytes.NewReader(valid))); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	baseline := bytes.Clone(f.logos["alice:mock"])
	for _, tc := range []struct {
		mime string
		body []byte
		want string
	}{
		{"image/svg+xml", valid, "Content-Type"}, {"image/png", []byte(`<svg onload="alert(1)"/>`), "unsupported"},
		{"image/png", []byte("\x89PNG\r\n\x1a\ninvalid"), "invalid PNG"}, {"image/png", make([]byte, providerconfig.MaxLogoBytes+1), "1 MiB"},
	} {
		w := put("/v1/admin/providers/mock/logo", "alice", tc.mime, io.NopCloser(bytes.NewReader(tc.body)))
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := put("/v1/admin/providers/mock/logo", "alice", "image/png", brokenLogoBody{}); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	f.fail = true
	if w := put("/v1/admin/providers/mock/logo", "alice", "image/png", io.NopCloser(bytes.NewReader(valid))); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	f.fail = false
	if f.writes != 1 || !bytes.Equal(baseline, f.logos["alice:mock"]) {
		t.Fatal("failed replacement changed logo")
	}
	url := "/provider-assets/alice/mock/icon.png?v=saved"
	if w := sendTest(h, "GET", url, "", "bob", ""); w.Code != 404 {
		t.Fatal("cross-owner logo exposed", w.Code)
	}
	if w := sendTest(h, "GET", url, "", "alice", ""); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), valid) || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(w.Code, w.Header())
	}
	if w := put("/v1/system/providers/mock/logo", "alice", "image/png", io.NopCloser(bytes.NewReader(valid))); w.Code != 200 || !bytes.Equal(valid, f.logos[identity.SharedOwnerID+":mock"]) {
		t.Fatal("shared logo stored for wrong owner", w.Code, w.Body)
	}
}
