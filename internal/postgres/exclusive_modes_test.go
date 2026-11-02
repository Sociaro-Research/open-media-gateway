package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"testing"
)

func TestExclusiveModesTransactionalAdmission(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", user.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE system_settings SET shared_resources=TRUE,personal_providers=TRUE`); err != nil {
		t.Fatal(err)
	}
	for _, admitted := range []bool{false, true} {
		for _, shared := range []bool{false, true} {
			request := identity.WithOwnerID(ctx, user.ID)
			owner := user.ID
			if shared {
				owner = identity.SharedOwnerID
			}
			request = identity.WithResourceOwners(request, owner, owner)
			if admitted {
				request = system.WithAdmissionCheck(request)
			}
			tx, e := db.BeginTx(request, nil)
			if e != nil {
				t.Fatal(e)
			}
			policy, e := lockAdmissionPolicy(request, tx)
			if e == nil {
				e = checkAdmissionPolicy(request, tx, policy)
			}
			tx.Rollback()
			if admitted && !shared {
				if !errors.Is(e, core.ErrProviderUnavailable) {
					t.Fatal("administrator bypassed legacy hybrid", e)
				}
			} else if e != nil {
				t.Fatal("shared/new or admitted work blocked", e)
			}
		}
	}
	for _, owners := range [][2]string{{user.ID, identity.SharedOwnerID}, {identity.SharedOwnerID, user.ID}} {
		mixed := system.WithAdmissionCheck(identity.WithResourceOwners(identity.WithOwnerID(ctx, user.ID), owners[0], owners[1]))
		tx, e := db.BeginTx(mixed, nil)
		if e != nil {
			t.Fatal(e)
		}
		p, e := lockAdmissionPolicy(mixed, tx)
		if e == nil {
			e = checkAdmissionPolicy(mixed, tx, p)
		}
		tx.Rollback()
		if !errors.Is(e, core.ErrProviderUnavailable) {
			t.Fatal("mixed owners bypassed exclusive company mode", e)
		}
	}
	companyID := uuid.NewString()
	for _, owner := range []string{companyID, uuid.NewString()} {
		request := system.WithAdmissionCheck(identity.WithResourceOwners(identity.WithOrganization(identity.WithOwnerID(ctx, user.ID), companyID, false), identity.SharedOwnerID, owner))
		tx, e := db.BeginTx(request, nil)
		if e != nil {
			t.Fatal(e)
		}
		p, e := lockAdmissionPolicy(request, tx)
		if e == nil {
			e = checkAdmissionPolicy(request, tx, p)
		}
		tx.Rollback()
		if owner == companyID {
			if e != nil {
				t.Fatal("company-owned S3 denied", e)
			}
		} else if !errors.Is(e, core.ErrProviderUnavailable) {
			t.Fatal("foreign company storage admitted", e)
		}
	}
	request := system.WithAdmissionCheck(identity.WithResourceOwners(identity.WithOwnerID(ctx, user.ID), user.ID, user.ID))
	task := core.Task{ID: uuid.NewString(), Status: "queued", MediaType: "image", Provider: "mock", Model: "mock-image-v1", Prompt: "No persisted key ID", OwnerID: user.ID}
	if _, _, err = NewTaskRepository(db).CreateOrGet(request, task, "", ""); !errors.Is(err, core.ErrProviderUnavailable) {
		t.Fatal("nil-key HTTP task bypassed transactional policy", err)
	}
	var inserted int
	if err = db.QueryRow(`SELECT count(*) FROM tasks WHERE id=$1`, task.ID).Scan(&inserted); err != nil || inserted != 0 {
		t.Fatal("denied task persisted", err)
	}
	before, err := repo.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveSettings(ctx, user.ID, before); err == nil {
		t.Fatal("repository persisted hybrid")
	}
	if got, _ := repo.Settings(ctx); got.Version != before.Version || !got.SharedResources || !got.PersonalProviders {
		t.Fatal("rejected write mutated legacy state")
	}
	before.PersonalProviders = false
	saved, err := repo.SaveSettings(ctx, user.ID, before)
	if err != nil || saved.PersonalProviders || !saved.SharedResources {
		t.Fatal("explicit exclusive transition", err)
	}
	private := saved
	private.SharedResources = false
	private.PersonalProviders = true
	if _, err = repo.SaveSettings(centralWorkspaceTestContext(t), user.ID, private); !errors.Is(err, system.ErrForbidden) {
		t.Fatal("central storage accepted personal mode", err)
	}
	if _, err = repo.SaveSettings(ctx, user.ID, before); !errors.Is(err, system.ErrConflict) {
		t.Fatal("version conflict lost", err)
	}
}
