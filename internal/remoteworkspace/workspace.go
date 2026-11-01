// Package remoteworkspace connects one local owner to one remote company.
// Remote jobs, money and files remain authoritative; this is not a provider adapter.
package remoteworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("remote workspace is not connected")
var ErrContextRequired = errors.New("a reviewed remote workspace context is required")
var ErrConflict = errors.New("connection changed; review its current state")
var ErrCredential = errors.New("use a generation-only key bound to the selected remote company")
var ErrUnavailable = errors.New("remote workspace could not be verified; check server, key and membership")
var ErrRoute = errors.New("this remote operation is not supported by the pilot")

const Prefix = "/v1/system/remote-workspace/proxy"
const MaxJSON = 4 << 20

type Record struct {
	Provisioning     string     `json:"provisioning,omitempty"`
	Context          string     `json:"context,omitempty"`
	OperationScope   string     `json:"operation_scope,omitempty"`
	Owner            string     `json:"-"`
	Version          int64      `json:"version"`
	Active           bool       `json:"connected"`
	Origin           string     `json:"origin"`
	OrganizationID   string     `json:"organization_id"`
	OrganizationName string     `json:"organization_name"`
	UserID           string     `json:"-"`
	KeyID            string     `json:"-"`
	TokenHash        []byte     `json:"-"`
	Ciphertext       []byte     `json:"-"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
}

// SnapshotID is an optimistic dispatch precondition, not an authorization token.
// The credential remains server-side. Scope stays stable across same-account key
// renewal; snapshot changes on every saved connection revision.
func (r Record) OperationScopeID() string {
	if !r.Active {
		return ""
	}
	sum := sha256.Sum256([]byte(r.Owner + "\x00" + r.Origin + "\x00" + r.OrganizationID + "\x00" + r.UserID))
	return hex.EncodeToString(sum[:])
}
func (r Record) SnapshotID() string {
	if !r.Active {
		return ""
	}
	raw, _ := json.Marshal([]any{r.OperationScopeID(), r.Version, r.KeyID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (r Record) MarshalJSON() ([]byte, error) {
	type view Record
	value := view(r)
	value.Context = r.SnapshotID()
	value.OperationScope = r.OperationScopeID()
	return json.Marshal(value)
}

type Repository interface {
	RemoteWorkspace(context.Context, string) (Record, error)
	SaveRemoteWorkspace(context.Context, Record, int64) (Record, error)
	DisconnectRemoteWorkspace(context.Context, string, int64) (Record, error)
}
type Manager struct {
	Repo     Repository
	Cipher   *providerconfig.Cipher
	client   *http.Client
	download *http.Client
}

func New(repo Repository, cipher *providerconfig.Cipher, client *http.Client) *Manager {
	copied := *client
	// Never send a customer credential to a redirect, storage host or environment proxy.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Manager{Repo: repo, Cipher: cipher, client: &copied, download: client}
}
func Origin(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("use the HTTPS server origin without a path, query or credentials")
	}
	// Equivalent URL spellings must retain the same durable installation proof.
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := u.Port()
	if port != "" && port != "443" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
}

type KeyInfo struct {
	Key struct {
		ID             string   `json:"id"`
		UserID         string   `json:"user_id"`
		OrganizationID string   `json:"organization_id"`
		ResourceMode   string   `json:"resource_mode"`
		Scopes         []string `json:"scopes"`
	} `json:"key"`
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"organization"`
}

func (m *Manager) Connect(ctx context.Context, owner, origin, token, company string, version int64) (Record, error) {
	return m.connect(ctx, owner, origin, token, company, version, "legacy")
}
func (m *Manager) connect(ctx context.Context, owner, origin, token, company string, version int64, provisioning string) (Record, error) {
	origin, err := Origin(origin)
	if err != nil {
		return Record{}, err
	}
	if _, err = uuid.Parse(company); err != nil {
		return Record{}, ErrCredential
	}
	if len(token) < 16 || len(token) > 512 || strings.ContainsAny(token, "\r\n\t ") {
		return Record{}, ErrCredential
	}
	res, err := m.request(ctx, origin, token, http.MethodGet, "/v1/key", nil, "")
	if err != nil {
		return Record{}, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Record{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxJSON+1))
	if err != nil || len(raw) > MaxJSON {
		return Record{}, ErrUnavailable
	}
	var info KeyInfo
	if json.Unmarshal(raw, &info) != nil || info.Key.OrganizationID != company || info.Organization.ID != company || info.Key.ResourceMode != "shared" || len(info.Key.Scopes) != 1 || info.Key.Scopes[0] != "generation" {
		return Record{}, ErrCredential
	}
	if _, err = uuid.Parse(info.Key.ID); err != nil {
		return Record{}, ErrCredential
	}
	if _, err = uuid.Parse(info.Key.UserID); err != nil {
		return Record{}, ErrCredential
	}
	encrypted, err := m.Cipher.Encrypt([]byte(token))
	if err != nil {
		return Record{}, err
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	return m.Repo.SaveRemoteWorkspace(ctx, Record{Provisioning: provisioning, Owner: owner, Active: true, Origin: origin, OrganizationID: company, OrganizationName: info.Organization.Name, UserID: info.Key.UserID, KeyID: info.Key.ID, TokenHash: hash[:], Ciphertext: encrypted, VerifiedAt: &now}, version)
}
func (m *Manager) request(ctx context.Context, origin, token, method, path string, body io.Reader, idempotency string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Open-Media-Gateway remote-workspace pilot")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	return m.client.Do(req)
}

// Allowed is intentionally a media/customer-read API, never arbitrary management.
func Allowed(method, path string) bool {
	if strings.ContainsAny(path, "%\\?#") || strings.Contains(path, "..") || strings.Contains(path, "//") {
		return false
	}
	read := method == http.MethodGet || method == http.MethodHead
	if path == "/v1/key" || path == "/v1/models" || path == "/v1/generations" {
		return read || (path == "/v1/generations" && method == http.MethodPost)
	}
	if path == "/v1/generations/estimate" {
		return method == http.MethodPost
	}
	parts := strings.Split(strings.TrimPrefix(path, "/v1/generations/"), "/")
	if !strings.HasPrefix(path, "/v1/generations/") || len(parts) > 2 {
		return false
	}
	if _, err := uuid.Parse(parts[0]); err != nil {
		return false
	}
	if len(parts) == 1 {
		return read
	}
	switch parts[1] {
	case "content":
		return read
	case "last-frame":
		return read
	}
	return false
}
func (m *Manager) Proxy(ctx context.Context, owner, method, path, query string, body []byte, idempotency, expectedContext string) (*http.Response, Record, string, error) {
	if !Allowed(method, path) {
		return nil, Record{}, "", ErrRoute
	}
	if len(body) > MaxJSON {
		return nil, Record{}, "", ErrRoute
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return nil, Record{}, "", ErrRoute
	}
	for k, v := range values {
		if (path != "/v1/generations" || (k != "limit" && k != "offset")) || len(v) != 1 {
			return nil, Record{}, "", ErrRoute
		}
	}
	r, err := m.Repo.RemoteWorkspace(ctx, owner)
	if err != nil {
		return nil, r, "", err
	}
	if !r.Active {
		return nil, r, "", ErrNotFound
	}
	if expectedContext == "" {
		return nil, r, "", ErrContextRequired
	}
	if expectedContext != r.SnapshotID() {
		return nil, r, "", ErrConflict
	}
	raw, err := m.Cipher.Decrypt(r.Ciphertext)
	if err != nil {
		return nil, r, "", err
	}
	token := string(raw)
	if method == http.MethodPost && path == "/v1/generations" {
		if idempotency == "" || len(idempotency) > 200 {
			return nil, r, "", errors.New("Idempotency-Key is required for remote generation")
		}
		hash := sha256.Sum256([]byte(owner + "\x00" + r.Origin + "\x00" + r.OrganizationID + "\x00" + idempotency))
		idempotency = "workspace-" + hex.EncodeToString(hash[:])
	}
	if query != "" {
		path += "?" + values.Encode()
	}
	res, err := m.request(ctx, r.Origin, token, method, path, strings.NewReader(string(body)), idempotency)
	if err != nil {
		return nil, r, "", ErrUnavailable
	}
	return res, r, token, nil
}

// Rewrite only recognized local API links. Remote origins and file URLs must not
// become an arbitrary credential-bearing proxy or active browser content.
func Rewrite(value any, origin, token, expectedContext string) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			Rewrite(item, origin, token, expectedContext)
		}
	case map[string]any:
		for key, item := range v {
			if text, ok := item.(string); ok {
				text = strings.ReplaceAll(text, token, "[redacted]")
				switch key {
				case "status_url", "content_url", "result_url", "last_frame_url", "preview_url", "download_url":
					u, e := url.Parse(text)
					if e != nil {
						v[key] = ""
						continue
					}
					if u.IsAbs() {
						if u.Scheme+"://"+u.Host != origin {
							v[key] = ""
							continue
						}
					}
					if Allowed(http.MethodGet, u.Path) {
						text = Prefix + u.Path + "?workspace_context=" + url.QueryEscape(expectedContext)
					} else {
						text = ""
					}
				}
				v[key] = text
			} else {
				Rewrite(item, origin, token, expectedContext)
			}
		}
	}
}

// Download follows only the supplied client's checked public-network policy and
// sends no customer token, cookies or arbitrary caller headers to object storage.
func (m *Manager) Download(ctx context.Context, raw, method string) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrRoute
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, ErrRoute
	}
	return m.download.Do(req)
}
