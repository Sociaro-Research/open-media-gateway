package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/google/uuid"
)

type ProviderType struct {
	IconURL        string              `json:"icon_url,omitempty"`
	IconMonochrome bool                `json:"icon_monochrome,omitempty"`
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Fields         []providerkit.Field `json:"fields"`
	Profiles       []core.MediaModel   `json:"profiles"`
}
type CreateConnection struct {
	APIType string `json:"api_type"`
	Name    string `json:"name"`
}

func (m *Manager) Types() []ProviderType {
	result := []ProviderType{}
	for _, d := range m.definitions() {
		if d.Free {
			continue
		}
		t := ProviderType{ID: d.ID, Name: d.Name, Fields: d.Fields}
		if d.Profiles != nil {
			t.Profiles = d.Profiles()
		}
		result = append(result, t)
	}
	return result
}
func (m *Manager) Create(ctx context.Context, input CreateConnection) (AdminProvider, error) {
	d, ok := m.findSpec(input.APIType)
	if !ok || d.Free || d.ID != input.APIType {
		return AdminProvider{}, ErrUnsupportedProvider
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 {
		return AdminProvider{}, errors.New("name must contain 1–120 characters")
	}
	id := d.ID + "--" + uuid.NewString()
	c := d.Defaults(Credentials{Connection: &providerkit.Connection{ManualCatalog: true}})
	ciphertext, err := m.encryptCredentials(c)
	if err != nil {
		return AdminProvider{}, err
	}
	if err = m.repo.Upsert(ctx, providerconfig.Record{ProviderID: id, Name: name, CredentialsCiphertext: ciphertext, CredentialSource: "console"}); err != nil {
		return AdminProvider{}, err
	}
	return m.describe(ctx, id, false)
}
func (m *Manager) Providers(ctx context.Context) (map[string]core.MediaProvider, error) {
	result := m.ManagedProviders()
	records, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.Archived {
			delete(result, record.ProviderID)
			continue
		}
		if _, ok := m.findSpec(record.ProviderID); !ok {
			continue
		}
		result[record.ProviderID] = &managedProvider{id: record.ProviderID, name: record.Name, manager: m}
	}
	return result, nil
}
func (m *Manager) Provider(ctx context.Context, id string) (core.MediaProvider, error) {
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		if errors.Is(err, providerconfig.ErrNotFound) || errors.Is(err, ErrUnsupportedProvider) {
			return nil, errors.Join(core.ErrProviderUnavailable, err)
		}
		return nil, err
	}
	return &managedProvider{id: record.ProviderID, name: record.Name, manager: m}, nil
}
func canonicalConnectionID(id string, d providerkit.Definition) string {
	if strings.Contains(id, "--") {
		return id
	}
	return d.ID
}
func connectionIdentity(id string, d providerkit.Definition, c Credentials) string {
	if c.Connection == nil && !strings.Contains(id, "--") {
		return d.Identity(c)
	}
	// Include routing, headers and manual model contracts, but not billing secrets.
	c.BillingAccessKeyID = ""
	c.BillingAccessKeySecret = ""
	// Library management credentials do not identify the inference/billing account.
	if c.Extra != nil {
		copy := make(map[string]string, len(c.Extra))
		for k, v := range c.Extra {
			if !strings.HasPrefix(k, "asset_") {
				copy[k] = v
			}
		}
		c.Extra = copy
	}

	account := d.Identity(c)
	if c.Extra == nil {
		c.Extra = map[string]string{}
	} else {
		raw, _ := json.Marshal(c.Extra)
		var copy map[string]string
		_ = json.Unmarshal(raw, &copy)
		c.Extra = copy
	}
	c.Extra["connection_id"] = id
	return account + ":" + credentialFingerprint(c)
}
func (m *Manager) pin(ctx context.Context, id string, c Credentials) (string, error) {
	d, _ := m.findSpec(id)
	fingerprint := connectionIdentity(id, d, c)
	if revisions, ok := m.repo.(providerconfig.Revisions); ok {
		encrypted, err := m.encryptCredentials(c)
		if err != nil {
			return "", err
		}
		if err = revisions.SaveRevision(ctx, id, fingerprint, encrypted); err != nil {
			return "", err
		}
	}
	return fingerprint, nil
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func validOperation(s string) bool { return s == "" || s == "models" || s == "generate" || s == "poll" }
func validateConnection(d providerkit.Definition, c *providerkit.Connection, old *providerkit.Connection) error {
	if err := core.ValidateTimeoutSeconds(c.TimeoutSeconds); err != nil {
		return err
	}
	if len(c.Headers) > 64 || len(c.Models) > 250 {
		return errors.New("too many headers or manual models")
	}
	saved := map[string]string{}
	if old != nil {
		for _, h := range old.Headers {
			if h.Value != nil {
				saved[h.Operation+"/"+strings.ToLower(h.Name)] = *h.Value
			}
		}
	}
	seen := map[string]bool{}
	for i := range c.Headers {
		h := &c.Headers[i]
		h.Name = strings.TrimSpace(h.Name)
		key := h.Operation + "/" + strings.ToLower(h.Name)
		if !headerName.MatchString(h.Name) || !validOperation(h.Operation) || seen[key] {
			return errors.New("invalid or duplicate header name or operation")
		}
		seen[key] = true
		switch strings.ToLower(h.Name) {
		case "host", "content-length", "connection", "transfer-encoding", "content-type":
			return errors.New("transport and content-type headers are controlled by the API adapter")
		}
		if h.Value == nil {
			value, ok := saved[key]
			if !ok {
				return errors.New("new headers require a value")
			}
			h.Value = &value
		}
		if len(*h.Value) > 8192 || strings.ContainsAny(*h.Value, "\r\n\x00") {
			return errors.New("invalid header value")
		}
		h.Hint = ""
	}
	for operation, endpoint := range c.Endpoints {
		if operation == "" || !validOperation(operation) {
			return errors.New("endpoint operation must be models, generate or poll")
		}
		if endpoint == "" {
			delete(c.Endpoints, operation)
			continue
		}
		check := strings.ReplaceAll(endpoint, "{task_id}", "task")
		u, err := url.Parse(check)
		if err != nil || u.Fragment != "" || u.User != nil || strings.ContainsAny(endpoint, "\r\n") || (!(u.Scheme == "https" || u.Scheme == "http") && !(u.Scheme == "" && strings.HasPrefix(endpoint, "/") && !strings.HasPrefix(endpoint, "//"))) {
			return errors.New("endpoint must be an HTTP(S) URL or an absolute path")
		}
		if u.Scheme != "" && u.Host == "" {
			return errors.New("endpoint host is required")
		}
	}
	profiles := map[string]core.MediaModel{}
	if d.Profiles != nil {
		for _, model := range d.Profiles() {
			profiles[model.ID] = model
		}
	}
	seen = map[string]bool{}
	for i := range c.Models {
		model := &c.Models[i]
		id := model.Model.ID
		if id == "" || id != strings.TrimSpace(id) || len(id) > 200 || strings.ContainsAny(id, "\r\n\x00") || seen[id] {
			return errors.New("manual models require distinct upstream IDs of 1–200 characters")
		}
		seen[id] = true
		profile, ok := profiles[model.Profile]
		if !ok {
			return errors.New("choose a supported API request profile")
		}
		if !core.MediaGenerationModel(profile) || !core.MediaGenerationModel(model.Model) || model.Model.MediaType != profile.MediaType {
			return errors.New("model media type must match the selected request profile")
		}
		if model.Model.DisplayName == "" {
			model.Model.DisplayName = id
		}
		if model.Model.MinDuration < 0 || model.Model.MaxDuration < model.Model.MinDuration || model.Model.MinImageInputs < 0 || model.Model.MaxImageInputs < model.Model.MinImageInputs {
			return errors.New("model limits are inconsistent")
		}
		if model.PriceUSD != nil && (math.IsNaN(*model.PriceUSD) || math.IsInf(*model.PriceUSD, 0) || *model.PriceUSD < 0 || *model.PriceUSD > 1000000) {
			return errors.New("request price must be between 0 and 1000000 USD")
		}
		model.Model.Manual = true
		model.Model.Pricing = core.MediaModelPricing{}
		model.Model.Metadata = nil
		model.Model.PublicID = ""
		for _, parameter := range model.Model.Parameters {
			if err := validateBody(map[string]any{parameter.Key: nil}); err != nil {
				return err
			}
		}
		if err := validateBody(model.ExtraBody); err != nil {
			return err
		}
	}
	return validateBody(c.ExtraBody)
}
func validateBody(body map[string]any) error {
	for key := range body {
		switch key {
		case "model", "prompt", "input", "messages", "content", "images":
			return fmt.Errorf("extra body cannot replace %s", key)
		}
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 65536 {
		return errors.New("extra body must be a JSON object at most ≈65.54 KB (65536 bytes)")
	}
	return nil
}
func publicConnection(c *providerkit.Connection) *providerkit.Connection {
	if c == nil {
		return &providerkit.Connection{}
	}
	raw, _ := json.Marshal(c)
	var copy providerkit.Connection
	_ = json.Unmarshal(raw, &copy)
	for i := range copy.Headers {
		copy.Headers[i].Value = nil
		copy.Headers[i].Hint = "••••"
	}
	return &copy
}

// configurationClient applies overrides only to API requests. Redirects are never
// followed with credentials; result downloading uses the separate storage client.
func configurationClient(client *http.Client, c Credentials) *http.Client {
	copy := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = &connectionTransport{base: base, credentials: c}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("provider API redirects are not supported; configure the final endpoint")
	}
	return &copy
}
