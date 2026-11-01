package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestMediaAccessRenewalPreservesExpiryAndAuthorization(t *testing.T) {
	key, frame := "video.mp4", "frame.png"
	task := core.Task{ID: "24e1c949-9161-4c83-a7ac-fdb83eddb44b", Status: core.TaskSucceeded, StorageKey: &key, LastFrameStorageKey: &frame}
	now := time.Now().UTC().Truncate(time.Second)
	signer := newMediaURLSigner("fixture-signing-secret", time.Hour)
	signer.now = func() time.Time { return now }
	s := &Server{service: serviceStub{task: task}, health: healthStub{}, apiKey: "fixture-key", storage: &memoryMedia{}, mediaSigner: signer}
	h := s.handler()
	endpoint := "/v1/generations/" + task.ID + "/media-access"
	type access struct {
		Preview  string    `json:"preview_url"`
		Download string    `json:"download_url"`
		Frame    string    `json:"last_frame_url"`
		Expires  time.Time `json:"expires_at"`
	}
	renew := func() access {
		t.Helper()
		w := sendTest(h, "GET", endpoint, "", "", "fixture-key")
		if w.Code != 200 {
			t.Fatalf("renewal status %d", w.Code)
		}
		var value access
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if !value.Expires.Equal(now.Add(time.Hour)) {
			t.Fatal("renewal changed configured lifetime")
		}
		return value
	}
	initial := renew()
	now = now.Add(2 * time.Hour)
	for _, url := range []string{initial.Preview, initial.Download, initial.Frame} {
		if w := sendTest(h, "GET", url, "", "", ""); w.Code != http.StatusForbidden {
			t.Fatalf("expired artifact status %d", w.Code)
		}
	}
	for _, secret := range []string{"", "wrong-key"} {
		if w := sendTest(h, "GET", endpoint, "", "", secret); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized renewal status %d", w.Code)
		}
	}
	fresh := renew()
	for _, url := range []string{fresh.Preview, fresh.Download, fresh.Frame} {
		if w := sendTest(h, "GET", url, "", "", ""); w.Code != http.StatusOK {
			t.Fatalf("fresh artifact status %d", w.Code)
		}
	}
	task.ResultDeletedAt = &now
	s.service = serviceStub{task: task}
	if w := sendTest(h, "GET", endpoint, "", "", "fixture-key"); w.Code != http.StatusNotFound {
		t.Fatalf("deleted media renewal status %d", w.Code)
	}
}
