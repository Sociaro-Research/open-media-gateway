package references

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func uploadedHEIFResourceStream(t *testing.T) (context.Context, *Resolver, core.ResourceSourceInput, *aacUploadRepository, *aacUploadStore, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	data := heifUploadFixture(640, 512, 0, false, true)
	repo, store := &aacUploadRepository{}, &aacUploadStore{}
	library := New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), "heif-resource-stream-owner")
	item, err := library.Upload(ctx, "source.heif", bytes.NewReader(data))
	if err != nil || item.ContentType != "image/heic" {
		t.Fatal("HEIF upload failed", item, err)
	}
	source := core.ResourceSourceInput{ReferenceID: item.ID, MediaType: "image", ContentType: item.ContentType, SizeBytes: item.SizeBytes, SHA256: item.SHA256}
	return ctx, NewResolver(library, "unused"), source, repo, store, data, dir
}

func TestResourceStreamUploadedHEIFRetainsVerifiedBytesAndCanonicalMIME(t *testing.T) {
	for _, alias := range []string{"image/heic", "image/heif"} {
		t.Run(alias, func(t *testing.T) {
			ctx, resolver, source, _, _, data, dir := uploadedHEIFResourceStream(t)
			source.ContentType = alias
			source.URL = "https://127.0.0.1/must-not-fetch"
			opened, err := resolver.OpenResourceSource(ctx, source)
			if err != nil {
				t.Fatal("uploaded HEIF cannot reach verified provider stream", err)
			}
			defer opened.Reader.Close()
			if opened.ContentType != "image/heic" || opened.SizeBytes != int64(len(data)) {
				t.Fatal("HEIF stream metadata changed", opened)
			}
			got, err := io.ReadAll(opened.Reader)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("HEIF stream bytes changed", err)
			}
			if err = opened.Reader.Close(); err != nil {
				t.Fatal(err)
			}
			resourceStreamAssertNoStage(t, dir)
		})
	}
}

func TestResourceStreamUploadedHEIFRejectsForgedEvidenceBeforeDelivery(t *testing.T) {
	for _, kind := range []string{"hash", "size", "type", "changed bytes", "truncated bytes", "forged stored MIME"} {
		t.Run(kind, func(t *testing.T) {
			ctx, resolver, source, repo, store, _, dir := uploadedHEIFResourceStream(t)
			switch kind {
			case "hash":
				source.SHA256 = strings.Repeat("f", 64)
			case "size":
				source.SizeBytes++
			case "type":
				source.ContentType = "image/png"
			case "changed bytes":
				store.data[len(store.data)-1] ^= 1
			case "truncated bytes":
				store.data = store.data[:len(store.data)-1]
			case "forged stored MIME":
				source.ContentType = "image/png"
				repo.item.ContentType, store.kind = "image/png", "image/png"
			}
			opened, err := resolver.OpenResourceSource(ctx, source)
			if !errors.Is(err, ErrResourceSource) || opened.Reader != nil {
				t.Fatal("unverified HEIF reached provider", opened, err)
			}
			resourceStreamAssertNoStage(t, dir)
		})
	}
}

func TestResourceStreamHEIFPreservesOtherMIMEPolicy(t *testing.T) {
	for _, kind := range []string{"image/png", "image/jpeg", "image/webp", "image/gif", "video/mp4", "video/quicktime", "video/webm", "audio/mpeg", "audio/wave", "audio/ogg", "audio/flac", "audio/aac", "audio/mp4"} {
		if got, ok := resourceSourceMIME(kind); !ok || got != kind {
			t.Fatal("existing resource MIME was removed", kind, got, ok)
		}
	}
	for _, kind := range []string{"image/avif", "image/heic-sequence", "image/heif-sequence", "image/svg+xml", "application/octet-stream"} {
		if _, ok := resourceSourceMIME(kind); ok {
			t.Fatal("unrelated resource MIME became supported", kind)
		}
	}
	if MaxBytes != 200<<20 || commonLimit("image/heic") != 30<<20 {
		t.Fatal("resource or HEIF byte policy changed")
	}
}
