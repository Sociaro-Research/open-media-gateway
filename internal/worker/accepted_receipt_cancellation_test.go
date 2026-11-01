package worker

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

// The reader deliberately ignores context: cancellation must close the storage
// boundary rather than waiting for an in-flight Read to notice it.
type receiptCancellationReader struct {
	started    chan struct{}
	closed     chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func (r *receiptCancellationReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}
func (*receiptCancellationReader) Seek(int64, int) (int64, error) {
	return 0, errors.New("unexpected seek")
}
func (r *receiptCancellationReader) Close() error {
	r.closeCalls.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type receiptCancellationStore struct {
	reader *receiptCancellationReader
	opens  atomic.Int32
}

func (*receiptCancellationStore) Archive(context.Context, string, string) (core.StoredMedia, error) {
	return core.StoredMedia{}, errors.New("unexpected archive")
}
func (s *receiptCancellationStore) Open(context.Context, string) (storage.Object, error) {
	s.opens.Add(1)
	return storage.Object{Reader: s.reader, ContentType: "image/png", SizeBytes: 1}, nil
}

func TestAcceptedReceiptCheckpointCancellationClosesBlockedReader(t *testing.T) {
	for _, frame := range []bool{false, true} {
		name := "artifact"
		if frame {
			name = "last-frame"
		}
		t.Run(name, func(t *testing.T) {
			reader := &receiptCancellationReader{started: make(chan struct{}), closed: make(chan struct{})}
			store := &receiptCancellationStore{reader: reader}
			artifact := core.StoredArtifact{StorageKey: "task/attempts/prior/image.png", ContentType: "image/png", SizeBytes: 1, SHA256: strings.Repeat("a", 64)}
			progress := &receiptArchiveProgress{artifacts: []core.StoredArtifact{artifact}, frame: &artifact}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, restored, err := progress.restored(ctx, store, 0, frame)
				if restored {
					done <- errors.New("canceled checkpoint was accepted")
					return
				}
				done <- err
			}()
			select {
			case <-reader.started:
			case <-time.After(3 * time.Second):
				cancel()
				_ = reader.Close()
				<-done
				t.Fatal("checkpoint did not start reading")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation classification: %v", err)
				}
			case <-time.After(3 * time.Second):
				_ = reader.Close()
				<-done
				t.Fatal("cancellation did not close the blocked checkpoint reader")
			}
			if n := reader.closeCalls.Load(); n != 1 {
				t.Fatalf("reader closed %d times, want exactly once", n)
			}
			if n := store.opens.Load(); n != 1 {
				t.Fatalf("storage opened %d times, want once", n)
			}
		})
	}
}

func TestAcceptedReceiptCheckpointAlreadyCanceledDoesNotOpenStorage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &receiptCancellationStore{}
	progress := &receiptArchiveProgress{artifacts: []core.StoredArtifact{{StorageKey: "task/attempts/prior/image.png"}}}
	_, restored, err := progress.restored(ctx, store, 0, false)
	if restored || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled checkpoint: restored=%v err=%v", restored, err)
	}
	if n := store.opens.Load(); n != 0 {
		t.Fatalf("canceled recovery opened storage %d times", n)
	}
}
