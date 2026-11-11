package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func centralWorkspaceTestContext(t *testing.T) context.Context {
	t.Helper()
	meta, err := installation.Describe(installation.DefaultCentralOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return installation.WithContext(context.Background(), meta)
}
func TestWorkspaceHostingCanonicalRole(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewSystemRepository(db)
	base := context.Background()
	u, err := NewAuthRepository(db).UpsertGoogleUser(base, uuid.NewString(), uuid.NewString()+"@fixture.test", "Customer", "")
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("w", 32))
	manager := wa.New(repo, cipher)
	for _, origin := range []string{installation.DefaultCentralOrigin, "https://CONSOLE.SOCIARO.AI/", "https://console.sociaro.ai:443/", "https://console-staging.fixture.test", "https://console.sociaro.ai:8443", ""} {
		for _, legacy := range []bool{false, true} {
			for _, shared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/legacy=%t/shared=%t", origin, legacy, shared), func(t *testing.T) {
					if _, err := db.Exec(`UPDATE system_settings SET customer_workspaces=$1,shared_resources=$2`, legacy, shared); err != nil {
						t.Fatal(err)
					}
					meta, err := installation.Describe(origin)
					if err != nil {
						t.Fatal(err)
					}
					ctx := installation.WithContext(base, meta)
					device, _ := wa.DeviceCode()
					proof, _ := wa.DeviceCode()
					v, err := manager.Start(ctx, device, wa.Challenge{InstallationProof: proof, Label: "Fixture", Origin: "http://localhost"})
					if meta.Role != "central" {
						if !errors.Is(err, wa.ErrDisabled) {
							t.Fatalf("ordinary start: %v", err)
						}
						if _, _, _, err = repo.Describe(ctx, u.ID, "missing"); !errors.Is(err, wa.ErrDisabled) {
							t.Fatal("ordinary inspect", err)
						}
						if _, err = repo.Approve(ctx, u.ID, "missing", wa.Approval{Consent: true}); !errors.Is(err, wa.ErrDisabled) {
							t.Fatal("ordinary approve", err)
						}
						if _, err = repo.Redeem(ctx, wa.Digest(device), apikey.Key{}, nil, nil); !errors.Is(err, wa.ErrDisabled) {
							t.Fatal("ordinary redeem", err)
						}
						return
					}
					if err != nil {
						t.Fatal("central start", err)
					}
					if _, _, _, err = repo.Describe(ctx, u.ID, v.Code); err != nil {
						t.Fatal("central inspect", err)
					}
					approved, err := repo.Approve(ctx, u.ID, v.Code, wa.Approval{Consent: true})
					if err != nil || approved.Company == "" {
						t.Fatal("central approve", err)
					}
					if _, err = db.Exec(`UPDATE workspace_authorizations SET next_poll_at=NOW()-interval '1 second' WHERE device_hash=$1`, wa.Digest(device)); err != nil {
						t.Fatal(err)
					}
					issued, token, err := manager.Poll(ctx, device)
					if err != nil || token == "" || issued.Company != approved.Company {
						t.Fatal("central redeem", err)
					}
					// Cleanup remains authorized after losing central identity, with old flags false.
					ordinary := context.Background()
					if _, err = repo.Grants(ordinary, u.ID); err != nil {
						t.Fatal(err)
					}
					if err = repo.Revoke(ordinary, u.ID, issued.KeyID); err != nil {
						t.Fatal(err)
					}
					if err = repo.Cancel(ordinary, wa.Digest(device)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestLegacyHostingSettingCannotBeWritten(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewSystemRepository(db)
	ctx := context.Background()
	u, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", u.ID, true, true); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		if _, err = db.Exec(`UPDATE system_settings SET customer_workspaces=$1`, legacy); err != nil {
			t.Fatal(err)
		}
		before, err := repo.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		next := before
		next.CustomerWorkspaces = !legacy
		next.GenerationUI = !before.GenerationUI
		saved, err := repo.SaveSettings(ctx, u.ID, next)
		if err != nil || saved.CustomerWorkspaces != legacy || saved.Version != before.Version+1 {
			t.Fatal("legacy override persisted or version changed", err)
		}
		var stored bool
		if err = db.QueryRow(`SELECT customer_workspaces FROM system_settings`).Scan(&stored); err != nil || stored != legacy {
			t.Fatal("legacy value mutated", err)
		}
		if _, err = repo.SaveSettings(ctx, u.ID, next); !errors.Is(err, system.ErrConflict) {
			t.Fatal("stale settings override", err)
		}
	}
}
