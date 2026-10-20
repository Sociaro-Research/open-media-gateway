package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	OutboundPrivateHosts    []string
	Providers               map[string]providerkit.Settings
	Port                    string
	MetricsAddr             string
	DatabaseURL             string
	RedisURL                string
	ClientAPIKey            string
	MediaProvider           string
	DefaultMediaModel       string
	Migrate                 string
	PollInterval            time.Duration
	ProviderTaskTimeout     time.Duration
	MaxPollErrors           int
	ProviderHTTPTimeout     time.Duration
	DispatchInterval        time.Duration
	OperationConcurrency    int
	ArchiveConcurrency      int
	WorkerShutdownTimeout   time.Duration
	WebhookSigningSecret    string
	WebhookHTTPTimeout      time.Duration
	WebhookMaxAttempts      int
	WebhookRetryBase        time.Duration
	StorageDriver           string
	StorageLocalPath        string
	StorageMaxBytes         int64
	StorageDownloadTimeout  time.Duration
	MediaURLSigningSecret   string
	ProviderCredentialsKey  string
	MediaURLTTL             time.Duration
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	GoogleOAuthRedirectURL  string
	SessionSigningSecret    string
	SessionTTL              time.Duration
	DocsPublicURL           string
	AuthPublicURL           string
	UIPublicURL             string
	ResendAPIKey            string
	EmailFrom               string
}

func Load() (Config, error) {
	c := Config{
		Port:                    env("PORT", "8080"),
		MetricsAddr:             strings.TrimSpace(os.Getenv("METRICS_ADDR")),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		RedisURL:                os.Getenv("REDIS_URL"),
		ClientAPIKey:            os.Getenv("CLIENT_API_KEY"),
		MediaProvider:           strings.ToLower(env("MEDIA_PROVIDER", env("VIDEO_PROVIDER", registry.DefaultID()))),
		DefaultMediaModel:       env("DEFAULT_MEDIA_MODEL", os.Getenv("DEFAULT_VIDEO_MODEL")),
		Migrate:                 strings.ToLower(env("MIGRATE", "auto")),
		WebhookSigningSecret:    os.Getenv("WEBHOOK_SIGNING_SECRET"),
		StorageDriver:           strings.ToLower(env("STORAGE_DRIVER", "local")),
		StorageLocalPath:        env("STORAGE_LOCAL_PATH", "./data/media"),
		MediaURLSigningSecret:   os.Getenv("MEDIA_URL_SIGNING_SECRET"),
		GoogleOAuthClientID:     os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		GoogleOAuthClientSecret: os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
		GoogleOAuthRedirectURL:  os.Getenv("GOOGLE_OAUTH_REDIRECT_URL"),
		SessionSigningSecret:    os.Getenv("SESSION_SIGNING_SECRET"),
		DocsPublicURL:           os.Getenv("DOCS_PUBLIC_URL"),
		AuthPublicURL:           os.Getenv("AUTH_PUBLIC_URL"),
		UIPublicURL:             os.Getenv("UI_PUBLIC_URL"),
		ResendAPIKey:            os.Getenv("RESEND_API_KEY"),
		EmailFrom:               os.Getenv("EMAIL_FROM"),
	}
	c.ProviderCredentialsKey = env("PROVIDER_CREDENTIALS_ENCRYPTION_KEY", c.MediaURLSigningSecret)
	if c.MetricsAddr != "" {
		_, port, err := net.SplitHostPort(c.MetricsAddr)
		number, numberErr := strconv.Atoi(port)
		if err != nil || numberErr != nil || number < 1 || number > 65535 {
			return Config{}, errors.New("METRICS_ADDR must be a host:port listener address with port 1-65535")
		}
	}
	for _, host := range strings.Split(os.Getenv("OUTBOUND_ALLOW_PRIVATE_HOSTS"), ",") {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		valid := true
		if ip, err := netip.ParseAddr(host); err == nil {
			valid = ip.Zone() == ""
		} else {
			for _, label := range strings.Split(host, ".") {
				if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
					valid = false
				}
				for _, c := range label {
					if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
						valid = false
					}
				}
			}
			if len(host) > 253 {
				valid = false
			}
		}
		if !valid {
			return Config{}, errors.New("OUTBOUND_ALLOW_PRIVATE_HOSTS must contain exact hostnames or IP literals")
		}
		c.OutboundPrivateHosts = append(c.OutboundPrivateHosts, host)
	}
	var err error
	if c.OperationConcurrency, err = integer("WORKER_OPERATION_CONCURRENCY", 64); err != nil {
		return Config{}, err
	}
	if c.OperationConcurrency > 256 {
		return Config{}, fmt.Errorf("WORKER_OPERATION_CONCURRENCY must not exceed 256")
	}
	if c.ArchiveConcurrency, err = integer("ARCHIVE_CONCURRENCY", 4); err != nil {
		return Config{}, err
	}
	if c.ArchiveConcurrency > 256 {
		return Config{}, fmt.Errorf("ARCHIVE_CONCURRENCY must not exceed 256")
	}
	if c.PollInterval, err = duration("PROVIDER_POLL_INTERVAL", time.Second); err != nil {
		return Config{}, err
	}
	if c.ProviderTaskTimeout, err = duration("PROVIDER_TASK_TIMEOUT", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if c.ProviderHTTPTimeout, err = duration("PROVIDER_HTTP_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if c.WorkerShutdownTimeout, err = duration("WORKER_SHUTDOWN_TIMEOUT", 25*time.Minute); err != nil {
		return Config{}, err
	}
	if c.DispatchInterval, err = duration("DISPATCH_INTERVAL", time.Second); err != nil {
		return Config{}, err
	}
	if c.WebhookHTTPTimeout, err = duration("WEBHOOK_HTTP_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if c.WebhookRetryBase, err = duration("WEBHOOK_RETRY_BASE", time.Second); err != nil {
		return Config{}, err
	}
	if c.StorageDownloadTimeout, err = duration("STORAGE_DOWNLOAD_TIMEOUT", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if c.MediaURLTTL, err = duration("MEDIA_URL_TTL", time.Hour); err != nil {
		return Config{}, err
	}
	if c.SessionTTL, err = duration("SESSION_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if c.StorageMaxBytes, err = integer64("STORAGE_MAX_BYTES", 2<<30); err != nil {
		return Config{}, err
	}
	if c.MaxPollErrors, err = integer("MAX_POLL_ERRORS", 5); err != nil {
		return Config{}, err
	}
	// Consecutive polling failures are persisted in a PostgreSQL INTEGER.
	if c.MaxPollErrors > math.MaxInt32 {
		return Config{}, errors.New("MAX_POLL_ERRORS must be between 1 and 2147483647")
	}
	if c.WebhookMaxAttempts, err = integer("WEBHOOK_MAX_ATTEMPTS", 8); err != nil {
		return Config{}, err
	}
	if c.DatabaseURL == "" || c.RedisURL == "" || c.ClientAPIKey == "" || c.WebhookSigningSecret == "" || c.MediaURLSigningSecret == "" {
		return Config{}, errors.New("DATABASE_URL, REDIS_URL, CLIENT_API_KEY, WEBHOOK_SIGNING_SECRET and MEDIA_URL_SIGNING_SECRET are required")
	}
	googleValues := []string{c.GoogleOAuthClientID, c.GoogleOAuthClientSecret, c.GoogleOAuthRedirectURL}
	googleConfigured := 0
	for _, value := range googleValues {
		if strings.TrimSpace(value) != "" {
			googleConfigured++
		}
	}
	if googleConfigured > 0 && c.SessionSigningSecret == "" {
		return Config{}, errors.New("Google login bootstrap requires SESSION_SIGNING_SECRET")
	}
	if (c.ResendAPIKey == "") != (c.EmailFrom == "") {
		return Config{}, errors.New("RESEND_API_KEY and EMAIL_FROM must be configured together")
	}
	if c.ResendAPIKey != "" && c.SessionSigningSecret == "" {
		return Config{}, errors.New("email authentication requires SESSION_SIGNING_SECRET")
	}
	if c.AuthPublicURL == "" && c.GoogleOAuthRedirectURL != "" {
		u, err := url.Parse(c.GoogleOAuthRedirectURL)
		if err != nil || u.Host == "" {
			return Config{}, errors.New("invalid Google redirect URL")
		}
		c.AuthPublicURL = u.Scheme + "://" + u.Host
	}
	if c.UIPublicURL == "" {
		c.UIPublicURL = c.AuthPublicURL
	}
	if c.SessionSigningSecret != "" && c.AuthPublicURL == "" {
		return Config{}, errors.New("AUTH_PUBLIC_URL is required for browser authentication")
	}
	if c.SessionSigningSecret != "" && len(c.SessionSigningSecret) < 32 {
		return Config{}, errors.New("SESSION_SIGNING_SECRET must contain at least 32 characters")
	}
	if c.StorageDriver != "local" {
		return Config{}, fmt.Errorf("unsupported STORAGE_DRIVER %q", c.StorageDriver)
	}
	if c.Migrate != "auto" && c.Migrate != "off" {
		return Config{}, errors.New("MIGRATE must be auto or off")
	}
	var overrides map[string]providerkit.Settings
	if raw := os.Getenv("PROVIDER_SETTINGS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			return Config{}, errors.New("PROVIDER_SETTINGS must be a JSON object of provider settings")
		}
	}
	c.Providers = map[string]providerkit.Settings{}
	for _, d := range registry.All() {
		values := providerkit.Settings{}
		for k, v := range d.RuntimeDefaults {
			values[k] = v
		}
		for k, name := range d.RuntimeEnv {
			if v := os.Getenv(name); v != "" {
				values[k] = v
			}
		}
		values["model"] = env(d.ModelEnv, d.DefaultModel)
		for _, f := range d.Fields {
			values[f.Key] = env(f.Env, f.Default)
		}
		for k, v := range overrides[d.ID] {
			values[k] = v
		}
		if err := d.ValidateRuntime(values); err != nil {
			return Config{}, err
		}
		c.Providers[d.ID] = values
	}
	c.MediaProvider = registry.Canonical(c.MediaProvider)
	if _, ok := registry.Find(c.MediaProvider); !ok {
		return Config{}, fmt.Errorf("unsupported MEDIA_PROVIDER %q", c.MediaProvider)
	}
	if c.DefaultMediaModel == "" {
		c.DefaultMediaModel = c.Providers[c.MediaProvider]["model"]
	}

	return c, nil
}

func envValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func integer(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func integer64(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func (c Config) ProviderModels() map[string]string {
	result := map[string]string{}
	for _, d := range registry.All() {
		result[d.ID] = d.DefaultModel
		if v := c.Providers[d.ID]["model"]; v != "" {
			result[d.ID] = v
		}
	}
	return result
}
