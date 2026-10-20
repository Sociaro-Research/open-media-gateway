package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/google/uuid"
)

func TestProviderConnectionsVersionsAndEncryptedRevisions(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Existing integration helpers isolate each suite by database schema.
	schema := "provider_connections_" + uuid.NewString()
	schema = replaceHyphens(schema)
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewProviderSettingsRepository(db)
	ctx := identity.WithOwnerID(context.Background(), identity.SystemOwnerID)
	record := providerconfig.Record{ProviderID: "openai--" + uuid.NewString(), Name: "Private", CredentialsCiphertext: []byte("encrypted-test-payload"), CredentialSource: "console"}
	if err = repo.Upsert(ctx, record); err != nil {
		t.Fatal(err)
	}
	record, err = repo.Get(ctx, record.ProviderID)
	if err != nil || record.Version != 1 {
		t.Fatal(record, err)
	}
	if err = repo.SaveRevision(ctx, record.ProviderID, "revision-1", record.CredentialsCiphertext); err != nil {
		t.Fatal(err)
	}
	stale := record
	record.Name = "Renamed"
	record.Archived = true
	if err = repo.Upsert(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = repo.Upsert(ctx, stale); err == nil {
		t.Fatal("stale settings overwrote current settings")
	}
	latest, _ := repo.Get(ctx, record.ProviderID)
	if latest.Version != 2 || latest.Name != "Renamed" || !latest.Archived {
		t.Fatal(latest)
	}
	bytes, err := repo.GetRevision(ctx, record.ProviderID, "revision-1")
	if err != nil || string(bytes) != "encrypted-test-payload" {
		t.Fatal(string(bytes), err)
	}
	other := identity.WithOwnerID(ctx, uuid.NewString())
	if _, err = repo.GetRevision(other, record.ProviderID, "revision-1"); err != providerconfig.ErrNotFound {
		t.Fatal("revision leaked across owners", err)
	}
}
func replaceHyphens(s string) string {
	var out []rune
	for _, r := range s {
		if r == '-' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}
