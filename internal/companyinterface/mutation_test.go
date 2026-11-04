package companyinterface

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCompanyMutationDescriptorCopiesNestedAuthority(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	original := Principal{Grant: Grant{ExpiresAt: &expires}, Scopes: []string{"generation"}}
	want := expires
	ctx := WithMutationAuthority(context.Background(), original, original, Credentials{Installation: "private installation", Session: "private session", Key: "private key", KeyPresent: true}, "generation", "generation-json")
	expires = expires.Add(time.Hour)
	original.Scopes[0] = "system:manage"
	first, ok := MutationAuthorityFromContext(ctx)
	if !ok || !first.Original.Grant.ExpiresAt.Equal(want) || first.Original.Scopes[0] != "generation" {
		t.Fatal("input aliases authority")
	}
	*first.Effective.Grant.ExpiresAt = expires
	first.Original.Scopes[0] = "changed"
	second, _ := MutationAuthorityFromContext(ctx)
	if !second.Effective.Grant.ExpiresAt.Equal(want) || second.Original.Scopes[0] != "generation" {
		t.Fatal("accessor aliases authority")
	}
	raw, e := json.Marshal(second)
	if e != nil || string(raw) != "{}" {
		t.Fatal("server-private descriptor serialized")
	}
}
