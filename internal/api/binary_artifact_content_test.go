package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func addBinaryArtifactFixture(t *testing.T, f *artifactBoundaryFixture) core.StoredArtifact {
	t.Helper()
	ctx := identity.WithOwnerID(context.Background(), f.service.task.OwnerID)
	data := "<html><script>opaque tracking; never inline</script></html>"
	m, err := f.store.Local.ArchiveSource(ctx, f.service.task.ID+"/artifacts/binary", core.ArtifactSource{Reader: io.NopCloser(strings.NewReader(data)), ContentType: "application/octet-stream", SizeBytes: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	a := core.StoredArtifact{ID: uuid.NewString(), ProviderID: "private-upstream-tracking", MediaType: "binary", Format: "bin", StorageKey: m.StorageKey, ContentType: m.ContentType, SizeBytes: m.SizeBytes, SHA256: m.SHA256}
	f.service.task.StoredArtifacts = append(f.service.task.StoredArtifacts, a)
	f.bytes[a.ID] = []byte(data)
	return a
}
func TestBinaryArtifactContentAlwaysAttachmentAndOwnerPrivate(t *testing.T) {
	f := newArtifactBoundaryFixture(t)
	a := addBinaryArtifactFixture(t, f)
	response := sendTest(f.handler, "GET", "/v1/generations/"+f.service.task.ID+"/media-access", "", "", f.ownerKey)
	var access artifactBoundaryAccess
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &access); err != nil || len(access.Artifacts) != 3 {
		t.Fatal(err, len(access.Artifacts))
	}
	signed := ""
	for _, item := range access.Artifacts {
		if item.ID == a.ID {
			signed = item.Preview
		}
	}
	if signed == "" {
		t.Fatal("binary artifact has no owner capability")
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, path := range []string{f.contentURL(a.ID), signed} {
			key := ""
			if path == f.contentURL(a.ID) {
				key = f.ownerKey
			}
			r := sendTest(f.handler, method, path, "", "", key)
			artifactBoundaryAssertBytes(t, r, method, a, f.bytes[a.ID])
			if !strings.HasPrefix(r.Header().Get("Content-Disposition"), "attachment;") || r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("opaque artifact delivered inline", r.Header())
			}
		}
		if r := sendTest(f.handler, method, f.contentURL(a.ID), "", "", f.foreignKey); r.Code != 404 {
			t.Fatal("foreign binary read", r.Code)
		}
	}
	for _, path := range []string{"/v1/generations/" + f.service.task.ID, "/v1/generations/" + f.service.task.ID + "/media-access"} {
		r := sendTest(f.handler, "GET", path, "", "", f.ownerKey)
		if r.Code != 200 || strings.Contains(r.Body.String(), a.ProviderID) || strings.Contains(r.Body.String(), a.StorageKey) || strings.Contains(r.Body.String(), *f.service.task.ProviderResultURL) || strings.Contains(r.Body.String(), f.service.task.ProviderIdentity) {
			t.Fatal("private binary source leaked", r.Code, r.Body.String())
		}
	}
}
func TestBinaryArtifactDirectDeliveryForcesAttachment(t *testing.T) {
	key, kind := "s3:fixture/owner/results/task/data.bin", "application/octet-stream"
	task := core.Task{ID: uuid.NewString(), OwnerID: uuid.NewString(), Status: core.TaskSucceeded, StorageKey: &key, ResultType: &kind}
	store := &directMedia{}
	h := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", store, "signing-secret", time.Hour)
	signed, _ := newMediaURLSigner("signing-secret", time.Hour).create(task.ID)
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "https://console.example.test"+signed, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 307 || !strings.HasPrefix(store.disposition, "attachment;") || store.owner != task.OwnerID || store.method != method {
			t.Fatal(w.Code, store)
		}
	}
}
