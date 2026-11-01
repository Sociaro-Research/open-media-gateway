package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/google/uuid"
)

type logoBrowserService struct {
	serviceStub
	manager *providers.Manager
}

func (s logoBrowserService) ListProviders(ctx context.Context) []core.ProviderDescriptor {
	items, err := s.manager.List(ctx)
	if err != nil {
		return nil
	}
	out := []core.ProviderDescriptor{}
	for _, p := range items {
		out = append(out, core.ProviderDescriptor{ID: p.ID, Name: p.Name, IconURL: p.IconURL, IconMonochrome: true, DefaultModel: "fixture", Models: []core.MediaModel{{ID: "fixture", DisplayName: "Fixture model", MediaType: "image", Resolutions: []string{"640x640", "1024x1024"}, Qualities: []string{"low", "high"}, Backgrounds: []string{"opaque", "transparent"}, Formats: []string{"png", "webp"}}}})
	}
	return out
}

// Executed as a separate owned process by the parent test. Killing/restarting
// this process proves logos do not depend on a manager, browser or Go heap.
func TestProviderLogoBrowserService(t *testing.T) {
	addr := os.Getenv("GATEWAY_LOGO_TEST_LISTEN")
	if addr == "" {
		t.Skip("owned browser-test subprocess only")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("GATEWAY_LOGO_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	people := postgres.NewAuthRepository(db)
	sessions, err := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher("logo-browser-fixture-secret")
	manager := providers.NewManager(postgres.NewProviderSettingsRepository(db), cipher, config.Config{ProviderHTTPTimeout: time.Second})
	h := NewGateway(Dependencies{Service: logoBrowserService{manager: manager}, Health: healthStub{}, Auth: sessions, System: postgres.NewSystemRepository(db), ProviderAdmin: manager, Keys: postgres.NewAPIKeyRepository(db)})
	root, _ := filepath.Abs("../../web/public")
	mux := http.NewServeMux()
	mux.Handle("/ui/", http.FileServer(http.Dir(root)))
	for _, prefix := range []string{"/v1/", "/auth/", "/provider-assets/", "/site.json", "/branding/", "/healthz"} {
		mux.Handle(prefix, h)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "ui/index.html"))
	})
	if err := http.ListenAndServe(addr, mux); err != nil {
		t.Fatal(err)
	}
}

func TestProviderLogoBrowserPersistence(t *testing.T) {
	node := os.Getenv("GATEWAY_LOGO_BROWSER_NODE")
	if node == "" {
		t.Skip("set GATEWAY_LOGO_BROWSER_NODE for real browser/process restart test")
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
	schema := "provider_logo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	people := postgres.NewAuthRepository(db)
	user, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Logo administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if err = people.CreateSession(ctx, hash[:], user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	foreign, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Other logo owner", "")
	if err != nil {
		t.Fatal(err)
	}
	foreignToken := uuid.NewString()
	foreignHash := sha256.Sum256([]byte(foreignToken))
	if err = people.CreateSession(ctx, foreignHash[:], foreign.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewProviderSettingsRepository(db)
	cipher, _ := providerconfig.NewCipher("logo-browser-fixture-secret")
	before := map[string]providerconfig.Record{}
	for _, owner := range []string{user.ID, identity.SharedOwnerID} {
		c := identity.WithResourceOwners(identity.WithOwnerID(ctx, user.ID), owner, owner)
		manager := providers.NewManager(repo, cipher, config.Config{})
		if err = manager.Bootstrap(c); err != nil {
			t.Fatal(err)
		}
		// All models and credentials are synthetic; manual catalogs make no network calls.
		for _, id := range []string{"openai", "sonilo"} {
			record, _ := repo.Get(c, id)
			raw, _ := json.Marshal(providerkit.Credentials{APIKey: "synthetic-logo-test-key", Connection: &providerkit.Connection{ManualCatalog: true}})
			record.CredentialsCiphertext, _ = cipher.Encrypt(raw)
			record.CredentialSource = "console"
			if err = repo.Upsert(c, record); err != nil {
				t.Fatal(err)
			}
		}
		records, _ := repo.List(c)
		for _, record := range records {
			before[owner+":"+record.ProviderID] = record
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	var child *exec.Cmd
	start := func() {
		child = exec.Command(os.Args[0], "-test.run=^TestProviderLogoBrowserService$")
		child.Env = append(os.Environ(), "GATEWAY_LOGO_TEST_DSN="+u.String(), "GATEWAY_LOGO_TEST_LISTEN="+addr)
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			res, err := http.Get("http://" + addr + "/healthz")
			if err == nil {
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode == 200 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("owned logo test service did not become ready")
	}
	stop := func() {
		if child != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			child = nil
		}
	}
	defer stop()
	script, _ := filepath.Abs("../../scripts/ui/provider-logos.cjs")
	for _, phase := range []string{"upload", "restart"} {
		start()
		runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		cmd := exec.CommandContext(runCtx, node, script, "http://"+addr, token, user.ID, phase, foreignToken)
		cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(script)))
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("logo browser %s: %v\n%s", phase, err, output)
		}
		t.Log(strings.TrimSpace(string(output)))
		stop()
	}
	for key, want := range before {
		parts := strings.SplitN(key, ":", 2)
		c := identity.WithResourceOwners(ctx, parts[0], parts[0])
		got, err := repo.Get(c, parts[1])
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("logo mutation changed credentials/settings", key, err)
		}
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM provider_logos`).Scan(&count); err != nil || count != 2 {
		t.Fatal(fmt.Sprintf("expected two persisted private logos, got %d: %v", count, err))
	}
}
