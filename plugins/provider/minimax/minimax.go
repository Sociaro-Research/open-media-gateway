package minimax

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const maxResponseBytes = 4 << 20
const maxRequestBytes = 64_000_000

var numericID = regexp.MustCompile(`^[0-9]{1,40}$`)

type Adapter struct {
	fileOperationPrice *money.USD
	key, base          string
	client             *http.Client
	checkMu            sync.Mutex
	check              providerkit.CredentialCheck
}

func New(key, base string, client *http.Client) (*Adapter, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("MiniMax API key is required and must be a single line")
	}
	if base == "" {
		base = DefaultBaseURL
	}
	u, e := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if e != nil || u.Scheme != "https" || u.Host != "api.minimax.io" || u.Path != "" || u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("MiniMax endpoint must be https://api.minimax.io; regional products require separately reviewed contracts")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{key: key, base: u.String(), client: &copied}, nil
}
func (*Adapter) Name() string { return "minimax" }

type APIError struct{ Status, Code int }

func (e *APIError) Error() string {
	label := "provider rejected request"
	switch e.Code {
	case 1000, 1013:
		label = "provider service error"
	case 1001:
		label = "provider timeout"
	case 1002, 1039:
		label = "rate limit exceeded"
	case 1004, 2049:
		label = "authentication failed"
	case 1008:
		label = "insufficient balance"
	case 1026, 1027:
		label = "content safety rejection"
	case 2013:
		label = "invalid provider parameters"
	}
	if e.Code == 0 {
		switch e.Status {
		case 401, 403:
			label = "authentication or access denied"
		case 402:
			label = "insufficient balance"
		case 429:
			label = "rate limit exceeded"
		case 404:
			label = "resource not found or expired"
		}
	}
	return fmt.Sprintf("MiniMax: %s (HTTP %d, code %d)", label, e.Status, e.Code)
}
func (a *Adapter) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if e != nil {
		return nil, errors.New("MiniMax request could not be created")
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("User-Agent", "OpenMediaGateway-MiniMax/1.0")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, e := httpclient.Do(a.client, req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("MiniMax transport failed; submission outcome may be unknown")
	}
	defer resp.Body.Close()
	responseLimit := maxResponseBytes
	if path == "/v1/image_generation" {
		responseLimit = 256 << 20
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, int64(responseLimit)+1))
	if e != nil || len(data) > responseLimit {
		return nil, errors.New("MiniMax response is unreadable or exceeds the response limit")
	}
	var envelope struct {
		Base *struct {
			Code *int `json:"status_code"`
		} `json:"base_resp"`
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(data, &envelope)
	code := 0
	if envelope.Base != nil {
		if envelope.Base.Code == nil {
			return nil, errors.New("MiniMax response status code is missing")
		}
		code = *envelope.Base.Code
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 || envelope.Type == "error" || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return nil, &APIError{Status: resp.StatusCode, Code: code}
	}
	if strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/v1/models") && envelope.Base == nil {
		return nil, errors.New("MiniMax response status envelope is missing")
	}
	if !json.Valid(data) {
		return nil, errors.New("MiniMax returned malformed JSON")
	}
	return data, nil
}
func (a *Adapter) json(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil || len(b) > maxRequestBytes {
			return errors.New("MiniMax request exceeds 64 MB or cannot be encoded")
		}
		body = bytes.NewReader(b)
	}
	b, e := a.do(ctx, method, path, body, "application/json")
	if e != nil {
		return e
	}
	if out != nil && json.Unmarshal(b, out) != nil {
		return errors.New("MiniMax returned an invalid response schema")
	}
	return nil
}

// Handles authenticate the provider account and Gateway data owner, including
// connections sharing one upstream account. They survive worker restarts without
// accepting caller-supplied raw provider task or file IDs.
type handle struct {
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Model     string `json:"model,omitempty"`
	Kind      string `json:"kind"`
	Purpose   string `json:"purpose,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
	InputHash string `json:"input_hash,omitempty"`
}

func (a *Adapter) seal(ctx context.Context, h handle) string {
	connection := a.requestConnectionScope(ctx)
	if connection == "" {
		return ""
	}
	h.Owner = identity.OwnerID(ctx)
	b, _ := json.Marshal(h)
	m := hmac.New(sha256.New, []byte(a.key+"\x00"+a.base+"\x00"+connection))
	m.Write(b)
	return "minimax:" + base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (a *Adapter) open(ctx context.Context, s string) (handle, error) {
	connection := a.requestConnectionScope(ctx)
	invalid := errors.New("invalid MiniMax resource handle or resource owner")
	if connection == "" || len(s) > 2048 || !strings.HasPrefix(s, "minimax:") {
		return handle{}, invalid
	}
	parts := strings.Split(strings.TrimPrefix(s, "minimax:"), ".")
	if len(parts) != 2 {
		return handle{}, invalid
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return handle{}, invalid
	}
	mac, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return handle{}, invalid
	}
	m := hmac.New(sha256.New, []byte(a.key+"\x00"+a.base+"\x00"+connection))
	m.Write(b)
	var h handle
	if !hmac.Equal(mac, m.Sum(nil)) || json.Unmarshal(b, &h) != nil || !numericID.MatchString(h.ID) || h.Owner != identity.OwnerID(ctx) || h.ExpiresAt > 0 && time.Now().Unix() >= h.ExpiresAt {
		return handle{}, invalid
	}
	return h, nil
}
func (a *Adapter) CheckCredentials(ctx context.Context) error {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if time.Since(a.check.CheckedAt) < 5*time.Minute && a.check.Err == nil {
		return nil
	}
	started := time.Now()
	var out struct {
		Object string `json:"object"`
		Data   *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	e := a.json(providerkit.WithCredentialCheck(ctx), "GET", "/v1/models", nil, &out)
	if e == nil && (out.Object != "list" || out.Data == nil) {
		e = errors.New("MiniMax model-list schema is malformed; authentication is unverified")
	}
	a.check = providerkit.CredentialCheck{CheckedAt: time.Now().UTC(), LatencyMS: time.Since(started).Milliseconds(), Err: e, Warning: "Authentication only: image/video entitlement, whitelist access, balance and generation remain unverified."}
	return e
}
func (a *Adapter) CredentialStatus() providerkit.CredentialCheck {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	if time.Since(a.check.CheckedAt) >= 5*time.Minute {
		return providerkit.CredentialCheck{}
	}
	return a.check
}
func publicURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

// The shared host supplies concrete ID/owner/revision in context after resolving
// authorized credentials. Missing scope is never replaced by caller input.
func (a *Adapter) requestConnectionScope(ctx context.Context) string {
	if scope, ok := core.ProviderConnectionScopeFromContext(ctx); ok {
		if scope.Revision == "" {
			return ""
		}
		b, _ := json.Marshal(scope)
		sum := sha256.Sum256(b)
		return base64.RawURLEncoding.EncodeToString(sum[:])
	}
	return ""
}
