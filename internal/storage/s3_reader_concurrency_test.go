package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type s3ReaderTransport func(*http.Request) (*http.Response, error)

func (f s3ReaderTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type s3BlockedBody struct {
	started              chan struct{}
	done                 chan struct{}
	startOnce, closeOnce sync.Once
	closes               atomic.Int32
	staleBytes           bool
}

func newS3BlockedBody(staleBytes bool) *s3BlockedBody {
	return &s3BlockedBody{started: make(chan struct{}), done: make(chan struct{}), staleBytes: staleBytes}
}
func (b *s3BlockedBody) Read(p []byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.done
	if b.staleBytes {
		return copy(p, "old"), nil
	}
	return 0, os.ErrClosed
}
func (b *s3BlockedBody) Close() error {
	b.closes.Add(1)
	b.closeOnce.Do(func() { close(b.done) })
	return nil
}
func s3ReaderResponse(req *http.Request, body io.ReadCloser, length int64) *http.Response {
	return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"video/mp4"}, "Content-Length": {fmt.Sprint(length)}, "ETag": {`"synthetic-etag"`}}, ContentLength: length, Body: body, Request: req}
}
func concurrentS3Reader(t *testing.T, transport http.RoundTripper) *s3Reader {
	t.Helper()
	store, err := NewS3(testS3Config(), &http.Client{Transport: transport}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	etag := `"synthetic-etag"`
	return &s3Reader{ctx: context.Background(), store: store, key: "generated/owner/result.mp4", size: 6, etag: &etag}
}
func waitS3Signal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic S3 operation did not reach synchronization point")
	}
}

type s3ReadResult struct {
	n    int
	err  error
	data string
}

func startS3Read(r *s3Reader) <-chan s3ReadResult {
	done := make(chan s3ReadResult, 1)
	go func() { p := make([]byte, 3); n, err := r.Read(p); done <- s3ReadResult{n, err, string(p[:n])} }()
	return done
}
func waitS3Read(t *testing.T, ch <-chan s3ReadResult) s3ReadResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("Read stayed blocked after cancellation/seek")
		return s3ReadResult{}
	}
}

func TestS3ReaderConcurrentCloseUnblocksBodyAndIsIdempotent(t *testing.T) {
	body := newS3BlockedBody(false)
	calls := atomic.Int32{}
	r := concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return s3ReaderResponse(req, body, 6), nil
	}))
	read := startS3Read(r)
	waitS3Signal(t, body.started)
	closed := make(chan struct{})
	go func() { _ = r.Close(); close(closed) }()
	waitS3Signal(t, closed)
	got := waitS3Read(t, read)
	if got.n != 0 || !errors.Is(got.err, os.ErrClosed) {
		t.Fatal(got)
	}
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Go(func() {
			if err := r.Close(); err != nil {
				t.Errorf("idempotent close: %v", err)
			}
		})
	}
	group.Wait()
	if body.closes.Load() != 1 || calls.Load() != 1 {
		t.Fatal("body close repeated or reader reopened", body.closes.Load(), calls.Load())
	}
	if _, err := r.Seek(0, io.SeekStart); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("closed reader issued another request")
	}
}
func TestS3ReaderConcurrentSeekDiscardsStaleReadAndPreservesRange(t *testing.T) {
	old := newS3BlockedBody(true)
	calls := atomic.Int32{}
	ranges := make(chan string, 2)
	r := concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		ranges <- req.Header.Get("Range")
		if req.Header.Get("If-Match") != `"synthetic-etag"` {
			t.Error("conditional version fence changed")
		}
		if n == 1 {
			return s3ReaderResponse(req, old, 6), nil
		}
		return s3ReaderResponse(req, io.NopCloser(strings.NewReader("2345")), 4), nil
	}))
	defer r.Close()
	read := startS3Read(r)
	waitS3Signal(t, old.started)
	if offset, err := r.Seek(2, io.SeekStart); err != nil || offset != 2 {
		t.Fatal(offset, err)
	}
	stale := waitS3Read(t, read)
	if stale.n != 0 || stale.err == nil {
		t.Fatal("stale read bytes escaped after seek", stale)
	}
	if offset, err := r.Seek(0, io.SeekCurrent); err != nil || offset != 2 {
		t.Fatal("stale read changed seek offset", offset, err)
	}
	p := make([]byte, 3)
	n, err := r.Read(p)
	if err != nil || n != 3 || string(p) != "234" {
		t.Fatal(n, string(p), err)
	}
	if offset, err := r.Seek(0, io.SeekCurrent); err != nil || offset != 5 {
		t.Fatal(offset, err)
	}
	if first, second := <-ranges, <-ranges; first != "bytes=0-" || second != "bytes=2-" {
		t.Fatal(first, second)
	}
	if old.closes.Load() != 1 {
		t.Fatal("seek did not detach/close old body exactly once", old.closes.Load())
	}
}
func TestS3ReaderCloseCancelsPendingGetBeforeBody(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	r := concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		close(cancelled)
		return nil, req.Context().Err()
	}))
	read := startS3Read(r)
	waitS3Signal(t, started)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	waitS3Signal(t, cancelled)
	got := waitS3Read(t, read)
	if got.n != 0 || !errors.Is(got.err, os.ErrClosed) {
		t.Fatal(got)
	}
	r.mu.Lock()
	body, closed := r.body, r.closed
	r.mu.Unlock()
	if body != nil || !closed {
		t.Fatal("late request reopened a closed reader")
	}
}
func TestS3ReaderSeekCancelsPendingGetWithoutLosingNewPosition(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	calls := atomic.Int32{}
	r := concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-req.Context().Done()
			close(cancelled)
			return nil, req.Context().Err()
		}
		if req.Header.Get("Range") != "bytes=2-" {
			t.Error("pending cancelled read overwrote new range")
		}
		return s3ReaderResponse(req, io.NopCloser(strings.NewReader("2345")), 4), nil
	}))
	defer r.Close()
	read := startS3Read(r)
	waitS3Signal(t, started)
	if offset, err := r.Seek(2, io.SeekStart); err != nil || offset != 2 {
		t.Fatal(offset, err)
	}
	waitS3Signal(t, cancelled)
	if got := waitS3Read(t, read); got.n != 0 || got.err == nil {
		t.Fatal(got)
	}
	p := make([]byte, 3)
	if n, err := r.Read(p); err != nil || n != 3 || string(p) != "234" {
		t.Fatal(n, string(p), err)
	}
	if offset, err := r.Seek(0, io.SeekCurrent); err != nil || offset != 5 {
		t.Fatal(offset, err)
	}
}
func TestS3ReaderRacedReadSeekAndCloseKeepStateFenced(t *testing.T) {
	for i := 0; i < 50; i++ {
		body := newS3BlockedBody(true)
		r := concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) { return s3ReaderResponse(req, body, 6), nil }))
		read := startS3Read(r)
		waitS3Signal(t, body.started)
		var group sync.WaitGroup
		group.Go(func() { _, _ = r.Seek(2, io.SeekStart) })
		group.Go(func() { _ = r.Close() })
		group.Go(func() { _ = r.Close() })
		group.Wait()
		got := waitS3Read(t, read)
		if got.n != 0 || got.err == nil {
			t.Fatal("stale bytes escaped concurrent close/seek", got)
		}
		r.mu.Lock()
		closed, present, offset := r.closed, r.body != nil, r.offset
		r.mu.Unlock()
		if !closed || present || offset != 0 && offset != 2 || body.closes.Load() != 1 {
			t.Fatal("closed reader state or offset lost fencing", closed, present, offset, body.closes.Load())
		}
	}
}

func TestS3ReaderLateResponseCannotAttachAfterClose(t *testing.T) {
	body := newS3BlockedBody(false)
	var r *s3Reader
	r = concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		// Close after the transport has produced response state but before GetObject
		// has returned ownership of Body to Read.
		if err := r.Close(); err != nil {
			t.Error(err)
		}
		return s3ReaderResponse(req, body, 6), nil
	}))
	defer body.closeOnce.Do(func() { close(body.done) })
	got := waitS3Read(t, startS3Read(r))
	if got.n != 0 || !errors.Is(got.err, os.ErrClosed) || body.closes.Load() == 0 {
		t.Fatal("late response body escaped close", got, body.closes.Load())
	}
	r.mu.Lock()
	attached, closed := r.body != nil, r.closed
	r.mu.Unlock()
	if attached || !closed {
		t.Fatal("late response reopened closed reader")
	}
}
func TestS3ReaderLateResponseCannotOverwriteSeek(t *testing.T) {
	body := newS3BlockedBody(false)
	calls := atomic.Int32{}
	var r *s3Reader
	r = concurrentS3Reader(t, s3ReaderTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			if offset, err := r.Seek(2, io.SeekStart); err != nil || offset != 2 {
				t.Error(offset, err)
			}
			return s3ReaderResponse(req, body, 6), nil
		}
		if req.Header.Get("Range") != "bytes=2-" {
			t.Error("late response replaced requested seek range")
		}
		return s3ReaderResponse(req, io.NopCloser(strings.NewReader("2345")), 4), nil
	}))
	defer r.Close()
	defer body.closeOnce.Do(func() { close(body.done) })
	if got := waitS3Read(t, startS3Read(r)); got.n != 0 || got.err == nil || body.closes.Load() == 0 {
		t.Fatal("late response escaped seek fencing", got, body.closes.Load())
	}
	if offset, err := r.Seek(0, io.SeekCurrent); err != nil || offset != 2 {
		t.Fatal(offset, err)
	}
	data := make([]byte, 3)
	if n, err := r.Read(data); err != nil || n != 3 || string(data) != "234" {
		t.Fatal(n, string(data), err)
	}
}
