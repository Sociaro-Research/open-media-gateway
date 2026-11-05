package auth

import "testing"

func TestGoogleAuthoritativeEmail(t *testing.T) {
	for _, tc := range []struct {
		name, email, hostedDomain string
		verified, want            bool
	}{
		{"Gmail", "person@gmail.com", "", true, true},
		{"normalized Gmail", " Person@GMAIL.COM ", "", true, true},
		{"Workspace", "person@company.example", "company.example", true, true},
		{"Workspace alias domain", "person@alias.example", "company.example", true, true},
		{"third-party address", "person@company.example", "", true, false},
		{"unverified Gmail", "person@gmail.com", "", false, false},
		{"unverified Workspace", "person@company.example", "company.example", false, false},
		{"Gmail lookalike", "person@gmail.com.example", "", true, false},
		{"empty hosted domain", "person@company.example", " ", true, false},
		{"missing address", "", "company.example", true, false},
		{"missing local part", "@gmail.com", "", true, false},
		{"missing domain", "person@", "company.example", true, false},
		{"multiple separators", "person@other@gmail.com", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := GoogleIdentity{Email: tc.email, EmailVerified: tc.verified, HostedDomain: tc.hostedDomain}
			if got := g.AuthoritativeEmail(); got != tc.want {
				t.Fatalf("authoritative email = %v, want %v", got, tc.want)
			}
		})
	}
}
