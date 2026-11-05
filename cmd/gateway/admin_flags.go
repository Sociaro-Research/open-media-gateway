package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type adminFlags struct {
	userID, search, email, name, keyName, hash, provider string
}

// Keep argument errors local: no database initialization, migrations or supplied
// values belong in this preflight. Repository checks still enforce authorization.
func parseAdminFlags(args []string) (adminFlags, error) {
	var opts adminFlags
	if err := validateAdminCommand(args); err != nil {
		return opts, err
	}
	command := args[0]
	flags := flag.NewFlagSet("admin "+command, flag.ContinueOnError)
	// The flag package echoes unknown option names. They may contain secrets.
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.userID, "user-id", "", "existing user UUID")
	flags.StringVar(&opts.search, "search", "", "name, email or ID")
	flags.StringVar(&opts.email, "email", "", "new administrator email")
	flags.StringVar(&opts.name, "name", "Administrator", "display name")
	flags.StringVar(&opts.keyName, "key-name", "Server administrator", "API key name")
	flags.StringVar(&opts.hash, "hash", "", "stored catalogue document SHA-256")
	flags.StringVar(&opts.provider, "provider", "", "catalogue provider connection ID (empty means all)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, adminCommandHint(command))
			return opts, err
		}
		// Only include a flag name after checking it against the fixed vocabulary.
		const missing = "flag needs an argument: -"
		if name, ok := strings.CutPrefix(err.Error(), missing); ok && flags.Lookup(name) != nil {
			return opts, fmt.Errorf("gateway admin %s: --%s requires a value; %s", command, name, adminCommandHint(command))
		}
		return opts, fmt.Errorf("gateway admin %s: flag provided but not defined; %s", command, adminCommandHint(command))
	}
	if flags.NArg() != 0 {
		return opts, fmt.Errorf("gateway admin %s: unexpected positional argument #1; use named flags; %s", command, adminCommandHint(command))
	}
	var unsupported string
	flags.Visit(func(f *flag.Flag) {
		if unsupported == "" && !adminFlagAllowed(command, f.Name) {
			unsupported = f.Name
		}
	})
	if unsupported != "" {
		return opts, fmt.Errorf("gateway admin %s: --%s is not supported for this command; %s", command, unsupported, adminCommandHint(command))
	}
	switch command {
	case "bootstrap", "grant", "revoke", "issue-key":
		if _, err := uuid.Parse(opts.userID); err != nil {
			return opts, fmt.Errorf("gateway admin %s: --user-id is required and must be an existing user's UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx); %s", command, adminCommandHint(command))
		}
	case "init":
		// Match InitializeAdministrator's normalization and email acceptance rules.
		email := strings.ToLower(strings.TrimSpace(opts.email))
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
			return opts, fmt.Errorf("gateway admin init: --email is required and must be a plain email address (user@example.com); %s", adminCommandHint(command))
		}
	case "catalog-document":
		if hash, err := hex.DecodeString(opts.hash); err != nil || len(hash) != 32 {
			return opts, fmt.Errorf("gateway admin catalog-document: --hash is required and must be a SHA-256 digest (64 hexadecimal characters); %s", adminCommandHint(command))
		}
	}
	if command == "init" || command == "issue-key" {
		name := strings.TrimSpace(opts.keyName)
		if name == "" || utf8.RuneCountInString(name) > 80 {
			return opts, fmt.Errorf("gateway admin %s: --key-name must contain 1 to 80 characters after trimming whitespace; example: --key-name 'Server administrator'; %s", command, adminCommandHint(command))
		}
	}
	return opts, nil
}

func adminFlagAllowed(command, name string) bool {
	switch command {
	case "init":
		return name == "email" || name == "name" || name == "key-name"
	case "issue-key":
		return name == "user-id" || name == "key-name"
	case "bootstrap", "grant", "revoke":
		return name == "user-id"
	case "users":
		return name == "search"
	case "catalog-document":
		return name == "hash"
	case "catalog-warnings":
		return name == "provider"
	default:
		return false
	}
}

func adminCommandHint(command string) string {
	switch command {
	case "init":
		return "use --email EMAIL [--name NAME] [--key-name NAME]; example: gateway admin init --email admin@example.com"
	case "bootstrap", "grant", "revoke", "issue-key":
		options := "--user-id UUID"
		if command == "issue-key" {
			options += " [--key-name NAME]"
		}
		return "use " + options + "; find the existing user's ID with gateway admin users; example: gateway admin " + command + " --user-id 123e4567-e89b-12d3-a456-426614174000 (replace with the existing user's UUID)"
	case "catalog-document":
		return "use --hash SHA256; copy a stored document hash from gateway admin catalog-warnings; example: gateway admin catalog-document --hash " + strings.Repeat("0", 64) + " (replace with the stored document's hash)"
	case "catalog-warnings":
		return "use [--provider ID]; example: gateway admin catalog-warnings"
	case "users":
		return "use [--search TEXT]; example: gateway admin users --search admin@example.com"
	default:
		return "no flags are required; example: gateway admin setup-code"
	}
}
