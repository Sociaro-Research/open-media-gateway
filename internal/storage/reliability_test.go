package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestLocalConcurrentLargeArchivesKeepStreamingMemoryBound(t *testing.T) {
	const size = 64 << 20
	const transfers = 4
	chunk := bytes.Repeat([]byte("streaming archive fixture\n"), 4096)
	started := make(chan struct{}, transfers)
	release := make(chan struct{})
	// The provider generates the body from a small reusable chunk, so this
	// allocation measurement cannot accidentally count a full fixture in memory.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", fmt.Sprint(size))
		for left := size; left > 0; {
			n := min(left, len(chunk))
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			left -= n
		}
	}))
	defer server.Close()
	store, err := NewLocal(t.TempDir(), server.Client(), size)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wantHash := sha256.New()
	for left := size; left > 0; {
		n := min(left, len(chunk))
		wantHash.Write(chunk[:n])
		left -= n
	}
	want := hex.EncodeToString(wantHash.Sum(nil))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var wg sync.WaitGroup
	for i := 0; i < transfers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			media, err := store.Archive(ctx, fmt.Sprint(i), server.URL)
			if err != nil {
				t.Error(err)
				return
			}
			if media.SizeBytes != size || media.SHA256 != want {
				t.Errorf("large archive lost integrity: %+v", media)
				return
			}
			object, err := store.Open(ctx, media.StorageKey)
			if err != nil {
				t.Error(err)
				return
			}
			defer object.Reader.Close()
			hash := sha256.New()
			n, err := io.Copy(hash, object.Reader)
			if err != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != want {
				t.Errorf("stored large archive lost bytes: %d %v", n, err)
			}
		}(i)
	}
	for i := 0; i < transfers; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			close(release)
			wg.Wait()
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	wg.Wait()
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	// A generous allocation budget independent of CPU/disk speed. Buffering
	// even one complete body per transfer would exceed it by at least 4x.
	if allocated > 64<<20 {
		t.Fatalf("256 MiB of streaming transfers allocated %d bytes; budget is 64 MiB", allocated)
	}
	t.Logf("four 64 MiB archives retained exact bytes; total Go allocation %.2f MiB", float64(allocated)/(1<<20))
}

func TestLocalInterruptedTransfersLeaveNoPartialResult(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			started := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Length", "1048576")
				w.Write([]byte("partial video"))
				w.(http.Flusher).Flush()
				once.Do(func() { close(started) })
				if cancelled {
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			root := t.TempDir()
			store, err := NewLocal(root, server.Client(), 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := store.Archive(ctx, "interrupted", server.URL); done <- err }()
			<-started
			if cancelled {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("incomplete transfer committed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled archive hung")
			}
			files, err := filepath.Glob(filepath.Join(root, "interrupted", "*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("partial data remains: %v %v", files, err)
			}
		})
	}
}
