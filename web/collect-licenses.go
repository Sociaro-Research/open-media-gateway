//go:build ignore

// Collect the notices for the exact Caddy runtime dependency graph.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type moduleInfo struct {
	Path, Version, Dir string
	Main               bool
}
type moduleNotice struct {
	Module  string            `json:"module"`
	Version string            `json:"version"`
	Notices map[string]string `json:"notices_sha256"`
}

func collectModule(module moduleInfo, destination string) (moduleNotice, error) {
	record := moduleNotice{Module: module.Path, Version: module.Version, Notices: map[string]string{}}
	entries, err := os.ReadDir(module.Dir)
	if err != nil {
		return record, err
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if !strings.HasPrefix(name, "license") && !strings.HasPrefix(name, "copying") && !strings.HasPrefix(name, "notice") {
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(module.Dir, entry.Name()))
		if err != nil {
			return record, err
		}
		folder := filepath.Join(destination, module.Path+"@"+module.Version)
		if err = os.MkdirAll(folder, 0755); err != nil {
			return record, err
		}
		if err = os.WriteFile(filepath.Join(folder, entry.Name()), data, 0644); err != nil {
			return record, err
		}
		digest := sha256.Sum256(data)
		record.Notices[entry.Name()] = hex.EncodeToString(digest[:])
	}
	if len(record.Notices) == 0 {
		return record, fmt.Errorf("review and include missing runtime dependency notice: %s@%s", module.Path, module.Version)
	}
	return record, nil
}

func collect(destination string) error {
	command := exec.Command("go", "list", "-mod=readonly", "-deps", "-json", "github.com/caddyserver/caddy/v2/cmd/caddy")
	data, err := command.Output()
	if err != nil {
		return fmt.Errorf("resolve Caddy runtime dependencies: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	modules := map[string]moduleInfo{}
	for {
		var item struct{ Module *moduleInfo }
		if err = decoder.Decode(&item); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if item.Module != nil && !item.Module.Main {
			modules[item.Module.Path] = *item.Module
		}
	}
	if len(modules) == 0 {
		return fmt.Errorf("Caddy runtime dependency graph is empty")
	}
	if err = os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	records := make([]moduleNotice, 0, len(names))
	for _, name := range names {
		record, e := collectModule(modules[name], destination)
		if e != nil {
			return e
		}
		records = append(records, record)
	}
	license, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "LICENSE"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(destination, "Go-LICENSE"), license, 0644); err != nil {
		return err
	}
	index, err := json.MarshalIndent(struct {
		Package   string         `json:"runtime_package"`
		GoVersion string         `json:"go_version"`
		Modules   []moduleNotice `json:"modules"`
	}{"github.com/caddyserver/caddy/v2/cmd/caddy", runtime.Version(), records}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(destination, "modules.json"), append(index, '\n'), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "README.txt"), []byte("Caddy runtime Go module notices, copied without modification from the exact build dependency graph.\nmodules.json records versions and notice SHA-256 hashes. The project LICENSE/NOTICE and browser JS notices are shipped separately.\n"), 0644)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: collect-licenses DESTINATION")
		os.Exit(2)
	}
	if err := collect(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
