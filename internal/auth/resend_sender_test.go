package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
)

func TestResendSenderWithoutSiteName(t *testing.T) {
	for _, tc := range []struct {
		name, from, brand, wantName string
		branded                     bool
	}{
		{name: "bare sender", from: "login@example.test"},
		{name: "bracketed sender", from: "<login@example.test>"},
		{name: "blank site name", from: "login@example.test", branded: true},
		{name: "whitespace site name", from: "login@example.test", brand: "  ", branded: true},
		{name: "configured sender name", from: "Configured Service <login@example.test>", wantName: "Configured Service"},
		{name: "blank site preserves configured name", from: "Configured Service <login@example.test>", branded: true, wantName: "Configured Service"},
		{name: "site overrides configured name", from: "Configured Service <login@example.test>", brand: "Portal", branded: true, wantName: "Portal"},
	} {
		for _, purpose := range []string{"verify", "reset"} {
			t.Run(tc.name+"/"+purpose, func(t *testing.T) {
				sender, err := NewResend("fixture-key", tc.from)
				if err != nil {
					t.Fatal(err)
				}
				if tc.branded {
					b := branding.Defaults()
					b.Name = tc.brand
					sender.brand = &b
				}
				calls := 0
				sender.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					var payload struct {
						From string `json:"from"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					address, err := mail.ParseAddress(payload.From)
					if err != nil || address.Address != "login@example.test" || address.Name != tc.wantName {
						t.Fatalf("unexpected sender %q (%v)", payload.From, err)
					}
					// Resend rejects angle brackets without a display name, even though net/mail accepts them.
					if tc.wantName == "" && payload.From != "login@example.test" {
						t.Errorf("invalid bare Resend sender: %q", payload.From)
						return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
				})
				if err := sender.Send(context.Background(), "user@example.test", purpose, "https://example.test/#email_"+purpose+"=fixture", "fixture"); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("unexpected requests: %d", calls)
				}
			})
		}
	}
}

func TestResendNormalizesUnnamedSenderForAllMail(t *testing.T) {
	sender, err := NewResend("fixture-key", "<login@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	sender.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var payload struct {
			From string `json:"from"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.From != "login@example.test" {
			t.Fatalf("invalid bare sender %q", payload.From)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
	})
	ctx := context.Background()
	if err := sender.SendNotification(ctx, "user@example.test", "Update", "Details", "notice"); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendCatalogReport(ctx, "user@example.test", "Report", "Details", "<p>Details</p>", "catalog"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("requests=%d", calls)
	}
}
