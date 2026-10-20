package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

func TestOrganizationRequestLoggingScrubsCompletedTasks(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "request_logging_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	if _, err = base.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "admin-"+uuid.NewString()+"@example.test", "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "manager-"+uuid.NewString()+"@example.test", "Manager", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), "member-"+uuid.NewString()+"@example.test", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	system := NewSystemRepository(db)
	company, err := system.CreateOrganization(ctx, admin.ID, "Private requests", manager.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = system.SetOrganizationMember(ctx, admin.ID, company.ID, member.ID, "member", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if err = system.SetOrganizationRequestLogging(ctx, member.ID, company.ID, true); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("member changed request logging: %v", err)
	}
	if err = system.SetOrganizationRequestLogging(ctx, manager.ID, company.ID, true); err != nil {
		t.Fatal(err)
	}
	companies, err := system.Organizations(ctx, manager.ID, false)
	if err != nil || len(companies) != 1 || !companies[0].MinimizeRequestLogging {
		t.Fatalf("setting not visible to manager: %v, %v", companies, err)
	}

	taskCtx := identity.WithOrganization(identity.WithUser(ctx, manager), company.ID, true)
	repo := NewTaskRepository(db)
	webhook := "https://private.example.test/hook"
	newTask := func() core.Task {
		now := time.Now().UTC()
		return core.Task{
			ID: uuid.NewString(), OrganizationID: company.ID, ActorID: manager.ID,
			Status: core.TaskQueued, Provider: "mock", Model: "mock-image-v1", MediaType: "image",
			Prompt: "private prompt", WebhookURL: &webhook, CreatedAt: now, UpdatedAt: now,
			Options: core.MediaOptions{ImageURLs: []string{"https://private.example.test/image"}, Instructions: "private instructions", NegativePrompt: "private negative prompt", Resolution: "1024x1024"},
		}
	}
	for _, terminal := range []core.TaskStatus{core.TaskSucceeded, core.TaskFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			task := newTask()
			accepted, created, err := repo.CreateOrGet(taskCtx, task, "", "hash")
			if err != nil || !created || !accepted.MinimizeRequestLogging || !accepted.HadImageInput {
				t.Fatalf("task did not inherit policy: %+v %v %v", accepted, created, err)
			}
			if err = repo.MarkRunning(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			if err = repo.SetProviderResultURL(ctx, task.ID, "https://private.example.test/result"); err != nil {
				t.Fatal(err)
			}
			if err = repo.SetProviderUsage(ctx, task.ID, core.ProviderUsage{Raw: []byte(`{"echo":"private provider usage"}`), Metrics: []core.UsageMetric{{Name: "total_tokens", Unit: "token", Quantity: 3}}}); err != nil {
				t.Fatal(err)
			}
			if terminal == core.TaskFailed {
				err = repo.MarkFailed(ctx, task.ID, "provider echoed private prompt")
			} else {
				err = repo.MarkSucceeded(ctx, task.ID, core.StoredMedia{StorageKey: task.ID + "/result.png", SourceURL: "https://private.example.test/result"}, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.SetProviderUsage(ctx, task.ID, core.ProviderUsage{Raw: []byte(`{"echo":"late private usage"}`), Metrics: []core.UsageMetric{{Name: "total_tokens", Unit: "token", Quantity: 4}}}); err != nil {
				t.Fatal(err)
			}
			stored, err := repo.Get(taskCtx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Prompt != "" || stored.WebhookURL != nil || stored.ProviderResultURL != nil || len(stored.Options.ImageURLs) != 0 || stored.Options.Instructions != "" || stored.Options.NegativePrompt != "" || stored.Options.Resolution != "1024x1024" || !stored.HadImageInput || stored.Usage == nil || len(stored.Usage.Raw) != 0 || len(stored.Usage.Metrics) != 1 {
				t.Fatalf("completed request was not minimized: %+v", stored)
			}
			if terminal == core.TaskFailed && (stored.Error == nil || strings.Contains(*stored.Error, "private")) {
				t.Fatalf("provider error retained request content: %+v", stored.Error)
			}
			var payloads string
			if err = db.QueryRow(`SELECT string_agg(payload::text, ' ') FROM webhook_deliveries WHERE task_id=$1`, task.ID).Scan(&payloads); err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private prompt", "private instructions", "private negative prompt", "private.example.test", "provider echoed", "private provider usage"} {
				if strings.Contains(payloads, private) {
					t.Fatalf("webhook payload retained request content: %s", payloads)
				}
			}
		})
	}

	if err = system.SetOrganizationRequestLogging(ctx, manager.ID, company.ID, false); err != nil {
		t.Fatal(err)
	}
	full := newTask()
	accepted, _, err := repo.CreateOrGet(taskCtx, full, "", "hash")
	if err != nil || accepted.MinimizeRequestLogging {
		t.Fatalf("full logging did not resume for new request: %+v %v", accepted, err)
	}
	if err = repo.MarkFailed(ctx, full.ID, "provider echoed private prompt"); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(taskCtx, full.ID)
	if err != nil || stored.Prompt != full.Prompt || stored.WebhookURL == nil || stored.Options.Instructions != full.Options.Instructions {
		t.Fatalf("default logging changed: %+v %v", stored, err)
	}
}
