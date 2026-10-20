// Check package functions in a shipped Linux Go binary, including stripped builds.
// govulncheck falls back to module-level findings when its symbol table is absent.
package main

import (
	"debug/elf"
	"debug/gosym"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: check-go-binary BINARY FORBIDDEN_PACKAGE [FORBIDDEN_PACKAGE...]")
		os.Exit(2)
	}
	if err := check(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(path string, forbidden []string) error {
	f, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	pcln, text := f.Section(".gopclntab"), f.Section(".text")
	if pcln == nil || text == nil {
		return fmt.Errorf("cannot assess binary without Go function metadata")
	}
	data, err := pcln.Data()
	if err != nil {
		return err
	}
	table, err := gosym.NewTable(nil, gosym.NewLineTable(data, text.Addr))
	if err != nil {
		return err
	}
	if len(table.Funcs) == 0 {
		return fmt.Errorf("cannot assess binary with an empty Go function table")
	}
	found := map[string]int{}
	for _, fn := range table.Funcs {
		for _, pkg := range forbidden {
			if strings.HasPrefix(fn.Name, pkg+".") || strings.HasPrefix(fn.Name, pkg+"/") {
				found[pkg]++
			}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"binary": path, "function_count": len(table.Funcs), "forbidden_package_functions": found}); err != nil {
		return err
	}
	if len(found) != 0 {
		return fmt.Errorf("forbidden package functions are present in the binary")
	}
	return nil
}
