package providers

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
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/google/uuid"
)

type extendedSourceLibrary struct {
	owner, path string
	item        references.Image
	calls       int
}

func (l *extendedSourceLibrary) Open(ctx context.Context, id string) (references.Image, storage.Object, error) {
	l.calls++
	if identity.OwnerID(ctx) != l.owner || id != l.item.ID {
		return references.Image{}, storage.Object{}, storage.ErrObjectNotFound
	}
	file, err := os.Open(l.path)
	if err != nil {
		return references.Image{}, storage.Object{}, err
	}
	return l.item, storage.Object{Reader: file, ContentType: l.item.ContentType, SizeBytes: l.item.SizeBytes}, nil
}

type extendedSourceAdapter struct {
	models    []core.MediaModel
	source    core.ResourceSourceInput
	listCalls *int
	tamper    string
}

func (*extendedSourceAdapter) Name() string { return "sourceprobe" }
func (a *extendedSourceAdapter) ListModels(context.Context) ([]core.MediaModel, error) {
	*a.listCalls++
	return nil, errors.New("dispatch must not invoke discovery")
}
func (a *extendedSourceAdapter) ReviewedCatalogModels() []core.MediaModel { return a.models }
func (a *extendedSourceAdapter) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{}, errors.New("unused")
}
func (a *extendedSourceAdapter) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	opener, ok := core.ResourceSourceOpenerFromContext(ctx)
	if !ok {
		return core.ProviderTask{}, errors.New("host opener missing")
	}
	switch a.tamper {
	case "owner":
		ctx = identity.WithOwnerID(ctx, "foreign-owner")
	case "connection":
		scope, _ := core.ProviderConnectionScopeFromContext(ctx)
		scope.ID = "foreign-connection"
		ctx = core.WithProviderConnectionScope(ctx, scope)
	case "revision":
		scope, _ := core.ProviderConnectionScopeFromContext(ctx)
		scope.Revision = "foreign-revision"
		ctx = core.WithProviderConnectionScope(ctx, scope)
	}
	source, err := opener.OpenResourceSource(ctx, a.source)
	if err != nil {
		return core.ProviderTask{}, err
	}
	defer source.Reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, source.Reader)
	if err != nil || size != a.source.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != a.source.SHA256 {
		return core.ProviderTask{}, errors.New("source bytes changed")
	}
	return core.ProviderTask{ID: "fixture-source-verified"}, nil
}

func TestManagerScopesExtendedSourceFromActualPureCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("runtime-generated 200 MiB boundary fixture")
	}
	file, err := os.CreateTemp(t.TempDir(), "source-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header := testmedia.MP4(5000, 1000, 150)
	index := bytes.Index(header, []byte("tkhd"))
	binary.BigEndian.PutUint32(header[index+4+76:], 1280<<16)
	binary.BigEndian.PutUint32(header[index+4+80:], 720<<16)
	file.Write(header)
	padding := make([]byte, 8)
	binary.BigEndian.PutUint32(padding, uint32(references.MaxBytes+1-int64(len(header))))
	copy(padding[4:], "free")
	file.Write(padding)
	if err = file.Truncate(references.MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Seek(0, io.SeekStart)
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	item := references.Image{ID: uuid.NewString(), ContentType: "video/mp4", StorageKey: "private-source", SizeBytes: references.MaxBytes + 1, SHA256: hex.EncodeToString(hash.Sum(nil))}
	library := &extendedSourceLibrary{owner: owner, path: file.Name(), item: item}
	resolver := references.NewResolver(library, "fixture-only")
	native, err := kling.New("fixture", "https://api-singapore.klingai.com", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("native provider network invoked")
		return nil, errors.New("forbidden")
	})})
	if err != nil {
		t.Fatal(err)
	}
	var model core.MediaModel
	for _, m := range native.ReviewedCatalogModels() {
		if m.ID == kling.OmniVideo4 {
			model = m
		}
	}
	if model.ID == "" {
		t.Fatal("native model missing")
	}
	source := core.ResourceSourceInput{ReferenceID: item.ID, ContentType: item.ContentType, MediaType: "video", SizeBytes: item.SizeBytes, SHA256: item.SHA256}
	for _, tc := range []struct {
		name, requested, tamper string
		manual                  bool
		allowed                 bool
	}{{name: "reviewed exact operation", requested: model.ID, allowed: true}, {name: "unknown operation", requested: "unknown"}, {name: "manual connection", requested: model.ID, manual: true}, {name: "foreign owner", requested: model.ID, tamper: "owner"}, {name: "changed connection", requested: model.ID, tamper: "connection"}, {name: "changed revision", requested: model.ID, tamper: "revision"}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
			cipher, _ := providerconfig.NewCipher("source-model-test")
			manager := NewManager(repo, cipher, config.Config{}, resolver)
			listCalls := 0
			manager.registry = registry.MustRegistry(providerkit.Definition{ID: "sourceprobe", Name: "Source probe", Fields: []providerkit.Field{{Key: "api_key", Required: true, Secret: true}}, Factory: func(Credentials, providerkit.Settings, *http.Client) (core.MediaProvider, error) {
				return &extendedSourceAdapter{models: []core.MediaModel{model}, source: source, listCalls: &listCalls, tamper: tc.tamper}, nil
			}})
			ctx := identity.WithOwnerID(context.Background(), owner)
			connection := "sourceprobe--" + uuid.NewString()
			credentials := Credentials{APIKey: "fixture-key", Connection: &providerkit.Connection{ManualCatalog: tc.manual}}
			encrypted, err := manager.encryptCredentials(credentials)
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.Upsert(ctx, providerconfig.Record{ProviderID: connection, Enabled: true, CredentialsCiphertext: encrypted}); err != nil {
				t.Fatal(err)
			}
			provider, err := manager.Provider(ctx, connection)
			if err != nil {
				t.Fatal(err)
			}
			before := library.calls
			result, err := provider.Generate(ctx, core.MediaRequest{Model: tc.requested})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t result=%+v err=%v", tc.allowed, result, err)
			}
			if !tc.allowed && library.calls != before {
				t.Fatal("invalid host proof opened storage")
			}
			if listCalls != 0 {
				t.Fatal("dispatch performed model discovery")
			}
		})
	}
}
