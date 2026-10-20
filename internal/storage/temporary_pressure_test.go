//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Opt in only on a freshly mounted, bounded disposable filesystem. The Docker
// recovery runner provides an 8 MiB tmpfs; never use an ordinary media volume.
func TestTemporaryCleanupFullFilesystem(t *testing.T) {
	root := os.Getenv("GATEWAY_STORAGE_PRESSURE_ROOT")
	if root == "" {
		t.Skip("requires an isolated 8 MiB filesystem from the Docker recovery runner")
	}
	var capacity syscall.Statfs_t
	if err := syscall.Statfs(root, &capacity); err != nil {
		t.Fatal(err)
	}
	if size := uint64(capacity.Blocks) * uint64(capacity.Bsize); size < 4<<20 || size > 16<<20 {
		t.Fatalf("refuse to fill filesystem with unexpected capacity: %d", size)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("pressure filesystem must be empty: %v %v", entries, err)
	}
	store, err := NewLocal(root, http.DefaultClient, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := func(size int) string {
		return "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), size))
	}
	before, err := store.Archive(ctx, "before", source(8192))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "after"), 0o700); err != nil {
		t.Fatal(err)
	}
	active, err := store.createTemporary(root, "reference")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if _, err := active.Write(make([]byte, 16384)); err != nil {
		t.Fatal(err)
	}
	orphan, err := store.createTemporary(root, "media")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(orphan, bytes.NewReader(make([]byte, 4<<20)), 4<<20); err != nil {
		t.Fatal(err)
	}
	if err := orphan.Close(); err != nil {
		t.Fatal(err)
	}
	filler, err := os.Create(filepath.Join(root, "unrelated-pressure-filler"))
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 32768)
	for {
		_, err := filler.Write(chunk)
		if errors.Is(err, syscall.ENOSPC) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = filler.Close()
	if _, err := store.Archive(ctx, "after", source(1<<20)); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("expected real ENOSPC, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "after/video.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("full disk published partial media: %v", err)
	}
	result, err := store.CleanupTemporaryFiles(ctx)
	if err != nil || result.Removed != 1 || result.RemovedBytes != 4<<20 || result.SkippedActive != 1 {
		t.Fatalf("cleanup on full disk: %+v %v", result, err)
	}
	if _, err := os.Stat(filler.Name()); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
	after, err := store.Archive(ctx, "after", source(1<<20))
	if err != nil || after.SizeBytes != 1<<20 {
		t.Fatalf("archive did not recover after reclaim: %+v %v", after, err)
	}
	for _, media := range []struct {
		key  string
		size int64
	}{{before.StorageKey, 8192}, {after.StorageKey, 1 << 20}} {
		object, err := store.Open(ctx, media.key)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(object.Reader)
		_ = object.Reader.Close()
		if err != nil || int64(len(data)) != media.size || !bytes.Equal(data, bytes.Repeat([]byte("x"), int(media.size))) {
			t.Fatalf("published result changed: %s %v", media.key, err)
		}
	}
	_ = active.Close()
	result, err = store.CleanupTemporaryFiles(ctx)
	if err != nil || result.Removed != 1 || result.RemovedBytes != 16384 {
		t.Fatalf("released reference lock not reclaimed: %+v %v", result, err)
	}
	t.Log("real ENOSPC: no partial publication; 4 MiB reclaimed; active transfer and published results preserved; 1 MiB archive recovered")
}
