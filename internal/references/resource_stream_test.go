package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

type resourceStreamReader struct {
	*bytes.Reader
	closed atomic.Int32
}

func (r *resourceStreamReader) Close() error { r.closed.Add(1); return nil }

type resourceStreamLibrary struct {
	owner    string
	item     Image
	object   storage.Object
	err      error
	calls    int
	openedID string
}

func (l *resourceStreamLibrary) Open(ctx context.Context, id string) (Image, storage.Object, error) {
	l.calls++
	l.openedID = id
	if identity.OwnerID(ctx) != l.owner {
		return Image{}, storage.Object{}, errors.New("private foreign owner diagnostic")
	}
	return l.item, l.object, l.err
}

type resourceStreamFixture struct {
	ctx     context.Context
	source  core.ResourceSourceInput
	library *resourceStreamLibrary
	reader  *resourceStreamReader
	dir     string
	data    []byte
}

func newResourceStreamFixture(t *testing.T) *resourceStreamFixture {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	data := testmedia.WAV()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	id, owner := uuid.NewString(), uuid.NewString()
	reader := &resourceStreamReader{Reader: bytes.NewReader(data)}
	item := Image{ID: id, StorageKey: "private/storage/reference.wav", ContentType: "audio/wave", SizeBytes: int64(len(data)), SHA256: digest}
	object := storage.Object{Key: item.StorageKey, ContentType: "audio/x-wav", SizeBytes: item.SizeBytes, Reader: reader}
	return &resourceStreamFixture{ctx: identity.WithOwnerID(context.Background(), owner), source: core.ResourceSourceInput{ReferenceID: id, MediaType: "audio", ContentType: "audio/wav", SizeBytes: item.SizeBytes, SHA256: digest}, library: &resourceStreamLibrary{owner: owner, item: item, object: object}, reader: reader, dir: dir, data: data}
}
func resourceStreamAssertNoStage(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("resource stage was not removed", entries, err)
	}
}

func TestResourceStreamVerifiesLocalBytesAndRemovesPrivateStageOnClose(t *testing.T) {
	f := newResourceStreamFixture(t)
	f.source.URL = "https://127.0.0.1/do-not-fetch?signature=private-caller-token"
	opened, err := NewResolver(f.library, "unused").OpenResourceSource(f.ctx, f.source)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ContentType != "audio/wave" || opened.SizeBytes != int64(len(f.data)) || f.reader.closed.Load() != 1 || f.library.calls != 1 || f.library.openedID != f.source.ReferenceID {
		t.Fatal("source was not verified/closed before provider delivery", opened, f.reader.closed.Load())
	}
	staged, ok := opened.Reader.(*resourceStagedSource)
	if !ok {
		t.Fatal("provider reader bypassed verified staging")
	}
	if filepath.Dir(staged.path) != f.dir {
		t.Fatal("stage escaped controlled temp directory")
	}
	info, err := os.Stat(staged.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("stage was not owner-only", info.Mode())
	}
	payload, err := io.ReadAll(opened.Reader)
	if err != nil || !bytes.Equal(payload, f.data) {
		t.Fatal("provider received different bytes", err)
	}
	if err = opened.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err = opened.Reader.Close(); err != nil {
		t.Fatal("stage Close was not idempotent", err)
	}
	resourceStreamAssertNoStage(t, f.dir)
}

func TestResourceStreamRejectsUnauthorizedForeignAndURLOnlyInputs(t *testing.T) {
	for _, kind := range []string{"forbidden role", "foreign owner", "URL only", "invalid UUID", "invalid digest", "oversize", "zero size", "wrong media kind"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceStreamFixture(t)
			ctx := f.ctx
			switch kind {
			case "forbidden role":
				ctx = identity.WithOrganizationRole(ctx, uuid.NewString(), "admin")
			case "foreign owner":
				ctx = identity.WithOwnerID(context.Background(), uuid.NewString())
			case "URL only":
				f.source.ReferenceID = ""
				f.source.URL = "https://169.254.169.254/private"
			case "invalid UUID":
				f.source.ReferenceID = "opaque-provider-id"
			case "invalid digest":
				f.source.SHA256 = "caller-asserted"
			case "oversize":
				f.source.SizeBytes = MaxBytes + 1
			case "zero size":
				f.source.SizeBytes = 0
			case "wrong media kind":
				f.source.MediaType = "video"
			}
			opened, err := NewResolver(f.library, "unused").OpenResourceSource(ctx, f.source)
			if err == nil || opened.Reader != nil {
				t.Fatal("untrusted source reached provider", opened, err)
			}
			if kind == "forbidden role" && !errors.Is(err, ErrReadForbidden) {
				t.Fatal(err)
			}
			if kind != "foreign owner" && f.library.calls != 0 {
				t.Fatal("unauthorized/unbound input opened storage")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "169.254") {
				t.Fatal("private source diagnostic escaped", err)
			}
			resourceStreamAssertNoStage(t, f.dir)
		})
	}
}

func TestResourceStreamRejectsChangedMetadataAndBytesBeforeUpload(t *testing.T) {
	for _, kind := range []string{"record hash", "record missing hash", "record size", "object size", "record MIME", "object MIME", "record ID", "changed bytes", "truncated bytes", "extra bytes", "all MIME assertions wrong", "nil reader", "open error"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceStreamFixture(t)
			switch kind {
			case "record hash":
				f.library.item.SHA256 = strings.Repeat("f", 64)
			case "record missing hash":
				f.library.item.SHA256 = ""
			case "record size":
				f.library.item.SizeBytes++
			case "object size":
				f.library.object.SizeBytes++
			case "record MIME":
				f.library.item.ContentType = "video/mp4"
			case "object MIME":
				f.library.object.ContentType = "image/png"
			case "record ID":
				f.library.item.ID = uuid.NewString()
			case "changed bytes":
				changed := bytes.Clone(f.data)
				changed[len(changed)-1] ^= 1
				f.reader.Reader = bytes.NewReader(changed)
			case "truncated bytes":
				f.reader.Reader = bytes.NewReader(f.data[:len(f.data)-1])
			case "extra bytes":
				f.reader.Reader = bytes.NewReader(append(bytes.Clone(f.data), 1))
			case "all MIME assertions wrong":
				f.source.ContentType = "image/png"
				f.source.MediaType = "image"
				f.library.item.ContentType = "image/png"
				f.library.object.ContentType = "image/png"
			case "nil reader":
				f.library.object.Reader = nil
			case "open error":
				f.library.err = errors.New("private signed URL and credential must not escape")
			}
			opened, err := NewResolver(f.library, "unused").OpenResourceSource(f.ctx, f.source)
			if !errors.Is(err, ErrResourceSource) || opened.Reader != nil {
				t.Fatal("changed source reached provider", opened, err)
			}
			if kind != "nil reader" && f.reader.closed.Load() != 1 {
				t.Fatal("original source was not closed exactly once", f.reader.closed.Load())
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "credential") {
				t.Fatal("raw source failure escaped", err)
			}
			resourceStreamAssertNoStage(t, f.dir)
		})
	}
}

type resourceBlockingReader struct {
	started             chan struct{}
	done                chan struct{}
	readOnce, closeOnce sync.Once
	closed              atomic.Int32
}

func (r *resourceBlockingReader) Read([]byte) (int, error) {
	r.readOnce.Do(func() { close(r.started) })
	<-r.done
	return 0, errors.New("private blocked source diagnostic")
}
func (r *resourceBlockingReader) Seek(int64, int) (int64, error) { return 0, nil }
func (r *resourceBlockingReader) Close() error {
	r.closeOnce.Do(func() { r.closed.Add(1); close(r.done) })
	return nil
}
func TestResourceStreamCancellationClosesBlockedSourceAndRemovesStage(t *testing.T) {
	f := newResourceStreamFixture(t)
	blocked := &resourceBlockingReader{started: make(chan struct{}), done: make(chan struct{})}
	f.library.object.Reader = blocked
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	type outcome struct {
		source core.ArtifactSource
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		source, err := NewResolver(f.library, "unused").OpenResourceSource(ctx, f.source)
		finished <- outcome{source, err}
	}()
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not start staging")
	}
	cancel()
	select {
	case result := <-finished:
		if !errors.Is(result.err, context.Canceled) || result.source.Reader != nil || blocked.closed.Load() != 1 {
			t.Fatal("canceled stream was exposed or leaked", result, blocked.closed.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not close the blocked source")
	}
	resourceStreamAssertNoStage(t, f.dir)
}
func TestResourceStreamCancellationAfterOpenRemovesStage(t *testing.T) {
	f := newResourceStreamFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	source, err := NewResolver(f.library, "unused").OpenResourceSource(ctx, f.source)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	staged := source.Reader.(*resourceStagedSource)
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(staged.path); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = os.Stat(staged.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("context left a returned stage behind", err)
	}
	if _, err = source.Reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled provider stream remained readable", err)
	}
	if err = source.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	resourceStreamAssertNoStage(t, f.dir)
}
func TestResourceStreamTempFailureAndPreCanceledContextAreSafe(t *testing.T) {
	f := newResourceStreamFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := NewResolver(f.library, "unused").OpenResourceSource(ctx, f.source); !errors.Is(err, context.Canceled) || f.library.calls != 0 {
		t.Fatal("pre-canceled request opened storage", err)
	}
	missing := filepath.Join(f.dir, "not-created")
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, missing)
	}
	if source, err := NewResolver(f.library, "unused").OpenResourceSource(f.ctx, f.source); !errors.Is(err, ErrResourceSource) || source.Reader != nil || f.reader.closed.Load() != 1 {
		t.Fatal("temporary-file failure leaked original", source, err)
	}
	resourceStreamAssertNoStage(t, f.dir)
}

type resourceCancelOpenLibrary struct {
	started chan struct{}
	reader  *resourceStreamReader
}

func (l *resourceCancelOpenLibrary) Open(ctx context.Context, _ string) (Image, storage.Object, error) {
	close(l.started)
	<-ctx.Done()
	return Image{}, storage.Object{Reader: l.reader}, errors.New("private canceled open diagnostic")
}
func TestResourceStreamCancellationDuringOpenClosesReturnedReader(t *testing.T) {
	f := newResourceStreamFixture(t)
	library := &resourceCancelOpenLibrary{started: make(chan struct{}), reader: f.reader}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		source, err := NewResolver(library, "unused").OpenResourceSource(ctx, f.source)
		if source.Reader != nil {
			source.Reader.Close()
		}
		finished <- err
	}()
	select {
	case <-library.started:
	case <-time.After(2 * time.Second):
		t.Fatal("local open did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || f.reader.closed.Load() != 1 {
			t.Fatal("canceled open leaked reader or diagnostic", err, f.reader.closed.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local open ignored cancellation")
	}
	resourceStreamAssertNoStage(t, f.dir)
}

func TestResourceStreamVersionedStorageKeysUseVerifiedBytes(t *testing.T) {
	f := newResourceStreamFixture(t)
	// Manager.Open resolves a connection-tagged record key to a physical key;
	// they need not be the same string, and neither may choose provider bytes.
	f.library.item.StorageKey = "s3:" + uuid.NewString() + "/" + f.library.object.Key
	source, err := NewResolver(f.library, "unused").OpenResourceSource(f.ctx, f.source)
	if err != nil {
		t.Fatal("versioned storage reference rejected", err)
	}
	data, err := io.ReadAll(source.Reader)
	if err != nil || !bytes.Equal(data, f.data) {
		t.Fatal("versioned storage changed verified bytes", err)
	}
	if err = source.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	resourceStreamAssertNoStage(t, f.dir)
}

type resourceStreamTransport func(*http.Request) (*http.Response, error)

func (f resourceStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResourceStreamActualS3ReaderCancellationUsesNoNetwork(t *testing.T) {
	f := newResourceStreamFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	body := &resourceBlockingReader{started: make(chan struct{}), done: make(chan struct{})}
	client := &http.Client{Transport: resourceStreamTransport(func(r *http.Request) (*http.Response, error) {
		headers := http.Header{"Content-Type": []string{"audio/wave"}, "Content-Length": []string{strconv.Itoa(len(f.data))}, "Etag": []string{`"synthetic-version"`}}
		switch r.Method {
		case "HEAD":
			return &http.Response{StatusCode: 200, Header: headers, Body: http.NoBody, ContentLength: int64(len(f.data)), Request: r}, nil
		case "GET":
			if r.Header.Get("Range") != "bytes=0-" || r.Header.Get("If-Match") != `"synthetic-version"` {
				return nil, errors.New("missing trusted storage range/version")
			}
			return &http.Response{StatusCode: 200, Header: headers, Body: body, ContentLength: int64(len(f.data)), Request: r}, nil
		default:
			return nil, errors.New("unexpected storage method")
		}
	})}
	store, err := storage.NewS3(storage.S3Config{Endpoint: "https://synthetic-storage.invalid", Region: "test-region", Bucket: "fixture-bucket", PathStyle: true, AccessKeyID: "fixture-only-key", SecretAccessKey: "fixture-only-secret"}, client, MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Open(ctx, "objects/fixture.wav")
	if err != nil {
		t.Fatal(err)
	}
	f.library.object = object
	f.library.item.StorageKey = "s3:" + uuid.NewString() + "/" + object.Key
	finished := make(chan error, 1)
	go func() {
		source, e := NewResolver(f.library, "unused").OpenResourceSource(ctx, f.source)
		if source.Reader != nil {
			source.Reader.Close()
		}
		finished <- e
	}()
	select {
	case <-body.started:
	case err := <-finished:
		t.Fatal("S3 source did not reach its blocked body", err)
	case <-time.After(2 * time.Second):
		t.Fatal("S3 body did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || body.closed.Load() != 1 {
			t.Fatal("actual S3 reader cancellation leaked or raced", err, body.closed.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actual S3 reader Close did not unblock resource staging")
	}
	resourceStreamAssertNoStage(t, f.dir)
}
