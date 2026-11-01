package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

// Opt-in bounded history campaign, using only the disposable test database.
func TestGenerationCursorQueryPlans(t *testing.T) {
	if os.Getenv("GATEWAY_JOURNAL_PERF") != "1" {
		t.Skip("set GATEWAY_JOURNAL_PERF=1 with disposable test services")
	}
	db := auditDatabase(t, true)
	r := NewTaskRepository(db)
	alice, bob, company := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{alice, bob} {
		if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1,$2,$3,'Fixture')`, id, id, id+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Fixture')`, company); err != nil {
		t.Fatal(err)
	}
	c := identity.WithOrganizationRole(identity.WithOwnerID(context.Background(), alice), company, "supermanager")
	project, err := r.CreateProject(c, "Performance fixture")
	if err != nil {
		t.Fatal(err)
	}
	// Wide task payloads make accidental full-history materialization visible.
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,provider_owner_id,storage_owner_id,status,provider,model,media_type,prompt,project_id,created_at)
	SELECT gen_random_uuid(),$1::uuid,CASE WHEN i%2=0 THEN $2::uuid ELSE $3::uuid END,$1::uuid,$1::uuid,'succeeded','mock','test-video','video',repeat('synthetic ',128),CASE WHEN i%3=0 THEN $4::uuid ELSE NULL END,'2026-10-01'::timestamptz-i*interval '1 second' FROM generate_series(1,40000) i`, company, alice, bob, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`VACUUM ANALYZE tasks`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, filter, extra string
		member              bool
	}{
		{"all", "", "", false},
		{"unassigned", core.NoProjectFilter, " AND project_id IS NULL", false},
		{"project", project.ID, " AND project_id='" + project.ID + "'::uuid", false},
		{"member", "", " AND actor_id='" + alice + "'::uuid", true},
		{"member-unassigned", core.NoProjectFilter, " AND actor_id='" + alice + "'::uuid AND project_id IS NULL", true},
		{"member-project", project.ID, " AND actor_id='" + alice + "'::uuid AND project_id='" + project.ID + "'::uuid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := c
			if tc.member {
				ctx = identity.WithOrganizationRole(identity.WithOwnerID(context.Background(), alice), company, "member")
			}
			var p core.GenerationPosition
			where := `owner_id=$1::uuid` + tc.extra
			if err := db.QueryRow(`SELECT created_at,id FROM tasks WHERE `+where+` ORDER BY created_at DESC,id DESC OFFSET 5000 LIMIT 1`, company).Scan(&p.CreatedAt, &p.ID); err != nil {
				t.Fatal(err)
			}
			query := `SELECT ` + taskColumns + ` FROM tasks WHERE ` + where + ` AND (created_at,id)<($2::timestamptz,$3::uuid) ORDER BY created_at DESC,id DESC LIMIT 11`
			var raw []byte
			if err := db.QueryRow(`EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) `+query, company, p.CreatedAt, p.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"Node Type": "Sort"`) || !strings.Contains(string(raw), `"Index Cond"`) {
				t.Fatal("deep page must seek in index, without sorting history", string(raw))
			}
			var plan []map[string]any
			if err := json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			t.Logf("40,000 history rows, deep cursor data plan: %s", raw)
			if err := db.QueryRow(`EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT count(*) FROM tasks WHERE `+where, company).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `Index Only Scan`) {
				t.Fatal("filtered count should use a narrow covering index", string(raw))
			}
			t.Logf("Filtered count plan: %s", raw)
			start := time.Now()
			for i := 0; i < 10; i++ {
				page, err := r.ListGenerationPage(ctx, core.GenerationPageQuery{ProjectID: tc.filter, Limit: 10, Before: &p})
				if err != nil || len(page.Tasks) != 10 || page.Total < 5000 {
					t.Fatal("bounded deep page", len(page.Tasks), page.Total, err)
				}
			}
			t.Logf("Ten deep pages including exact filtered counts: %s (mean %s)", time.Since(start), time.Since(start)/10)
			inventory, err := r.ListGenerationPage(ctx, core.GenerationPageQuery{ProjectID: tc.filter, Limit: 100, Before: &p, VersionsOnly: true, SkipTotal: true})
			if err != nil || len(inventory.Tasks) != 0 || len(inventory.Versions) != 100 || inventory.Total != 0 {
				t.Fatal("inventory must not materialize full tasks or recount history", inventory, err)
			}
			t.Logf("100-record inventory: %d full card payloads; repeated count omitted", len(inventory.Tasks))
		})
	}
}
