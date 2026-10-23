package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/legal"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"strings"
	"testing"
)

type legalFixture struct {
	*systemFixture
	documents map[string]legal.Document
}

func (f *legalFixture) LegalDocument(_ context.Context, k string) (legal.Document, error) {
	return f.documents[k], nil
}
func (f *legalFixture) SaveLegalDocument(_ context.Context, actor, k string, d legal.Document) (legal.Document, error) {
	if !f.admins[actor] {
		return d, system.ErrForbidden
	}
	if d.Version != f.documents[k].Version {
		return d, system.ErrConflict
	}
	d.Version++
	f.documents[k] = d
	return d, nil
}
func TestLegalDocumentsPermissionsAndPublicContent(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &legalFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, documents: map[string]legal.Document{"privacy": {Version: 1}, "terms": {Version: 1}}}
	s.system = f
	h := s.handler()
	for _, user := range []string{"", "bob", "alice"} {
		expected := 200
		if user == "" {
			expected = 401
		} else if user == "bob" {
			expected = 403
		}
		for _, method := range []string{"GET", "PUT"} {
			w := sendTest(h, method, "/v1/system/legal/privacy", `{"text":"hello","version":1}`, user, "")
			if w.Code != expected {
				t.Fatalf("%s %s: %d %s", user, method, w.Code, w.Body.String())
			}
		}
	}
	w := sendTest(h, "GET", "/legal/privacy", "", "", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	var public map[string]any
	if json.Unmarshal(w.Body.Bytes(), &public) != nil || len(public) != 1 || public["text"] != "hello" {
		t.Fatal(public)
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"text":"stale","version":1}`, 409},
		{`{"version":2}`, 400},
		{`{"text":null,"version":2}`, 400},
		{`{"text":"x","version":2,"unexpected":true}`, 400},
		{`{"text":"x","version":0}`, 400},
		{`{"text":"\u0000","version":2}`, 400},
		{`{"text":"` + strings.Repeat("x", legal.MaxTextBytes+1) + `","version":2}`, 400},
		{`{"text":"<script>window.pwned=true</script>","version":2}`, 200},
		{`{"text":"","version":3}`, 200},
	} {
		w = sendTest(h, "PUT", "/v1/system/legal/privacy", tc.body, "alice", "")
		if w.Code != tc.code {
			t.Fatalf("%d expected %d: %s", w.Code, tc.code, w.Body.String())
		}
	}
	if f.documents["terms"].Version != 1 {
		t.Fatal("other document changed")
	}
	if sendTest(h, "GET", "/legal/other", "", "", "").Code != 404 {
		t.Fatal("unknown kind")
	}
	if sendTest(h, "POST", "/legal/privacy", "", "", "").Code != 405 {
		t.Fatal("public write")
	}
}
