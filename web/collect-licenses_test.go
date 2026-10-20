//go:build ignore

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectRuntimeLicenseAndNoticeExactly(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{"LICENSE": "Fixture license\n", "NOTICE": "Fixture attribution\n", "COPYING.txt": "Fixture extra terms\n", "README.md": "not a notice"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	module := moduleInfo{Path: "example.test/runtime", Version: "v1.2.3", Dir: source}
	record, err := collectModule(module, destination)
	if err != nil {
		t.Fatal(err)
	}
	if record.Module != module.Path || record.Version != module.Version || len(record.Notices) != 3 {
		t.Fatal(record)
	}
	for name, digest := range record.Notices {
		actual, err := os.ReadFile(filepath.Join(destination, module.Path+"@"+module.Version, name))
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != string(original) || digest != fmt.Sprintf("%x", sha256.Sum256(original)) {
			t.Fatal("notice changed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, module.Path+"@"+module.Version, "README.md")); !os.IsNotExist(err) {
		t.Fatal("non-notice copied")
	}
}

func TestMissingRuntimeLicenseFailsClosed(t *testing.T) {
	module := moduleInfo{Path: "example.test/unreviewed", Version: "v0.1.0", Dir: t.TempDir()}
	if _, err := collectModule(module, t.TempDir()); err == nil || !strings.Contains(err.Error(), module.Path+"@"+module.Version) {
		t.Fatal("missing notice did not stop collection", err)
	}
}

func TestLicenseSymlinkCannotSubstituteForNotice(t *testing.T) {
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("not a module notice"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectModule(moduleInfo{Path: "example.test/symlink", Version: "v1", Dir: source}, t.TempDir()); err == nil {
		t.Fatal("symlink accepted")
	}
}
