package main

import (
	"fmt"
	"io"

	"github.com/Sociaro-Research/open-media-gateway/internal/buildinfo"
)

const cliUsage = `Usage: gateway [--help | --version | healthcheck | admin COMMAND [OPTIONS]]

With no arguments, start the gateway server using its environment configuration.
  --help, -h, help       Show this help without connecting to services.
  --version, version    Show available build identity without connecting to services.
  healthcheck           Check the running gateway's local health endpoint.
  admin                 Run an administrative command (gateway admin --help).
`

const adminUsage = "usage: gateway admin setup-code | init --email EMAIL [--name NAME] | users [--search EMAIL] | bootstrap|grant|revoke|issue-key --user-id UUID | catalog-warnings [--provider ID] | catalog-document --hash SHA256"

type cliHandlers struct {
	serve  func() error
	admin  func([]string) error
	health func() error
}

// Only the explicit server and service commands may cross the startup boundary.
func executeCLI(args []string, out io.Writer, handlers cliHandlers) error {
	if len(args) == 0 {
		return handlers.serve()
	}
	switch args[0] {
	case "--help", "-h", "help", "--version", "version", "healthcheck":
		if len(args) != 1 {
			return fmt.Errorf("unexpected arguments for %s; use gateway --help", args[0])
		}
		switch args[0] {
		case "--help", "-h", "help":
			_, err := io.WriteString(out, cliUsage)
			return err
		case "--version", "version":
			_, err := fmt.Fprintln(out, buildinfo.Identity("gateway"))
			return err
		default:
			return handlers.health()
		}
	case "admin":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			_, err := fmt.Fprintln(out, adminUsage)
			return err
		}
		if err := validateAdminCommand(args[1:]); err != nil {
			return err
		}
		return handlers.admin(args[1:])
	default:
		return fmt.Errorf("unknown command %q; use gateway --help", args[0])
	}
}

func validateAdminCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", adminUsage)
	}
	switch args[0] {
	case "setup-code", "init", "users", "bootstrap", "grant", "revoke", "issue-key", "catalog-warnings", "catalog-document":
		return nil
	default:
		return fmt.Errorf("unknown admin command; %s; example: gateway admin users", adminUsage)
	}
}
