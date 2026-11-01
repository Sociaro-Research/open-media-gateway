package worker

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"io"
	"strings"
	"testing"
)

type openedArtifactProvider struct {
	multiResultProvider
	calls  int
	owner  string
	closed bool
}
type artifactCloseReader struct {
	io.Reader
	closed *bool
}

func (r artifactCloseReader) Close() error { *r.closed = true; return nil }
func (p *openedArtifactProvider) OpenArtifact(ctx context.Context, handle string) (core.ArtifactSource, error) {
	p.calls++
	p.owner = identity.OwnerID(ctx)
	if handle != "opaque-signed-handle" {
		return core.ArtifactSource{}, errors.New("foreign handle")
	}
	return core.ArtifactSource{Reader: artifactCloseReader{strings.NewReader("file bytes"), &p.closed}, ContentType: "image/png", SizeBytes: 10}, nil
}

type streamArtifactStore struct {
	multiStore
	content, kind string
	size          int64
}

func (s *streamArtifactStore) ArchiveSource(ctx context.Context, id string, source core.ArtifactSource) (core.StoredMedia, error) {
	defer source.Reader.Close()
	b, err := io.ReadAll(source.Reader)
	if err != nil {
		return core.StoredMedia{}, err
	}
	s.content = string(b)
	s.kind = source.ContentType
	s.size = source.SizeBytes
	return core.StoredMedia{StorageKey: id + "/result", ContentType: source.ContentType, SizeBytes: int64(len(b)), SHA256: "synthetic"}, nil
}
func TestAuthenticatedArtifactUsesResolvedProviderAndTrustedStream(t *testing.T) {
	p := &openedArtifactProvider{}
	store := &streamArtifactStore{}
	ctx := identity.WithOwnerID(context.Background(), "accepted-owner")
	m, err := archiveProviderArtifact(ctx, store, p, "task/attempts/claim", core.ProviderArtifact{MediaType: "image", SourceReference: "opaque-signed-handle"})
	if err != nil || p.calls != 1 || p.owner != "accepted-owner" || !p.closed || store.content != "file bytes" || m.SourceURL != "" || len(store.paths) != 0 {
		t.Fatal(m, err, p, store)
	}
	if _, err = archiveProviderArtifact(ctx, store, p, "task", core.ProviderArtifact{SourceReference: "tampered"}); err == nil || len(store.paths) != 0 {
		t.Fatal("opaque handle fell through to HTTP", err)
	}
	if _, err = archiveProviderArtifact(ctx, store, multiResultProvider{}, "task", core.ProviderArtifact{SourceReference: "opaque-signed-handle"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if _, err = archiveProviderArtifact(ctx, &multiStore{}, p, "task", core.ProviderArtifact{SourceReference: "opaque-signed-handle"}); err == nil || p.calls != 2 {
		t.Fatal("opened before checking storage support", err, p.calls)
	}
}
func TestTextAndJSONArtifactsUseTypedStream(t *testing.T) {
	for _, kind := range []string{"text", "json"} {
		store := &streamArtifactStore{}
		text := "typed context"
		mime := "text/plain"
		if kind == "json" {
			text = `{"result":"yes"}`
			mime = "application/json"
		}
		_, err := archiveProviderArtifact(context.Background(), store, nil, "task", core.ProviderArtifact{MediaType: kind, Text: text})
		if err != nil || store.content != text || store.kind != mime || store.size != int64(len(text)) || len(store.paths) != 0 {
			t.Fatal(err, store)
		}
	}
}
func TestArtifactSourceVariantsAreExclusiveBeforeStorage(t *testing.T) {
	for _, a := range []core.ProviderArtifact{
		{MediaType: "image", URL: "https://cdn.example.test/a.png", SourceReference: "opaque"},
		{MediaType: "text", URL: "https://cdn.example.test/a", Text: "context"},
		{MediaType: "image", Text: "fake image"},
	} {
		repo := &multiResultRepo{repoStub: repoStub{task: core.Task{ID: "variants", Status: core.TaskRunning}}}
		store := &multiStore{}
		w := &Worker{repo: repo, storage: store}
		if err := w.archiveArtifacts(context.Background(), context.Background(), nil, repo.task, "", "", []core.ProviderArtifact{a}); err != nil {
			t.Fatal(err)
		}
		if repo.task.Status != core.TaskFailed || len(store.paths) != 0 {
			t.Fatal("invalid source archived", repo.task, store)
		}
	}
}
