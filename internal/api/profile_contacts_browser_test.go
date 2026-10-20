package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/google/uuid"
)

// CI explicitly enables this check with Node/Playwright and isolated PostgreSQL.
// The browser uses the real profile/auth/company handlers and repositories.
func TestProfileContactsBrowserWorkflow(t *testing.T) {
	node := os.Getenv("GATEWAY_PROFILE_BROWSER_NODE")
	if node == "" {
		t.Skip("enable the real browser workflow with GATEWAY_PROFILE_BROWSER_NODE")
	}
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required for the browser workflow")
	}
	ctx := context.Background()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "profile_browser_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := base.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop browser test schema %s: %v", schema, err)
		}
	}()
	schemaCtx, cancelSchema := context.WithTimeout(ctx, 5*time.Second)
	_, err = base.ExecContext(schemaCtx, `CREATE SCHEMA `+schema)
	cancelSchema()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	people := postgres.NewAuthRepository(db)
	users := make([]identity.User, 3)
	tokens := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, name := range []string{"Contact editor", "Company viewer", "Fixture administrator"} {
		users[i], err = people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(tokens[i]))
		if err = people.CreateSession(ctx, hash[:], users[i].ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	sys := postgres.NewSystemRepository(db)
	if err = sys.SetAdmin(ctx, "", users[2].ID, true, true); err != nil {
		t.Fatal(err)
	}
	company, err := sys.CreateOrganization(ctx, users[2].ID, "Contact workflow company", users[1].ID, "profile-browser.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = sys.SetOrganizationMember(ctx, users[2].ID, company.ID, users[0].ID, org.MemberRole, nil, true, false); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGateway(Dependencies{Service: serviceStub{}, Health: healthStub{}, Auth: manager, System: sys})
	root, err := filepath.Abs("../../web/public")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ui/", http.FileServer(http.Dir(root)))
	for _, prefix := range []string{"/v1/", "/auth/", "/site.json"} {
		mux.Handle(prefix, handler)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/index.html"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	script, err := filepath.Abs("../../scripts/ui/profile-contacts-server.cjs")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(runCtx, node, script, server.URL, tokens[0], tokens[1], company.ID).CombinedOutput()
	if err != nil {
		t.Fatalf("profile browser workflow: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	// An independent repository read confirms the final browser write reached SQL.
	hash := sha256.Sum256([]byte(tokens[0]))
	saved, err := postgres.NewAuthRepository(db).UserBySession(ctx, hash[:], time.Now())
	if err != nil || len(saved.Contacts) != 1 || saved.Contacts[0].Label != "Saved contact" || saved.Contacts[0].URL != "mailto:editor@example.test" {
		t.Fatalf("persisted browser draft: %+v, %v", saved.Contacts, err)
	}
}
