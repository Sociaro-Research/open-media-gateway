package config

import (
	"strings"
	"testing"
)

func TestEmailAuthenticationConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "SESSION_SIGNING_SECRET", "RESEND_API_KEY", "EMAIL_FROM", "AUTH_PUBLIC_URL"} {
		t.Setenv(name, "")
	}
	if _, err := Load(); err != nil {
		t.Fatal("legacy API-key mode", err)
	}
	t.Setenv("SESSION_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("AUTH_PUBLIC_URL", "https://example.test")
	if _, err := Load(); err != nil {
		t.Fatal("email-only configuration without delivery", err)
	}
	t.Setenv("RESEND_API_KEY", "fixture-key")
	if _, err := Load(); err == nil {
		t.Fatal("partial email sender accepted")
	}
	t.Setenv("EMAIL_FROM", "Sociaro <login@example.test>")
	if _, err := Load(); err != nil {
		t.Fatal("complete email-only configuration", err)
	}
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "google-fixture")
	if _, err := Load(); err != nil {
		t.Fatal("Google bootstrap validation must be deferred until persisted settings are read", err)
	}
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "fixture-secret")
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "https://example.test/auth/google/callback")
	t.Setenv("AUTH_PUBLIC_URL", "")
	if _, err := Load(); err != nil {
		t.Fatal("combined Google and email configuration", err)
	}
}
