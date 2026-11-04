package companyinterface

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCompanyResourceGenerationResponseKeepsOptionalLocationBound(t *testing.T) {
	secret, _ := NewSecret()
	company := uuid.NewString()
	id := uuid.NewString()
	expected := "/v1/generations/" + id
	prepared := CustomerContext{snapshot: InstallationSnapshot{Installation: LocalInstallation{Company: company, GrantID: uuid.NewString(), CentralOrigin: "https://central.example", LocalOrigin: "https://local.example", Credential: secret, GrantEpoch: 1}}}
	for _, tc := range []struct {
		name, location, statusURL, organization string
		duplicate, valid                        bool
	}{{"absent", "", expected, company, false, true}, {"exact", expected, expected, company, false, true}, {"foreign", "https://foreign.example" + expected, expected, company, false, false}, {"other-object", "/v1/generations/" + uuid.NewString(), expected, company, false, false}, {"encoded", "/v1/%67enerations/" + id, expected, company, false, false}, {"query", expected + "?return_url=x", expected, company, false, false}, {"wrong-body", "", expected + "/content", company, false, false}, {"wrong-company", "", expected, uuid.NewString(), false, false}, {"duplicate", expected, expected, company, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := NewClient("https://central.example")
			calls := 0
			client.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := json.Marshal(map[string]any{"id": id, "object": "generation", "organization_id": tc.organization, "status_url": tc.statusURL, "prompt": "https://untrusted.example/keep"})
				response := protocolResponse(202, string(raw))
				response.Header.Set("ETag", `"central-bytes"`)
				if tc.location != "" {
					response.Header.Set("Location", tc.location)
				}
				if tc.duplicate {
					response.Header.Add("Location", tc.location)
				}
				return response, nil
			})
			result, e := client.customer(context.Background(), prepared, "POST", "/v1/provider-resources", "", []byte(`{"provider":"kling","workflow_model":"fixture","kind":"voice","name":"fixture"}`), http.Header{"Content-Type": []string{"application/json"}, "Idempotency-Key": []string{"resource-stable"}}, true, "gw_"+strings.Repeat("k", 43))
			if calls != 1 {
				t.Fatal("uncertain resource mutation resent")
			}
			if !tc.valid {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal("unbound resource response accepted", e)
				}
				return
			}
			if e != nil || result.Header.Get("ETag") != "" || !strings.Contains(string(result.Body), LocalCustomerPrefix+"v1/generations/"+id) || !strings.Contains(string(result.Body), "https://untrusted.example/keep") {
				t.Fatal("typed mapping or validator handling changed", e)
			}
			want := ""
			if tc.location != "" {
				want = LocalCustomerPrefix + "v1/generations/" + id
			}
			if result.Header.Get("Location") != want {
				t.Fatal("invented/lost resource Location")
			}
		})
	}
	// Optional Location is restricted to tagged resource admissions.
	raw, _ := json.Marshal(map[string]any{"id": id, "object": "generation", "status_url": expected})
	for _, response := range []string{"generation", "template-generations"} {
		if _, _, e := generationStatusResponse(Contract{ResponseURLs: response}, 202, "POST", "/v1/generations", company, http.Header{}, raw); !errors.Is(e, ErrUnavailable) {
			t.Fatal("ordinary generation Location requirement weakened")
		}
	}
}
func TestCompanyResourceContractBudgetsAndDenials(t *testing.T) {
	company := uuid.NewString()
	p := Principal{Grant: Grant{Company: company}, Kind: CentralKey, Scopes: []string{"generation"}}
	for _, tc := range []struct {
		method, path, query, body string
		valid                     bool
	}{{"GET", "/v1/models/kling%2Fvoice/preparations", "provider=kling", "", true}, {"GET", "/v1/provider-resources", "provider=kling&kind=voice&limit=20&offset=0", "", true}, {"DELETE", "/v1/references/providers/bytedance/assets/" + uuid.NewString(), "detach=1", "", true}, {"GET", "/v1/references/providers/bytedance/groups", "inventory=1", "", false}, {"POST", "/v1/references/providers/bytedance/import", "", `{"asset_id":"x"}`, false}, {"DELETE", "/v1/provider-resources/" + uuid.NewString(), "", "", false}, {"POST", "/v1/references/providers/bytedance/assets", "", `{"reference_id":"x","url":"https://foreign.example"}`, false}, {"GET", "/v1/provider-resources", "provider=kling&provider=other", "", false}} {
		_, e := RequestContract(tc.method, tc.path, tc.query, []byte(tc.body), p)
		if (e == nil) != tc.valid {
			t.Fatalf("contract %s %s: %v", tc.method, tc.path, e)
		}
	}
	if CustomerTimeout("POST", "/v1/references/providers/bytedance/assets") != 45*time.Second || CustomerTimeout("POST", "/v1/provider-resources") != ProtocolTimeout || CustomerBodyLimit("POST", "/v1/provider-resource-presets/import") != 32<<10 {
		t.Fatal("resource-specific budget changed")
	}
}
