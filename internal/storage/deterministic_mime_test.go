package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

var deterministicMediaCases = []struct{ kind, base, extension, canonical string }{
	{"image/png", "image", ".png", "image/png"},
	{"image/jpeg", "image", ".jpg", "image/jpeg"},
	{"image/webp", "image", ".webp", "image/webp"},
	{"image/gif", "image", ".gif", "image/gif"},
	{"image/heic", "image", ".heic", "image/heic"},
	{"image/heif", "image", ".heic", "image/heic"},
	{"video/mp4", "video", ".mp4", "video/mp4"},
	{"video/quicktime", "video", ".mov", "video/quicktime"},
	{"video/webm", "video", ".webm", "video/webm"},
	{"audio/mpeg", "audio", ".mp3", "audio/mpeg"},
	{"audio/wave", "audio", ".wav", "audio/wave"},
	{"audio/wav", "audio", ".wav", "audio/wave"},
	{"audio/x-wav", "audio", ".wav", "audio/wave"},
	{"audio/aac", "audio", ".aac", "audio/aac"},
	{"audio/x-aac", "audio", ".aac", "audio/aac"},
	{"audio/mp4", "audio", ".m4a", "audio/mp4"},
	{"audio/x-m4a", "audio", ".m4a", "audio/mp4"},
	{"audio/m4a", "audio", ".m4a", "audio/mp4"},
	{"audio/ogg", "audio", ".ogg", "audio/ogg"},
	{"audio/flac", "audio", ".flac", "audio/flac"},
}

func TestDeterministicKnownMediaMIMERoundTrip(t *testing.T) {
	for _, tc := range deterministicMediaCases {
		t.Run(tc.kind, func(t *testing.T) {
			base, extension := mediaFileName(tc.kind)
			if base != tc.base || extension != tc.extension {
				t.Errorf("filename %s%s want %s%s", base, extension, tc.base, tc.extension)
			}
			store, err := NewLocal(t.TempDir(), http.DefaultClient, 1024)
			if err != nil {
				t.Fatal(err)
			}
			ctx := identity.WithOwnerID(context.Background(), "mime-fixture-owner")
			payload := []byte("opaque storage bytes; the reference library owns media inspection")
			saved, err := store.StoreReference(ctx, uuid.NewString(), payload, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(payload)
			if !strings.HasSuffix(saved.StorageKey, "/"+tc.base+tc.extension) || saved.SHA256 != hex.EncodeToString(sum[:]) || saved.SizeBytes != int64(len(payload)) || saved.ContentType != tc.kind {
				t.Errorf("storage metadata changed: %#v", saved)
			}
			object, err := store.Open(ctx, saved.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer object.Reader.Close()
			got, err := io.ReadAll(object.Reader)
			if err != nil || !bytes.Equal(got, payload) || object.SizeBytes != int64(len(payload)) {
				t.Fatal("reopened bytes/size changed", err)
			}
			if object.ContentType != tc.canonical {
				t.Errorf("reopened MIME %q want %q", object.ContentType, tc.canonical)
			}
		})
	}
}

// Poisoning only a child process's in-memory Go registry proves known formats
// do not inherit host MIME mappings. No operating-system configuration changes.
func TestDeterministicKnownMediaMIMEIgnoresHostRegistry(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_MIME_REGISTRY_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestDeterministicKnownMediaMIMEIgnoresHostRegistry$", "-test.count=1")
		command.Env = append(os.Environ(), "GATEWAY_TEST_MIME_REGISTRY_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated MIME registry check: %v\n%s", err, output)
		}
		return
	}
	for _, tc := range deterministicMediaCases {
		if err := mime.AddExtensionType(tc.extension, "application/x-conflicting-host-mime"); err != nil {
			t.Fatal(err)
		}
	}
	TestDeterministicKnownMediaMIMERoundTrip(t)
}

func TestDeterministicMIMEPreservesBinaryAndUnknownFallbacks(t *testing.T) {
	for _, tc := range []struct{ kind, base, extension string }{{"application/octet-stream", "data", ".bin"}, {"text/plain", "text", ".txt"}, {"application/json", "data", ".json"}, {"video/x-unknown-gateway-fixture", "video", ".mp4"}} {
		base, extension := mediaFileName(tc.kind)
		if base != tc.base || extension != tc.extension {
			t.Fatal("existing filename behavior changed", tc, base, extension)
		}
	}
	for _, tc := range []struct{ kind, base, fallback string }{{"image/x-unknown-gateway-fixture", "image", ".img"}, {"audio/x-unknown-gateway-fixture", "audio", ".audio"}} {
		base, extension := mediaFileName(tc.kind)
		if base != tc.base || extension != tc.fallback {
			t.Fatal("unknown registry fallback changed", tc, base, extension)
		}
	}
	// Existing binary keys still reopen with their current registry semantics.
	root := t.TempDir()
	store, err := NewLocal(root, http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "unknown.bin"), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	object, err := store.Open(context.Background(), "unknown.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer object.Reader.Close()
	if object.ContentType != mime.TypeByExtension(".bin") {
		t.Fatal("binary MIME behavior changed", object.ContentType)
	}
}
