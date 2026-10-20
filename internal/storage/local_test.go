package storage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocalArchiveAndOpen(t *testing.T) {
	store, err := NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.Archive(context.Background(), "task-1", "data:video/mp4;base64,c21hbGwtdmlkZW8=")
	if err != nil {
		t.Fatal(err)
	}
	if media.StorageKey != "task-1/video.mp4" || media.SizeBytes != 11 || media.SHA256 == "" {
		t.Fatalf("unexpected media metadata: %#v", media)
	}
	object, err := store.Open(context.Background(), media.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Reader.Close()
	content, err := io.ReadAll(object.Reader)
	if err != nil || string(content) != "small-video" {
		t.Fatalf("unexpected stored content %q: %v", content, err)
	}
}

func TestLocalRejectsOversizedResult(t *testing.T) {
	store, err := NewLocal(t.TempDir(), http.DefaultClient, 4)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Archive(context.Background(), "task-1", "data:video/mp4;base64,dG9vLWxhcmdl")
	if err == nil || !strings.Contains(err.Error(), "STORAGE_MAX_BYTES") {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestLocalRejectsEscapingKey(t *testing.T) {
	store, err := NewLocal(t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(context.Background(), "../secret"); err == nil {
		t.Fatal("expected traversal key to be rejected")
	}
}
