package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/audit"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCompanyReadExistingReportAuditInfoHandlers(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	task := completeMediaTask(t, f, store, ctx, testmedia.PNG512("company report fixture"))
	key := companyReadKey(t, f)
	for _, suffix := range []string{"/audit/events", "/audit/revoked-keys", "/reports", "/reports?format=csv", "/reports?view=summary"} {
		t.Run(suffix, func(t *testing.T) {
			path := "/v1/organizations/" + v.Tokens.Company + suffix
			direct := resourceDirect(f, "GET", path, "", key, "")
			expectFixture(t, direct, 200)
			got := facadeRequest(t, f, "GET", path, "", "", true, nil)
			expectFixture(t, got, 200)
			if suffix == "/reports" && !strings.Contains(got.Body.String(), task.ID) {
				t.Fatal("completed task absent", got.Body.String())
			}
		})
	}
	t.Run("info", func(t *testing.T) {
		expectFixture(t, resourceDirect(f, "GET", "/v1/info", "", key, ""), 200)
		expectFixture(t, facadeRequest(t, f, "GET", "/v1/info", "", "", true, nil), 200)
	})
}

func companyReadKey(t *testing.T, f *activeHTTPFixture) string {
	t.Helper()
	v := f.current(t)
	w := callFixture(t, f.central, fixtureCentral, "/v1/api-keys", `{"name":"company read fixture","organization_id":"`+v.Tokens.Company+`","resource_mode":"shared","scopes":["generation","organizations:manage","keys:manage"]}`, f.centralCookie)
	expectFixture(t, w, 201)
	var key struct{ Secret string }
	if json.Unmarshal(w.Body.Bytes(), &key) != nil || key.Secret == "" {
		t.Fatal("key not returned")
	}
	return key.Secret
}

func companyDirectBrowser(f *activeHTTPFixture, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, fixtureCentral+path, strings.NewReader(body))
	r.AddCookie(f.centralCookie)
	r.Header.Set("Origin", fixtureCentral)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.central.ServeHTTP(w, r)
	return w
}

func TestCompanyReadReportsFiltersCSVAndRecipient(t *testing.T) {
	f, store, _, ctx := mediaHTTPFixture(t)
	v := f.current(t)
	expectFixture(t, companyDirectBrowser(f, "PUT", "/v1/organizations/"+v.Tokens.Company, `{"name":"=1+1","default_weekly_limit_usd":null}`), 204)
	task := completeMediaTask(t, f, store, ctx, testmedia.PNG512("report row"))
	key := companyReadKey(t, f)
	member, memberKey := resourceCoworkerKey(t, f, "member")
	base := "/v1/organizations/" + v.Tokens.Company + "/reports"
	for _, method := range []string{"GET", "HEAD"} {
		for _, query := range []string{"", "?format=csv", "?view=summary&interval=day", "?generation_id=" + task.ID, "?user_id=" + member} {
			direct := resourceDirect(f, method, base+query, "", key, "")
			got := facadeRequest(t, f, method, base+query, "", "", true, nil)
			expectFixture(t, direct, 200)
			expectFixture(t, got, 200)
			if !bytes.Equal(direct.Body.Bytes(), got.Body.Bytes()) {
				t.Fatal("report bytes differ", method, query)
			}
			for _, header := range []string{"Content-Type", "Content-Disposition"} {
				if direct.Header().Get(header) != got.Header().Get(header) {
					t.Fatal("report metadata", header)
				}
			}
			if method == "HEAD" && got.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
			if method == "GET" && query == "?format=csv" {
				rows, e := csv.NewReader(strings.NewReader(got.Body.String())).ReadAll()
				if e != nil || len(rows) != 2 || rows[1][1] != "'=1+1" || rows[1][2] != v.Tokens.Company {
					t.Fatal("CSV population/formula", e, rows)
				}
				if strings.Contains(rows[0][0], "Profit") || strings.Contains(got.Body.String(), "Provider charged") || strings.Contains(got.Body.String(), "Markup (%)") {
					t.Fatal("commercial columns leaked")
				}
				if got.Header().Get("Content-Length") != strconv.Itoa(got.Body.Len()) {
					t.Fatal("CSV length")
				}
			}
		}
	}
	// SDK stays the actual key recipient; browser management stays the signed-in
	// company actor even when the selected generation key belongs to a coworker.
	for _, query := range []string{"", "?user_id=" + v.Tokens.Actor, "?user=" + v.Tokens.Actor, "?generation_id=" + task.ID} {
		direct := resourceDirect(f, "GET", base+query, "", memberKey, "")
		sdk := facadeRequest(t, f, "GET", base+query, "", memberKey, false, nil)
		expectFixture(t, direct, 200)
		expectFixture(t, sdk, 200)
		if !bytes.Equal(direct.Body.Bytes(), sdk.Body.Bytes()) || strings.Contains(sdk.Body.String(), task.ID) {
			t.Fatal("member report widened")
		}
	}
	browser := facadeRequest(t, f, "GET", base, "", memberKey, true, nil)
	expectFixture(t, browser, 200)
	if !strings.Contains(browser.Body.String(), task.ID) {
		t.Fatal("browser management used selected recipient")
	}
	for _, query := range []string{"?format=pdf", "?offset=-1", "?timezone=invalid", "?generation_id=bad", "?view=summary&format=csv"} {
		direct := resourceDirect(f, "GET", base+query, "", key, "")
		got := facadeRequest(t, f, "GET", base+query, "", "", true, nil)
		expectFixture(t, direct, 400)
		expectFixture(t, got, 400)
	}
	for _, path := range []string{base + "?organization_id=" + uuid.NewString(), base + "?format=csv&format=csv", "/v1/system/reports", "/v1/admin/reports", "/v1/organizations/" + uuid.NewString() + "/reports"} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", key, true, nil), 403)
	}
	expectFixture(t, facadeRequest(t, f, "GET", base, "", "invalid", true, nil), 403)
	// Global central administration must not add commercial report fields.
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(context.Background(), "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	got := facadeRequest(t, f, "GET", base+"?format=csv", "", "", true, nil)
	expectFixture(t, got, 200)
	if strings.Contains(got.Body.String(), "Profit") || strings.Contains(got.Body.String(), "Provider charged") {
		t.Fatal("central admin escaped company ceiling")
	}
}

func TestCompanyReadAuditHistoryRedactionAndTenant(t *testing.T) {
	f, _, _, _ := mediaHTTPFixture(t)
	v := f.current(t)
	ctx := context.Background()
	sys := postgres.NewSystemRepository(f.centralDB)
	key := companyReadKey(t, f)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	access, e := sys.Organization(ctx, v.Tokens.Actor, v.Tokens.Company)
	if e != nil {
		t.Fatal(e)
	}
	if e = sys.Ensure(ctx, []modelaccess.Ref{{Provider: "mock", Model: "audit-model", Name: "Audit model"}}); e != nil {
		t.Fatal(e)
	}
	models, e := sys.List(ctx, v.Tokens.Company)
	if e != nil || len(models) == 0 {
		t.Fatal("audit model fixture", e)
	}
	access.ModelIDs = []string{models[0].ID}
	if _, e = sys.SaveOrganization(ctx, v.Tokens.Actor, v.Tokens.Company, access); e != nil {
		t.Fatal(e)
	}
	base := "/v1/organizations/" + v.Tokens.Company + "/audit/"
	listed := facadeRequest(t, f, "GET", base+"events?scope=organization&action=model_access", "", "", true, nil)
	expectFixture(t, listed, 200)
	var events struct{ Data []audit.Event }
	if json.Unmarshal(listed.Body.Bytes(), &events) != nil || len(events.Data) == 0 {
		t.Fatal("audit event absent", listed.Body.String())
	}
	id := events.Data[0].ID
	for _, method := range []string{"GET", "HEAD"} {
		for _, suffix := range []string{"events?scope=organization&action=model_access", fmt.Sprintf("events/%d", id)} {
			direct := resourceDirect(f, method, base+suffix, "", key, "")
			got := facadeRequest(t, f, method, base+suffix, "", "", true, nil)
			expectFixture(t, direct, 200)
			expectFixture(t, got, 200)
			if !bytes.Equal(direct.Body.Bytes(), got.Body.Bytes()) {
				t.Fatal("audit projection mismatch")
			}
			if method == "GET" && (!strings.Contains(got.Body.String(), `"restricted":true`) || strings.Contains(got.Body.String(), `"model_ids"`)) {
				t.Fatal("admin audit detail escaped company ceiling")
			}
		}
	}
	_, issued, e := postgres.NewAPIKeyRepository(f.centralDB).Authenticate(ctx, apikey.Hash(key))
	if e != nil {
		t.Fatal(e)
	}
	expectFixture(t, companyDirectBrowser(f, "DELETE", "/v1/api-keys/"+issued.ID, ""), 204)
	revoked := facadeRequest(t, f, "GET", base+"revoked-keys?offset=0", "", "", true, nil)
	expectFixture(t, revoked, 200)
	if !strings.Contains(revoked.Body.String(), issued.ID) || strings.Contains(revoked.Body.String(), key) {
		t.Fatal("revoked history missing or secret leaked")
	}
	foreign, e := sys.CreateOrganization(ctx, v.Tokens.Actor, "Foreign audit", v.Tokens.Actor, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	other, e := sys.Organization(ctx, v.Tokens.Actor, foreign.ID)
	if e != nil {
		t.Fatal(e)
	}
	other.ModelIDs = []string{models[0].ID}
	if _, e = sys.SaveOrganization(ctx, v.Tokens.Actor, foreign.ID, other); e != nil {
		t.Fatal(e)
	}
	foreignList := companyDirectBrowser(f, "GET", "/v1/organizations/"+foreign.ID+"/audit/events", "")
	expectFixture(t, foreignList, 200)
	var foreignEvents struct{ Data []audit.Event }
	if json.Unmarshal(foreignList.Body.Bytes(), &foreignEvents) != nil || len(foreignEvents.Data) == 0 {
		t.Fatal("foreign event fixture", foreignList.Body.String())
	}
	rows := foreignEvents.Data
	expectFixture(t, facadeRequest(t, f, "GET", fmt.Sprintf("%sevents/%d", base, rows[0].ID), "", "", true, nil), 404)
	for _, path := range []string{base + "events?organization_id=" + foreign.ID, "/v1/audit/events", "/v1/audit/revoked-keys", base + "events/01", base + "events/-1", base + "events/9223372036854775808"} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", "", true, nil), 403)
	}
	// A live lower-role member cannot borrow a coworker's stronger management key.
	_, manager, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e = f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, v.Tokens.Company, manager.ID); e != nil {
		t.Fatal(e)
	}
	if e = sys.SetOrganizationMember(ctx, manager.ID, v.Tokens.Company, v.Tokens.Actor, "member", nil, false, false); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, facadeRequest(t, f, "GET", base+"events", "", "", true, nil), 403)
}

func TestCompanyReadStorageProjectionUsageAndInfo(t *testing.T) {
	var deps api.Dependencies
	f, _, _, ctx := mediaHTTPFixture(t, func(d *api.Dependencies) { deps = *d })
	v := f.current(t)
	cipher, e := providerconfig.NewCipher("isolated-company-read-cipher")
	if e != nil {
		t.Fatal(e)
	}
	repo := postgres.NewStorageSettingsRepository(f.centralDB)
	manager, e := storage.NewManager(repo, cipher, t.TempDir(), &http.Client{Transport: noResourceNetwork{t}}, 8<<20)
	if e != nil {
		t.Fatal(e)
	}
	storageCtx := identity.WithResourceOwners(ctx, identity.SharedOwnerID, v.Tokens.Company)
	// Read-only settings fixture: persist the same encrypted record shape without
	// running Manager.Update's network check. The no-network client remains armed.
	raw, e := json.Marshal(storage.S3Config{Endpoint: "https://storage.fixture.test", Region: "fixture", Bucket: "company-read", Prefix: "company-prefix", PathStyle: true, AccessKeyID: "synthetic-visible-key-id", SecretAccessKey: "synthetic-never-visible-secret"})
	if e != nil {
		t.Fatal(e)
	}
	encrypted, e := cipher.Encrypt(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.Save(storageCtx, storage.SettingsRecord{Driver: "s3", ConnectionID: uuid.NewString(), Ciphertext: encrypted}); e != nil {
		t.Fatal(e)
	}

	if e = repo.RecordObject(ctx, "company-reference", "", 1234); e != nil {
		t.Fatal(e)
	}
	if e = repo.RecordObject(identity.WithOwnerID(context.Background(), identity.SharedOwnerID), "other-shared-reference", "", 9876); e != nil {
		t.Fatal(e)
	}
	deps.Storage = manager
	f.central = mutationGateway(t, f, func(d *api.Dependencies) { *d = deps })
	key := companyReadKey(t, f)
	for _, path := range []string{"/v1/organizations/" + v.Tokens.Company + "/storage", "/v1/storage/usage", "/v1/info"} {
		for _, method := range []string{"GET", "HEAD"} {
			direct := resourceDirect(f, method, path, "", key, "")
			got := facadeRequest(t, f, method, path, "", v.Tokens.PlaygroundKey, true, nil)
			expectFixture(t, direct, 200)
			expectFixture(t, got, 200)
			if !bytes.Equal(direct.Body.Bytes(), got.Body.Bytes()) {
				t.Fatal("read projection changed", path)
			}
			if strings.Contains(got.Body.String(), "synthetic-never-visible-secret") {
				t.Fatal("storage secret")
			}
			if method == "GET" && strings.HasSuffix(path, "/storage") && (!strings.Contains(got.Body.String(), "synthetic-visible-key-id") || !strings.Contains(got.Body.String(), `"can_manage":true`)) {
				t.Fatal("manager view lost")
			}
			if method == "GET" && path == "/v1/storage/usage" {
				var usage storage.Usage
				if json.Unmarshal(got.Body.Bytes(), &usage) != nil || usage.UsedBytes != 1234 || usage.Applies {
					t.Fatal("owner-resolved usage changed", got.Body.String())
				}
			}
			if method == "GET" && path == "/v1/info" {
				var info struct{ Console map[string]any }
				json.Unmarshal(got.Body.Bytes(), &info)
				if info.Console["installation_role"] != "central" || info.Console["central_origin"] != fixtureCentral || info.Console["personal_providers"] != false {
					t.Fatal("central info metadata", got.Body.String())
				}
			}
		}
	}
	expectFixture(t, facadeRequest(t, f, "GET", "/v1/storage/usage", "", "", true, nil), 403)
	// Private storage config does not become an administrative operation ceiling.
	for _, path := range []string{"/v1/admin/storage", "/v1/system/storage", "/v1/organizations/" + uuid.NewString() + "/storage"} {
		expectFixture(t, facadeRequest(t, f, "GET", path, "", key, true, nil), 403)
	}
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		expectFixture(t, facadeRequest(t, f, method, "/v1/organizations/"+v.Tokens.Company+"/storage", "{}", key, true, nil), 403)
	}
	// Model policy updates occur directly on central, never through the grant.
	sys := postgres.NewSystemRepository(f.centralDB)
	if e = sys.SetAdmin(context.Background(), "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	models, e := sys.List(ctx, v.Tokens.Company)
	if e != nil || len(models) == 0 {
		t.Fatal("info catalog did not initialize", e)
	}
	disabled := false
	for _, m := range models {
		if _, e = sys.Update(context.Background(), v.Tokens.Actor, m.ID, modelaccess.Update{PublicID: m.PublicID, EnabledByDefault: &disabled, Version: m.Version}); e != nil {
			t.Fatal(e)
		}
	}
	got := facadeRequest(t, f, "GET", "/v1/info", "", "", true, nil)
	expectFixture(t, got, 200)
	var info struct {
		Provider, Model string
		Live            bool
	}
	if json.Unmarshal(got.Body.Bytes(), &info) != nil || info.Provider != "" || info.Model != "" || info.Live {
		t.Fatal("disabled operator catalog leaked", got.Body.String())
	}
}

func TestCompanyReadSDKScopesRemainIndependentFromBrowserManagement(t *testing.T) {
	f, _, _, _ := mediaHTTPFixture(t)
	v := f.current(t)
	key := v.Tokens.PlaygroundKey // Actual minimal delegated bootstrap key; no synthetic scope edits.
	base := "/v1/organizations/" + v.Tokens.Company
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", base + "/audit/events", 403}, {"HEAD", base + "/audit/events", 403},
		{"GET", base + "/audit/revoked-keys", 403}, {"GET", base + "/storage", 403}, {"HEAD", base + "/storage", 403},
		{"GET", base + "/reports", 200}, {"HEAD", base + "/reports", 403}, {"GET", base + "/reports?format=csv", 200},
		{"GET", "/v1/info", 200}, {"HEAD", "/v1/info", 200},
	} {
		direct := resourceDirect(f, tc.method, tc.path, "", key, "")
		sdk := facadeRequest(t, f, tc.method, tc.path, "", key, false, nil)
		expectFixture(t, direct, tc.status)
		expectFixture(t, sdk, tc.status)
		if tc.method == "HEAD" && sdk.Body.Len() != 0 {
			t.Fatal("SDK HEAD error body")
		}
	}
	for _, path := range []string{base + "/audit/events", base + "/audit/revoked-keys", base + "/reports"} {
		// Browser management always uses the verified company Supermanager, with
		// independent validation (not management scope adoption) of a selected key.
		expectFixture(t, facadeRequest(t, f, "HEAD", path, "", key, true, nil), 200)
		expectFixture(t, facadeRequest(t, f, "GET", path, "", "invalid", true, nil), 403)
	}
}
