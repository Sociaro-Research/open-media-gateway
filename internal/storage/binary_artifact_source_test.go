package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

func TestTrustedBinarySourcePreservesOpaqueTypeBytesAndChecksum(t *testing.T) {
	for _, payload := range [][]byte{[]byte("<html><script>synthetic</script></html>"), []byte(`{"unknown":"format"}`), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)} {
		s, _ := NewLocal(t.TempDir(), http.DefaultClient, 1024)
		source, reader := artifactSource(payload, "application/octet-stream", int64(len(payload)))
		m, err := s.ArchiveSource(context.Background(), "task/attempts/claim/artifacts/3", source)
		sum := sha256.Sum256(payload)
		if err != nil || m.ContentType != "application/octet-stream" || !strings.HasSuffix(m.StorageKey, "/data.bin") || m.SourceURL != "" || m.SHA256 != hex.EncodeToString(sum[:]) || reader.closed.Load() != 1 {
			t.Fatal(m, err)
		}
		o, err := s.Open(context.Background(), m.StorageKey)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(o.Reader)
		o.Reader.Close()
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatal(err)
		}
	}
	// URL-based media retains sniff promotion; only an explicitly trusted stream
	// may preserve arbitrary bytes as an opaque binary attachment.
	s, _ := NewLocal(t.TempDir(), http.DefaultClient, 1024)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)
	m, err := s.Archive(context.Background(), "ordinary", "data:application/octet-stream;base64,"+base64.StdEncoding.EncodeToString(png))
	if err != nil || m.ContentType != "image/png" {
		t.Fatal("ordinary URL behavior changed", m, err)
	}
}
func TestTrustedBinarySourceFailureCleansAndCloses(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		size, max  int64
		fail       bool
	}{{"size bound", "application/octet-stream", -1, 8, false}, {"declared size", "application/octet-stream", 100, 1024, false}, {"read failure", "application/octet-stream", -1, 1024, true}, {"active MIME", "text/html", -1, 1024, false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s, _ := NewLocal(root, http.DefaultClient, tc.max)
			source, reader := artifactSource([]byte(strings.Repeat("opaque", 30)), tc.kind, tc.size)
			if tc.fail {
				reader.failure = io.ErrUnexpectedEOF
			}
			if _, err := s.ArchiveSource(context.Background(), "failed", source); err == nil || reader.closed.Load() != 1 {
				t.Fatal(err, reader.closed.Load())
			}
			noArtifactFiles(t, root)
		})
	}
}
func TestTrustedBinarySourceManagerOwnerAndAccounting(t *testing.T) {
	repo := &artifactAccounting{settingsMemory: newSettingsMemory()}
	cipher, _ := providerconfig.NewCipher("synthetic-stream-key")
	m, err := NewManager(repo, cipher, t.TempDir(), http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(context.Background(), "binary-owner")
	source, reader := artifactSource([]byte("opaque"), "application/octet-stream", -1)
	media, err := m.ArchiveSource(ctx, "task/attempts/claim/artifacts/3", source)
	if err != nil || media.StorageKey != "binary-owner/results/task/attempts/claim/artifacts/3/data.bin" || repo.owner != "binary-owner" || repo.task != "task" || repo.size != 6 || reader.closed.Load() != 1 || media.SourceURL != "" {
		t.Fatal(media, err, repo)
	}
}
