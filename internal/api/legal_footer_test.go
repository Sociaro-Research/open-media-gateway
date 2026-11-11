package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type footerFixture struct {
	*brandingFixture
	footer legal.Footer
}

func (f *footerFixture) LegalFooter(context.Context) (legal.Footer, error) { return f.footer, nil }
func (f *footerFixture) SaveLegalFooter(_ context.Context, actor string, next legal.Footer) (legal.Footer, error) {
	if !f.admins[actor] {
		return next, system.ErrForbidden
	}
	if next.Version != f.footer.Version {
		return next, system.ErrConflict
	}
	next.CopyrightHolder = strings.TrimSpace(next.CopyrightHolder)
	next.Version++
	f.footer = next
	return next, nil
}

func TestLegalFooterPermissionsValidationAndPublicConfiguration(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &footerFixture{brandingFixture: &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}, footer: legal.Footer{Version: 1}}
	s.system = f
	h := s.handler()
	for _, tc := range []struct {
		user string
		code int
	}{{"", 401}, {"bob", 403}, {"alice", 200}} {
		for _, method := range []string{"GET", "PUT"} {
			w := sendTest(h, method, "/v1/system/legal/footer", `{"copyright_holder":"Configured & <operator>","version":1}`, tc.user, "")
			if w.Code != tc.code {
				t.Fatalf("%s %s: %d %s", method, tc.user, w.Code, w.Body.String())
			}
		}
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Generate","scopes":["generation"]}`)
	for _, method := range []string{"GET", "PUT"} {
		if w := sendTest(h, method, "/v1/system/legal/footer", `{"copyright_holder":"Denied","version":2}`, "", key); w.Code != 403 {
			t.Fatal("generation key", w.Code)
		}
	}
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manage footer","scopes":["generation","system:manage"]}`)
	if w := sendTest(h, "GET", "/v1/system/legal/footer", "", "", manager); w.Code != 200 {
		t.Fatal("management key", w.Code)
	}
	f.admins["alice"] = false
	for _, method := range []string{"GET", "PUT"} {
		if w := sendTest(h, method, "/v1/system/legal/footer", `{"copyright_holder":"Denied","version":2}`, "", manager); w.Code != 403 {
			t.Fatal("revoked administrator", w.Code)
		}
	}
	f.admins["alice"] = true
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"copyright_holder":"stale","version":1}`, 409}, {`{"version":2}`, 400}, {`{"copyright_holder":null,"version":2}`, 400}, {`{"copyright_holder":"x","version":0}`, 400}, {`{"copyright_holder":"x","version":2,"extra":true}`, 400}, {`{"copyright_holder":"line\nline","version":2}`, 400}, {`{"copyright_holder":"\u0000","version":2}`, 400}, {`{"copyright_holder":"` + strings.Repeat("é", 101) + `","version":2}`, 400},
	} {
		w := sendTest(h, "PUT", "/v1/system/legal/footer", tc.body, "alice", "")
		if w.Code != tc.code {
			t.Fatalf("%d expected %d: %s", w.Code, tc.code, w.Body.String())
		}
	}
	if f.footer.CopyrightHolder != "Configured & <operator>" || f.brand.Name != "" {
		t.Fatal("invalid/independent writes changed stored state", f.footer)
	}
	for _, wrapped := range []bool{false, true} {
		handler := h
		if wrapped {
			var err error
			handler, err = WithPublicAPI(h, "https://api.fixture.test", "https://console.fixture.test", "https://docs.fixture.test", f)
			if err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest("GET", "https://api.fixture.test/site.json", nil)
		if wrapped {
			request = httptest.NewRequest("GET", "https://docs.fixture.test/site.json", nil)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		var body map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		var footer map[string]any
		if json.Unmarshal(body["legal"], &footer) != nil || len(footer) != 1 || footer["copyright_holder"] != f.footer.CopyrightHolder {
			t.Fatal("public footer leaks or loses saved holder", footer)
		}
	}
	if w := sendTest(h, "PUT", "/v1/system/legal/footer", `{"copyright_holder":"","version":2}`, "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := sendTest(h, "GET", "/site.json", "", "", "")
	if !strings.Contains(w.Body.String(), legal.DefaultCopyrightHolder) {
		t.Fatal("neutral fallback", w.Body.String())
	}
}

func TestLegalFooterRejectsUnicodeLineSeparatorsWithoutWrites(t *testing.T) {
	for _, separator := range []string{"\u2028", "\u2029"} {
		t.Run(separator, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &footerFixture{brandingFixture: &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}, footer: legal.Footer{CopyrightHolder: "Original holder", Version: 1}}
			s.system = f
			body, err := json.Marshal(legal.Footer{CopyrightHolder: "First" + separator + "Second", Version: 1})
			if err != nil {
				t.Fatal(err)
			}
			w := sendTest(s.handler(), "PUT", "/v1/system/legal/footer", string(body), "alice", "")
			if w.Code != 400 || f.footer.CopyrightHolder != "Original holder" || f.footer.Version != 1 {
				t.Fatalf("invalid single-line input changed footer: status=%d stored=%+v", w.Code, f.footer)
			}
		})
	}
}

func TestLegalFooterValidationIdentifiesFieldAndCorrectiveAction(t *testing.T) {
	for _, tc := range []struct {
		name, body, field, action string
	}{
		{"missing holder", `{"version":1}`, "copyright_holder", "empty string"},
		{"invalid version", `{"copyright_holder":"value","version":0}`, "version", "current version"},
		{"oversized UTF-8", `{"copyright_holder":"` + strings.Repeat("é", 101) + `","version":1}`, "copyright_holder", "202 UTF-8 bytes; the limit is 200 bytes"},
		{"line separator", `{"copyright_holder":"SECRET\u2028VALUE","version":1}`, "copyright_holder", "remove control characters and line separators"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &footerFixture{brandingFixture: &brandingFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, brand: branding.Defaults()}, footer: legal.Footer{CopyrightHolder: "Original", Version: 1}}
			s.system = f
			w := sendTest(s.handler(), "PUT", "/v1/system/legal/footer", tc.body, "alice", "")
			var response struct {
				Error struct{ Code, Param, Message string }
			}
			if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error.Code != "invalid_request" || response.Error.Param != tc.field || !strings.Contains(response.Error.Message, tc.action) {
				t.Fatal("missing actionable field diagnosis", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "SECRET") || f.footer.CopyrightHolder != "Original" || f.footer.Version != 1 {
				t.Fatal("rejected input leaked or changed saved state")
			}
		})
	}
}
