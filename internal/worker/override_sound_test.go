package worker

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func TestOverrideSoundWorkerPublishesOnlyProcessedMedia(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture dependency unavailable")
	}
	dir := t.TempDir()
	video, audio := filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.wav")
	for _, args := range [][]string{{"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-c:a", "aac", video}, {"-f", "lavfi", "-i", "sine=frequency=880:duration=0.5", audio}} {
		if out, err := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture %s: %v", out, err)
		}
	}
	source, _ := os.ReadFile(video)
	sound, _ := os.ReadFile(audio)
	store, err := storage.NewLocal(filepath.Join(dir, "media"), http.DefaultClient, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), owner)
	id := uuid.NewString()
	a, err := store.StoreReference(ctx, id, sound, "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &core.SoundOverride{ReferenceID: id, OwnerID: owner, DataOwnerID: owner, StorageOwnerID: owner, StorageKey: a.StorageKey, SHA256: a.SHA256, SizeBytes: a.SizeBytes}
	task := core.Task{ID: uuid.NewString(), OwnerID: owner, MediaType: "video", Status: core.TaskRunning, Options: core.MediaOptions{OverrideSoundID: id, OverrideSound: snapshot}}
	repo := &repoStub{task: task}
	w := &Worker{repo: repo, storage: store}
	sourceURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(source)
	if err = w.archiveResult(ctx, ctx, task, sourceURL, ""); err != nil {
		t.Fatal(err)
	}
	if repo.task.Status != core.TaskSucceeded || repo.task.StorageKey == nil {
		t.Fatalf("not completed: %+v", repo.task.Error)
	}
	object, err := store.Open(ctx, *repo.task.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	object.Reader.Close()
	// Corrupted replacement must fail without a successful original result.
	broken := *snapshot
	broken.SHA256 = "changed"
	task.Options.OverrideSound = &broken
	task.ID = uuid.NewString()
	repo.task = task
	if err = w.archiveResult(ctx, ctx, task, sourceURL, ""); err != nil {
		t.Fatal(err)
	}
	if repo.task.Status != core.TaskFailed || repo.task.StorageKey != nil {
		t.Fatal("failed override published source")
	}
	// A canceled attempt leaves the running task recoverable; a new attempt works.
	task.ID = uuid.NewString()
	task.Options.OverrideSound = snapshot
	repo.task = task
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = w.archiveResult(canceled, canceled, task, sourceURL, ""); err == nil || repo.task.Status != core.TaskRunning {
		t.Fatal("cancelled attempt was finalized")
	}
	if err = w.archiveResult(ctx, ctx, task, sourceURL, ""); err != nil || repo.task.Status != core.TaskSucceeded {
		t.Fatal("recovery failed", err)
	}
}
