package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type emailSettingsFixture struct{ record auth.DeliveryRecord }

func (f *emailSettingsFixture) EmailDelivery(context.Context) (auth.DeliveryRecord, error) {
	return f.record, nil
}
func (f *emailSettingsFixture) SaveEmailDelivery(_ context.Context, _ string, r auth.DeliveryRecord) (auth.DeliveryRecord, error) {
	if r.Version != f.record.Version {
		return r, system.ErrConflict
	}
	r.Version++
	f.record = r
	return r, nil
}
func (f *emailSettingsFixture) BootstrapEmailDelivery(context.Context, auth.DeliveryRecord) error {
	return nil
}

func TestEmailDeliveryAdministration(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	repo := &emailSettingsFixture{record: auth.DeliveryRecord{Version: 1}}
	s.emailDelivery = auth.NewDeliveryManager(repo, cipher)
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation"}`)
	_, adminKey := issueTestKey(t, h, "alice", `{"name":"System","scopes":["generation","system:manage"]}`)
	body := `{"provider":"resend","from":"Sociaro <login@example.test>","api_key":"fixture-secret-key","version":1}`
	for _, tc := range []struct {
		session, key string
		status       int
	}{{"", "", 401}, {"bob", "", 403}, {"", "service-secret", 403}, {"", generation, 403}, {"alice", "", 200}, {"", adminKey, 200}} {
		for _, method := range []string{"GET", "PUT"} {
			requestBody := ""
			if method == "PUT" {
				requestBody = body
			}
			w := sendTest(h, method, "/v1/system/email", requestBody, tc.session, tc.key)
			if w.Code != tc.status {
				t.Fatalf("%s %+v: %d %s", method, tc, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "fixture-secret-key") || strings.Contains(w.Body.String(), "ciphertext") {
				t.Fatal("secret exposed")
			}
			if tc.status == 200 && method == "PUT" {
				repo.record.Version = 1
			}
		}
	}
	for _, bad := range []string{`{"version":1}`, `{"provider":"resend","from":"broken","version":1}`, `{"provider":"other","from":"sender@example.test","version":1}`, `{"provider":"","from":"","version":1,"unexpected":true}`} {
		if w := sendTest(h, "PUT", "/v1/system/email", bad, "alice", ""); w.Code != 400 {
			t.Fatal("invalid input", w.Code, w.Body.String())
		}
	}
	w := sendTest(h, "PUT", "/v1/system/email", strings.Replace(body, `"version":1`, `"version":99`, 1), "alice", "")
	if w.Code != 409 {
		t.Fatal("stale settings accepted", w.Code)
	}
	w = sendTest(h, "PUT", "/v1/system/email", `{"provider":"","from":"","version":1}`, "alice", "")
	var result auth.DeliverySettings
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Enabled || result.KeyConfigured || len(repo.record.KeyCiphertext) != 0 {
		t.Fatal("disable failed", w.Code, w.Body.String())
	}
}
