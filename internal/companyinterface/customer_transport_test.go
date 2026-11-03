package companyinterface

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCompanyCustomerClientFixedHeadersRedirectAndNoMutationRetry(t *testing.T) {
	secret, _ := NewSecret()
	prepared := CustomerContext{snapshot: InstallationSnapshot{Installation: LocalInstallation{Company: uuid.NewString(), GrantID: uuid.NewString(), CentralOrigin: "https://central.example", LocalOrigin: "https://local.example", Credential: secret, GrantEpoch: 1}}}
	key := "gw_" + strings.Repeat("k", 43)
	for _, mode := range []string{"success", "redirect", "lost"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := NewClient("https://central.example")
			calls := 0
			c.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "central.example" || r.URL.Path != CustomerSDKPrefix+"v1/projects" || r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get(InstallationHeader) != secret || r.Header.Get(LocalOriginHeader) != "https://local.example" {
					t.Fatal("fixed credential binding")
				}
				for _, name := range []string{"Cookie", "Forwarded", "X-Forwarded-Host", "X-API-Key", SessionHeader, LocalSessionHeader} {
					if len(r.Header.Values(name)) != 0 {
						t.Fatal("caller header leaked", name)
					}
				}
				if mode == "lost" {
					return nil, errors.New("uncertain mutation")
				}
				response := protocolResponse(201, `{"id":"fixture"}`)
				response.Header.Set("Set-Cookie", "attacker=1")
				if mode == "redirect" {
					response.StatusCode = 307
					response.Header.Set("Location", "https://storage.example/target")
				}
				return response, nil
			})
			incoming := http.Header{"Content-Type": []string{"application/json"}, "Cookie": []string{"bad=1"}, "Forwarded": []string{"host=other"}, "X-Forwarded-Host": []string{"other"}, "X-API-Key": []string{"bad"}, InstallationHeader: []string{"bad"}, SessionHeader: []string{"bad"}, LocalSessionHeader: []string{"bad"}}
			result, e := c.customer(context.Background(), prepared, "POST", "/v1/projects", "", []byte(`{"name":"fixture"}`), incoming, true, key)
			if calls != 1 {
				t.Fatal("customer mutation retried", calls)
			}
			if mode == "success" {
				if e != nil || result.Status != 201 || len(result.Header.Values("Set-Cookie")) != 0 {
					t.Fatal("unsafe response", e)
				}
			} else if !errors.Is(e, ErrUnavailable) {
				t.Fatal("redirect/lost reply not terminal", e)
			}
		})
	}
}
func TestCompanyExpandedContractsKeepTenantAndBodyShape(t *testing.T) {
	company := uuid.NewString()
	p := Principal{Grant: Grant{Company: company}, Kind: CentralSession}
	for _, tc := range []struct{ method, path, query, body string }{
		{"PUT", "/v1/organizations/" + company, "", `{"name":"ok","default_weekly_limit_usd":null}`},
		{"PUT", "/v1/organizations/" + company + "/members/" + uuid.NewString(), "", `{"role":"member","weekly_limit_usd":null}`},
		{"GET", "/v1/organizations", "view=summary&timezone=Europe%2FBerlin&from=2026-11-01&to=2026-11-03", ""},
	} {
		if _, e := RequestContract(tc.method, tc.path, tc.query, []byte(tc.body), p); e != nil {
			t.Fatal("valid exact shape denied", tc, e)
		}
	}
	for _, tc := range []struct{ method, path, query, body string }{
		{"PUT", "/v1/organizations/" + company, "", `{"name":"ok","default_weekly_limit_usd":null,"credit_limit_usd":1}`},
		{"GET", "/v1/api-keys/usage", "organization_id=" + uuid.NewString(), ""},
		{"GET", "/v1/api-keys/usage", "month=2026-11&month=2026-12", ""},
		{"GET", "/v1/organizations", "view=summary#fragment", ""},
		{"POST", "/v1/projects", "", `{"name":"ok","owner_id":"foreign"}`},
	} {
		if _, e := RequestContract(tc.method, tc.path, tc.query, []byte(tc.body), p); e == nil {
			t.Fatal("invalid shape admitted", tc)
		}
	}
	if Classify("GET", "/v1/models/vendor%2Fmodel/preparations").ID != "model-preparations" || Classify("GET", "/v1/models/vendor/model").ID != "central-model" {
		t.Fatal("model route ambiguity")
	}
}
