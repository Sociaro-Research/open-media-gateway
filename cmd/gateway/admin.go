package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/google/uuid"
	"os"
	"time"
)

func adminCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gateway admin setup-code | init --email EMAIL [--name NAME] | users [--search EMAIL] | bootstrap|grant|revoke|issue-key --user-id UUID | catalog-warnings [--provider ID] | catalog-document --hash SHA256")
	}
	flags := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	id := flags.String("user-id", "", "existing user ID")
	search := flags.String("search", "", "name, email or ID")
	email := flags.String("email", "", "new administrator email")
	name := flags.String("name", "Administrator", "display name")
	keyName := flags.String("key-name", "Server administrator", "API key name")
	hash := flags.String("hash", "", "stored catalogue document SHA-256")
	provider := flags.String("provider", "", "catalogue provider connection ID (empty means all)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	if args[0] == "setup-code" {
		if len(os.Getenv("SESSION_SIGNING_SECRET")) < 32 {
			return errors.New("configure SESSION_SIGNING_SECRET and AUTH_PUBLIC_URL before browser setup")
		}
		if err = postgres.Migrate(db); err != nil {
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
		text, err := postgres.NewCatalogRepository(db).Document(ctx, *hash)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"hash": *hash, "text": text})
	}
	if args[0] == "catalog-warnings" {
		catalog := postgres.NewCatalogRepository(db)
		schedules, err := catalog.Schedules(ctx)
		if err != nil {
			return err
		}
		result := map[string]any{}
		for _, s := range schedules {
			if *provider != "" && s.Provider != *provider {
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
		if err = postgres.Migrate(db); err != nil {
			return err
		}
		if args[0] == "issue-key" {
			if _, err = uuid.Parse(*id); err != nil {
				return errors.New("provide an administrator UUID with --user-id")
			}
		}
		var result any
		if args[0] == "init" {
			key, secret, e := repo.InitializeAdministrator(ctx, *email, *name, *keyName)
			if e != nil {
				return e
			}
			result = map[string]any{"key": key, "secret": secret}
		} else {
			key, secret, e := repo.IssueAdministratorKey(ctx, *id, *keyName)
			if e != nil {
				return e
			}
			result = map[string]any{"key": key, "secret": secret}
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if args[0] == "users" {
		users, err := repo.Users(ctx, *search, false, 100, 0)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(users)
	}
	if args[0] != "bootstrap" && args[0] != "grant" && args[0] != "revoke" {
		return errors.New("unknown admin command")
	}
	if _, err = uuid.Parse(*id); err != nil {
		return errors.New("provide an existing user's UUID with --user-id")
	}
	if err = repo.SetAdmin(ctx, "", *id, args[0] != "revoke", args[0] == "bootstrap"); err != nil {
		return err
	}
	fmt.Println("Administrator role updated for", *id)
	return nil
}
