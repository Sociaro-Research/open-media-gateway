package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestRejectedDirectReferenceDurabilityCleanupAndRetry(t *testing.T) {
	db, library, store, ctx := uploadFixture(t)
	db.SetMaxOpenConns(1)
	input := uploadInput("too-small.png")
	input.Validation = &referencevalidation.Target{Provider: "fixture", Model: "image"}
	validate := func(_ context.Context, target *referencevalidation.Target, files []referencevalidation.File, _ bool) error {
		if target == nil {
			t.Fatal("pinned model context lost")
		}
		return referencevalidation.ValidateFiles(core.MediaModel{ID: "image", ReferenceMediaLimits: &core.ReferenceMediaLimits{Image: core.MediaFileLimits{MinDimension: 300}}}, files, false)
	}
	plan, err := library.BeginUploadWithValidation(ctx, input, validate)
	if err != nil {
		t.Fatal(err)
	}
	store.failAbort.Store(true)
	_, err = library.FinishUploadWithValidation(ctx, plan.ID, validate)
	if !errors.Is(err, referencevalidation.ErrInvalid) || !strings.Contains(err.Error(), "2×2") {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM reference_images WHERE owner_id=$1`, identity.OwnerID(ctx)).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected file entered library", count, err)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT state FROM reference_uploads WHERE owner_id=$1 AND id=$2`, identity.OwnerID(ctx), plan.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var session references.UploadSession
	json.Unmarshal(raw, &session)
	if session.Rejected == "" || session.ResultID != "" || session.Storage.UploadID == "" {
		t.Fatal("rejection/cleanup state lost", session)
	}
	store.failAbort.Store(false)
	_, err = library.FinishUploadWithValidation(ctx, plan.ID, validate)
	if !errors.Is(err, referencevalidation.ErrInvalid) || store.completed.Load() != 1 {
		t.Fatal("rejected retry repeated completion", err)
	}
	if _, ok := store.objects.Load(session.Storage.Key); ok {
		t.Fatal("rejected bytes remained after successful cleanup")
	}
	good, err := library.BeginUpload(ctx, uploadInput("valid-control.png"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := library.FinishUpload(ctx, good.ID)
	if err != nil {
		t.Fatal(err)
	}
	expireUploads(t, db, identity.OwnerID(ctx))
	if err = library.CleanupUploads(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = library.Get(ctx, saved.ID); err != nil {
		t.Fatal("cleanup deleted valid control", err)
	}
}

func TestReferenceInspectionUsesOwnerCreatorAndContentPermissions(t *testing.T) {
	db, library, store, ctx := uploadFixture(t)
	db.SetMaxOpenConns(1)
	owner := identity.OwnerID(ctx)
	alice := identity.WithOrganizationRole(identity.WithUser(ctx, identity.User{ID: uuid.NewString()}), owner, organization.Developer)
	bob := identity.WithOrganizationRole(identity.WithUser(ctx, identity.User{ID: uuid.NewString()}), owner, organization.MemberRole)
	data := testmedia.PNG512("owned")
	sum := sha256.Sum256(data)
	id := uuid.NewString()
	key := "owned-fixture"
	item := references.Image{ID: id, Name: "private.png", ContentType: "image/png", SizeBytes: int64(len(data)), StorageKey: key, SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Now()}
	if err := NewReferenceRepository(db).Insert(alice, item); err != nil {
		t.Fatal(err)
	}
	store.objects.Store(key, data)
	if _, err := library.Inspect(alice, id); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Inspect(bob, id); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("other creator inspected content", err)
	}
	manager := identity.WithOrganizationRole(alice, owner, organization.Manager)
	if _, err := library.Inspect(manager, id); !errors.Is(err, references.ErrReadForbidden) {
		t.Fatal("metadata-only manager inspected content", err)
	}
	// Within upload completion this read must use the transaction connection.
	input := uploadInput("new.png")
	input.Validation = &referencevalidation.Target{Provider: "fixture", Model: "image", ReferenceIDs: []string{id}}
	validate := func(ctx context.Context, target *referencevalidation.Target, _ []referencevalidation.File, _ bool) error {
		for _, id := range target.ReferenceIDs {
			if _, err := library.Inspect(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}
	plan, err := library.BeginUploadWithValidation(alice, input, validate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = library.FinishUploadWithValidation(alice, plan.ID, validate); err != nil {
		t.Fatal("transactional validation deadlocked or lost creator scope", err)
	}
}

func TestReferenceAdmissionErrorAppliedAfterRealIdempotencyResolution(t *testing.T) {
	db := auditDatabase(t, true)
	actor, err := NewAuthRepository(db).UpsertGoogleUser(context.Background(), uuid.NewString(), uuid.NewString()+"@example.test", "Reference test", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), actor))
	repo := NewTaskRepository(db)
	failure := referencevalidation.Invalid("motion.mp4", "average frame rate 23.804 FPS; requires 24–60 FPS")
	now := time.Now()
	task := core.Task{ID: uuid.NewString(), ActorID: actor.ID, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Synthetic", CreatedAt: now, UpdatedAt: now, AdmissionError: failure}
	if _, _, err = repo.CreateOrGet(ctx, task, "same", "hash"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, identity.OwnerID(ctx)).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid admission persisted", n, err)
	}
	task.AdmissionError = nil
	saved, created, err := repo.CreateOrGet(ctx, task, "same", "hash")
	if err != nil || !created {
		t.Fatal(err)
	}
	task.ID = uuid.NewString()
	task.AdmissionError = storage.ErrObjectNotFound
	retry, created, err := repo.CreateOrGet(ctx, task, "same", "hash")
	if err != nil || created || retry.ID != saved.ID {
		t.Fatal("accepted retry lost", err)
	}
	if _, _, err = repo.CreateOrGet(ctx, task, "same", "changed"); !errors.Is(err, core.ErrIdempotencyConflict) {
		t.Fatal("conflict bypassed", err)
	}
}
