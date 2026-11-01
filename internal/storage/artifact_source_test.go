package storage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

type artifactChunkSource struct {
	reader       io.Reader
	chunk, reads int
	closed       atomic.Int32
	failure      error
}

func (r *artifactChunkSource) Read(p []byte) (int, error) {
	r.reads++
	if r.failure != nil && r.reads > 2 {
		return 0, r.failure
	}
	if r.chunk > 0 && len(p) > r.chunk {
		p = p[:r.chunk]
	}
	return r.reader.Read(p)
}
func (r *artifactChunkSource) Close() error { r.closed.Add(1); return nil }
func artifactSource(data []byte, kind string, size int64) (core.ArtifactSource, *artifactChunkSource) {
	reader := &artifactChunkSource{reader: bytes.NewReader(data), chunk: 37}
	return core.ArtifactSource{Reader: reader, ContentType: kind, SizeBytes: size}, reader
}
func noArtifactFiles(t *testing.T, root string) {
	t.Helper()
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Errorf("uncommitted artifact retained: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestArchiveSourceStreamsExactBytesAndChecksum(t *testing.T) {
	for _, kind := range []string{"text/plain", "text/plain; charset=utf-8", "application/json", "image/png"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte(strings.Repeat("synthetic text\n", 12000))
			if kind == "application/json" {
				data = []byte(`{"large":"` + strings.Repeat("synthetic", 12000) + `","values":[1,true,null]}`)
			}
			if kind == "image/png" {
				data = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)
			}
			s, err := NewLocal(t.TempDir(), http.DefaultClient, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			source, reader := artifactSource(data, kind, int64(len(data)))
			media, err := s.ArchiveSource(context.Background(), "task/attempts/claim/artifacts/2", source)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if media.SizeBytes != int64(len(data)) || media.SHA256 != hex.EncodeToString(sum[:]) || media.SourceURL != "" || reader.closed.Load() != 1 || reader.reads < 3 {
				t.Fatal(media, reader.closed.Load(), reader.reads)
			}
			object, err := s.Open(context.Background(), media.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(object.Reader)
			object.Reader.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("stored stream differs", err)
			}
			if kind == "application/json" && object.ContentType != "application/json" {
				t.Fatal(object.ContentType)
			}
		})
	}
}
func TestArchiveSourceRejectsUnsafeOrInvalidStreamsAndCleansUp(t *testing.T) {
	for _, tc := range []struct {
		name, kind, data string
		size, max        int64
		failure          bool
	}{
		{"declared too large", "text/plain", "synthetic", 9, 8, false},
		{"stream too large", "text/plain", "synthetic", -1, 8, false},
		{"size mismatch", "text/plain", "synthetic", 8, 64, false},
		{"empty", "text/plain", "", 0, 64, false},
		{"bad size", "text/plain", "x", -2, 64, false},
		{"html mime", "text/html", "<html>active</html>", -1, 64, false},
		{"script mime", "application/javascript", "alert(1)", -1, 64, false},
		{"wrong media mime", "image/png", "<html>active</html>", -1, 64, false},
		{"invalid json", "application/json", `{"x":}`, -1, 64, false},
		{"trailing json", "application/json", `{"x":1} {"x":2}`, -1, 64, false},
		{"failed reader", "text/plain", "read this in small chunks", -1, 64, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := NewLocal(t.TempDir(), http.DefaultClient, tc.max)
			source, reader := artifactSource([]byte(tc.data), tc.kind, tc.size)
			if tc.failure {
				reader.chunk = 2
				reader.failure = errors.New("opaque-provider-ref-and-credentials")
			}
			_, err := s.ArchiveSource(context.Background(), "failed", source)
			if err == nil || strings.Contains(err.Error(), "opaque-provider-ref-and-credentials") || reader.closed.Load() != 1 {
				t.Fatal(err, reader.closed.Load())
			}
			noArtifactFiles(t, s.root)
		})
	}
	s, _ := NewLocal(t.TempDir(), http.DefaultClient, 64)
	if _, err := s.ArchiveSource(context.Background(), "nil", core.ArtifactSource{}); err == nil {
		t.Fatal("missing reader accepted")
	}
	// Trusted stream MIME support must not change ordinary provider URL policy.
	for _, url := range []string{"data:text/plain;base64,dGV4dA==", "data:application/json;base64,e30="} {
		if _, err := s.Archive(context.Background(), "url", url); err == nil {
			t.Fatal("URL whitelist expanded")
		}
	}
}
func TestArchiveSourceCancellationClosesBlockedReader(t *testing.T) {
	s, _ := NewLocal(t.TempDir(), http.DefaultClient, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	pipe, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, err := s.ArchiveSource(ctx, "cancelled", core.ArtifactSource{Reader: pipe, ContentType: "text/plain", SizeBytes: -1})
		done <- err
	}()
	if _, err := writer.Write([]byte("partial stream")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not close blocked stream")
	}
	noArtifactFiles(t, s.root)
	source, reader := artifactSource([]byte("never consumed"), "text/plain", -1)
	if _, err := s.ArchiveSource(ctx, "already-cancelled", source); !errors.Is(err, context.Canceled) || reader.closed.Load() != 1 {
		t.Fatal(err, reader.closed.Load())
	}
}
func TestArchiveSourceJSONGrammarMatchesStandardValidator(t *testing.T) {
	inputs := []string{`null`, `true`, `false`, `0`, `-0`, `-12.5e+3`, `[1,2,{"a":"\\\"\u001f"}]`, `{}`, `[]`, `"literal"`, `01`, `1.`, `+1`, `1e`, `[1,]`, `{"a":1,}`, `{"a" 1}`, `"\q"`, `"\u000x"`, `true false`, `[`, `{"a":`, "\"control\n\"", strings.Repeat("[", 10001) + strings.Repeat("]", 10001)}
	for _, input := range inputs {
		source, reader := artifactSource([]byte(input), "application/json", -1)
		s, _ := NewLocal(t.TempDir(), http.DefaultClient, int64(len(input)+1))
		_, err := s.ArchiveSource(context.Background(), "json", source)
		if (err == nil) != json.Valid([]byte(input)) {
			t.Fatalf("grammar mismatch %.160q: %v", input, err)
		}
		if reader.closed.Load() != 1 {
			t.Fatal("reader not closed")
		}
	}
}
func TestS3ArchiveSourceStagesStreamsAndCleansFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "denied upload"}[fail], func(t *testing.T) {
			remote := newMemoryS3()
			if fail {
				remote.deny = "PUT"
			}
			s, _ := NewS3(testS3Config(), &http.Client{Transport: remote}, 1<<20)
			s.stagingPath = t.TempDir()
			data := []byte(`{"recognition":"` + strings.Repeat("text ", 10000) + `"}`)
			source, reader := artifactSource(data, "application/json", -1)
			media, err := s.ArchiveSource(context.Background(), "task/attempts/claim/artifacts/2", source)
			if (err != nil) != fail || reader.closed.Load() != 1 {
				t.Fatal(err, reader.closed.Load())
			}
			entries, _ := os.ReadDir(s.stagingPath)
			if len(entries) != 0 {
				t.Fatal("S3 staging retained", entries)
			}
			if !fail {
				if media.SourceURL != "" || !strings.HasSuffix(media.StorageKey, "/data.json") || media.SizeBytes != int64(len(data)) {
					t.Fatal(media)
				}
				object, err := s.Open(context.Background(), media.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(object.Reader)
				object.Reader.Close()
				if err != nil || !bytes.Equal(data, got) {
					t.Fatal("S3 stream differs", err)
				}
			}
		})
	}
}

type artifactAccounting struct {
	*settingsMemory
	owner, key, task string
	size             int64
	private, shared  int
}

func (r *artifactAccounting) RecordResultObject(ctx context.Context, key, task string, size int64) error {
	r.owner, r.key, r.task, r.size = identity.OwnerID(ctx), key, task, size
	r.private++
	return nil
}
func (r *artifactAccounting) RecordObject(ctx context.Context, key, task string, size int64) error {
	r.owner, r.key, r.task, r.size = identity.OwnerID(ctx), key, task, size
	r.shared++
	return nil
}
func (*artifactAccounting) RemoveObject(context.Context, string) error              { return nil }
func (*artifactAccounting) SharedUsage(context.Context) (Usage, error)              { return Usage{}, nil }
func (*artifactAccounting) PendingObjects(context.Context) ([]PendingObject, error) { return nil, nil }
func (*artifactAccounting) ResolveObject(context.Context, string, *int64) error     { return nil }
func TestArchiveSourceManagerOwnerPathsAndAccounting(t *testing.T) {
	for _, shared := range []bool{false, true} {
		repo := &artifactAccounting{settingsMemory: newSettingsMemory()}
		cipher, _ := providerconfig.NewCipher("synthetic-stream-key")
		m, err := NewManager(repo, cipher, t.TempDir(), http.DefaultClient, 1024)
		if err != nil {
			t.Fatal(err)
		}
		ctx := identity.WithOwnerID(context.Background(), "owner-one")
		if shared {
			ctx = identity.WithResourceOwners(ctx, "provider-owner", identity.SharedOwnerID)
		}
		source, reader := artifactSource([]byte("recognized text"), "text/plain", -1)
		media, err := m.ArchiveSource(ctx, "task/attempts/claim/artifacts/2", source)
		if err != nil || reader.closed.Load() != 1 || media.SourceURL != "" || media.StorageKey != "owner-one/results/task/attempts/claim/artifacts/2/text.txt" || repo.owner != "owner-one" || repo.task != "task" || repo.key != media.StorageKey || repo.size != 15 {
			t.Fatal(media, repo, err)
		}
		if shared && repo.shared != 1 || !shared && repo.private != 1 {
			t.Fatal("accounting class changed", repo)
		}
	}
}

type artifactUploadCancellation struct{ started chan struct{} }

func (r artifactUploadCancellation) RoundTrip(req *http.Request) (*http.Response, error) {
	close(r.started)
	<-req.Context().Done()
	return nil, req.Context().Err()
}
func TestS3ArchiveSourceCancellationCleansStaging(t *testing.T) {
	transport := artifactUploadCancellation{started: make(chan struct{})}
	s, _ := NewS3(testS3Config(), &http.Client{Transport: transport}, 1024)
	s.stagingPath = t.TempDir()
	source, reader := artifactSource([]byte("synthetic"), "text/plain", -1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.ArchiveSource(ctx, "cancel-upload", source); done <- err }()
	select {
	case <-transport.started:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not cancel")
	}
	if reader.closed.Load() != 1 {
		t.Fatal("source not closed exactly once")
	}
	entries, _ := os.ReadDir(s.stagingPath)
	if len(entries) != 0 {
		t.Fatal("cancelled upload staging retained", entries)
	}
}

func TestArchiveSourceJSONDifferentialInputs(t *testing.T) {
	random := rand.New(rand.NewSource(253))
	alphabet := []byte("{}[],:\"\\0123456789.-+eE truefalsnul\t\r\n")
	for i := 0; i < 10000; i++ {
		data := make([]byte, random.Intn(96))
		for j := range data {
			data[j] = alphabet[random.Intn(len(alphabet))]
		}
		got := validateArtifactJSON(bufio.NewReader(bytes.NewReader(data))) == nil
		if got != json.Valid(data) {
			t.Fatalf("JSON grammar differs for %.160q", data)
		}
	}
}
func TestArchiveSourcePinnedS3DestinationAndAccounting(t *testing.T) {
	repo := &artifactAccounting{settingsMemory: newSettingsMemory()}
	remote := newMemoryS3()
	cipher, _ := providerconfig.NewCipher("synthetic-stream-key")
	m, err := NewManager(repo, cipher, t.TempDir(), &http.Client{Transport: remote}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(context.Background(), "owner-one")
	record, _, err := m.prepare(ctx, s3Update())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	pinned, err := m.ForArchive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, SettingsRecord{Driver: "local"}); err != nil {
		t.Fatal(err)
	}
	source, reader := artifactSource([]byte("recognized text"), "text/plain", -1)
	media, err := pinned.(SourceArchiver).ArchiveSource(ctx, "task/attempts/claim/artifacts/2", source)
	if err != nil || reader.closed.Load() != 1 || !strings.HasPrefix(media.StorageKey, "s3:"+record.ConnectionID+"/") || !strings.Contains(media.StorageKey, "/owner-one/results/task/attempts/claim/artifacts/2/") || repo.private != 1 || repo.key != media.StorageKey || repo.owner != "owner-one" || repo.task != "task" {
		t.Fatal(media, repo, err)
	}
}
