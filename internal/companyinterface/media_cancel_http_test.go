package companyinterface_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

type stalledMediaReader struct {
	storage.ReadSeekCloser
	read                      int
	entered, closed, released chan struct{}
	enterOnce, closeOnce      sync.Once
}

func (r *stalledMediaReader) Read(p []byte) (int, error) {
	if r.read >= 256<<10 {
		r.enterOnce.Do(func() { close(r.entered) })
		<-r.closed
		close(r.released)
		return 0, context.Canceled
	}
	n, e := r.ReadSeekCloser.Read(p)
	r.read += n
	return n, e
}
func (r *stalledMediaReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed); r.ReadSeekCloser.Close() })
	return nil
}

type stalledMediaStore struct {
	*storage.Local
	key    string
	opened chan *stalledMediaReader
}

func (s *stalledMediaStore) Open(ctx context.Context, key string) (storage.Object, error) {
	object, e := s.Local.Open(ctx, key)
	if e != nil || key != s.key {
		return object, e
	}
	reader := &stalledMediaReader{ReadSeekCloser: object.Reader, entered: make(chan struct{}), closed: make(chan struct{}), released: make(chan struct{})}
	object.Reader = reader
	s.opened <- reader
	return object, nil
}
func TestCompanyMediaCancellationClosesBlockedStorageReader(t *testing.T) {
	for _, mode := range []string{"central-revocation", "caller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			var stalled *stalledMediaStore
			f, store, _, ctx := mediaHTTPFixture(t, func(d *api.Dependencies) {
				stalled = &stalledMediaStore{Local: d.Storage.(*storage.Local), opened: make(chan *stalledMediaReader, 1)}
				d.Storage = stalled
			})
			v := f.current(t)
			data := append(testmedia.PNG512("stalled media"), bytes.Repeat([]byte("isolated bytes"), 200000)...)
			task := completeMediaTask(t, f, store, ctx, data)
			stalled.key = *task.StorageKey
			local := httptest.NewServer(f.local)
			defer local.Close()
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, e := http.NewRequestWithContext(requestContext, "GET", local.URL+ci.LocalCustomerPrefix+"v1/generations/"+task.ID+"/content", nil)
			if e != nil {
				t.Fatal(e)
			}
			req.Host = strings.TrimPrefix(fixtureLocal, "https://")
			req.AddCookie(f.browser)
			req.Header.Set("Origin", fixtureLocal)
			req.Header.Set("Authorization", "Bearer "+v.Tokens.PlaygroundKey)
			type result struct {
				body   []byte
				err    error
				status int
			}
			done := make(chan result, 1)
			go func() {
				response, e := local.Client().Do(req)
				if e != nil {
					done <- result{err: e}
					return
				}
				defer response.Body.Close()
				body, e := io.ReadAll(response.Body)
				done <- result{body, e, response.StatusCode}
			}()
			var reader *stalledMediaReader
			select {
			case reader = <-stalled.opened:
			case <-time.After(5 * time.Second):
				t.Fatal("storage not opened")
			}
			select {
			case <-reader.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("actual storage Read did not stall")
			}
			if mode == "central-revocation" {
				revokeMutationFixture(t, f, v, true)
			} else {
				cancel()
			}
			select {
			case <-reader.closed:
			case <-time.After(3 * time.Second):
				t.Fatal("cancel did not Close opened storage")
			}
			select {
			case <-reader.released:
			case <-time.After(3 * time.Second):
				t.Fatal("blocked Read did not release")
			}
			select {
			case r := <-done:
				if r.err == nil || len(r.body) >= len(data) {
					t.Fatal("partial canceled stream reported complete", r.status, len(r.body), r.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled handler/client did not terminate")
			}
		})
	}
}
func TestCompanyMediaPeriodicAuthorityCheckHasShortDeadline(t *testing.T) {
	entered := make(chan time.Duration, 1)
	ctx, stop := ci.WatchMedia(context.Background(), func(check context.Context) error {
		deadline, ok := check.Deadline()
		if !ok {
			return errors.New("missing child deadline")
		}
		entered <- time.Until(deadline)
		<-check.Done()
		return check.Err()
	})
	defer stop()
	select {
	case remaining := <-entered:
		if remaining <= 0 || remaining > ci.MediaAuthorityTimeout {
			t.Fatal("authority budget", remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("periodic fence not called")
	}
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(ci.MediaAuthorityTimeout + time.Second):
		t.Fatal("stalled authority check did not cancel stream")
	}
}
