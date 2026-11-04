package companyinterface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCompanyPaymentReadSurfacesTwoOriginsAndCurrentPolicy(t *testing.T) {
	f, wire := facadeHTTP(t)
	v := f.current(t)
	provider, id := uuid.NewString(), uuid.NewString()
	if _, e := f.centralDB.Exec(`INSERT INTO payment_providers(id,kind,name,enabled,live,account_id,created_by) VALUES($1,'stripe','Fixture',TRUE,TRUE,'fixture',$2)`, provider, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	if _, e := f.centralDB.Exec(`INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,fee_mode,amount_cents,currency,live,expires_at,stablecoin) VALUES($1,$2,$3,$4,'read-fixture','service',1000,'usd',TRUE,clock_timestamp()-INTERVAL '1 hour','{"address":"0x1111111111111111111111111111111111111111"}')`, id, v.Tokens.Company, provider, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("q", 32))
	funding := payments.New(postgres.NewSystemRepository(f.centralDB), cipher, fixtureCentral, nil)
	f.central = mutationGateway(t, f, func(d *api.Dependencies) { d.Payments = funding })
	key, secret, e := apikey.Generate(apikey.CreateInput{UserID: v.Tokens.Actor, OrganizationID: v.Tokens.Company, Name: "Read fixture", ResourceMode: "shared", StorageMode: "shared", Scopes: []string{apikey.Generation, apikey.OrganizationsManage}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = postgres.NewAPIKeyRepository(f.centralDB).Create(identity.WithUser(context.Background(), identity.User{ID: v.Tokens.Actor}), v.Tokens.Actor, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		var s string
		if e := f.centralDB.QueryRow(`SELECT jsonb_build_object('payments',(SELECT jsonb_agg(to_jsonb(p))FROM payments p),'credits',(SELECT jsonb_agg(to_jsonb(c))FROM organization_credits c),'invoices',(SELECT jsonb_agg(to_jsonb(i))FROM payment_invoice_documents i))::text`).Scan(&s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	before := snapshot()
	base := "/v1/organizations/" + v.Tokens.Company
	paths := []string{base + "/payment-methods", base + "/payments?offset=0", base + "/payments/" + id + "/qr"}
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			direct := resourceDirect(f, method, path, "", secret, "")
			expectFixture(t, direct, 200)
			for _, browser := range []bool{true, false} {
				supplied := secret
				if browser {
					supplied = ""
				}
				got := facadeRequest(t, f, method, path, "", supplied, browser, nil)
				expectFixture(t, got, 200)
				if !bytes.Equal(got.Body.Bytes(), direct.Body.Bytes()) {
					t.Fatal("central/local view mismatch", method, path)
				}
				if got.Header().Get("Content-Type") != direct.Header().Get("Content-Type") || got.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("read headers")
				}
			}
		}
		expectFixture(t, facadeRequest(t, f, "GET", path, "", v.Tokens.PlaygroundKey, false, nil), 403)
		expectFixture(t, facadeRequest(t, f, "GET", strings.Replace(path, v.Tokens.Company, uuid.NewString(), 1), "", secret, false, nil), 403)
	}
	list := facadeRequest(t, f, "GET", base+"/payments?offset=0", "", "", true, nil)
	var result struct{ Data []payments.Payment }
	if json.Unmarshal(list.Body.Bytes(), &result) != nil || len(result.Data) != 1 || result.Data[0].Status != "expired" {
		t.Fatal("projected list", list.Body.String())
	}
	qrPath := paths[2]
	direct := resourceDirect(f, "GET", qrPath, "", secret, "")
	ranged := facadeRequest(t, f, "GET", qrPath, "", secret, false, func(r *http.Request) { r.Header.Set("Range", "bytes=0-5") })
	expectFixture(t, ranged, 200)
	if !bytes.Equal(direct.Body.Bytes(), ranged.Body.Bytes()) {
		t.Fatal("invented Range semantics")
	}
	wire.lose.Store(true)
	expectFixture(t, facadeRequest(t, f, "GET", paths[1], "", "", true, nil), 503)
	expectFixture(t, facadeRequest(t, f, "GET", paths[1], "", "", true, nil), 200)
	if snapshot() != before {
		t.Fatal("navigation/read recovery changed financial state")
	}
	if countFixture(t, f.localDB, "payments") != 0 || countFixture(t, f.localDB, "organization_credits") != 0 {
		t.Fatal("local financial fallback")
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	for _, path := range paths {
		for _, browser := range []bool{true, false} {
			expectFixture(t, facadeRequest(t, f, "GET", path, "", secret, browser, nil), 403)
		}
	}
	if snapshot() != before {
		t.Fatal("denied reads changed finance")
	}
}
