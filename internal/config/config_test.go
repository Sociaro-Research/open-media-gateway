package config

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMaxPollErrorsConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "RESEND_API_KEY", "EMAIL_FROM", "SESSION_SIGNING_SECRET"} {
		t.Setenv(name, "")
	}
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 5}, {"1", 1}, {"5", 5}, {"2147483647", 2147483647},
	} {
		t.Run("valid_"+tc.value, func(t *testing.T) {
			t.Setenv("MAX_POLL_ERRORS", tc.value)
			c, err := Load()
			if err != nil || c.MaxPollErrors != tc.want {
				t.Fatalf("MAX_POLL_ERRORS=%q: got %d, %v; want %d", tc.value, c.MaxPollErrors, err, tc.want)
			}
		})
	}
	for _, value := range []string{"0", "-1", "invalid", "2147483648", "2147483649", "9223372036854775807", "9223372036854775808"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Setenv("MAX_POLL_ERRORS", value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MAX_POLL_ERRORS") {
				t.Fatalf("accepted incompatible MAX_POLL_ERRORS=%s on %d-bit runtime: %v", value, strconv.IntSize, err)
			}
		})
	}
}

func TestWorkerConcurrencyConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "SESSION_SIGNING_SECRET", "RESEND_API_KEY", "EMAIL_FROM", "AUTH_PUBLIC_URL", "WORKER_CONCURRENCY", "WORKER_OPERATION_CONCURRENCY", "ARCHIVE_CONCURRENCY", "WORKER_SHUTDOWN_TIMEOUT"} {
		t.Setenv(name, "")
	}
	c, err := Load()
	if err != nil || c.OperationConcurrency != 64 || c.ArchiveConcurrency != 4 || c.WorkerShutdownTimeout != 25*time.Minute {
		t.Fatalf("default concurrency: operations=%d archive=%d error=%v", c.OperationConcurrency, c.ArchiveConcurrency, err)
	}
	for _, value := range []string{"0s", "-1s", "invalid"} {
		t.Setenv("WORKER_SHUTDOWN_TIMEOUT", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WORKER_SHUTDOWN_TIMEOUT") {
			t.Fatalf("expected shutdown timeout validation for %q, got %v", value, err)
		}
	}
	t.Setenv("WORKER_SHUTDOWN_TIMEOUT", "2m")
	for _, name := range []string{"WORKER_OPERATION_CONCURRENCY", "ARCHIVE_CONCURRENCY"} {
		for _, value := range []string{"0", "-1", "257", "invalid"} {
			t.Setenv(name, value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected validation for %s=%s, got %v", name, value, err)
			}
		}
		t.Setenv(name, "")
	}
	t.Setenv("WORKER_CONCURRENCY", "1")
	c, err = Load()
	if err != nil || c.OperationConcurrency != 64 {
		t.Fatalf("obsolete lifecycle setting changed operation limit: %d %v", c.OperationConcurrency, err)
	}
	t.Setenv("WORKER_OPERATION_CONCURRENCY", "1")
	c, err = Load()
	if err != nil || c.OperationConcurrency != 1 {
		t.Fatalf("minimum operation concurrency rejected: %d %v", c.OperationConcurrency, err)
	}
	t.Setenv("WORKER_OPERATION_CONCURRENCY", "256")
	t.Setenv("ARCHIVE_CONCURRENCY", "8")
	c, err = Load()
	if err != nil || c.OperationConcurrency != 256 || c.ArchiveConcurrency != 8 || c.WorkerShutdownTimeout != 2*time.Minute {
		t.Fatalf("configured concurrency: operations=%d archive=%d error=%v", c.OperationConcurrency, c.ArchiveConcurrency, err)
	}
}

func TestOutboundPrivateExceptionsRequireExactHosts(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "RESEND_API_KEY", "EMAIL_FROM"} {
		t.Setenv(name, "")
	}
	for _, value := range []string{"*", "*.internal", "http://host", "host:80", "[::1]", "fe80::1%eth0", "a b", "a\tb", "a..b", "-host"} {
		t.Setenv("OUTBOUND_ALLOW_PRIVATE_HOSTS", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OUTBOUND_ALLOW_PRIVATE_HOSTS") {
			t.Fatalf("invalid host %q accepted: %v", value, err)
		}
	}
	t.Setenv("OUTBOUND_ALLOW_PRIVATE_HOSTS", " S3.INTERNAL.,127.0.0.1,fd00::1 ")
	c, err := Load()
	if err != nil || strings.Join(c.OutboundPrivateHosts, ",") != "s3.internal,127.0.0.1,fd00::1" {
		t.Fatalf("valid hosts rejected: %+v, %v", c.OutboundPrivateHosts, err)
	}
}

func TestMetricsListenerConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "RESEND_API_KEY", "EMAIL_FROM", "METRICS_ADDR"} {
		t.Setenv(name, "")
	}
	c, err := Load()
	if err != nil || c.MetricsAddr != "" {
		t.Fatalf("native metrics should be disabled by default: %+v %v", c, err)
	}
	for _, address := range []string{"127.0.0.1:9091", ":9091", "[::1]:9091"} {
		t.Setenv("METRICS_ADDR", address)
		if c, err := Load(); err != nil || c.MetricsAddr != address {
			t.Fatalf("valid listener %q: %v", address, err)
		}
	}
	for _, address := range []string{"http://localhost:9091", "9091", ":0", ":65536", ":invalid"} {
		t.Setenv("METRICS_ADDR", address)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "METRICS_ADDR") {
			t.Fatalf("invalid listener %q accepted: %v", address, err)
		}
	}
}

func TestLegacyAPIOriginConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "synthetic")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "RESEND_API_KEY", "EMAIL_FROM", "SESSION_SIGNING_SECRET", "UI_PUBLIC_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("AUTH_PUBLIC_URL", "https://console-staging.fixture.test")
	t.Setenv("LEGACY_API_ORIGIN", "")
	cfg, err := Load()
	if err != nil || cfg.LegacyAPIOrigin != "" {
		t.Fatal("legacy host enabled by default", err)
	}
	t.Setenv("LEGACY_API_ORIGIN", "https://MEDIA-GATEWAY-DEV.fixture.test:443/")
	cfg, err = Load()
	if err != nil || cfg.LegacyAPIOrigin != "https://media-gateway-dev.fixture.test" || cfg.AuthPublicURL != "https://console-staging.fixture.test" || cfg.UIPublicURL != cfg.AuthPublicURL {
		t.Fatal("canonical settings changed", err)
	}
	for _, raw := range []string{"http://localhost", "https://old.example.test:8443", "https://old.example.test/path", "https://a.test,https://b.test"} {
		t.Setenv("LEGACY_API_ORIGIN", raw)
		if _, err = Load(); err == nil || !strings.Contains(err.Error(), "LEGACY_API_ORIGIN") {
			t.Fatal("invalid legacy configuration accepted")
		}
	}
	t.Setenv("AUTH_PUBLIC_URL", "")
	t.Setenv("LEGACY_API_ORIGIN", "https://old.example.test")
	if _, err = Load(); err == nil {
		t.Fatal("legacy host enabled without canonical origin")
	}
}

func TestPublicOriginsAreCheckedBeforeStartup(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CLIENT_API_KEY", "WEBHOOK_SIGNING_SECRET", "MEDIA_URL_SIGNING_SECRET"} {
		t.Setenv(name, "fixture")
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_REDIRECT_URL", "RESEND_API_KEY", "EMAIL_FROM", "SESSION_SIGNING_SECRET", "LEGACY_API_ORIGIN"} {
		t.Setenv(name, "")
	}
	names := []string{"AUTH_PUBLIC_URL", "UI_PUBLIC_URL", "DOCS_PUBLIC_URL"}
	for _, bad := range []string{"http://192.168.1.10:8088", "gateway.example.test", "https://gateway.example.test/console"} {
		for _, name := range names {
			t.Setenv("AUTH_PUBLIC_URL", "https://gateway.example.test")
			t.Setenv("UI_PUBLIC_URL", "")
			t.Setenv("DOCS_PUBLIC_URL", "")
			t.Setenv(name, bad)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%q: want an error naming %s, got %v", name, bad, name, err)
			}
		}
	}
	// An origin taken from the Google redirect URL is checked too.
	t.Setenv("AUTH_PUBLIC_URL", "")
	t.Setenv("DOCS_PUBLIC_URL", "")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "fixture")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "fixture")
	t.Setenv("SESSION_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "http://192.168.1.10:8088/auth/google/callback")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTH_PUBLIC_URL") {
		t.Fatalf("origin from GOOGLE_OAUTH_REDIRECT_URL not checked: %v", err)
	}
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "SESSION_SIGNING_SECRET", "GOOGLE_OAUTH_REDIRECT_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("AUTH_PUBLIC_URL", "http://localhost:8088")
	t.Setenv("UI_PUBLIC_URL", "")
	t.Setenv("DOCS_PUBLIC_URL", "https://docs.example.test/")
	c, err := Load()
	if err != nil || c.UIPublicURL != "http://localhost:8088" {
		t.Fatalf("valid public origins rejected: %q %v", c.UIPublicURL, err)
	}
}
