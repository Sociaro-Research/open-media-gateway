//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package storage

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTemporaryS3Child(t *testing.T) {
	if os.Getenv("GATEWAY_TEMP_S3_CHILD") != "1" {
		return
	}
	store, err := NewS3(S3Config{Endpoint: os.Getenv("GATEWAY_TEMP_URL"), Region: "test-1", Bucket: "test",
		AccessKeyID: "test", SecretAccessKey: "test", PathStyle: true}, &http.Client{Timeout: time.Minute}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store.stagingPath = os.Getenv("GATEWAY_TEMP_ROOT")
	source := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 8192)))
	if _, err := store.Archive(context.Background(), "task", source); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryS3StagingProtectsUploadAndRecoversAfterKill(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			release, started := make(chan struct{}), make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bytes, err := io.ReadAll(r.Body)
				if err != nil || string(bytes) != strings.Repeat("x", 8192) {
					t.Errorf("S3 staging changed upload bytes: %d %v", len(bytes), err)
				}
				started <- struct{}{}
				select {
				case <-release:
					w.Header().Set("ETag", `"test"`)
					w.WriteHeader(http.StatusOK)
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			root := t.TempDir()
			child := exec.Command(os.Args[0], "-test.run=^TestTemporaryS3Child$")
			child.Env = append(os.Environ(), "GATEWAY_TEMP_S3_CHILD=1", "GATEWAY_TEMP_ROOT="+root, "GATEWAY_TEMP_URL="+server.URL)
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("S3 upload did not start")
			}
			cleaner := &Local{root: root, client: http.DefaultClient, stagingOnly: true}
			result, err := cleaner.CleanupTemporaryFiles(context.Background())
			if err != nil || result.SkippedActive != 1 || result.RemovedDirectories != 0 {
				t.Fatalf("active S3 staging swept: %+v %v", result, err)
			}
			if kill {
				if err := child.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = child.Wait()
				result, err = cleaner.CleanupTemporaryFiles(context.Background())
				if err != nil || result.RemovedDirectories != 1 || result.RemovedBytes != 8192 || result.Removed != 1 {
					t.Fatalf("crashed S3 staging not reclaimed: %+v %v", result, err)
				}
			} else {
				release <- struct{}{}
				if err := child.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("S3 staging retained: %v %v", entries, err)
			}
		})
	}
}
