package companyinterface_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestCompanyMediaTypedGenerationLinks(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("typed generation links")
	task := completeMediaTask(t, f, store, ctx, data)
	for _, path := range []string{"/v1/generations/" + task.ID, "/v1/organizations/" + v.Tokens.Company + "/generations/" + task.ID} {
		got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, got, 200)
		var view struct {
			ID         string
			ContentURL string `json:"content_url"`
			StatusURL  string `json:"status_url"`
		}
		if json.Unmarshal(got.Body.Bytes(), &view) != nil || view.ID != task.ID || view.ContentURL != ci.LocalCustomerPrefix+"v1/generations/"+task.ID+"/content" || view.StatusURL != ci.LocalCustomerPrefix+"v1/generations/"+task.ID {
			t.Fatal("wrong typed object", got.Body.String())
		}
		content := facadeRequest(t, f, "GET", "/"+strings.TrimPrefix(view.ContentURL, ci.LocalCustomerPrefix), "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, content, 200)
		if !bytes.Equal(content.Body.Bytes(), data) {
			t.Fatal("mapped content changed")
		}
	}
	for _, path := range []string{"/v1/generations/" + task.ID + "/media-access", "/v1/organizations/" + v.Tokens.Company + "/generations/" + task.ID + "/media-access"} {
		got := facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, got, 200)
		var links struct {
			Preview  string `json:"preview_url"`
			Download string `json:"download_url"`
		}
		json.Unmarshal(got.Body.Bytes(), &links)
		for _, link := range []string{links.Preview, links.Download} {
			if !strings.HasPrefix(link, ci.LocalCustomerPrefix+"media/"+task.ID+"?") {
				t.Fatal("unmapped signed media", link)
			}
			result := facadeRequest(t, f, "GET", "/"+strings.TrimPrefix(link, ci.LocalCustomerPrefix), "", v.Tokens.PlaygroundKey, true, nil)
			expectFixture(t, result, 200)
			if !bytes.Equal(result.Body.Bytes(), data) {
				t.Fatal("signed content changed")
			}
		}
	}
	// Report links retain their report-scoped policy instead of becoming generic media reads.
	reportPath := "/v1/organizations/" + v.Tokens.Company + "/reports/" + task.ID
	direct := resourceDirect(f, "GET", reportPath, "", v.Tokens.PlaygroundKey, "")
	expectFixture(t, direct, 200)
	got := facadeRequest(t, f, "GET", reportPath, "", "", true, nil)
	expectFixture(t, got, 200)
	var report struct {
		Content bool              `json:"content_access"`
		Links   map[string]string `json:"links"`
	}
	json.Unmarshal(got.Body.Bytes(), &report)
	if !report.Content || report.Links["content_url"] != ci.LocalCustomerPrefix+strings.TrimPrefix(reportPath, "/")+"/content" {
		t.Fatal("wrong report link", got.Body.String())
	}
	result := facadeRequest(t, f, "HEAD", reportPath+"/content", "", "", true, func(r *http.Request) { r.Header.Set("Range", "bytes=1-19") })
	expectFixture(t, result, 206)
	if result.Body.Len() != 0 {
		t.Fatal("HEAD report body")
	}
}

func TestCompanyMediaArtifactAndLastFrameRoutes(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	data := testmedia.PNG512("artifact fixture")
	auxiliary, e := store.Archive(ctx, uuid.NewString(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data))
	if e != nil {
		t.Fatal(e)
	}
	task := completeMediaTask(t, f, store, ctx, data, func(media *core.StoredMedia) {
		media.LastFrameStorageKey = auxiliary.StorageKey
		media.Artifacts = []core.StoredArtifact{{ID: "frame_1", MediaType: "image", Format: "png", StorageKey: auxiliary.StorageKey, ContentType: auxiliary.ContentType, SizeBytes: auxiliary.SizeBytes, SHA256: auxiliary.SHA256}}
	})
	for _, path := range []string{"/v1/generations/" + task.ID + "/artifacts/frame_1/content", "/v1/generations/" + task.ID + "/last-frame", "/v1/organizations/" + v.Tokens.Company + "/generations/" + task.ID + "/last-frame", "/v1/organizations/" + v.Tokens.Company + "/reports/" + task.ID + "/last-frame"} {
		for _, method := range []string{"GET", "HEAD"} {
			headers := http.Header{"Range": {"bytes=1-19"}}
			direct := mediaDirectHeaders(f, method, path, v.Tokens.PlaygroundKey, headers)
			got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Range", "bytes=1-19") })
			expectFixture(t, direct, 206)
			expectFixture(t, got, 206)
			if !bytes.Equal(got.Body.Bytes(), direct.Body.Bytes()) || got.Header().Get("Content-Range") != direct.Header().Get("Content-Range") || got.Header().Get("Content-Length") != direct.Header().Get("Content-Length") {
				t.Fatal("artifact/last-frame parity", method, path)
			}
		}
	}
	got := facadeRequest(t, f, "GET", "/v1/generations/"+task.ID+"/media-access", "", v.Tokens.PlaygroundKey, true, nil)
	expectFixture(t, got, 200)
	var links struct {
		Last      string `json:"last_frame_url"`
		Artifacts []struct {
			ID      string
			Preview string `json:"preview_url"`
		}
	}
	if json.Unmarshal(got.Body.Bytes(), &links) != nil || len(links.Artifacts) != 1 || links.Artifacts[0].ID != "frame_1" {
		t.Fatal("missing typed artifacts", got.Body.String())
	}
	for _, link := range []string{links.Last, links.Artifacts[0].Preview} {
		if !strings.HasPrefix(link, ci.LocalCustomerPrefix+"media/"+task.ID+"?") {
			t.Fatal("wrong signed artifact URL", link)
		}
		out := facadeRequest(t, f, "GET", "/"+strings.TrimPrefix(link, ci.LocalCustomerPrefix), "", v.Tokens.PlaygroundKey, true, nil)
		expectFixture(t, out, 200)
		if !bytes.Equal(out.Body.Bytes(), data) {
			t.Fatal("signed artifact bytes")
		}
	}
}
