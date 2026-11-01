package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

func TestOrganizationSafeMethodsPreservePostgresState(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "method_safety_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
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
	people, sys := postgres.NewAuthRepository(db), postgres.NewSystemRepository(db)
	admin, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = sys.SetAdmin(ctx, "", admin.ID, true, true); err != nil {
		t.Fatal(err)
	}
	company, err := sys.CreateOrganization(ctx, admin.ID, "Original", owner.ID, uuid.NewString()+".example.test")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := sys.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.SharedResources = true
	if _, err = sys.SaveSettings(ctx, admin.ID, settings); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if err = people.CreateSession(ctx, hash[:], owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: manager, System: sys, Keys: postgres.NewAPIKeyRepository(db)})
	server := httptest.NewServer(h)
	defer server.Close()
	send := func(method, path, body, key string) (int, http.Header, []byte) {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if key == "" {
			r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: token})
		} else {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, data
	}
	keys := []string{}
	for _, scopes := range []string{`["generation"]`, `["generation","organizations:manage"]`} {
		status, _, data := send("POST", "/v1/api-keys", `{"name":"Method fixture","organization_id":"`+company.ID+`","scopes":`+scopes+`}`, "")
		var key struct {
			Secret string `json:"secret"`
		}
		if status != 201 || json.Unmarshal(data, &key) != nil || key.Secret == "" {
			t.Fatalf("key setup: %d", status)
		}
		keys = append(keys, key.Secret)
	}
	state := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, `SELECT name || ':' || COALESCE(default_weekly_limit_micros::text,'null') FROM organizations WHERE id=$1`, company.ID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := state()
	path := "/v1/organizations/" + company.ID
	body := `{"name":"Changed","default_weekly_limit_usd":10}`
	for _, key := range keys {
		for _, method := range []string{"GET", "HEAD"} {
			for _, payload := range []string{body, ""} {
				status, headers, data := send(method, path, payload, key)
				want := 200
				if key == keys[0] && method == "HEAD" {
					want = 403
				}
				if status != want || state() != before {
					t.Fatalf("%s status=%d database=%q before=%q", method, status, state(), before)
				}
				if method == "HEAD" && (len(data) != 0 || headers.Get("Content-Length") == "") {
					t.Fatal("HEAD protocol response", headers, len(data))
				}
			}
		}
	}
	if status, _, _ := send("PUT", path, body, keys[0]); status != 403 || state() != before {
		t.Fatal("generation key changed database", status)
	}
	if status, _, data := send("PUT", path, body, keys[1]); status != 204 || state() != "Changed:10000000" {
		t.Fatalf("management PUT: %d %s state=%s", status, data, state())
	}
}
