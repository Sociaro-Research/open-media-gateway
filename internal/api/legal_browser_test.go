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

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

// Opt in with Node/Playwright and disposable PostgreSQL. Browser writes use the
// real session, permission, company and legal handlers/repositories; no API mocks.
func TestLegalDocumentsBrowserWorkflow(t *testing.T) {
	node := os.Getenv("GATEWAY_LEGAL_BROWSER_NODE")
	if node == "" {
		t.Skip("enable with GATEWAY_LEGAL_BROWSER_NODE")
	}
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "legal_browser_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
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
	sys := postgres.NewSystemRepository(db)
	tokens := []string{uuid.NewString(), uuid.NewString()}
	for i, name := range []string{"Legal administrator", "Ordinary member"} {
		user, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256([]byte(tokens[i]))
		if e = people.CreateSession(ctx, hash[:], user.ID, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			if e = sys.SetAdmin(ctx, "", user.ID, true, true); e != nil {
				t.Fatal(e)
			}
		}
	}
	manager, err := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGateway(Dependencies{Service: serviceStub{}, Health: healthStub{}, Auth: manager, System: sys, Payments: payments.New(sys, nil, "http://127.0.0.1", nil)})
	root, err := filepath.Abs("../../web/public")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ui/", http.FileServer(http.Dir(root)))
	for _, prefix := range []string{"/v1/", "/auth/", "/legal/", "/site.json"} {
		mux.Handle(prefix, handler)
	}
	mux.HandleFunc("/privacy", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/privacy.html"))
	})
	mux.HandleFunc("/terms", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/terms.html"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/index.html"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	script, err := filepath.Abs("../../scripts/ui/legal-documents-server.cjs")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(runCtx, node, script, server.URL, tokens[0], tokens[1]).CombinedOutput()
	if err != nil {
		t.Fatalf("legal browser workflow: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	privacy, err := sys.LegalDocument(ctx, "privacy")
	if err != nil || privacy.Text != "" || privacy.Version < 4 {
		t.Fatal("browser reset not persisted", privacy, err)
	}
	terms, err := sys.LegalDocument(ctx, "terms")
	if err != nil || terms.Text != "Terms from browser" {
		t.Fatal("independent terms write not persisted", terms, err)
	}
}
