package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// This acceptance test keeps a complete 85 data set, including the kind added
// only by 085, through the supported schema-81 application rollback window.
func TestProviderResources85CombinedCyclePreservesMetadataAndStoredBytes(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	assertVersion := func(want int64) {
		t.Helper()
		v, err := goose.GetDBVersion(db)
		if err != nil || v != want {
			t.Fatal("wrong migration version", v, want, err)
		}
	}
	assertVersion(81)
	user, company, key, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	device, _ := wa.DeviceCode()
	cipher, err := providerconfig.NewCipher(strings.Repeat("v", 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte("synthetic-85-legacy-token"))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users(id,email,display_name,is_system_admin) VALUES($1,'cycle85@fixture.test','Preserved customer',TRUE)`, user)
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Preserved legacy company')`, company)
	exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, user)
	exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,1234567,'promo','cycle85-original-credit')`, uuid.NewString(), company, user)
	exec(`INSERT INTO user_api_keys(id,user_id,created_by,organization_id,name,prefix,token_hash,scopes,resource_mode,storage_mode,expires_at) VALUES($1,$2,$2,$3,'Legacy cycle85','fixture',$4,'["generation"]','shared','shared',NOW()+INTERVAL '1 day')`, key, user, company, apikey.Hash("synthetic-85-legacy-token"))
	exec(`INSERT INTO workspace_authorizations(device_hash,user_code,client_name,client_origin,status,expires_at,actor_id,organization_id,key_id,token_ciphertext) VALUES($1,$2,'Legacy cycle85','http://legacy.fixture.lan','issued',NOW()+INTERVAL '5 minutes',$3,$4,$5,$6)`, wa.Digest(device), wa.UserCode(device), user, company, key, encrypted)
	exec(`INSERT INTO customer_workspace_owners(user_id,organization_id) VALUES($1,$2)`, user, company)
	exec(`INSERT INTO remote_workspaces(owner_id,version,active,origin,organization_id,organization_name,remote_user_id,remote_key_id,token_hash,token_ciphertext) VALUES($1::uuid,1,TRUE,'https://central.fixture.test',$2,'Preserved legacy company',$1::text,$3,$4,$5)`, user, company, key, apikey.Hash("synthetic-85-legacy-token"), encrypted)
	exec(`INSERT INTO tasks(id,owner_id,actor_id,api_key_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,cost,provider_task_id,provider_result_url,provider_identity,provider_usage,template_snapshot) VALUES($1,$2,$3,$4,'succeeded','mock','preserved-image','Preserved accepted request',$5,$2,'{"expected_usd":0.123456,"actual_usd":0.12,"metered_usd":0.12,"tariff_usd":0.12,"markup_percent":10,"status":"confirmed","billing_instance_id":"private-billing-snapshot","invoice_ids":["private-invoice-snapshot"],"pricing":{"currency":"USD","unit":"request","price":0.12,"per":1,"source":"synthetic accepted tariff"}}','private-provider-task','https://provider.fixture.invalid/private-result','preserved-canonical-resource-revision','{"metrics":[{"name":"images","quantity":1}],"raw":{"private":"retained provider usage"}}','{"name":"Preserved template","revision":7,"prompt":"Original template text"}')`, taskID, company, user, key, identity.SharedOwnerID)
	local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	storageCtx := identity.WithOwnerID(ctx, company)
	payloads := map[string][]byte{}
	archive := func(id, kind string, data []byte) core.StoredMedia {
		t.Helper()
		m, err := local.ArchiveSource(storageCtx, company+"/results/"+taskID+"/"+id, core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: kind, SizeBytes: int64(len(data))})
		if err != nil {
			t.Fatal(err)
		}
		payloads[m.StorageKey] = bytes.Clone(data)
		if err := NewStorageSettingsRepository(db).RecordResultObject(storageCtx, m.StorageKey, taskID, m.SizeBytes); err != nil {
			t.Fatal(err)
		}
		return m
	}
	primary := archive("primary", "image/png", testmedia.PNG512("cycle85-primary"))
	exec(`UPDATE tasks SET result_url=$2,storage_key=$3,result_content_type=$4,result_size_bytes=$5,result_sha256=$6,stored_at=$7 WHERE id=$1`, taskID, "/v1/generations/"+taskID+"/content", primary.StorageKey, primary.ContentType, primary.SizeBytes, primary.SHA256, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	legacyTables := []string{"users", "organizations", "organization_members", "organization_credits", "user_api_keys", "workspace_authorizations", "customer_workspace_owners", "remote_workspaces", "tasks", "stored_objects", "result_objects"}
	legacy := newResourceLegacyProjection(t, db, legacyTables...)
	before := legacy.snapshot(t, db, "85cycle-81-before-first-upgrade")
	if err := goose.UpTo(db, "migrations", 85); err != nil {
		t.Fatal(err)
	}
	assertVersion(85)
	if legacy.snapshot(t, db, "85cycle-85-after-first-upgrade") != before {
		t.Fatal("81-to85 upgrade changed a pre-existing field or row count")
	}
	assertResourceBindingsEmptyDefault(t, db)

	// Exercise the existing real provisioning fixture, not a fake proof row.
	exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	repo := NewSystemRepository(db)
	proof, _ := wa.DeviceCode()
	authority := wa.New(repo, cipher)
	freshDevice, challenge := dedicatedStart(t, authority, proof, "")
	grant := dedicatedIssue(t, db, repo, authority, user, freshDevice, challenge)
	proofCipher, err := cipher.Encrypt([]byte(proof))
	if err != nil {
		t.Fatal(err)
	}
	installed, err := repo.InstallationIdentity(ctx, user, "https://central.fixture.test", proofCipher)
	if err != nil || !bytes.Equal(installed, proofCipher) {
		t.Fatal("installation proof was not retained", err)
	}
	exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,7654321,'promo','cycle85-dedicated-credit')`, uuid.NewString(), grant.Company, user)
	seed := seedResourcePreservationMetadata(t, db, company, identity.SharedOwnerID)
	resourceCtx := identity.WithResourceOwners(storageCtx, identity.SharedOwnerID, company)
	resourceRepo := NewProviderResourceRepository(db)
	digest := sha256.Sum256(payloads[primary.StorageKey])
	fileIntent := providerresources.Intent{Kind: "file_resource", Subtype: "image", Name: "Preserved uploaded file", Tags: []string{"synthetic", "private"}, Sources: []core.ResourceSourceEvidence{{ReferenceID: uuid.NewString(), MediaType: "image", SHA256: hex.EncodeToString(digest[:])}}}
	fileJob := providerresources.Job{ProviderID: "mock", ConnectionID: "mock", Revision: "preserved-canonical-resource-revision", Intent: fileIntent}
	saved, created, err := resourceRepo.ReserveCreate(resourceCtx, fileJob, "cycle85-completed-file")
	if err != nil || !created {
		t.Fatal(saved, created, err)
	}
	if _, started, err := resourceRepo.StartJob(resourceCtx, saved.ID); err != nil || !started {
		t.Fatal(started, err)
	}
	file, err := resourceRepo.CompleteResource(resourceCtx, saved.ID, core.ProviderResource{Kind: "file_resource", Subtype: "image", RemoteID: "private-opaque-file-900719925474099312345", Status: core.ProviderSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	fileJob.Intent.Name = "Preserved pending uploaded file"
	pending, created, err := resourceRepo.ReserveCreate(resourceCtx, fileJob, "cycle85-pending-file")
	if err != nil || !created {
		t.Fatal(pending, created, err)
	}
	if _, started, err := resourceRepo.StartJob(resourceCtx, pending.ID); err != nil || !started {
		t.Fatal(started, err)
	}
	if err := resourceRepo.SaveHandle(resourceCtx, pending.ID, "private-scoped-file-operation-handle", ""); err != nil {
		t.Fatal(err)
	}
	if err := resourceRepo.RecordUncertain(resourceCtx, pending.ID, "ignored provider diagnostic"); err != nil {
		t.Fatal(err)
	}
	scope := func(context.Context, string) (core.ProviderConnectionScope, error) {
		return core.ProviderConnectionScope{ID: "mock", OwnerID: identity.SharedOwnerID, Revision: fileJob.Revision}, nil
	}
	fileRefs := []core.ResourceReference{{BindingID: file.ID, ReferenceID: "file_1"}}
	snapshots, err := providerresources.New(resourceRepo, scope).ResolveResourceReferences(resourceCtx, "mock", fileRefs)
	if err != nil {
		t.Fatal(err)
	}
	var optionsJSON []byte
	if err := db.QueryRow(`SELECT request_options FROM tasks WHERE id=$1`, seed.consumerTask).Scan(&optionsJSON); err != nil {
		t.Fatal(err)
	}
	var options core.MediaOptions
	if err := json.Unmarshal(optionsJSON, &options); err != nil {
		t.Fatal(err)
	}
	element, err := resourceRepo.GetBinding(resourceCtx, options.ResourceSnapshots[0].BindingID)
	if err != nil || element.Kind != "element" || element.BoundVoiceBindingID == "" {
		t.Fatal("preservation seed has no auto-bound voice", err)
	}
	voiceRefs := []core.ResourceReference{{BindingID: element.BoundVoiceBindingID, ReferenceID: "voice_1"}}
	voiceSnapshots, err := providerresources.New(resourceRepo, scope).ResolveResourceReferences(resourceCtx, "mock", voiceRefs)
	if err != nil {
		t.Fatal(err)
	}
	options.ResourceReferences = append(options.ResourceReferences, voiceRefs...)
	options.ResourceSnapshots = append(options.ResourceSnapshots, voiceSnapshots...)
	options.ResourceReferences = append(options.ResourceReferences, fileRefs...)
	options.ResourceSnapshots = append(options.ResourceSnapshots, snapshots...)
	optionsJSON, err = json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET request_options=$2 WHERE id=$1`, seed.consumerTask, optionsJSON)

	// Every StoredArtifact field is populated where meaningful, including private
	// provider identity, group, duration, opaque format, key, MIME, size and hash.
	artifacts := []core.StoredArtifact{}
	group := uuid.NewString()
	for i, fixture := range []struct {
		kind, format, mime string
		data               []byte
		duration           float64
	}{
		{"image", "png", "image/png", payloads[primary.StorageKey], 0},
		{"audio", "wav", "audio/wav", testmedia.WAV(), 1.25},
		{"video", "mp4", "video/mp4", testmedia.MP4(5000, 1000, 150), 5},
		{"text", "txt", "text/plain", []byte("preserved recognized text\n"), 0},
		{"json", "json", "application/json", []byte(`{"preserved":"structured result","items":[1,true]}`), 0},
		{"binary", "bin", "application/octet-stream", []byte{0, 1, 2, 3, 255, 254}, 0},
	} {
		id := uuid.NewString()
		m := primary
		if i != 0 {
			m = archive("artifacts/"+id, fixture.mime, fixture.data)
		}
		artifacts = append(artifacts, core.StoredArtifact{ID: id, ProviderID: "private-provider-artifact-" + id, MediaType: fixture.kind, Format: fixture.format, Duration: fixture.duration, GroupID: group, StorageKey: m.StorageKey, ContentType: m.ContentType, SizeBytes: m.SizeBytes, SHA256: m.SHA256})
	}
	last := archive("last-frame", "image/png", testmedia.PNG512("cycle85-last-frame"))
	artifactJSON, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET result_artifacts=$2,last_frame_storage_key=$3,last_frame_url=$4 WHERE id=$1`, taskID, artifactJSON, last.StorageKey, "/v1/generations/"+taskID+"/last-frame")
	// Keep a populated deletion marker too; the byte survives until physical
	// cleanup, and rollback must preserve both the marker and its indexed object.
	deletedID := uuid.NewString()
	deletedKey := company + "/results/" + deletedID + "/data.bin"
	deletedPayload := []byte("deleted-result-pending-cleanup")
	deletedMedia, err := local.ArchiveSource(storageCtx, company+"/results/"+deletedID, core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(deletedPayload)), ContentType: "application/octet-stream", SizeBytes: int64(len(deletedPayload))})
	if err != nil || deletedMedia.StorageKey != deletedKey {
		t.Fatal(deletedMedia, err)
	}
	payloads[deletedKey] = deletedPayload
	exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,provider,model,status,prompt,storage_key,result_content_type,result_size_bytes,result_sha256,result_deleted_at) VALUES($1,$2,$3,$2,'mock','preserved-deleted','succeeded','Deleted metadata',$4,$5,$6,$7,'2026-10-31T12:00:01Z')`, deletedID, company, identity.SharedOwnerID, deletedKey, deletedMedia.ContentType, deletedMedia.SizeBytes, deletedMedia.SHA256)
	if err := NewStorageSettingsRepository(db).RecordResultObject(storageCtx, deletedKey, deletedID, deletedMedia.SizeBytes); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE result_objects SET deleted_at='2026-10-31T12:00:01Z' WHERE owner_id=$1 AND storage_key=$2`, company, deletedKey)

	verify := func(phase string) {
		t.Helper()
		for key, want := range payloads {
			object, err := local.Open(storageCtx, key)
			if err != nil {
				t.Fatal(phase, key, err)
			}
			got, err := io.ReadAll(object.Reader)
			object.Reader.Close()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("archived bytes changed", phase, key, err)
			}
			sum := sha256.Sum256(got)
			t.Logf("preserved local bytes phase=%s bytes=%d sha256=%x", phase, len(got), sum)
		}
		var raw []byte
		if err := db.QueryRow(`SELECT result_artifacts FROM tasks WHERE id=$1`, taskID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var actual []core.StoredArtifact
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		if len(actual) != len(artifacts) {
			t.Fatal("artifact metadata lost", phase, len(actual))
		}
		for i, a := range actual {
			want := artifacts[i]
			if a != want {
				t.Fatal("artifact field changed", phase, i, a, want)
			}
			data := payloads[a.StorageKey]
			sum := sha256.Sum256(data)
			if a.SizeBytes != int64(len(data)) || a.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("artifact no longer identifies stored bytes", phase, i)
			}
		}
		b, err := resourceRepo.GetBinding(resourceCtx, file.ID)
		if err != nil || b.Kind != "file_resource" || b.RemoteID != file.RemoteID {
			t.Fatal("file binding lost", phase, b, err)
		}
		j, err := resourceRepo.GetJob(resourceCtx, pending.ID)
		if err != nil || j.Kind != "file_resource" || j.Status != providerresources.Running || j.Handle != "private-scoped-file-operation-handle" || j.Warning == "" {
			t.Fatal("uncertain file job lost", phase, j, err)
		}
		if got, err := providerresources.New(resourceRepo, scope).ResolveResourceReferences(resourceCtx, "mock", options.ResourceReferences); err != nil || len(got) != len(options.ResourceSnapshots) {
			t.Fatal("resource snapshots cannot resolve after cycle", phase, got, err)
		}
		var actualCompany, actualUser string
		if err := db.QueryRow(`SELECT organization_id::text,user_id::text FROM workspace_installations WHERE installation_hash=$1`, grant.InstallationHash).Scan(&actualCompany, &actualUser); err != nil || actualCompany != grant.Company || actualUser != user {
			t.Fatal("proof company changed", phase, actualCompany, actualUser, err)
		}
		recovered, err := repo.InstallationIdentity(ctx, user, "https://central.fixture.test", nil)
		if err != nil || !bytes.Equal(recovered, proofCipher) {
			t.Fatal("installation proof ciphertext changed", phase, err)
		}
		grants, err := repo.Grants(ctx, user)
		if err != nil || len(grants) != 2 {
			t.Fatal("company grant lost", phase, grants, err)
		}
		var defaults int
		if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE model='old-writer-model' AND (result_artifacts<>'[]'::jsonb OR result_resource_bindings<>'[]'::jsonb)`).Scan(&defaults); err != nil || defaults != 0 {
			t.Fatal("schema81 inserts lost additive defaults", phase, defaults, err)
		}
	}
	allTables := append(append([]string{}, legacyTables...), "workspace_installations", "remote_installation_identities", "provider_resources", "provider_resource_jobs")
	full := newResourceLegacyProjection(t, db, allTables...)
	verify("85-populated")
	for round := 0; round < 2; round++ {
		prior := full.snapshot(t, db, "85cycle-85-before-down")
		if err := goose.DownTo(db, "migrations", 81); err != nil {
			t.Fatal(err)
		}
		assertVersion(81)
		if full.snapshot(t, db, "85cycle-81-after-down") != prior {
			t.Fatal("85-to81 rollback changed populated fields, rows, billing or proof bindings")
		}
		verify("81-retained")
		assertResourceMetadataSurvivesOldWriter(t, db, seed)
		verify("81-after-old-writer")
		afterWrite := full.snapshot(t, db, "85cycle-81-after-old-writer")
		if err := goose.UpTo(db, "migrations", 85); err != nil {
			t.Fatal(err)
		}
		assertVersion(85)
		if full.snapshot(t, db, "85cycle-85-after-reupgrade") != afterWrite {
			t.Fatal("81-to85 reupgrade changed retained fields or rows")
		}
		verify("85-reupgraded")
	}
}
