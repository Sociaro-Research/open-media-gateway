package api

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

// Discover the complete production route surface so new routes automatically
// enter the unsupported-method regression instead of relying on a hand list.
func registeredMethodMatrix(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	matrix := map[string][]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Fatalf("%s: route must have an explicit auditable method/path", name)
			}
			pattern, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			method, path, found := strings.Cut(pattern, " ")
			if !found || !strings.HasPrefix(path, "/") {
				t.Fatalf("%s: methodless route %q", name, pattern)
			}
			if !slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, method) {
				t.Fatalf("unreviewed method %q", pattern)
			}
			if !slices.Contains(matrix[path], method) {
				matrix[path] = append(matrix[path], method)
			}
			return true
		})
	}
	return matrix
}

type methodAuthFixture struct {
	browserAuthStub
	logins, callbacks int
}

func (f *methodAuthFixture) Login(w http.ResponseWriter, r *http.Request) {
	f.logins++
	f.browserAuthStub.Login(w, r)
}
func (f *methodAuthFixture) Callback(w http.ResponseWriter, r *http.Request) {
	f.callbacks++
	f.browserAuthStub.Callback(w, r)
}
func (*methodAuthFixture) Methods(http.ResponseWriter, *http.Request) {}
func (*methodAuthFixture) Email(http.ResponseWriter, *http.Request)   {}
func (*methodAuthFixture) Setup(http.ResponseWriter, *http.Request)   {}

func TestRegisteredRoutesRejectUnsupportedMethods(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = methodOrg()
	s.templates = &templateMemory{items: map[string]templates.Record{}, assets: map[string]templates.Asset{}}
	s.auth = &methodAuthFixture{}
	s.storageAdmin = &storageAdminStub{}
	s.references = &references.Library{}
	s.mediaSigner = newMediaURLSigner("fixture", time.Minute)
	h := s.handler()
	matrix := registeredMethodMatrix(t)
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	paths := []string{}
	checks := 0
	for path := range matrix {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, pattern := range paths {
		path := wildcard.ReplaceAllString(pattern, fixtureOrg)
		methods := []string{}
		// Static and wildcard routes can share a concrete URL (for example
		// GET /providers/types and PUT /providers/{id}). Allow includes both.
		for candidate, declared := range matrix {
			parts, values := strings.Split(candidate, "/"), strings.Split(path, "/")
			matches := len(parts) == len(values)
			for i, part := range parts {
				if strings.HasSuffix(part, "...}") && i < len(values) {
					matches = true
					break
				}
				if i >= len(values) || !strings.HasPrefix(part, "{") && part != values[i] {
					matches = false
					break
				}
			}
			if matches {
				for _, method := range declared {
					if !slices.Contains(methods, method) {
						methods = append(methods, method)
					}
				}
			}
		}
		if slices.Contains(methods, "GET") && !slices.Contains(methods, "HEAD") {
			methods = append(slices.Clone(methods), "HEAD")
		}
		slices.Sort(methods)
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
			if slices.Contains(methods, method) {
				continue
			}
			w := methodSend(h, method, path, `{"name":"Must not write"}`, "alice", "")
			checks++
			if w.Code != 405 {
				t.Fatalf("%s %s: want 405 got %d %s", method, pattern, w.Code, w.Body)
			}
			if got := w.Header().Get("Allow"); got != strings.Join(methods, ", ") {
				t.Fatalf("%s %s: Allow=%q want %v", method, pattern, got, methods)
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("unsupported HEAD returned a body")
			}
		}
		t.Logf("route %s methods=%s", pattern, strings.Join(methods, ","))
	}
	t.Logf("checked %d unsupported method/path combinations across %d route paths", checks, len(matrix))
}

func TestOrganizationMethodsPreserveRoleScopeAndBinding(t *testing.T) {
	for _, role := range []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole} {
		t.Run(role, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := methodOrg()
			s.system = f
			h := s.handler()
			_, generation := issueTestKey(t, h, "alice", `{"name":"Generation","organization_id":"`+fixtureOrg+`"}`)
			_, manage := issueTestKey(t, h, "alice", `{"name":"Manage","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage"]}`)
			_, foreign := issueTestKey(t, h, "alice", `{"name":"Foreign","organization_id":"`+fixtureOtherOrg+`","scopes":["generation","organizations:manage"]}`)
			_, systemOnly := issueTestKey(t, h, "alice", `{"name":"System without organizations","scopes":["generation","system:manage"]}`)
			f.role = role
			f.admins["alice"] = false
			path := "/v1/organizations/" + fixtureOrg
			body := `{"name":"Must not change","default_weekly_limit_usd":10}`
			for _, tc := range []struct {
				session, key string
				get, head    int
			}{{"", "", 401, 401}, {"alice", "", 200, 200}, {"", generation, 200, 403}, {"", manage, 200, 200}, {"", foreign, 403, 403}, {"", systemOnly, 200, 403}} {
				for _, method := range []string{"GET", "HEAD"} {
					w := methodSend(h, method, path, body, tc.session, tc.key)
					want := tc.get
					if method == "HEAD" {
						want = tc.head
					}
					if w.Code != want || f.writes != 0 || f.name != "Original" {
						t.Fatalf("%s %+v: %d writes=%d name=%s", method, tc, w.Code, f.writes, f.name)
					}
					if method == "HEAD" && w.Body.Len() != 0 {
						t.Fatal("HEAD error leaked body")
					}
				}
			}
			for _, key := range []string{generation, foreign, systemOnly} {
				if w := methodSend(h, "PUT", path, body, "", key); w.Code != 403 || f.writes != 0 {
					t.Fatal("scope/binding bypass", w.Code)
				}
			}
			// A role downgrade must still deny a management-key write.
			w := methodSend(h, "PUT", path, body, "", manage)
			want := 403
			if org.Manage(role) {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("legitimate write role=%s: %d %s", role, w.Code, w.Body)
			}
		})
	}
}

func TestOAuthHEADCannotStartOrCompleteLogin(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &methodAuthFixture{}
	s.auth = f
	h := s.handler()
	for _, path := range []string{"/auth/google", "/auth/google/callback?state=fixture&code=fixture"} {
		for _, method := range []string{"HEAD", "OPTIONS", "POST"} {
			w := methodSend(h, method, path, "", "", "")
			if w.Code != 405 || w.Header().Get("Set-Cookie") != "" || f.logins != 0 || f.callbacks != 0 {
				t.Fatalf("%s %s reached OAuth: %d", method, path, w.Code)
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("OAuth HEAD returned body")
			}
		}
	}
	for _, path := range []string{"/auth/google", "/auth/google/callback?state=fixture&code=fixture"} {
		if w := methodSend(h, "GET", path, "", "", ""); w.Code != 302 {
			t.Fatal("GET OAuth changed", w.Code)
		}
	}
	if f.logins != 1 || f.callbacks != 1 {
		t.Fatal("GET callback not dispatched")
	}
}

func TestHEADResponsesKeepHeadersWithoutBody(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = methodOrg()
	h := s.handler()
	for _, path := range []string{"/v1/organizations/" + fixtureOrg, "/v1/system/settings", "/openapi.json", "/not-a-route"} {
		get := methodSend(h, "GET", path, "", "alice", "")
		head := methodSend(h, "HEAD", path, `{"name":"ignored"}`, "alice", "")
		if head.Code != get.Code || head.Body.Len() != 0 {
			t.Fatalf("HEAD parity %s: %d/%d body=%q", path, get.Code, head.Code, head.Body.String())
		}
		for _, name := range []string{"Content-Type", "Cache-Control"} {
			if head.Header().Get(name) != get.Header().Get(name) {
				t.Fatalf("%s differs", name)
			}
		}
		if head.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) {
			t.Fatalf("HEAD %s representation length differs", path)
		}
	}
	server := httptest.NewServer(h)
	defer server.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "HEAD", server.URL+"/v1/organizations/"+fixtureOrg, strings.NewReader(`{"name":"ignored"}`))
	req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength <= 0 {
		t.Fatal("real HTTP HEAD headers", res.StatusCode, res.ContentLength)
	}
}

func TestHEADDoesNotInventAnUnknownRepresentationLength(t *testing.T) {
	h := headResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatal("HEAD method was rewritten")
		}
		w.Header().Set("Location", "https://storage.example.test/signed-head")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	w := methodSend(h, http.MethodHead, "/media/fixture", "", "", "")
	if w.Code != 307 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "" || w.Header().Get("Location") != "https://storage.example.test/signed-head" {
		t.Fatal("HEAD redirect headers changed", w.Code, w.Header())
	}
}

type methodSalesFixture struct {
	*methodOrgFixture
	system.SalesRepository
	writes int
}

func (f *methodSalesFixture) SalesStructure(context.Context, string) (system.SalesStructure, error) {
	return system.SalesStructure{Version: 1}, nil
}
func (f *methodSalesFixture) PlaceSalesMember(context.Context, string, string, system.SalesPlacement) (system.SalesStructure, error) {
	f.writes++
	return system.SalesStructure{}, nil
}

func TestLatentHEADFallthroughsChooseReadBranches(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &methodSalesFixture{methodOrgFixture: methodOrg()}
	s.system = f
	m := &methodModelFixture{modelAccessMemory: &modelAccessMemory{org: map[string]map[string]bool{}}}
	s.modelAccess = m
	s.providerAdmin = nil
	h := s.handler()
	for _, path := range []string{"/v1/organizations/" + fixtureOrg + "/members", "/v1/organizations/" + fixtureOrg + "/invitations", "/v1/system/model-settings", "/v1/system/sales"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := methodSend(h, method, path, `{"role":"supermanager","public_id":"changed","version":1}`, "alice", "")
			if w.Code != 200 || f.writes != 0 || m.writes != 0 {
				t.Fatalf("latent %s %s: %d writes=%d/%d", method, path, w.Code, f.writes, m.writes)
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD returned body")
			}
		}
	}
}

func TestSystemScopeDoesNotBypassOrganizationWriteScope(t *testing.T) {
	for _, path := range []string{"/v1/organizations", "/v1/organizations/" + fixtureOrg + "/credits"} {
		t.Run(path, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := methodOrg()
			s.system = f
			h := s.handler()
			_, limited := issueTestKey(t, h, "alice", `{"name":"No organization scope","scopes":["generation","system:manage"]}`)
			for _, method := range []string{"HEAD", "POST"} {
				w := methodSend(h, method, path, `{"name":"Changed","amount_usd":10}`, "", limited)
				if w.Code != 403 || f.writes != 0 || f.credits != 0 {
					t.Fatalf("%s bypassed missing organization scope: %d", method, w.Code)
				}
			}
		})
	}
}
