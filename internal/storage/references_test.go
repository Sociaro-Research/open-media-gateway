package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/google/uuid"
)

func TestReferenceDestinationsDeletionAndIsolation(t *testing.T) {
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	bob := identity.WithOwnerID(context.Background(), uuid.NewString())
	remote := newMemoryS3()
	manager := newTestManager(t, newSettingsMemory(), remote)
	data := []byte("reference-content")
	local, err := manager.StoreReference(ctx, uuid.NewString(), data, "image/png")
	if err != nil || !strings.HasPrefix(local.StorageKey, identity.OwnerID(ctx)+"/references/") {
		t.Fatalf("local key %q: %v", local.StorageKey, err)
	}
	if err := manager.DeleteReference(bob, local.StorageKey); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("cross-owner local deletion allowed", err)
	}
	if _, err := manager.Update(ctx, s3Update()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StoreReference(ctx, uuid.NewString(), data, "image/png"); !errors.Is(err, ErrDirectUploadRequired) {
		t.Fatal("S3 accepted proxied upload", err)
	}
	first, err := seedStoredS3Reference(manager, remote, ctx, uuid.NewString(), data, "image/png")
	if err != nil || !strings.Contains(first.StorageKey, "/"+identity.OwnerID(ctx)+"/references/") {
		t.Fatalf("S3 key %q: %v", first.StorageKey, err)
	}
	if err := manager.DeleteReference(bob, first.StorageKey); err == nil {
		t.Fatal("cross-owner S3 deletion allowed")
	}
	// Simulate files written by the previous release; both layouts must remain usable.
	legacyLocal := strings.Replace(local.StorageKey, identity.OwnerID(ctx)+"/references/", "references/"+identity.OwnerID(ctx)+"/", 1)
	oldFile, _ := manager.local.resolve(local.StorageKey)
	legacyFile, _ := manager.local.resolve(legacyLocal)
	if err := os.MkdirAll(filepath.Dir(legacyFile), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldFile, legacyFile); err != nil {
		t.Fatal(err)
	}
	local.StorageKey = legacyLocal
	for key, data := range remote.objects {
		legacy := strings.Replace(key, "/"+identity.OwnerID(ctx)+"/references/", "/references/"+identity.OwnerID(ctx)+"/", 1)
		if legacy != key {
			remote.objects[legacy] = data
			remote.types[legacy] = remote.types[key]
			delete(remote.objects, key)
			delete(remote.types, key)
		}
	}
	first.StorageKey = strings.Replace(first.StorageKey, "/"+identity.OwnerID(ctx)+"/references/", "/references/"+identity.OwnerID(ctx)+"/", 1)
	update := s3Update()
	update.Bucket = "new-reference-bucket"
	if _, err := manager.Update(ctx, update); err != nil {
		t.Fatal(err)
	}
	second, err := seedStoredS3Reference(manager, remote, ctx, uuid.NewString(), data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(ctx, Update{Driver: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{local.StorageKey, first.StorageKey, second.StorageKey} {
		object, err := manager.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(object.Reader)
		object.Reader.Close()
		if err != nil || string(got) != string(data) {
			t.Fatal("old destination lost", err)
		}
	}
	remote.deny = "DELETE"
	if err := manager.DeleteReference(ctx, first.StorageKey); err == nil {
		t.Fatal("denied deletion succeeded")
	}
	if _, err := manager.Open(ctx, first.StorageKey); err != nil {
		t.Fatal("failed deletion lost object", err)
	}
	remote.deny = ""
	for _, key := range []string{local.StorageKey, first.StorageKey, second.StorageKey} {
		if err := manager.DeleteReference(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Open(ctx, key); !errors.Is(err, ErrObjectNotFound) {
			t.Fatal("deleted object still available", err)
		}
		if err := manager.DeleteReference(ctx, key); err != nil {
			t.Fatal("retry failed", err)
		}
	}
	if err := manager.DeleteReference(ctx, "references/"+identity.OwnerID(ctx)+"/../../video.mp4"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("traversal allowed")
	}
}

func TestVersionedReferenceDeletionPurgesExactKeyAcrossPages(t *testing.T) {
	const key = "references/owner/ref/image.png"
	var deleted []string
	pages := 0
	transport := referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/xml")
		switch r.Method {
		case "DELETE":
			if r.URL.Path != "/media-test/"+key {
				t.Fatalf("wrong deletion target: %s", r.URL.Path)
			}
			version := r.URL.Query().Get("versionId")
			deleted = append(deleted, version)
			if version == "hide-old" {
				w.WriteHeader(404)
				fmt.Fprint(w, `<Error><Code>NoSuchVersion</Code></Error>`)
				return w.Result(), nil
			}
			if version == "" {
				w.Header().Set("x-amz-delete-marker", "true")
				w.Header().Set("x-amz-version-id", "hide-new")
			}
			w.WriteHeader(204)
		case "GET":
			if !r.URL.Query().Has("versions") || r.URL.Query().Get("prefix") != key {
				t.Fatal("must list only the exact reference prefix")
			}
			pages++
			if pages == 1 {
				fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>v1</NextVersionIdMarker><Version><Key>%s</Key><VersionId>v1</VersionId></Version><DeleteMarker><Key>%s</Key><VersionId>hide-new</VersionId></DeleteMarker></ListVersionsResult>`, key, key, key)
			} else {
				if r.URL.Query().Get("version-id-marker") != "v1" {
					t.Fatal("pagination cursor lost")
				}
				fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>v2</VersionId></Version><Version><Key>%s-neighbor</Key><VersionId>unrelated</VersionId></Version><DeleteMarker><Key>%s</Key><VersionId>hide-old</VersionId></DeleteMarker></ListVersionsResult>`, key, key, key)
			}
		default:
			t.Fatal(r.Method)
		}
		return w.Result(), nil
	})
	s, err := NewS3(testS3Config(), &http.Client{Transport: transport}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.deleteReferenceObject(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if strings.Join(deleted, ",") != ",v1,v2,hide-new,hide-old" || pages != 2 {
		t.Fatalf("wrong version deletion order: %v", deleted)
	}
}

type referenceVersionTransport func(*http.Request) (*http.Response, error)

func (f referenceVersionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVersionDeletionFailureKeepsHideMarkers(t *testing.T) {
	for _, deny := range []string{"list", "version"} {
		t.Run(deny, func(t *testing.T) {
			markerDeleted := false
			s, err := NewS3(testS3Config(), &http.Client{Transport: referenceVersionTransport(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				w.Header().Set("Content-Type", "application/xml")
				version := r.URL.Query().Get("versionId")
				if r.Method == "DELETE" && version == "" {
					w.Header().Set("x-amz-delete-marker", "true")
					w.WriteHeader(204)
				} else if (r.Method == "GET" && deny == "list") || version == "data" {
					w.WriteHeader(403)
					fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
				} else if r.Method == "GET" {
					fmt.Fprint(w, `<ListVersionsResult><Version><Key>ref</Key><VersionId>data</VersionId></Version><DeleteMarker><Key>ref</Key><VersionId>hide</VersionId></DeleteMarker></ListVersionsResult>`)
				} else {
					markerDeleted = true
					w.WriteHeader(204)
				}
				return w.Result(), nil
			})}, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.deleteReferenceObject(context.Background(), "ref"); err == nil || markerDeleted {
				t.Fatal("failed purge must remain hidden and retryable")
			}
		})
	}
}

// Existing-object fixtures exercise legacy read/delete compatibility, without
// reintroducing a gateway byte-upload implementation for S3.
func seedStoredS3Reference(m *Manager, remote *memoryS3, ctx context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	sum := sha256.Sum256(data)
	pending, err := m.PlanUpload(ctx, id, kind, hex.EncodeToString(sum[:]), int64(len(data)))
	if err != nil {
		return core.StoredMedia{}, err
	}
	store, key, err := m.objectS3(uploadContext(ctx, *pending), pending.Key)
	if err != nil {
		return core.StoredMedia{}, err
	}
	endpoint, _ := url.Parse(store.config.Endpoint)
	remote.objects[endpoint.Host+"/"+store.config.Bucket+"/"+key] = data
	remote.types[endpoint.Host+"/"+store.config.Bucket+"/"+key] = kind
	return core.StoredMedia{StorageKey: pending.Key, SizeBytes: int64(len(data)), ContentType: kind}, nil
}
