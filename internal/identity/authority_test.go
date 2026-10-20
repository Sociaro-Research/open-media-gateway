package identity

import (
	"context"
	"testing"
)

func TestSystemPermissionIsBoundToCredential(t *testing.T) {
	ctx := context.Background()
	if !SystemPermissionAllowed(ctx) {
		t.Fatal("browser/internal role check suppressed")
	}
	ctx = WithAPIKeyID(ctx, "limited")
	if SystemPermissionAllowed(ctx) {
		t.Fatal("missing scope must fail closed")
	}
	ctx = WithSystemPermission(ctx, true)
	if !SystemPermissionAllowed(ctx) {
		t.Fatal("explicit permission lost")
	}
	if SystemPermissionAllowed(WithAPIKeyID(ctx, "other")) {
		t.Fatal("permission transferred to another credential")
	}
	if SystemPermissionAllowed(WithSystemPermission(ctx, false)) {
		t.Fatal("permission revocation ignored")
	}
}
