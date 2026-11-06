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

func TestDirectKeyCompanyCeilingIsSeparateFromDelegation(t *testing.T) {
	ctx := WithDirectKeyCompanyBoundary(WithAPIKeyID(context.Background(), "ordinary-key"), "company-a")
	if DirectKeyCompanyBoundary(ctx) != "company-a" || CompanyBoundary(ctx) != "" || SystemPermissionAllowed(ctx) {
		t.Fatal("direct ceiling conferred delegated or system authority")
	}
	delegated := WithCompanyBoundary(ctx, "delegated-company")
	if CompanyBoundary(delegated) != "delegated-company" || DirectKeyCompanyBoundary(delegated) != "company-a" || SystemPermissionAllowed(WithSystemPermission(delegated, true)) {
		t.Fatal("direct ceiling replaced the delegated boundary")
	}
	if CompanyBoundary(WithDirectKeyCompanyBoundary(delegated, "")) != "delegated-company" {
		t.Fatal("clearing the direct ceiling removed delegated confinement")
	}
}
