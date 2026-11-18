package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestStoredSVGIsSandboxed(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 4096)
	if err != nil {
		t.Fatal(err)
	}
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="red"/><script>document.documentElement.setAttribute('data-script-ran','yes');fetch('/security-script-ran')</script></svg>`
	const id = "24e1c949-9161-4c83-a7ac-fdb83eddb44b"
	stored, err := store.Archive(context.Background(), id, "data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)))
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{ID: id, Status: core.TaskSucceeded, StorageKey: &stored.StorageKey}
	handler := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", store, "media-secret", time.Hour)
	signed, _ := newMediaURLSigner("media-secret", time.Hour).create(id)
	for _, route := range []string{signed, "/v1/generations/" + id + "/content"} {
		r := httptest.NewRequest("GET", route, nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" || w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("active media was not isolated: status=%d headers=%v", w.Code, w.Header())
		}
	}
	// Opt in to an actual browser + production UI proxy check. The ordinary
	// suite always checks the real API headers; it does not silently skip a test.
	if node := os.Getenv("GATEWAY_SECURITY_BROWSER_NODE"); node != "" {
		server := httptest.NewServer(handler)
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		script, err := filepath.Abs("../../scripts/ui/media-security.cjs")
		if err != nil {
			t.Fatal(err)
		}
		output, err := exec.CommandContext(ctx, node, script, server.URL, signed).CombinedOutput()
		if err != nil {
			t.Fatalf("browser media isolation: %v\n%s", err, output)
		}
		t.Log(strings.TrimSpace(string(output)))
	}
}

func TestVideoContentRejectsUnavailableVariants(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 4096)
	if err != nil {
		t.Fatal(err)
	}
	const id = "6f1d2a4e-3b7c-4d8e-9f00-112233445566"
	stored, err := store.Archive(context.Background(), id, "data:video/mp4;base64,"+base64.StdEncoding.EncodeToString([]byte("fixture video bytes")))
	if err != nil {
		t.Fatal(err)
	}
	task := core.Task{ID: id, MediaType: "video", Status: core.TaskSucceeded, StorageKey: &stored.StorageKey}
	handler := NewWithStorage(serviceStub{task: task}, healthStub{}, "secret", store, "media-secret", time.Hour)
	get := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	// OpenAI SDK downloadContent(id, {variant}) sends these; returning the MP4 for them would be wrong silently.
	for _, variant := range []string{"thumbnail", "spritesheet", ""} {
		for _, method := range []string{"GET", "HEAD"} {
			w := get(method, "/v1/videos/"+id+"/content?variant="+variant)
			if w.Code != http.StatusBadRequest || method == "GET" && !strings.Contains(w.Body.String(), `"param":"variant"`) {
				t.Fatalf("%s variant=%q: %d %s", method, variant, w.Code, w.Body)
			}
		}
	}
	for _, path := range []string{"/v1/videos/" + id + "/content", "/v1/videos/" + id + "/content?variant=video", "/v1/generations/" + id + "/content"} {
		if w := get("GET", path); w.Code != http.StatusOK || w.Body.String() != "fixture video bytes" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}
