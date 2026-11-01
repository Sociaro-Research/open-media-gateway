package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

type localResourceMIMERepository struct {
	aacUploadRepository
	owner string
}

func (r *localResourceMIMERepository) Get(ctx context.Context, id string) (Image, error) {
	if identity.OwnerID(ctx) != r.owner {
		return Image{}, storage.ErrObjectNotFound
	}
	return r.aacUploadRepository.Get(ctx, id)
}
func (r *localResourceMIMERepository) Insert(ctx context.Context, item Image) error {
	if identity.OwnerID(ctx) != r.owner {
		return storage.ErrObjectNotFound
	}
	return r.aacUploadRepository.Insert(ctx, item)
}
func (r *localResourceMIMERepository) FindByHash(ctx context.Context, hash string) (Image, error) {
	if identity.OwnerID(ctx) != r.owner {
		return Image{}, storage.ErrObjectNotFound
	}
	return r.aacUploadRepository.FindByHash(ctx, hash)
}

type localResourceMIMETransport struct{ t *testing.T }

func (f localResourceMIMETransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("local reference/stream test contacted network")
	return nil, nil
}

func localResourceMIMEStream(t *testing.T, name string, data []byte, wantKind, wantExtension string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewLocal(root, &http.Client{Transport: localResourceMIMETransport{t}}, MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), owner)
	repo := &localResourceMIMERepository{owner: owner}
	library := New(repo, store)
	item, err := library.Upload(ctx, name, bytes.NewReader(data))
	if err != nil {
		t.Fatal("actual local upload", err)
	}
	digest := sha256.Sum256(data)
	expectedSHA := hex.EncodeToString(digest[:])
	if item.SHA256 != expectedSHA || item.SizeBytes != int64(len(data)) || item.ContentType != wantKind || !strings.HasSuffix(item.StorageKey, wantExtension) {
		t.Errorf("actual local upload metadata: %#v", item)
	}
	_, object, err := library.Open(ctx, item.ID)
	if err != nil {
		t.Fatal("actual local reopen", err)
	}
	reopened, readErr := io.ReadAll(object.Reader)
	closeErr := object.Reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(reopened, data) || object.SizeBytes != int64(len(data)) || object.ContentType != wantKind {
		t.Errorf("actual local reopen MIME=%q size=%d read=%v close=%v", object.ContentType, object.SizeBytes, readErr, closeErr)
	}
	file, err := library.Inspect(ctx, item.ID)
	if err != nil {
		t.Fatal("actual local reinspection", err)
	}
	if file.SHA256 != expectedSHA || file.Info.ContentType != wantKind || file.Info.Size != int64(len(data)) {
		t.Fatal("reinspection lost original identity", file)
	}
	source := core.ResourceSourceInput{ReferenceID: item.ID, URL: "https://caller-host-must-not-be-used.invalid/private", MediaType: file.Type, ContentType: file.Info.ContentType, SizeBytes: file.Info.Size, SHA256: file.SHA256}
	resolver := NewResolver(library, "synthetic-local-source-secret")
	// MIME aliases are normalized only within the existing inspected format family.
	aliases := []string{wantKind}
	if wantKind == "image/heic" {
		aliases = append(aliases, "image/heif")
	}
	if wantKind == "audio/wave" {
		aliases = append(aliases, "audio/wav", "audio/x-wav")
	}
	for _, alias := range aliases {
		source.ContentType = alias
		stream, err := resolver.OpenResourceSource(ctx, source)
		if err != nil {
			t.Fatalf("actual local verified resource stream %s: %v", alias, err)
		}
		got, readErr := io.ReadAll(stream.Reader)
		closeErr := stream.Reader.Close()
		sum := sha256.Sum256(got)
		if readErr != nil || closeErr != nil || !bytes.Equal(got, data) || stream.ContentType != wantKind || stream.SizeBytes != int64(len(data)) || hex.EncodeToString(sum[:]) != expectedSHA {
			t.Fatal("verified stream changed type/hash/size", alias, readErr, closeErr, stream.ContentType)
		}
	}
	source.ContentType = wantKind
	for _, mutation := range []string{"type", "hash", "size"} {
		invalid := source
		switch mutation {
		case "type":
			invalid.ContentType = "application/octet-stream"
		case "hash":
			invalid.SHA256 = strings.Repeat("0", 64)
		case "size":
			invalid.SizeBytes++
		}
		result, err := resolver.OpenResourceSource(ctx, invalid)
		if !errors.Is(err, ErrResourceSource) || result.Reader != nil {
			t.Fatal("local storage weakened strict stream evidence", mutation, err)
		}
	}
	foreign := identity.WithOwnerID(context.Background(), uuid.NewString())
	if result, err := resolver.OpenResourceSource(foreign, source); !errors.Is(err, ErrResourceSource) || result.Reader != nil {
		t.Fatal("foreign source opened", err)
	}
	// Corrupt physical bytes without changing the accepted repository hash.
	altered := bytes.Clone(data)
	altered[len(altered)-1] ^= 0xff
	if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(item.StorageKey)), altered, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := resolver.OpenResourceSource(ctx, source); !errors.Is(err, ErrResourceSource) || result.Reader != nil {
		t.Fatal("corrupted local source reached provider stream", err)
	}
}

func localResourceMIMEOGG() []byte {
	body := make([]byte, 19)
	copy(body, "OpusHead")
	body[8] = 1
	body[9] = 1
	header := make([]byte, 28)
	copy(header, "OggS")
	binary.LittleEndian.PutUint64(header[6:], 48000)
	binary.LittleEndian.PutUint32(header[14:], 1)
	header[26] = 1
	header[27] = byte(len(body))
	return append(header, body...)
}
func localResourceMIMEFLAC() []byte {
	b := make([]byte, 42)
	copy(b, "fLaC")
	b[4] = 0x80
	b[7] = 34
	binary.BigEndian.PutUint64(b[18:], uint64(44100)<<44|44100)
	return b
}

func TestLocalResourceStreamMIMESyntheticHEIF(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"mdat.heif", heifUploadFixture(640, 512, 0, false, true)}, {"idat.heic", heifUploadFixture(640, 512, 0, true, false)}} {
		t.Run(tc.name, func(t *testing.T) { localResourceMIMEStream(t, tc.name, tc.data, "image/heic", ".heic") })
	}
}
func TestLocalResourceStreamMIMEAudioBytes(t *testing.T) {
	for _, tc := range []struct {
		name, kind, extension string
		data                  []byte
	}{{"wave.wav", "audio/wave", ".wav", testmedia.WAV()}, {"mpeg.mp3", "audio/mpeg", ".mp3", testmedia.MP3()}, {"adts.aac", "audio/aac", ".aac", aacUploadADTS()}, {"audio.m4a", "audio/mp4", ".m4a", aacUploadM4A(0, false, false)}, {"opus.ogg", "audio/ogg", ".ogg", localResourceMIMEOGG()}, {"lossless.flac", "audio/flac", ".flac", localResourceMIMEFLAC()}} {
		t.Run(tc.name, func(t *testing.T) { localResourceMIMEStream(t, tc.name, tc.data, tc.kind, tc.extension) })
	}
}
func TestLocalResourceStreamMIMERealHEIF(t *testing.T) {
	paths := os.Getenv("GATEWAY_TEST_HEIC_FIXTURES")
	if paths == "" {
		paths = os.Getenv("GATEWAY_TEST_HEIC_FIXTURE")
	}
	if paths == "" {
		t.Skip("requires independently encoded local HEIC fixtures")
	}
	for _, path := range filepath.SplitList(paths) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			localResourceMIMEStream(t, filepath.Base(path), data, "image/heic", ".heic")
		})
	}
}
