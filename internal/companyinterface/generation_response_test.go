package companyinterface

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func TestCompanyGenerationStatusURLIsObjectBound(t *testing.T) {
	secret, _ := NewSecret()
	company := uuid.NewString()
	id := uuid.NewString()
	other := uuid.NewString()
	prepared := CustomerContext{snapshot: InstallationSnapshot{Installation: LocalInstallation{Company: company, GrantID: uuid.NewString(), CentralOrigin: "https://central.example", LocalOrigin: "https://local.example", Credential: secret, GrantEpoch: 1}}}
	expected := "/v1/generations/" + id
	cases := []struct {
		name, location, statusURL, returnedID, organization, object, method, path string
		duplicate, valid                                                          bool
	}{
		{"valid", expected, expected, id, company, "generation", "POST", "/v1/generations", false, true},
		{"foreign host", "https://foreign.example" + expected, expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"absolute alias", "https://central.example" + expected, expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"foreign object", "/v1/generations/" + other, expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"encoded path", "/v1/%67enerations/" + id, expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"query injection", expected + "?return_url=https://foreign.example", expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"fragment", expected + "#other", expected, id, company, "generation", "POST", "/v1/generations", false, false},
		{"body mismatch", expected, "/v1/generations/" + other, id, company, "generation", "POST", "/v1/generations", false, false},
		{"foreign company", expected, expected, id, other, "generation", "POST", "/v1/generations", false, false},
		{"wrong object type", expected, expected, id, company, "video", "POST", "/v1/generations", false, false},
		{"duplicate", expected, expected, id, company, "generation", "POST", "/v1/generations", true, false},
		{"requested object mismatch", "", "/v1/generations/" + other, other, company, "generation", "GET", expected, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClient("https://central.example")
			calls := 0
			prompt := `user text: https://foreign.example/v1/generations/not-a-route`
			c.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := json.Marshal(map[string]any{"id": tc.returnedID, "object": tc.object, "organization_id": tc.organization, "status_url": tc.statusURL, "prompt": prompt, "parameters": map[string]any{"status_url": "https://foreign.example/arbitrary"}})
				status := 202
				if tc.method == "GET" {
					status = 200
				}
				out := protocolResponse(status, string(body))
				if tc.location != "" {
					out.Header.Set("Location", tc.location)
				}
				if tc.duplicate {
					out.Header.Add("Location", tc.location)
				}
				return out, nil
			})
			body := []byte(`{"model":"mock/fixture","prompt":"fixture"}`)
			if tc.method == "GET" {
				body = nil
			}
			result, e := c.customer(context.Background(), prepared, tc.method, tc.path, "", body, http.Header{"Content-Type": []string{"application/json"}}, true, "gw_"+strings.Repeat("k", 43))
			if calls != 1 {
				t.Fatal("mutation retried")
			}
			if !tc.valid {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal("unbound response accepted", e)
				}
				return
			}
			if e != nil || result.Header.Get("Location") != LocalCustomerPrefix+strings.TrimPrefix(expected, "/") {
				t.Fatal("valid status location lost", e)
			}
			var fields map[string]json.RawMessage
			json.Unmarshal(result.Body, &fields)
			var actual string
			json.Unmarshal(fields["prompt"], &actual)
			if actual != prompt || !strings.Contains(string(fields["parameters"]), "https://foreign.example/arbitrary") {
				t.Fatal("arbitrary user JSON rewritten")
			}
		})
	}
}

func TestCompanyGenerationHEADResponseIsRequestBound(t *testing.T) {
	id := uuid.NewString()
	company := uuid.NewString()
	path := "/v1/generations/" + id
	for _, tc := range []struct {
		name, path, location, body string
		duplicate, valid           bool
	}{
		{"bodyless", path, "", "", false, true},
		{"exact location", path, path, "", false, true},
		{"video", "/v1/videos/" + id, "/v1/videos/" + id, "", false, true},
		{"company", "/v1/organizations/" + company + "/generations/" + id, path, "", false, true},
		{"foreign object", path, "/v1/generations/" + uuid.NewString(), "", false, false},
		{"foreign origin", path, "https://foreign.example" + path, "", false, false},
		{"duplicate", path, path, "", true, false},
		{"unexpected body", path, "", `{"id":"` + id + `"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.location != "" {
				header.Set("Location", tc.location)
			}
			if tc.duplicate {
				header.Add("Location", tc.location)
			}
			raw, location, e := generationStatusResponse(Classify("HEAD", tc.path), 200, "HEAD", tc.path, company, header, []byte(tc.body))
			if !tc.valid {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal("unbound HEAD accepted", e)
				}
				return
			}
			if e != nil || len(raw) != 0 {
				t.Fatal("HEAD representation invented", e)
			}
			if tc.location != "" && location != LocalCustomerPrefix+strings.TrimPrefix(tc.location, "/") {
				t.Fatal("HEAD location lost")
			}
		})
	}
}
