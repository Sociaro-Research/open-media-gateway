package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

type projectLogoAuditFixture struct {
	db                           *sql.DB
	ctx                          context.Context
	company, foreign, actor, key string
	projects                     *TaskRepository
	logos                        *ProviderSettingsRepository
	system                       *SystemRepository
}

func newProjectLogoAuditFixture(t *testing.T) projectLogoAuditFixture {
	t.Helper()
	f := projectLogoAuditFixture{db: auditDatabase(t, true), company: uuid.NewString(), foreign: uuid.NewString(), actor: uuid.NewString(), key: uuid.NewString()}
	for _, id := range []string{f.company, f.foreign} {
		if _, err := f.db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Audit company')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1,$2,$3,'Audit actor')`, f.actor, f.actor, f.actor+"@example.test"); err != nil {
		t.Fatal(err)
	}
	f.ctx = audit.WithCorrelation(identity.WithAPIKeyID(identity.WithOrganizationRole(identity.WithOwnerID(context.Background(), f.actor), f.company, "supermanager"), f.key), "project-logo-correlation")
	f.projects, f.logos, f.system = NewTaskRepository(f.db), NewProviderSettingsRepository(f.db), NewSystemRepository(f.db)
	return f
}

func (f projectLogoAuditFixture) events(t *testing.T, action string) []audit.Event {
	t.Helper()
	filter := allAudit()
	filter.Action = action
	result, err := f.system.AuditEvents(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f projectLogoAuditFixture) attributed(t *testing.T, event audit.Event, kind, target string) {
	t.Helper()
	if event.Scope != "organization" || event.OrganizationID != f.company || event.OrganizationName != "Audit company" || event.ActorID != f.actor || event.ActorName != "Audit actor" || event.ActorType != "user" || event.CallingKeyID != f.key || event.CorrelationID != "project-logo-correlation" || event.Outcome != "success" || event.ObjectType != kind || event.ObjectID != target {
		t.Fatalf("incorrect attribution or scope: %+v", event)
	}
	filter := allAudit()
	filter.OrganizationID = f.foreign
	filter.Action = event.Action
	foreign, err := f.system.AuditEvents(context.Background(), filter)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign audit disclosure: %+v %v", foreign, err)
	}
	if _, err := f.system.AuditEvent(context.Background(), event.ID, f.foreign); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign audit detail: %v", err)
	}
}

func logoAuditPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: shade, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func (f projectLogoAuditFixture) provider(t *testing.T, owner string) context.Context {
	t.Helper()
	ctx := identity.WithResourceOwners(f.ctx, owner, owner)
	if err := f.logos.Upsert(ctx, providerconfig.Record{ProviderID: "mock", Name: "Audit provider", Enabled: true, CredentialsCiphertext: []byte("SECRET-CREDENTIAL-SENTINEL"), CredentialSource: "console"}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestCompanyProjectSuccessAudit(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	p, err := f.projects.CreateProject(f.ctx, "Original")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := f.projects.RenameProject(f.ctx, p.ID, "Renamed", p.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.projects.DeleteProject(f.ctx, p.ID, renamed.Version); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		action, label string
		before, after any
	}{
		{"project_created", "Original", nil, map[string]any{"name": "Original", "version": float64(p.Version)}},
		{"project_renamed", "Renamed", map[string]any{"name": "Original", "version": float64(p.Version)}, map[string]any{"name": "Renamed", "version": float64(renamed.Version)}},
		{"project_deleted", "Renamed", map[string]any{"name": "Renamed", "version": float64(renamed.Version)}, nil},
	} {
		events := f.events(t, expected.action)
		if len(events) != 1 {
			t.Errorf("%s: got %d events, want 1", expected.action, len(events))
			continue
		}
		f.attributed(t, events[0], "project", p.ID)
		if events[0].ObjectName != expected.label {
			t.Fatalf("missing immutable project name: %+v", events[0])
		}
		var details map[string]any
		if err := json.Unmarshal(events[0].Details, &details); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(details, map[string]any{"before": expected.before, "after": expected.after}) {
			t.Fatalf("unexpected project projection: %s", events[0].Details)
		}
	}
}

func TestProviderLogoSuccessAudit(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	ctx := f.provider(t, f.company)
	first, second := logoAuditPNG(t, 17), logoAuditPNG(t, 33)
	for _, data := range [][]byte{first, second} {
		if err := f.logos.SaveLogo(ctx, "mock", data); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.logos.RemoveLogo(ctx, "mock"); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"provider_logo_added", "provider_logo_replaced", "provider_logo_removed"} {
		events := f.events(t, action)
		if len(events) != 1 {
			t.Errorf("%s: got %d events, want 1", action, len(events))
			continue
		}
		f.attributed(t, events[0], "provider", f.company+"/mock")
		if events[0].ObjectName != "Audit provider" {
			t.Fatalf("provider label missing: %+v", events[0])
		}
		var details struct {
			Before, After struct {
				Configured bool `json:"configured"`
				Size       int  `json:"size_bytes"`
			}
		}
		if err := json.Unmarshal(events[0].Details, &details); err != nil {
			t.Fatal(err)
		}
		if action == "provider_logo_added" && (details.Before.Configured || !details.After.Configured || details.After.Size != len(first)) {
			t.Fatal(string(events[0].Details))
		}
		if action == "provider_logo_replaced" && (!details.Before.Configured || !details.After.Configured || details.Before.Size != len(first) || details.After.Size != len(second)) {
			t.Fatal(string(events[0].Details))
		}
		if action == "provider_logo_removed" && (!details.Before.Configured || details.After.Configured || details.Before.Size != len(second)) {
			t.Fatal(string(events[0].Details))
		}
		var safe map[string]map[string]any
		if err := json.Unmarshal(events[0].Details, &safe); err != nil || len(safe) != 2 || len(safe["before"]) != 2 || len(safe["after"]) != 2 {
			t.Fatalf("unexpected logo metadata: %s %v", events[0].Details, err)
		}
	}
	all, err := f.system.AuditEvents(context.Background(), allAudit())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRET-CREDENTIAL-SENTINEL", base64.StdEncoding.EncodeToString(first), base64.StdEncoding.EncodeToString(second)} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("sensitive logo/configuration content in audit")
		}
	}
}

func TestProjectLogoAuditNoOpsIsolationAndFailures(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	p, err := f.projects.CreateProject(f.ctx, "Project")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.projects.CreateProject(f.ctx, "PROJECT")
	if err != nil || retry.ID != p.ID || retry.Version != p.Version {
		t.Fatal(retry, err)
	}
	same, err := f.projects.RenameProject(f.ctx, p.ID, p.Name, p.Version)
	if err != nil || same.Version != p.Version+1 {
		t.Fatal("unchanged rename version changed", same, err)
	}
	if n := len(f.events(t, "project_created")); n != 1 {
		t.Fatalf("retry audit count %d", n)
	}
	if n := len(f.events(t, "project_renamed")); n != 0 {
		t.Fatalf("unchanged rename audited: %d", n)
	}
	if _, err := f.projects.RenameProject(f.ctx, p.ID, "Stale", p.Version); !errors.Is(err, core.ErrProjectConflict) {
		t.Fatal(err)
	}
	for _, other := range []context.Context{
		identity.WithOrganizationRole(f.ctx, f.foreign, "supermanager"),
		identity.WithOrganizationRole(f.ctx, f.company, "admin"),
	} {
		if _, err := f.projects.RenameProject(other, p.ID, "Forbidden", same.Version); err == nil {
			t.Fatal("unauthorized rename")
		}
		if err := f.projects.DeleteProject(other, p.ID, same.Version); err == nil {
			t.Fatal("unauthorized delete")
		}
	}
	if _, err := f.projects.CreateProject(identity.WithOrganizationRole(f.ctx, f.company, "admin"), "Forbidden"); !errors.Is(err, core.ErrProjectForbidden) {
		t.Fatal(err)
	}
	other, err := f.projects.CreateProject(f.ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.projects.RenameProject(f.ctx, p.ID, other.Name, same.Version); !errors.Is(err, core.ErrProjectNameTaken) {
		t.Fatal(err)
	}
	if n := len(f.events(t, "project_renamed")); n != 0 {
		t.Fatalf("failed rename audited: %d", n)
	}
	caseOnly, err := f.projects.RenameProject(f.ctx, p.ID, "PROJECT", same.Version)
	if err != nil || caseOnly.Name != "PROJECT" || caseOnly.Version != same.Version+1 {
		t.Fatal(caseOnly, err)
	}
	if n := len(f.events(t, "project_renamed")); n != 1 {
		t.Fatalf("case-only change missing: %d", n)
	}
	personal := identity.WithPersonalData(identity.WithOwnerID(context.Background(), f.actor))
	pp, err := f.projects.CreateProject(personal, "Private name")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.projects.DeleteProject(personal, pp.ID, pp.Version); err != nil {
		t.Fatal(err)
	}
	if n := len(f.events(t, "project_created")); n != 2 {
		t.Fatalf("personal project widened audit: %d", n)
	}
	ctx := f.provider(t, f.company)
	image := logoAuditPNG(t, 91)
	if err = f.logos.SaveLogo(ctx, "mock", image); err != nil {
		t.Fatal(err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", image); err != nil {
		t.Fatal(err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", []byte("SECRET-INVALID-PNG")); err == nil {
		t.Fatal("invalid logo accepted")
	}
	if err = f.logos.SaveLogo(ctx, "missing", image); err == nil {
		t.Fatal("missing provider accepted")
	}
	if err = f.logos.RemoveLogo(ctx, "missing"); err != nil {
		t.Fatal("missing removal changed", err)
	}
	if err = f.logos.RemoveLogo(identity.WithResourceOwners(ctx, f.foreign, f.foreign), "mock"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.logos.Logo(ctx, "mock"); err != nil || !bytes.Equal(got, image) {
		t.Fatal("foreign deletion changed logo", err)
	}
	for _, scope := range []struct{ owner, scope string }{{f.actor, "personal"}, {identity.SharedOwnerID, "system"}} {
		ownerctx := f.provider(t, scope.owner)
		if err = f.logos.SaveLogo(ownerctx, "mock", image); err != nil {
			t.Fatal(err)
		}
		events := f.events(t, "provider_logo_added")
		if len(events) == 0 || events[0].Scope != scope.scope || events[0].OrganizationID != "" || events[0].ObjectID != scope.owner+"/mock" {
			t.Fatalf("wrong resource-owner scope: %+v", events)
		}
	}
	if n := len(f.events(t, "provider_logo_added")); n != 3 {
		t.Fatalf("logo addition count %d", n)
	}
	if n := len(f.events(t, "provider_logo_replaced")); n != 0 {
		t.Fatalf("identical logo audited: %d", n)
	}
	if err = f.logos.RemoveLogo(ctx, "mock"); err != nil {
		t.Fatal(err)
	}
	if err = f.logos.RemoveLogo(ctx, "mock"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.events(t, "provider_logo_removed")); n != 1 {
		t.Fatalf("removal count %d", n)
	}
}

func TestProjectLogoAuditRollback(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	p, err := f.projects.CreateProject(f.ctx, "Keep project")
	if err != nil {
		t.Fatal(err)
	}
	task := uuid.NewString()
	if _, err = f.db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt,project_id,project_version) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','SECRET-TASK-SENTINEL',$4,7)`, task, f.company, f.actor, p.ID); err != nil {
		t.Fatal(err)
	}
	ctx := f.provider(t, f.company)
	first, second := logoAuditPNG(t, 1), logoAuditPNG(t, 255)
	if err = f.logos.SaveLogo(ctx, "mock", first); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`CREATE FUNCTION reject_project_logo_audit() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'project_%' OR NEW.action LIKE 'provider_logo_%' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_project_logo_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_project_logo_audit()`); err != nil {
		t.Fatal(err)
	}
	// No-op retries do not require a new success record, even when new records
	// cannot be inserted. Unchanged rename still retains its existing CAS step.
	if retry, err := f.projects.CreateProject(f.ctx, p.Name); err != nil || retry.ID != p.ID {
		t.Fatal("create retry attempted audit", retry, err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", first); err != nil {
		t.Fatal("unchanged logo attempted audit", err)
	}
	if err = f.logos.RemoveLogo(ctx, "absent"); err != nil {
		t.Fatal("absent logo attempted audit", err)
	}
	if _, err = f.projects.CreateProject(f.ctx, "Must roll back"); err == nil {
		t.Fatal("create survived failed audit")
	}
	if _, err = f.projects.RenameProject(f.ctx, p.ID, "Must roll back", p.Version); err == nil {
		t.Fatal("rename survived failed audit")
	}
	if err = f.projects.DeleteProject(f.ctx, p.ID, p.Version); err == nil {
		t.Fatal("delete survived failed audit")
	}
	projects, err := f.projects.ListProjects(f.ctx)
	if err != nil || len(projects) != 1 || projects[0] != p {
		t.Fatalf("project mutation escaped rollback: %+v %v", projects, err)
	}
	var project, prompt string
	var version int
	if err = f.db.QueryRow(`SELECT project_id::text,project_version,prompt FROM tasks WHERE id=$1`, task).Scan(&project, &version, &prompt); err != nil || project != p.ID || version != 7 || prompt != "SECRET-TASK-SENTINEL" {
		t.Fatal("task detachment escaped rollback", project, version, prompt, err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", second); err == nil {
		t.Fatal("replace survived failed audit")
	}
	if err = f.logos.RemoveLogo(ctx, "mock"); err == nil {
		t.Fatal("remove survived failed audit")
	}
	if logo, err := f.logos.Logo(ctx, "mock"); err != nil || !bytes.Equal(logo, first) {
		t.Fatal("logo mutation escaped rollback", err)
	}
	if _, err = f.db.Exec(`DELETE FROM provider_logos WHERE owner_id=$1`, f.company); err != nil {
		t.Fatal(err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", second); err == nil {
		t.Fatal("add survived failed audit")
	}
	if _, err = f.logos.Logo(ctx, "mock"); !errors.Is(err, providerconfig.ErrNotFound) {
		t.Fatal("failed add remained", err)
	}
	for _, action := range []string{"project_created", "provider_logo_added"} {
		if n := len(f.events(t, action)); n != 1 {
			t.Fatal("failed audit persisted", action, n)
		}
	}
	for _, action := range []string{"project_renamed", "project_deleted", "provider_logo_replaced", "provider_logo_removed"} {
		if n := len(f.events(t, action)); n != 0 {
			t.Fatal("failed audit persisted", action, n)
		}
	}
}

func TestProjectLogoAuditMutationFailureAndAssignment(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	p, err := f.projects.CreateProject(f.ctx, "Kept")
	if err != nil {
		t.Fatal(err)
	}
	ctx := f.provider(t, f.company)
	first, second := logoAuditPNG(t, 31), logoAuditPNG(t, 63)
	if err = f.logos.SaveLogo(ctx, "mock", first); err != nil {
		t.Fatal(err)
	}
	task := uuid.NewString()
	if _, err = f.db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt) VALUES($1,$2,$3,$2,$2,'succeeded','mock','mock-video-v1','video','Unrelated task content')`, task, f.company, f.actor); err != nil {
		t.Fatal(err)
	}
	before, err := f.system.AuditEvents(context.Background(), allAudit())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.projects.AssignTaskProject(f.ctx, task, p.ID, 0); err != nil {
		t.Fatal(err)
	}
	after, err := f.system.AuditEvents(context.Background(), allAudit())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("assignment added unrelated audit", err)
	}
	if _, err = f.db.Exec(`CREATE FUNCTION reject_test_domain_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic mutation failure'; END $$; CREATE TRIGGER reject_project_change BEFORE INSERT OR UPDATE OR DELETE ON generation_projects FOR EACH ROW EXECUTE FUNCTION reject_test_domain_change(); CREATE TRIGGER reject_logo_change BEFORE INSERT OR UPDATE OR DELETE ON provider_logos FOR EACH ROW EXECUTE FUNCTION reject_test_domain_change()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.projects.CreateProject(f.ctx, "Rejected"); err == nil {
		t.Fatal("create mutation failure ignored")
	}
	if _, err = f.projects.RenameProject(f.ctx, p.ID, "Rejected", p.Version); err == nil {
		t.Fatal("rename mutation failure ignored")
	}
	if err = f.projects.DeleteProject(f.ctx, p.ID, p.Version); err == nil {
		t.Fatal("delete mutation failure ignored")
	}
	if err = f.logos.SaveLogo(ctx, "mock", second); err == nil {
		t.Fatal("replace mutation failure ignored")
	}
	if err = f.logos.RemoveLogo(ctx, "mock"); err == nil {
		t.Fatal("remove mutation failure ignored")
	}
	if _, err = f.db.Exec(`DROP TRIGGER reject_logo_change ON provider_logos; DELETE FROM provider_logos; CREATE TRIGGER reject_logo_change BEFORE INSERT OR UPDATE OR DELETE ON provider_logos FOR EACH ROW EXECUTE FUNCTION reject_test_domain_change()`); err != nil {
		t.Fatal(err)
	}
	if err = f.logos.SaveLogo(ctx, "mock", second); err == nil {
		t.Fatal("add mutation failure ignored")
	}
	after, err = f.system.AuditEvents(context.Background(), allAudit())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed mutation left success event", err)
	}
	var linked string
	var version int
	if err = f.db.QueryRow(`SELECT project_id::text,project_version FROM tasks WHERE id=$1`, task).Scan(&linked, &version); err != nil || linked != p.ID || version != 1 {
		t.Fatal("failed delete detached task", linked, version, err)
	}
}

func TestProjectLogoAuditConcurrentNoOps(t *testing.T) {
	f := newProjectLogoAuditFixture(t)
	ctx := f.provider(t, f.company)
	data := logoAuditPNG(t, 77)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.projects.CreateProject(f.ctx, "Concurrent"); err != nil {
				t.Error(err)
			}
			if err := f.logos.SaveLogo(ctx, "mock", data); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(f.events(t, "project_created")); n != 1 {
		t.Fatalf("concurrent create count %d", n)
	}
	if n := len(f.events(t, "provider_logo_added")); n != 1 {
		t.Fatalf("concurrent logo add count %d", n)
	}
	if n := len(f.events(t, "provider_logo_replaced")); n != 0 {
		t.Fatalf("concurrent identical replacements %d", n)
	}
}
