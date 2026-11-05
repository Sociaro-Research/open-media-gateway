package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type legalErrorFixture struct {
	*legalFixture
	reads, writes int
}

func (f *legalErrorFixture) LegalDocument(ctx context.Context, kind string) (legal.Document, error) {
	f.reads++
	return f.legalFixture.LegalDocument(ctx, kind)
}

func (f *legalErrorFixture) SaveLegalDocument(ctx context.Context, actor, kind string, document legal.Document) (legal.Document, error) {
	f.writes++
	return f.legalFixture.SaveLegalDocument(ctx, actor, kind, document)
}

func TestLegalSettingsUnknownKindJSONAfterAuthorization(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		for _, tc := range []struct {
			user, code, typ string
			status          int
		}{
			{"", "unauthorized", "authentication_error", 401},
			{"bob", "system_policy", "permission_error", 403},
			{"alice", "not_found", "invalid_request_error", 404},
		} {
			t.Run(method+"/"+tc.code, func(t *testing.T) {
				s, _ := newPersonalTestServer()
				f := &legalErrorFixture{legalFixture: &legalFixture{
					systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}},
					documents:     map[string]legal.Document{"privacy": {Text: "unchanged", Version: 1}},
				}}
				s.system = f
				h := s.handler()
				// Invalid JSON must not take precedence over authorization or kind.
				w := sendTest(h, method, "/v1/system/legal/private-kind-sentinel", "{", tc.user, "")
				var body struct {
					Error map[string]any `json:"error"`
				}
				if w.Code != tc.status || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Error) != 4 || body.Error["code"] != tc.code || body.Error["param"] != nil || body.Error["type"] != tc.typ {
					t.Fatalf("%d %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private-kind-sentinel") || f.reads != 0 || f.writes != 0 || f.documents["privacy"].Text != "unchanged" {
					t.Fatal("unknown kind echoed or accessed legal content", w.Body.String(), f.reads, f.writes)
				}
				if tc.user != "alice" {
					known := sendTest(h, method, "/v1/system/legal/privacy", "{", tc.user, "")
					if known.Code != w.Code || known.Body.String() != w.Body.String() || f.reads != 0 || f.writes != 0 {
						t.Fatal("kind validation changed the existing access denial")
					}
				} else if message, _ := body.Error["message"].(string); !strings.Contains(message, "privacy or terms") {
					t.Fatal("missing kind correction", w.Body.String())
				}
			})
		}
	}
}

func TestLegalSettingsUnknownKindHEADAndPublicRead(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &legalFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}
	h := s.handler()
	get := sendTest(h, "GET", "/v1/system/legal/private-kind-sentinel", "", "alice", "")
	head := sendTest(h, "HEAD", "/v1/system/legal/private-kind-sentinel", "", "alice", "")
	if head.Code != 404 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "application/json" || head.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) {
		t.Fatal("HEAD lost JSON representation or body suppression", head.Code, head.Header(), head.Body.String())
	}
	public := sendTest(h, "GET", "/legal/private-kind-sentinel", "", "", "")
	if public.Code != 404 || public.Header().Get("Content-Type") != "text/plain; charset=utf-8" || public.Body.String() != "404 page not found\n" {
		t.Fatal("public non-API representation changed", public.Code, public.Header(), public.Body.String())
	}
}
