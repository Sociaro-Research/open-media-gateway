//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporaryArchiveChild(t *testing.T) {
	if os.Getenv("GATEWAY_TEMP_ARCHIVE_CHILD") != "1" {
		return
	}
	store, err := NewLocal(os.Getenv("GATEWAY_TEMP_ROOT"), &http.Client{Timeout: time.Minute}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.Archive(context.Background(), "child", os.Getenv("GATEWAY_TEMP_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if media.SizeBytes != 8192 {
		t.Fatal(media.SizeBytes)
	}
}

func TestTemporaryCleanupProtectsAnotherProcessAndRecoversAfterKill(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill=%t", kill), func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Length", "8192")
				_, _ = w.Write(make([]byte, 4096))
				w.(http.Flusher).Flush()
				select {
				case <-release:
					_, _ = w.Write(make([]byte, 4096))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			root := t.TempDir()
			child := exec.Command(os.Args[0], "-test.run=^TestTemporaryArchiveChild$")
			child.Env = append(os.Environ(), "GATEWAY_TEMP_ARCHIVE_CHILD=1", "GATEWAY_TEMP_ROOT="+root, "GATEWAY_TEMP_URL="+server.URL)
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()
			var partial string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				files, err := filepath.Glob(filepath.Join(root, "child", ".media-v2-*.part"))
				if err != nil {
					t.Fatal(err)
				}
				if len(files) == 1 {
					if info, err := os.Stat(files[0]); err == nil && info.Size() == 4096 {
						partial = files[0]
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if partial == "" {
				t.Fatal("child never created a partial archive")
			}
			// Age must never override a live process's ownership, even when stalled.
			old := time.Now().Add(-48 * time.Hour)
			if err := os.Chtimes(partial, old, old); err != nil {
				t.Fatal(err)
			}
			cleaner, err := NewLocal(root, http.DefaultClient, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			orphan, err := cleaner.createTemporary(root, "reference")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = orphan.Write([]byte("orphan"))
			_ = orphan.Close()
			result, err := cleaner.CleanupTemporaryFiles(context.Background())
			if err != nil || result.Removed != 1 || result.RemovedBytes != 6 || result.SkippedActive != 1 {
				t.Fatalf("unexpected cleanup: %+v %v", result, err)
			}
			if _, err := os.Stat(partial); err != nil {
				t.Fatalf("active transfer deleted: %v", err)
			}
			if kill {
				if err := child.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = child.Wait()
				result, err = cleaner.CleanupTemporaryFiles(context.Background())
				if err != nil || result.Removed != 1 || result.RemovedBytes != 4096 {
					t.Fatalf("crash cleanup: %+v %v", result, err)
				}
				if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("orphan retained: %v", err)
				}
			} else {
				release <- struct{}{}
				if err := child.Wait(); err != nil {
					t.Fatal(err)
				}
				result, err = cleaner.CleanupTemporaryFiles(context.Background())
				if err != nil || result.Removed != 0 {
					t.Fatalf("published file swept: %+v %v", result, err)
				}
				object, err := cleaner.Open(context.Background(), "child/video.mp4")
				if err != nil {
					t.Fatal(err)
				}
				defer object.Reader.Close()
				if n, err := io.Copy(io.Discard, object.Reader); err != nil || n != 8192 {
					t.Fatalf("active archive lost bytes: %d %v", n, err)
				}
			}
		})
	}
}

func TestTemporaryCleanupResumesBoundedScanAndPreservesOtherFiles(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocal(root, http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), ".media-v2-1.part")
	if err := os.WriteFile(outside, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "symlink-directory")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".media-v2-9.part")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"video.mp4", ".media-custom.part", ".reference-photo", ".media-42.part", ".reference-42"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12; i++ {
		directory := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := store.createTemporary(directory, "media")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("1234"))
		_ = file.Close()
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{".media-7.part", ".reference-7"} {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, passes := 0, 0
	for {
		result, err := store.cleanupTemporaryFiles(context.Background(), 3, time.Second)
		if err != nil || result.Scanned > 3 {
			t.Fatalf("unbounded cleanup: %+v %v", result, err)
		}
		removed += result.Removed
		passes++
		if !result.More {
			break
		}
		if passes > 50 {
			t.Fatal("bounded scan did not reach end of tree")
		}
	}
	if removed != 14 || passes < 2 {
		t.Fatalf("scan did not resume: %d removed in %d passes", removed, passes)
	}
	for _, name := range []string{"video.mp4", ".media-custom.part", ".reference-photo", ".media-42.part", ".reference-42"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("unrelated/recent legacy file deleted: %s %v", name, err)
		}
	}
	if bytes, err := os.ReadFile(outside); err != nil || string(bytes) != "external" {
		t.Fatalf("symlink escaped storage root: %q %v", bytes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CleanupTemporaryFiles(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
