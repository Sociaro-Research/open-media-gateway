package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func TestRemoteHistoricalRecordErrorsExcludeGeneratedJSONContent(t *testing.T) {
	const company = "11111111-1111-4111-8111-111111111111"
	const id = "abcdefab-1111-4111-8111-abcdefabcdef"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/key" {
			fmt.Fprintf(w, `{"key":{"id":"22222222-2222-4222-8222-222222222222","user_id":"33333333-3333-4333-8333-333333333333","organization_id":%q,"resource_mode":"shared","scopes":["generation"]},"organization":{"id":%q}}`, company, company)
			return
		}
		fmt.Fprintf(w, `{"object":"generation","id":%q,"error":{"code":"generation_failed","message":"private-body"},"options":{"parameters":{"object":"generation","error":"user-sentinel"}}}`, id)
	}))
	defer upstream.Close()
	repo := &remoteFixture{records: map[string]remote.Record{"alice": {Owner: "alice", Version: 1, Active: true, Provisioning: "legacy", Origin: upstream.URL, OrganizationID: company, UserID: "33333333-3333-4333-8333-333333333333"}}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("f", 32))
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	s.remoteWorkspaceManager = remote.New(repo, cipher, upstream.Client())
	h := s.handler()
	body, _ := json.Marshal(map[string]any{"origin": upstream.URL, "organization_id": company, "token": "synthetic-remote-key", "version": 1, "consent": true})
	if w := sendTest(h, "POST", "/v1/system/remote-workspace", string(body), "alice", ""); w.Code != 200 {
		t.Fatal("connect", w.Code, w.Body)
	}
	for _, suffix := range []string{"", "/content", "/last-frame"} {
		path := "/v1/system/remote-workspace/proxy/v1/generations/" + id + suffix
		w := sendRemoteContext(h, "GET", path, "", "alice", repo.records["alice"].SnapshotID(), "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "user-sentinel") || strings.Contains(w.Body.String(), "private-body") != (suffix != "") {
			t.Fatal("remote record/content boundary", suffix, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/v1/generations/estimate", "/v1/generations/" + id + "/artifacts/artifact-1/content", "/v1/videos/" + id + "/content"} {
		if remoteGenerationRecordPath(path) {
			t.Fatal("media/estimate admitted as diagnostic record", path)
		}
	}
}
