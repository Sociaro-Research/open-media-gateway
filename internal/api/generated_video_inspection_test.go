package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

// Reuse the codec suite's self-contained AAC track, with a two-second primary
// video timeline. Native result duration is intentionally independent.
func generatedVideoInspectionBytes() []byte {
	silent := testmedia.MP4(2000, 1000, 60)
	ftypSize := int(binary.BigEndian.Uint32(silent[:4]))
	ftyp := silent[:ftypSize]
	movie := bytes.Clone(silent[ftypSize+8:])
	track, payload := templateCodecTrack("aac", uint32(len(ftyp)+8))
	movie = append(movie, track...)
	data := append(bytes.Clone(ftyp), testmedia.Atom("mdat", payload)...)
	return append(data, testmedia.Atom("moov", movie)...)
}

func generatedVideoArchivedTask(t *testing.T, f *resourceAPIFixture, nativeDuration float64) core.Task {
	t.Helper()
	ctx := f.context("alice")
	id := uuid.NewString()
	data := generatedVideoInspectionBytes()
	archive, ok := f.s.storage.(storage.SourceArchiver)
	if !ok {
		t.Fatal("fixture storage cannot archive local bytes")
	}
	media, err := archive.ArchiveSource(ctx, id, core.ArtifactSource{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: "video/mp4", SizeBytes: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	handle, nativeAudio := "private-generated-task-handle", true
	task := core.Task{ID: id, OwnerID: identity.OwnerID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx), Provider: "kling", ProviderIdentity: resourceAPIRevision, Model: "reviewed-generated-video", MediaType: "video", Status: core.TaskSucceeded, ProviderTaskID: &handle, CreatedAt: time.Now().UTC(), HasStoredResult: true,
		Options:         core.MediaOptions{GenerateAudio: &nativeAudio, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{"https://provider-claim.invalid/video": {Type: "video", Duration: 99, AudioCodecs: []string{"provider-claimed-opus"}}}},
		StoredArtifacts: []core.StoredArtifact{{ID: "1", ProviderID: "private-primary-native-video", MediaType: "video", Duration: nativeDuration, StorageKey: media.StorageKey, ContentType: media.ContentType, SizeBytes: media.SizeBytes, SHA256: media.SHA256}},
	}
	return task
}

func generatedVideoInspectionRequest(t *testing.T, f *resourceAPIFixture, task core.Task, allowUnknown bool) (*core.ResourceCreateRequest, error) {
	t.Helper()
	f.tasks.tasks[task.ID] = task
	intent := &core.ResourceIntent{Kind: "file_resource", Subtype: "video", Name: "Owned archived output", GeneratedTaskID: task.ID}
	model := core.MediaModel{ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"video"}, Variants: []core.ResourceVariantSchema{{Subtype: "video", GeneratedSources: []core.GeneratedResourceRule{{Model: task.Model, AllowUnknownDuration: allowUnknown, MaxDuration: 10}}}}}}
	r := httptest.NewRequest("POST", "/v1/provider-resources", nil).WithContext(f.context("alice"))
	return f.s.resolveResourceCreation(r, intent, "kling", model)
}

func TestGeneratedVideoInspectionBindsPrimaryArchiveAndPreservesNativeDuration(t *testing.T) {
	for _, native := range []float64{0, 2, 4} {
		t.Run(map[float64]string{0: "native-unknown", 2: "native-matches-container", 4: "native-independent"}[native], func(t *testing.T) {
			f := newResourceAPIFixture(t)
			task := generatedVideoArchivedTask(t, f, native)
			primary := task.StoredArtifacts[0]
			secondary := primary
			secondary.ID = "2"
			secondary.ProviderID = "private-secondary-native-video"
			secondary.Duration = 9
			task.StoredArtifacts = append(task.StoredArtifacts, secondary)
			beforeArtifacts := append([]core.StoredArtifact(nil), task.StoredArtifacts...)
			beforeClaims := core.CloneInputMediaEvidence(task.Options.ReferenceMediaEvidence)
			result, err := generatedVideoInspectionRequest(t, f, task, true)
			if err != nil || result.GeneratedVideo == nil || result.GeneratedVideo.Inspection == nil {
				t.Fatal(result, err)
			}
			got := result.GeneratedVideo
			proof := got.Inspection
			sum := sha256.Sum256(generatedVideoInspectionBytes())
			if got.TaskID != task.ID || got.OutputID != primary.ProviderID || got.TaskHandle != *task.ProviderTaskID || got.SHA256 != primary.SHA256 || proof.ReferenceID != task.ID || proof.SHA256 != hex.EncodeToString(sum[:]) || proof.Type != "video" || proof.ContentType != "video/mp4" || proof.SizeBytes != primary.SizeBytes || proof.Duration != 2 || proof.FrameCount != 60 || proof.VideoStreams == nil || *proof.VideoStreams != 1 || proof.AudioStreams == nil || *proof.AudioStreams != 1 || !reflect.DeepEqual(proof.AudioCodecs, []string{"aac"}) {
				t.Fatal("archive proof/native primary identity lost", got)
			}
			if native == 0 && got.Duration != nil || native > 0 && (got.Duration == nil || *got.Duration != native) {
				t.Fatal("inspected timeline replaced native duration", got.Duration)
			}
			cloned := core.CloneResourceCreation(result)
			cloned.GeneratedVideo.Inspection.AudioCodecs[0] = "opus"
			*cloned.GeneratedVideo.Inspection.AudioStreams = 7
			if got.Inspection.AudioCodecs[0] != "aac" || *got.Inspection.AudioStreams != 1 {
				t.Fatal("legacy source clone shared private inspection")
			}
			proof.AudioCodecs[0] = "mutated-output"
			*proof.AudioStreams = 9
			again, err := generatedVideoInspectionRequest(t, f, task, true)
			if err != nil || again.GeneratedVideo.Inspection.AudioCodecs[0] != "aac" || *again.GeneratedVideo.Inspection.AudioStreams != 1 || !reflect.DeepEqual(beforeArtifacts, task.StoredArtifacts) || !reflect.DeepEqual(beforeClaims, task.Options.ReferenceMediaEvidence) {
				t.Fatal("inspection/output mutation changed original evidence", err)
			}
			if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
				t.Fatal("inspection submitted paid work")
			}
			public, _ := json.Marshal(core.GenerationView(task))
			if strings.Contains(string(public), "audio_codecs") || strings.Contains(string(public), primary.ProviderID) {
				t.Fatal("private archived inspection/native ID exposed")
			}
		})
	}
}

type generatedInspectionStore struct {
	storage.Store
	opened                                  int
	key, owner, providerOwner, storageOwner string
	change                                  func(*storage.Object)
	err                                     error
}

func (s *generatedInspectionStore) Open(ctx context.Context, key string) (storage.Object, error) {
	s.opened++
	s.key = key
	s.owner = identity.OwnerID(ctx)
	s.providerOwner = identity.ProviderOwnerID(ctx)
	s.storageOwner = identity.StorageOwnerID(ctx)
	if s.err != nil {
		return storage.Object{}, s.err
	}
	object, err := s.Store.Open(ctx, key)
	if err == nil && s.change != nil {
		s.change(&object)
	}
	return object, err
}

func TestGeneratedVideoInspectionRejectsForeignScopeAndTamperedArchive(t *testing.T) {
	for _, variant := range []string{"foreign-owner", "wrong-provider", "wrong-provider-owner", "stale-revision", "deleted", "not-complete", "expired", "missing-primary", "primary-not-video", "secondary-cannot-replace-primary", "hash", "size", "type", "storage-type", "storage-size", "truncated", "extra-byte", "wrong-bytes", "fake-container", "storage-error"} {
		t.Run(variant, func(t *testing.T) {
			f := newResourceAPIFixture(t)
			task := generatedVideoArchivedTask(t, f, 4)
			observed := &generatedInspectionStore{Store: f.s.storage}
			f.s.storage = observed
			replace := func(data []byte) {
				observed.change = func(o *storage.Object) {
					_ = o.Reader.Close()
					o.Reader = &generatedInspectionReader{Reader: bytes.NewReader(data)}
				}
			}
			switch variant {
			case "foreign-owner":
				task.OwnerID = identity.OwnerID(f.context("bob"))
			case "wrong-provider":
				task.Provider = "other-connection"
			case "wrong-provider-owner":
				task.ProviderOwnerID = "other-owner"
			case "stale-revision":
				task.ProviderIdentity = "retired-revision"
			case "deleted":
				now := time.Now()
				task.ResultDeletedAt = &now
			case "not-complete":
				task.Status = core.TaskRunning
			case "expired":
				task.CreatedAt = time.Now().Add(-31 * 24 * time.Hour)
			case "missing-primary":
				task.StoredArtifacts = nil
			case "primary-not-video":
				task.StoredArtifacts[0].MediaType = "image"
			case "secondary-cannot-replace-primary":
				secondary := task.StoredArtifacts[0]
				secondary.ID = "2"
				task.StoredArtifacts = append(task.StoredArtifacts, secondary)
				task.StoredArtifacts[0].SHA256 = strings.Repeat("f", 64)
			case "hash":
				task.StoredArtifacts[0].SHA256 = strings.Repeat("f", 64)
			case "size":
				task.StoredArtifacts[0].SizeBytes++
			case "type":
				task.StoredArtifacts[0].ContentType = "image/png"
			case "storage-type":
				observed.change = func(o *storage.Object) { o.ContentType = "video/webm" }
			case "storage-size":
				observed.change = func(o *storage.Object) { o.SizeBytes++ }
			case "truncated":
				data := generatedVideoInspectionBytes()
				replace(data[:len(data)-1])
			case "extra-byte":
				replace(append(generatedVideoInspectionBytes(), 0))
			case "wrong-bytes":
				data := generatedVideoInspectionBytes()
				data[len(data)-1] ^= 1
				replace(data)
			case "fake-container":
				data := bytes.Repeat([]byte{0}, len(generatedVideoInspectionBytes()))
				sum := sha256.Sum256(data)
				task.StoredArtifacts[0].SHA256 = hex.EncodeToString(sum[:])
				replace(data)
			case "storage-error":
				observed.err = errors.New("https://private-fixture.invalid/output?signature=secret")
			}
			result, err := generatedVideoInspectionRequest(t, f, task, true)
			if err == nil || result != nil {
				t.Fatal("foreign/tampered source authorized", result)
			}
			if !errors.Is(err, providerresources.ErrInvalid) && !errors.Is(err, providerresources.ErrNotFound) && !errors.Is(err, core.ErrTaskNotFound) {
				t.Fatal("unexpected unsafe error classification", err)
			}
			if strings.Contains(err.Error(), "signature=") || strings.Contains(err.Error(), "private-fixture.invalid") {
				t.Fatal("private storage diagnostic exposed", err)
			}
			if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
				t.Fatal("rejected inspection submitted work")
			}
		})
	}
}

type generatedInspectionReader struct{ *bytes.Reader }

func (*generatedInspectionReader) Close() error { return nil }

func TestGeneratedVideoInspectionReadsAcceptedOwnerStorageContext(t *testing.T) {
	f := newResourceAPIFixture(t)
	task := generatedVideoArchivedTask(t, f, 4)
	task.StorageOwnerID = "accepted-storage-owner"
	observed := &generatedInspectionStore{Store: f.s.storage}
	f.s.storage = observed
	_, err := generatedVideoInspectionRequest(t, f, task, true)
	if err != nil || observed.opened != 1 || observed.key != task.StoredArtifacts[0].StorageKey || observed.owner != task.OwnerID || observed.providerOwner != task.ProviderOwnerID || observed.storageOwner != task.StorageOwnerID {
		t.Fatal("archive read lost accepted owner scope", observed, err)
	}
}

type generatedInspectionBlockingReader struct {
	started              chan struct{}
	closed               chan struct{}
	startOnce, closeOnce sync.Once
	closes               atomic.Int32
}

func (r *generatedInspectionBlockingReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}
func (*generatedInspectionBlockingReader) Seek(int64, int) (int64, error) {
	return 0, errors.New("unexpected seek")
}
func (r *generatedInspectionBlockingReader) Close() error {
	r.closes.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func TestGeneratedVideoInspectionCancellationClosesBlockedArchive(t *testing.T) {
	f := newResourceAPIFixture(t)
	task := generatedVideoArchivedTask(t, f, 4)
	reader := &generatedInspectionBlockingReader{started: make(chan struct{}), closed: make(chan struct{})}
	observed := &generatedInspectionStore{Store: f.s.storage, change: func(o *storage.Object) { _ = o.Reader.Close(); o.Reader = reader }}
	f.s.storage = observed
	ctx, cancel := context.WithCancel(f.context("alice"))
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.s.inspectGeneratedVideo(ctx, task, task.StoredArtifacts[0]); done <- err }()
	select {
	case <-reader.started:
	case <-time.After(3 * time.Second):
		cancel()
		_ = reader.Close()
		<-done
		t.Fatal("archive read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation classification lost", err)
		}
	case <-time.After(3 * time.Second):
		_ = reader.Close()
		<-done
		t.Fatal("cancellation did not close blocked archive")
	}
	if reader.closes.Load() != 1 {
		t.Fatal("archive reader closed more than once", reader.closes.Load())
	}
}
