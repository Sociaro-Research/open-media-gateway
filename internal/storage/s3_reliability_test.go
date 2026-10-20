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
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
)

func TestS3HTTPUploadRetriesPreserveBytesAndCleanStaging(t *testing.T) {
	payload := bytes.Repeat([]byte("retryable S3 upload\n"), 100000)
	wantHash := sha256.Sum256(payload)
	want := hex.EncodeToString(wantHash[:])
	for _, mode := range []string{"retry-success", "retry-exhaustion", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			staging := t.TempDir()
			t.Setenv("TMPDIR", staging)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.Write(payload)
			}))
			defer source.Close()
			var attempts atomic.Int64
			observed := make(chan bool, 3)
			started := make(chan struct{}, 1)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/media-test/generated/retry-task/video.mp4" {
					w.WriteHeader(400)
					return
				}
				n := attempts.Add(1)
				hash := sha256.New()
				size, err := io.Copy(hash, r.Body)
				observed <- err == nil && size == int64(len(payload)) && r.ContentLength == size && hex.EncodeToString(hash.Sum(nil)) == want && r.Header.Get("X-Amz-Meta-Sha256") == want && strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
				if mode == "cancelled" {
					started <- struct{}{}
					<-r.Context().Done()
					return
				}
				if mode == "retry-exhaustion" || n == 1 {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(503)
					fmt.Fprint(w, `<Error><Code>SlowDown</Code><Message>private fixture diagnostic</Message></Error>`)
					return
				}
				w.Header().Set("ETag", `"fixture-etag"`)
				w.WriteHeader(200)
			}))
			defer remote.Close()
			cfg := testS3Config()
			cfg.Endpoint = remote.URL
			client := httpclient.New(10 * time.Second)
			defer client.CloseIdleConnections()
			store, err := NewS3(cfg, client, int64(len(payload)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if mode == "cancelled" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			media, err := store.Archive(ctx, "retry-task", source.URL)
			if mode == "retry-success" {
				if err != nil || media.SizeBytes != int64(len(payload)) || media.SHA256 != want || attempts.Load() != 2 {
					t.Fatalf("retry did not preserve the complete object: attempts=%d media=%+v err=%v", attempts.Load(), media, err)
				}
			} else {
				expected := int64(3)
				if mode == "cancelled" {
					expected = 1
				}
				if err == nil || media.StorageKey != "" || attempts.Load() != expected {
					t.Fatalf("failed upload reported success or wrong retries: attempts=%d media=%+v err=%v", attempts.Load(), media, err)
				}
				if strings.Contains(err.Error(), "private fixture diagnostic") || strings.Contains(err.Error(), cfg.SecretAccessKey) {
					t.Fatal("upload failure exposed remote diagnostics or credentials")
				}
			}
			for range int(attempts.Load()) {
				if !<-observed {
					t.Fatal("an HTTP upload attempt lost bytes, size, hash metadata or signature")
				}
			}
			// Keep the shared parent stable for cross-process directory locking;
			// every per-upload child must still disappear.
			inspection := staging
			if temporaryLockSupported {
				inspection = filepath.Join(staging, "gateway-s3-staging-v2")
			}
			files, err := os.ReadDir(inspection)
			if err != nil || len(files) != 0 {
				t.Fatalf("completed/cancelled/failed upload retained staging: %v %v", files, err)
			}
		})
	}
}
