package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"flag"
	"strings"
	"testing"
)

const adminTestUUID = "123e4567-e89b-12d3-a456-426614174000"

func TestAdminPreflightRejectsBeforeDatabase(t *testing.T) {
	const private = "synthetic-private-value"
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"missing command", nil, []string{"usage: gateway admin"}},
		{"unknown command", []string{private}, []string{"unknown admin command", "example: gateway admin users"}},
		{"missing email", []string{"init"}, []string{"admin init", "--email", "plain email address", "example:"}},
		{"blank email", []string{"init", "--email", " "}, []string{"--email", "plain email address"}},
		{"invalid email", []string{"init", "--email", private}, []string{"--email", "plain email address"}},
		{"display email", []string{"init", "--email", private + " <admin@example.com>"}, []string{"--email", "plain email address"}},
		{"newline email", []string{"init", "--email", "admin\r\n@example.com"}, []string{"--email", "plain email address"}},
		{"long email", []string{"init", "--email", strings.Repeat("a", 243) + "@example.com"}, []string{"--email", "plain email address"}},
		{"missing email value", []string{"init", "--email"}, []string{"--email requires a value", "example:"}},
		{"missing hash", []string{"catalog-document"}, []string{"--hash", "64 hexadecimal characters", "example:"}},
		{"short hash", []string{"catalog-document", "--hash", "1234"}, []string{"--hash", "64 hexadecimal characters"}},
		{"nonhex hash", []string{"catalog-document", "--hash", strings.Repeat("z", 64)}, []string{"--hash", "64 hexadecimal characters"}},
		{"missing hash value", []string{"catalog-document", "--hash"}, []string{"--hash requires a value"}},
		{"unknown option", []string{"users", "--" + private}, []string{"flag provided but not defined", "--search TEXT", "example:"}},
		{"unknown option value", []string{"users", "--unknown=" + private}, []string{"flag provided but not defined", "--search TEXT"}},
		{"unexpected argument", []string{"users", private}, []string{"positional argument #1", "named flags", "--search TEXT"}},
		{"inappropriate flag", []string{"users", "--email", private}, []string{"--email is not supported", "--search TEXT"}},
		{"setup flag", []string{"setup-code", "--user-id", private}, []string{"--user-id is not supported", "gateway admin setup-code"}},
		{"blank init key name", []string{"init", "--email", "admin@example.com", "--key-name", " "}, []string{"--key-name", "1 to 80 characters", "example:"}},
		{"long recovery key name", []string{"issue-key", "--user-id", adminTestUUID, "--key-name", strings.Repeat("a", 81)}, []string{"--key-name", "1 to 80 characters"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertAdminPreflightError(t, tc.args, tc.want, private)
		})
	}
	for _, command := range []string{"bootstrap", "grant", "revoke", "issue-key"} {
		for _, value := range []string{"", " ", private} {
			t.Run(command+"/"+value, func(t *testing.T) {
				args := []string{command}
				if value != "" {
					args = append(args, "--user-id", value)
				}
				assertAdminPreflightError(t, args, []string{"admin " + command, "--user-id", "UUID", "example:"}, private)
			})
		}
		t.Run(command+"/missing value", func(t *testing.T) {
			assertAdminPreflightError(t, []string{command, "--user-id"}, []string{"--user-id requires a value", "example:"}, private)
		})
	}
}

func assertAdminPreflightError(t *testing.T, args, want []string, private string) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://"+private+":%zz")
	var opens, migrations int
	err := adminCommandWithDatabase(args, func(context.Context, string) (*sql.DB, error) {
		opens++
		return nil, errors.New("unexpected database initialization")
	}, func(*sql.DB) error {
		migrations++
		return errors.New("unexpected migrations")
	})
	if err == nil || opens != 0 || migrations != 0 {
		t.Fatalf("err=%v database initializations=%d migrations=%d", err, opens, migrations)
	}
	for _, text := range want {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
	if strings.Contains(err.Error(), private) || strings.Contains(err.Error(), "postgres://") {
		t.Fatal("preflight error exposed supplied data")
	}
}

func TestAdminValidArgumentsReachDatabaseBoundary(t *testing.T) {
	for _, args := range [][]string{
		{"setup-code"}, {"init", "--email", "admin@example.com"},
		{"init", "--email", " ADMIN@EXAMPLE.COM ", "--name", "Admin", "--key-name", strings.Repeat("界", 80)},
		{"users"}, {"users", "--search", "admin@example.com"},
		{"bootstrap", "--user-id", adminTestUUID}, {"grant", "--user-id", adminTestUUID},
		{"revoke", "--user-id", adminTestUUID}, {"issue-key", "--user-id", adminTestUUID, "--key-name", "Recovery"},
		// uuid.Parse already accepts these forms; preflight must preserve them.
		{"issue-key", "--user-id", "urn:uuid:" + adminTestUUID},
		{"issue-key", "--user-id", strings.ReplaceAll(adminTestUUID, "-", "")},
		{"catalog-warnings"}, {"catalog-warnings", "--provider", "example-connection"},
		{"catalog-document", "--hash", strings.Repeat("ab", 32)},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			const private = "synthetic-private-database-error"
			var opens int
			err := adminCommandWithDatabase(args, func(context.Context, string) (*sql.DB, error) {
				opens++
				return nil, errors.New(private)
			}, func(*sql.DB) error {
				t.Fatal("migration after failed database initialization")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "cannot initialize PostgreSQL") || !strings.Contains(err.Error(), "DATABASE_URL") || strings.Contains(err.Error(), private) || opens != 1 {
				t.Fatalf("err=%v database initializations=%d", err, opens)
			}
		})
	}
}

func TestAdminParsedValuesPreserved(t *testing.T) {
	opts, err := parseAdminFlags([]string{"init", "--email", " ADMIN@EXAMPLE.COM ", "--name", " Example ", "--key-name", " Recovery "})
	if err != nil || opts.email != " ADMIN@EXAMPLE.COM " || opts.name != " Example " || opts.keyName != " Recovery " {
		t.Fatalf("values changed before repository validation: opts=%+v err=%v", opts, err)
	}
	opts, err = parseAdminFlags([]string{"init", "--email", "admin@example.com"})
	if err != nil || opts.name != "Administrator" || opts.keyName != "Server administrator" {
		t.Fatalf("defaults changed: opts=%+v err=%v", opts, err)
	}
}

func TestAdminMalformedDatabaseURLIsNotReflected(t *testing.T) {
	// Invalid URL escapes are rejected by pgx before dialing any database.
	const private = "synthetic-private-dsn"
	t.Setenv("DATABASE_URL", "postgres://"+private+":%zz")
	err := adminCommand([]string{"users"})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL syntax") || strings.Contains(err.Error(), private) || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("unsafe or unhelpful database initialization error: %v", err)
	}
}

// No driver connection can be created, even if dispatch accidentally starts a
// query. A migration sentinel stops valid write commands before any operation.
type adminNoConnection struct{ t *testing.T }

func (c adminNoConnection) Connect(context.Context) (driver.Conn, error) {
	c.t.Fatal("unexpected database driver connection")
	return nil, errors.New("unexpected database driver connection")
}
func (c adminNoConnection) Driver() driver.Driver { return nil }

func TestAdminMigrationDispatchPreserved(t *testing.T) {
	t.Setenv("SESSION_SIGNING_SECRET", strings.Repeat("s", 32))
	for _, args := range [][]string{{"setup-code"}, {"init", "--email", "admin@example.com"}, {"issue-key", "--user-id", adminTestUUID}} {
		t.Run(args[0], func(t *testing.T) {
			want := errors.New("migration boundary sentinel")
			var opens, migrations int
			err := adminCommandWithDatabase(args, func(context.Context, string) (*sql.DB, error) {
				opens++
				return sql.OpenDB(adminNoConnection{t}), nil
			}, func(*sql.DB) error {
				migrations++
				return want
			})
			if !errors.Is(err, want) || opens != 1 || migrations != 1 {
				t.Fatalf("err=%v database initializations=%d migrations=%d", err, opens, migrations)
			}
		})
	}
}

func TestAdminSubcommandHelpBeforeDatabase(t *testing.T) {
	for _, command := range []string{"setup-code", "init", "users", "bootstrap", "grant", "revoke", "issue-key", "catalog-warnings", "catalog-document"} {
		t.Run(command, func(t *testing.T) {
			err := adminCommandWithDatabase([]string{command, "--help"}, func(context.Context, string) (*sql.DB, error) {
				t.Fatal("help initialized database")
				return nil, nil
			}, func(*sql.DB) error {
				t.Fatal("help ran migrations")
				return nil
			})
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("subcommand help exit semantics changed: %v", err)
			}
		})
	}
}
