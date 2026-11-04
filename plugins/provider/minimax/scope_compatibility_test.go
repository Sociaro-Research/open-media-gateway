package minimax

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestConnectionHandlePreservesLegacyScopeAcrossAdmissionMetadata(t *testing.T) {
	a := fixture(t, nil)
	// Independently encode the pre-admission scope and handle wire formats.
	oldScope := []byte(`{"ID":"connection-a","OwnerID":"provider-owner","Revision":"revision-one"}`)
	sum := sha256.Sum256(oldScope)
	scopeHash := base64.RawURLEncoding.EncodeToString(sum[:])
	body := []byte(`{"id":"123","owner":"alice","model":"MiniMax-H3","kind":"task"}`)
	mac := hmac.New(sha256.New, []byte(a.key+"\x00"+a.base+"\x00"+scopeHash))
	mac.Write(body)
	token := "minimax:" + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	base := core.ProviderConnectionScope{ID: "connection-a", OwnerID: "provider-owner", Revision: "revision-one"}
	ctxFor := func(owner string, scope core.ProviderConnectionScope) context.Context {
		return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), owner), scope)
	}
	for _, metadata := range []struct {
		version     int64
		fingerprint string
	}{{0, ""}, {7, "catalog-one"}, {8, "catalog-two"}} {
		scope := base
		scope.SettingsVersion = metadata.version
		scope.CatalogFingerprint = metadata.fingerprint
		ctx := ctxFor("alice", scope)
		if got := a.requestConnectionScope(ctx); got != scopeHash {
			t.Fatalf("admission metadata changed historical scope: %q != %q", got, scopeHash)
		}
		h, err := a.open(ctx, token)
		if err != nil || h.ID != "123" {
			t.Fatalf("legacy handle rejected: %+v %v", h, err)
		}
		if got := a.seal(ctx, handle{ID: "123", Model: "MiniMax-H3", Kind: "task"}); got != token {
			t.Fatal("new handle is not historical-format compatible")
		}
	}
	for _, change := range []func(*core.ProviderConnectionScope){func(s *core.ProviderConnectionScope) { s.ID = "connection-b" }, func(s *core.ProviderConnectionScope) { s.OwnerID = "another-provider-owner" }, func(s *core.ProviderConnectionScope) { s.Revision = "revision-two" }, func(s *core.ProviderConnectionScope) { s.Revision = "" }} {
		scope := base
		change(&scope)
		if _, err := a.open(ctxFor("alice", scope), token); err == nil {
			t.Fatal("changed connection identity accepted legacy handle")
		}
	}
	if _, err := a.open(ctxFor("bob", base), token); err == nil {
		t.Fatal("another request owner accepted legacy handle")
	}
}
