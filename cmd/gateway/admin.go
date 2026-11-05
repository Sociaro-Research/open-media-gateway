package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

func adminCommand(args []string) error {
	return adminCommandWithDatabase(args, postgres.Open, postgres.Migrate)
}

func adminCommandWithDatabase(args []string, open func(context.Context, string) (*sql.DB, error), migrate func(*sql.DB) error) error {
	opts, err := parseAdminFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		// PostgreSQL parse/dial errors can include the DSN and credentials.
		return fmt.Errorf("gateway admin %s: cannot initialize PostgreSQL; check DATABASE_URL syntax (postgres://USER:PASSWORD@HOST:5432/DATABASE), credentials, server availability and database access", args[0])
	}
	defer db.Close()
	if args[0] == "setup-code" {
		if len(os.Getenv("SESSION_SIGNING_SECRET")) < 32 {
			return errors.New("configure SESSION_SIGNING_SECRET and AUTH_PUBLIC_URL before browser setup")
		}
		if err = migrate(db); err != nil {
			return err
		}
		secret := os.Getenv("PROVIDER_CREDENTIALS_ENCRYPTION_KEY")
		if secret == "" {
			secret = os.Getenv("MEDIA_URL_SIGNING_SECRET")
		}
		cipher, err := providerconfig.NewCipher(secret)
		if err != nil {
			return err
		}
		code, err := auth.InstallationSetupCode(ctx, postgres.NewAuthRepository(db), cipher)
		if err != nil {
			return err
		}
		fmt.Println(code)
		return nil
	}
	repo := postgres.NewSystemRepository(db)
	if args[0] == "catalog-document" {
		text, err := postgres.NewCatalogRepository(db).Document(ctx, opts.hash)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"hash": opts.hash, "text": text})
	}
	if args[0] == "catalog-warnings" {
		catalog := postgres.NewCatalogRepository(db)
		schedules, err := catalog.Schedules(ctx)
		if err != nil {
			return err
		}
		result := map[string]any{}
		for _, s := range schedules {
			if opts.provider != "" && s.Provider != opts.provider {
				continue
			}
			state, err := catalog.State(ctx, s.Provider)
			if err != nil {
				return err
			}
			result[s.Provider] = map[string]any{"version": state.Version, "last_check": s.LastCheck, "warnings": state.Warnings, "sources": state.Observations}
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	if args[0] == "init" || args[0] == "issue-key" {
		if err = migrate(db); err != nil {
			return err
		}
		var result any
		if args[0] == "init" {
			key, secret, e := repo.InitializeAdministrator(ctx, opts.email, opts.name, opts.keyName)
			if e != nil {
				return e
			}
			result = map[string]any{"key": key, "secret": secret}
		} else {
			key, secret, e := repo.IssueAdministratorKey(ctx, opts.userID, opts.keyName)
			if e != nil {
				return e
			}
			result = map[string]any{"key": key, "secret": secret}
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if args[0] == "users" {
		users, err := repo.Users(ctx, opts.search, false, 100, 0)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(users)
	}
	if err = repo.SetAdmin(ctx, "", opts.userID, args[0] != "revoke", args[0] == "bootstrap"); err != nil {
		return err
	}
	fmt.Println("Administrator role updated for", opts.userID)
	return nil
}
