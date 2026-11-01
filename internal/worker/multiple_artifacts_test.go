package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type multiResultProvider struct{ immediate bool }

func (multiResultProvider) Name() string { return "multi" }
func multiArtifacts() []core.ProviderArtifact {
	return []core.ProviderArtifact{{ID: "../../provider-image", MediaType: "image", Format: "png", URL: "https://cdn.example/a.png", GroupID: "series"}, {ID: "opaque-9007199254740993", MediaType: "image", Format: "png", URL: "https://cdn.example/b.png", GroupID: "series"}}
}
func (p multiResultProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	if p.immediate {
		return core.ProviderTask{ID: "remote", Artifacts: multiArtifacts()}, nil
	}
	return core.ProviderTask{ID: "remote"}, nil
}
func (multiResultProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{Status: core.ProviderSucceeded, Artifacts: multiArtifacts()}, nil
}

type multiResultRepo struct {
	repoStub
	stored core.StoredMedia
}

func (r *multiResultRepo) MarkSucceeded(ctx context.Context, id string, m core.StoredMedia, last string) error {
	r.stored = m
	r.task.StoredArtifacts = m.Artifacts
	return r.repoStub.MarkSucceeded(ctx, id, m, last)
}

type multiStore struct {
	paths, owners []string
	fail          int
	cancel        context.CancelFunc
}

func (s *multiStore) Archive(ctx context.Context, id, url string) (core.StoredMedia, error) {
	s.paths = append(s.paths, id)
	s.owners = append(s.owners, identity.OwnerID(ctx))
	if len(s.paths) == s.fail {
		if s.cancel != nil {
			s.cancel()
			return core.StoredMedia{}, context.Canceled
		}
		return core.StoredMedia{}, errors.New("synthetic second archive failure")
	}
	return core.StoredMedia{StorageKey: id + "/result.png", ContentType: "image/png", SizeBytes: 12, SHA256: "fixture"}, nil
}
func TestAllProviderArtifactsArchivedBeforeSuccess(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(map[bool]string{true: "immediate", false: "polled"}[immediate], func(t *testing.T) {
			repo := &multiResultRepo{repoStub: repoStub{task: core.Task{ID: "task-multi", Status: core.TaskQueued, Provider: "multi", OwnerID: "owner-one", MediaType: "image"}}}
			store := &multiStore{}
			w := New(repo, nil, multiResultProvider{immediate: immediate}, store, time.Millisecond, time.Second, 2, time.Second)
			for step := 0; step < 3 && repo.task.Status != core.TaskSucceeded && repo.task.Status != core.TaskFailed; step++ {
				err := w.process(core.WithClaimToken(context.Background(), "claim-fixture"), repo.task.ID)
				var pending *pollLater
				if err != nil && !errors.As(err, &pending) {
					t.Fatal(err)
				}
			}
			if repo.task.Status != core.TaskSucceeded || len(repo.stored.Artifacts) != 2 || len(store.paths) != 2 {
				t.Fatal("array result lost or committed early", repo.task.Status, repo.task.Error, repo.stored, store.paths)
			}
			for i, path := range store.paths {
				if !strings.HasPrefix(path, "task-multi/attempts/claim-fixture") || strings.Contains(path, "provider-image") || store.owners[i] != "owner-one" {
					t.Fatal("unsafe path or wrong owner", store.paths, store.owners)
				}
			}
			if repo.stored.Artifacts[0].ID != "1" || repo.stored.Artifacts[1].ID != "2" || repo.stored.Artifacts[1].ProviderID != "opaque-9007199254740993" {
				t.Fatal(repo.stored.Artifacts)
			}
			if repo.task.StorageKey == nil || *repo.task.StorageKey != repo.stored.Artifacts[0].StorageKey {
				t.Fatal("scalar primary compatibility lost")
			}
		})
	}
}
func TestPartialArtifactFailureNeverPublishesSuccess(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		repo := &multiResultRepo{repoStub: repoStub{task: core.Task{ID: "partial", Status: core.TaskRunning, MediaType: "image"}}}
		store := &multiStore{fail: 2}
		if cancelled {
			store.cancel = cancel
		}
		w := &Worker{repo: repo, storage: store, taskTimeout: time.Second, maxPollErrors: 2}
		// This exercises the normalized multi-result path directly after polling.
		providerID := "remote"
		repo.task.ProviderTaskID = &providerID
		repo.task.Provider = "multi"
		w.providers = map[string]core.MediaProvider{"multi": multiResultProvider{}}
		err := w.process(ctx, repo.task.ID)
		if cancelled && !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		if repo.task.Status == core.TaskSucceeded || len(repo.stored.Artifacts) != 0 {
			t.Fatal("partial archive published", repo.task)
		}
	}
}
