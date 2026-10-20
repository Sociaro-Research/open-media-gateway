package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

type profileNameFaultRepository struct {
	*postgres.AuthRepository
	failed atomic.Bool
}

func (r *profileNameFaultRepository) UpdateProfile(ctx context.Context, id string, in auth.ProfileUpdate) (identity.User, error) {
	// One repository failure exercises the real handler's 500 and identical retry.
	if in.Name != nil && *in.Name == "Retry after outage" && !r.failed.Swap(true) {
		return identity.User{}, errors.New("injected profile repository failure")
	}
	return r.AuthRepository.UpdateProfile(ctx, id, in)
}

func TestProfileNameBrowserReconciliation(t *testing.T) {
	node := os.Getenv("GATEWAY_PROFILE_NAME_BROWSER_NODE")
	if node == "" {
		t.Skip("enable browser reconciliation with GATEWAY_PROFILE_NAME_BROWSER_NODE")
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
	schema := "profile_name_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	user, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if err = people.CreateSession(ctx, hash[:], user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(ctx, &profileNameFaultRepository{AuthRepository: people}, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGateway(Dependencies{Service: serviceStub{}, Health: healthStub{}, Auth: manager, System: postgres.NewSystemRepository(db)})
	root, err := filepath.Abs("../../web/public")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ui/", http.FileServer(http.Dir(root)))
	for _, prefix := range []string{"/v1/", "/auth/", "/site.json"} {
		mux.Handle(prefix, handler)
	}
	// Test-only independent SQL measurement, never shipped in the gateway.
	mux.HandleFunc("GET /__test/name", func(w http.ResponseWriter, r *http.Request) {
		var name string
		if err := db.QueryRowContext(r.Context(), `SELECT display_name FROM users WHERE id=$1`, user.ID).Scan(&name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(name)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/index.html"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	script, err := filepath.Abs("../../scripts/ui/profile-name-server.cjs")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(runCtx, node, script, server.URL, token).CombinedOutput()
	if err != nil {
		t.Fatalf("profile name browser: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	var saved string
	if err := db.QueryRow(`SELECT display_name FROM users WHERE id=$1`, user.ID).Scan(&saved); err != nil || saved != "Final name" {
		t.Fatalf("final persisted name: %q, %v", saved, err)
	}
}
