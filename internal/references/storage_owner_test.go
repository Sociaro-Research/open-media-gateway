package references

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

type physicalSettings struct{ record storage.SettingsRecord }

func (r *physicalSettings) Get(context.Context) (storage.SettingsRecord, error) { return r.record, nil }
func (r *physicalSettings) Connection(context.Context, string) (storage.SettingsRecord, error) {
	return r.record, nil
}
func (r *physicalSettings) Save(_ context.Context, record storage.SettingsRecord) error {
	r.record = record
	return nil
}

type physicalRepository struct{ item Image }

func (r *physicalRepository) Get(ctx context.Context, id string) (Image, error) {
	if identity.OwnerID(ctx) != templates.LibraryOwnerID || id != r.item.ID {
		return Image{}, storage.ErrObjectNotFound
	}
	return r.item, nil
}
func (r *physicalRepository) List(context.Context, int, int) ([]Image, error) {
	return []Image{r.item}, nil
}
func (r *physicalRepository) Insert(context.Context, Image) error {
	return errors.New("unexpected insert")
}
func (r *physicalRepository) Delete(context.Context, string) error { return nil }
func (r *physicalRepository) FindByHash(context.Context, string) (Image, error) {
	return Image{}, storage.ErrObjectNotFound
}
func (r *physicalRepository) UnhashedBySize(context.Context, int64) ([]Image, error) { return nil, nil }
func (r *physicalRepository) SetHash(context.Context, string, string) error          { return nil }

type physicalS3 struct{ data []byte }

func (s *physicalS3) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{"Content-Type": []string{"video/mp4"}, "Content-Length": []string{strconv.Itoa(len(s.data))}}
	if r.Method == "PUT" {
		data, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		s.data = data
		h.Set("Content-Length", "0")
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	}
	body := io.NopCloser(bytes.NewReader(s.data))
	if r.Method == http.MethodHead {
		body = http.NoBody
	}
	return &http.Response{StatusCode: 200, Header: h, Body: body, ContentLength: int64(len(s.data)), Request: r}, nil
}

func TestPhysicalS3HEADReturnsMetadataWithoutBody(t *testing.T) {
	data := []byte{0, 0, 0, 8, 'f', 't', 'y', 'p'}
	transport := &physicalS3{data: data}
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			request, err := http.NewRequest(method, "https://storage.fixture.test/fixture/object", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.ContentLength != int64(len(data)) || response.Header.Get("Content-Length") != strconv.Itoa(len(data)) || response.Header.Get("Content-Type") != "video/mp4" {
				t.Fatal("HEAD and GET must retain the object's metadata", response)
			}
			got, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if method == http.MethodHead {
				if response.Body != http.NoBody || len(got) != 0 {
					t.Fatal("HEAD must return http.NoBody, retaining the equivalent GET length", got)
				}
			} else if !bytes.Equal(got, data) {
				t.Fatal("GET changed the binary object bytes", got)
			}
		})
	}
}
func TestMigratedS3ReferenceUsesTrustedPhysicalOwner(t *testing.T) {
	for _, owner := range []string{identity.SharedOwnerID, templates.LibraryOwnerID} {
		t.Run(owner, func(t *testing.T) {
			ctx := identity.WithResourceOwners(identity.WithDataOwnerID(context.Background(), owner), identity.SharedOwnerID, identity.SharedOwnerID)
			cipher, e := providerconfig.NewCipher("synthetic-test-key")
			if e != nil {
				t.Fatal(e)
			}
			transport := &physicalS3{}
			settings := &physicalSettings{}
			manager, e := storage.NewManager(settings, cipher, t.TempDir(), &http.Client{Transport: transport}, 1<<20)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = manager.Update(ctx, storage.Update{Driver: "s3", Endpoint: "https://storage.fixture.test", Region: "test", Bucket: "fixture", Prefix: "retained", PathStyle: true, AccessKeyID: "fixture-key", SecretAccessKey: "fixture-secret"}); e != nil {
				t.Fatal(e)
			}
			id := uuid.NewString()
			data := testmedia.MP4(5000, 1000, 150)
			stored, e := manager.StoreTemplateMedia(ctx, id, data, "video/mp4")
			if e != nil {
				t.Fatal(e)
			}
			canonical := identity.WithResourceOwners(identity.WithDataOwnerID(context.Background(), templates.LibraryOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
			if owner != templates.LibraryOwnerID {
				if _, e = manager.Open(canonical, stored.StorageKey); !errors.Is(e, storage.ErrObjectNotFound) {
					t.Fatal("fixture did not reproduce owner mismatch", e)
				}
			}
			repo := &physicalRepository{item: Image{ID: id, Name: "retained.mp4", StorageOwnerID: owner, ContentType: "video/mp4", SizeBytes: int64(len(data)), SHA256: stored.SHA256, StorageKey: stored.StorageKey}}
			library := New(repo, manager)
			_, object, e := library.Open(canonical, id)
			if e != nil {
				t.Fatal(e)
			}
			got, e := io.ReadAll(object.Reader)
			object.Reader.Close()
			if e != nil || !bytes.Equal(got, data) {
				t.Fatal("bytes changed", e)
			}
			if _, e = library.Inspect(canonical, id); e != nil {
				t.Fatal("binding inspection failed", e)
			}
			if u, e := library.DirectURL(canonical, id, "GET", time.Minute); e != nil || u == "" {
				t.Fatal("signed preview failed", e)
			}
			if _, _, e = library.Open(identity.WithOwnerID(context.Background(), uuid.NewString()), id); !errors.Is(e, storage.ErrObjectNotFound) {
				t.Fatal("private namespace escaped", e)
			}
		})
	}
}
