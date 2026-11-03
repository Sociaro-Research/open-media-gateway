package companyinterface

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

type protocolTransport func(*http.Request) (*http.Response, error)

func (f protocolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func protocolResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestCompanyHTTPClientFixedDestinationAndRedirect(t *testing.T) {
	for _, origin := range []string{"http://central.example", "https://central.example/path", "https://user:secret@central.example", "https://central.example/"} {
		if _, e := NewClient(origin); e == nil {
			t.Fatal("accepted noncanonical destination", origin)
		}
	}
	var contacted atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	client, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Capabilities(context.Background()); !errors.Is(e, ErrUnavailable) || contacted.Load() != 0 {
		t.Fatal("private destination reached", e, contacted.Load())
	}
	client, e = NewClient("https://central.example")
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	client.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		v := protocolResponse(302, `{}`)
		v.Header.Set("Location", "https://other.example/stolen")
		return v, nil
	})
	if _, e = client.Capabilities(context.Background()); !errors.Is(e, ErrUnavailable) || calls != 1 {
		t.Fatal("redirect followed", e, calls)
	}
}
func TestCompanyHTTPClientBoundedStrictResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"oversized", strings.Repeat("x", MaxProtocolBody+1), 200, ErrUnavailable},
		{"unknown_field", `{"purpose":"company-interface","version":1,"capabilities":[],"credential":"secret"}`, 200, ErrUnavailable},
		{"trailing_json", `{} {}`, 200, ErrUnavailable},
		{"unknown_error", `{"error":{"code":"secret-upstream-value"}}`, 500, ErrUnavailable},
		{"old", `not found`, 404, ErrProtocol},
		{"limited", `{"error":{"code":"company_interface_rate_limit"}}`, 429, ErrRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClient("https://central.example")
			c.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) { return protocolResponse(tc.status, tc.body), nil })
			if _, e := c.Capabilities(context.Background()); !errors.Is(e, tc.want) {
				t.Fatal(e)
			}
		})
	}
}
func TestCompanyHTTPClientSubstitutedRedemption(t *testing.T) {
	proof, _ := NewSecret()
	local, _ := NewSecret()
	request := Redemption{AttemptID: uuid.NewString(), InstallationProof: proof, LocalSession: local, LocalOrigin: "https://local.example", ConnectionRevision: 9}
	good := Tokens{AttemptID: request.AttemptID, CentralOrigin: "https://central.example", LocalOrigin: request.LocalOrigin, InstallationHash: InstallationDigest(proof), LocalSessionHash: Digest(local), ConnectionRevision: 9}
	for _, field := range []string{"attempt", "central", "local", "installation", "browser", "revision"} {
		t.Run(field, func(t *testing.T) {
			v := good
			switch field {
			case "attempt":
				v.AttemptID = uuid.NewString()
			case "central":
				v.CentralOrigin = "https://other.example"
			case "local":
				v.LocalOrigin = "https://other.example"
			case "installation":
				v.InstallationHash = Digest("other")
			case "browser":
				v.LocalSessionHash = Digest("other")
			case "revision":
				v.ConnectionRevision++
			}
			c, _ := NewClient(good.CentralOrigin)
			calls := 0
			c.http.Transport = protocolTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.Path != ProtocolBase+"/redeem" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("unexpected request authority")
				}
				raw, _ := json.Marshal(v)
				return protocolResponse(200, string(raw)), nil
			})
			if _, e := c.Redeem(context.Background(), request); !errors.Is(e, ErrConflict) || calls != 1 {
				t.Fatal("substitution or automatic mutation retry", e, calls)
			}
		})
	}
}
