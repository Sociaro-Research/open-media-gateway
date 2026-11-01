package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

type binaryArtifactProvider struct {
	multiResultProvider
	calls       int
	closed      bool
	kind, owner string
}

func (p *binaryArtifactProvider) OpenArtifact(ctx context.Context, ref string) (core.ArtifactSource, error) {
	p.calls++
	p.owner = identity.OwnerID(ctx)
	return core.ArtifactSource{Reader: artifactCloseReader{strings.NewReader("<html>opaque provider tracking</html>"), &p.closed}, ContentType: p.kind, SizeBytes: -1}, nil
}
func TestBinaryArtifactRequiresPrivateProviderSourceBeforeStorage(t *testing.T) {
	for _, artifact := range []core.ProviderArtifact{{MediaType: "binary", URL: "https://cdn.example.test/tracking"}, {MediaType: "binary", Text: "opaque"}, {MediaType: "binary", URL: "https://cdn.example.test/tracking", SourceReference: "private"}} {
		repo := &multiResultRepo{repoStub: repoStub{task: core.Task{ID: "binary", Status: core.TaskRunning}}}
		store := &multiStore{}
		provider := &binaryArtifactProvider{}
		w := &Worker{repo: repo, storage: store}
		if err := w.archiveArtifacts(context.Background(), context.Background(), provider, repo.task, "", "", []core.ProviderArtifact{artifact}); err != nil {
			t.Fatal(err)
		}
		if repo.task.Status != core.TaskFailed || provider.calls != 0 || len(store.paths) != 0 {
			t.Fatal("invalid binary source reached storage", repo.task, provider.calls, store.paths)
		}
	}
}
func TestBinaryArtifactArchivesOpaqueBytesPrivatelyAndChecksMIME(t *testing.T) {
	for _, kind := range []string{"application/octet-stream", "application/json", "text/html", "image/png"} {
		t.Run(kind, func(t *testing.T) {
			repo := &multiResultRepo{repoStub: repoStub{task: core.Task{ID: "binary-task", Status: core.TaskRunning, OwnerID: "accepted-owner"}}}
			local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 1024)
			if err != nil {
				t.Fatal(err)
			}
			p := &binaryArtifactProvider{kind: kind}
			w := &Worker{repo: repo, storage: local}
			ctx := identity.WithOwnerID(core.WithClaimToken(context.Background(), "accepted-claim"), repo.task.OwnerID)
			artifact := core.ProviderArtifact{ID: "selection_tracking", MediaType: "binary", Format: "bin", SourceReference: "private-opaque-reference"}
			if err := w.archiveArtifacts(ctx, ctx, p, repo.task, "", "", []core.ProviderArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
			if p.calls != 1 || !p.closed || p.owner != "accepted-owner" {
				t.Fatal(p)
			}
			if kind != "application/octet-stream" {
				if repo.task.Status != core.TaskFailed || len(repo.stored.Artifacts) != 0 {
					t.Fatal("binary MIME mismatch published", repo.task)
				}
				return
			}
			if repo.task.Status != core.TaskSucceeded || len(repo.stored.Artifacts) != 1 || repo.stored.SourceURL != "" || repo.task.ProviderResultURL != nil {
				t.Fatal(repo.task, repo.stored)
			}
			a := repo.stored.Artifacts[0]
			if a.MediaType != "binary" || a.ContentType != kind || a.Format != "bin" || a.ID != "1" || !strings.Contains(a.StorageKey, "/attempts/accepted-claim/") || strings.Contains(a.StorageKey, artifact.SourceReference) || a.SHA256 == "" {
				t.Fatal(a)
			}
			object, err := local.Open(ctx, a.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(object.Reader)
			object.Reader.Close()
			if err != nil || string(data) != "<html>opaque provider tracking</html>" {
				t.Fatal(err)
			}
		})
	}
}
