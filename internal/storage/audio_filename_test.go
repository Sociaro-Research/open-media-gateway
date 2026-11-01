package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestAudioAACM4AReferenceFilenamesAreDeterministic(t *testing.T) {
	for _, tc := range []struct{ kind, suffix string }{{"audio/aac", "/audio.aac"}, {"audio/mp4", "/audio.m4a"}} {
		t.Run(tc.kind, func(t *testing.T) {
			store, err := NewLocal(t.TempDir(), http.DefaultClient, 1024)
			if err != nil {
				t.Fatal(err)
			}
			ctx := identity.WithOwnerID(context.Background(), "audio-fixture-owner")
			payload := []byte("synthetic storage fixture")
			saved, err := store.StoreReference(ctx, "a6dd8e89-3b7c-40cf-8dc8-e7534c42371d", payload, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(saved.StorageKey, tc.suffix) || saved.ContentType != tc.kind {
				t.Fatal(saved)
			}
			object, err := store.Open(ctx, saved.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer object.Reader.Close()
			if object.ContentType != tc.kind {
				t.Fatalf("stored MIME %q; want %q", object.ContentType, tc.kind)
			}
			got, err := io.ReadAll(object.Reader)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatal("stored bytes changed", err)
			}
		})
	}
}
