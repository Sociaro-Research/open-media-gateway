package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	registry "github.com/Sociaro-Research/media-gateway-dev/plugins/provider"
)

var (
	ErrUnsupportedProvider  = errors.New("provider is not supported")
	ErrProviderDisabled     = errors.New("provider is disabled")
	ErrProviderUnconfigured = errors.New("provider credentials are not configured")
)

type Credentials = providerkit.Credentials
type ProviderUpdate = providerkit.ProviderUpdate
type AdminProvider struct {
	DefaultTimeoutSeconds int64                   `json:"default_timeout_seconds"`
	APIType               string                  `json:"api_type"`
	Version               int64                   `json:"version"`
	Archived              bool                    `json:"archived"`
	Connection            *providerkit.Connection `json:"connection"`
	Fields                []providerkit.Field     `json:"fields"`
	Values                map[string]string       `json:"values"`
	Hints                 map[string]string       `json:"hints"`
	IconURL               string                  `json:"icon_url,omitempty"`
	IconMonochrome        bool                    `json:"icon_monochrome,omitempty"`
	BillingHelp           string                  `json:"billing_help,omitempty"`

	SupportsBillingCredentials bool              `json:"supports_billing_credentials"`
	ID                         string            `json:"id"`
	Name                       string            `json:"name"`
	Description                string            `json:"description"`
	Enabled                    bool              `json:"enabled"`
	Configured                 bool              `json:"configured"`
	CredentialSource           string            `json:"credential_source"`
	CredentialHint             string            `json:"credential_hint,omitempty"`
	BaseURL                    string            `json:"base_url,omitempty"`
	MusicConfigured            bool              `json:"music_configured,omitempty"`
	MusicCredentialHint        string            `json:"music_credential_hint,omitempty"`
	MusicBaseURL               string            `json:"music_base_url,omitempty"`
	BillingConfigured          bool              `json:"billing_configured"`
	BillingAccessKeyHint       string            `json:"billing_access_key_hint,omitempty"`
	Region                     string            `json:"region,omitempty"`
	Status                     string            `json:"status"`
	StatusMessage              string            `json:"status_message"`
	LastCheckedAt              *time.Time        `json:"last_checked_at,omitempty"`
	CheckLatencyMS             int64             `json:"check_latency_ms,omitempty"`
	ModelCount                 int               `json:"model_count"`
	MediaCounts                map[string]int    `json:"media_counts"`
	Models                     []core.MediaModel `json:"models,omitempty"`
	UpdatedAt                  time.Time         `json:"updated_at"`
}

type cachedAdapter struct {
	fingerprint string
	adapter     core.MediaProvider
}

type ReferenceImageResolver interface {
	Resolve(context.Context, []string) ([]string, error)
}

type Manager struct {
	catalog         catalogsync.Repository
	registry        *registry.Registry
	systemSettings  system.SettingsReader
	referenceImages ReferenceImageResolver
	repo            providerconfig.Repository
	cipher          *providerconfig.Cipher
	cfg             config.Config
	client          *http.Client

	mu       sync.Mutex
	adapters map[string]cachedAdapter
}

func NewManager(repo providerconfig.Repository, cipher *providerconfig.Cipher, cfg config.Config, resolvers ...ReferenceImageResolver) *Manager {
	var resolver ReferenceImageResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &Manager{
		registry:        registry.Builtins(),
		referenceImages: resolver,
		repo:            repo, cipher: cipher, cfg: cfg,
		client:   httpclient.NewPublic(cfg.ProviderHTTPTimeout, cfg.OutboundPrivateHosts...),
		adapters: make(map[string]cachedAdapter),
	}
}

// Bootstrap creates every supported provider row. Environment credentials seed
// only an empty row, so subsequent console changes remain authoritative.
func (m *Manager) Bootstrap(ctx context.Context) error {
	for _, spec := range m.definitions() {
		if _, err := m.loadRecord(ctx, spec.ID); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) loadRecord(ctx context.Context, id string) (providerconfig.Record, error) {
	spec, ok := m.findSpec(id)
	if !ok {
		return providerconfig.Record{}, ErrUnsupportedProvider
	}
	id = canonicalConnectionID(id, spec)
	record, err := m.repo.Get(ctx, id)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, providerconfig.ErrNotFound) {
		return providerconfig.Record{}, err
	}
	if strings.Contains(id, "--") {
		return providerconfig.Record{}, providerconfig.ErrNotFound
	}
	record = providerconfig.Record{ProviderID: id, CredentialSource: "none", Enabled: m.isFree(id)}
	if identity.ProviderOwnerID(ctx) == identity.SystemOwnerID && !m.isFree(id) {
		credentials := m.environmentCredentials(id)
		if hasAnyCredentials(spec, credentials) {
			record.CredentialsCiphertext, err = m.encryptCredentials(credentials)
			if err != nil {
				return providerconfig.Record{}, err
			}
			record.CredentialSource = "environment"
			record.Enabled = true
		}
	}
	if err := m.repo.Upsert(ctx, record); err != nil {
		return providerconfig.Record{}, err
	}
	return m.repo.Get(ctx, id)
}

func (m *Manager) ManagedProviders() map[string]core.MediaProvider {
	result := make(map[string]core.MediaProvider, len(m.definitions()))
	for _, spec := range m.definitions() {
		result[spec.ID] = &managedProvider{id: spec.ID, manager: m}
	}
	return result
}

func (m *Manager) List(ctx context.Context) ([]AdminProvider, error) {
	available, err := m.Providers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(available))
	for id := range available {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]AdminProvider, 0, len(ids))
	for _, id := range ids {
		item, err := m.describe(ctx, id, true)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (m *Manager) Get(ctx context.Context, id string) (AdminProvider, error) {
	return m.describe(ctx, id, true)
}

func (m *Manager) Check(ctx context.Context, id string) (AdminProvider, error) {
	return m.describe(ctx, id, true)
}

func (m *Manager) Update(ctx context.Context, id string, update ProviderUpdate) (AdminProvider, error) {
	definition, ok := m.findSpec(id)
	if !ok {
		return AdminProvider{}, ErrUnsupportedProvider
	}
	id = canonicalConnectionID(id, definition)
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		return AdminProvider{}, err
	}
	if update.Version != nil && *update.Version != record.Version {
		return AdminProvider{}, errors.New("provider settings changed; reload before saving")
	}
	if update.Name != nil {
		name := strings.TrimSpace(*update.Name)
		if name == "" || len(name) > 120 {
			return AdminProvider{}, errors.New("name must contain 1–120 characters")
		}
		record.Name = name
	}
	if update.Archived != nil {
		record.Archived = *update.Archived
		if record.Archived {
			record.Enabled = false
		}
	}
	if update.Enabled != nil {
		record.Enabled = *update.Enabled && !record.Archived
	}
	if m.isFree(id) {
		record.CredentialSource = "none"
		record.CredentialsCiphertext = nil
		if err := m.repo.Upsert(ctx, record); err != nil {
			return AdminProvider{}, err
		}
		return m.describe(ctx, id, false)
	}

	credentials, err := m.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return AdminProvider{}, err
	}
	spec, _ := m.findSpec(id)
	if err := validateUpdate(spec, update); err != nil {
		return AdminProvider{}, err
	}

	if spec.Configured(credentials) {
		if _, err := m.pin(ctx, id, m.withDefaults(id, credentials)); err != nil {
			return AdminProvider{}, err
		}
	}
	previous := m.withDefaults(id, credentials)
	// Preserve the old snapshot when provider-specific fields are merged below.
	if credentials.Extra != nil {
		credentials.Extra = make(map[string]string, len(previous.Extra))
		for name, value := range previous.Extra {
			credentials.Extra[name] = value
		}
	}
	changed, _ := mergeCredentials(spec, &credentials, update)
	next := m.withDefaults(id, credentials)
	if update.Connection != nil {
		next.Connection = update.Connection
	}
	if err := validateDestinationSecrets(spec, previous, next, update); err != nil {
		return AdminProvider{}, err
	}
	if update.Connection != nil {
		if err := validateConnection(spec, update.Connection, credentials.Connection); err != nil {
			return AdminProvider{}, err
		}
		credentials.Connection = update.Connection
		changed = true
	}
	credentials = m.withDefaults(id, credentials)
	configured := spec.Configured(credentials)
	_, billingPartial := spec.GroupState(credentials, "billing")
	if billingPartial {
		return AdminProvider{}, errors.New("billing access requires both AccessKey ID and AccessKey Secret")
	}
	if !configured {
		// Clearing a required credential disconnects the provider instead of
		// rejecting the edit and leaving the previous account connected.
		for _, field := range spec.Fields {
			if value, supplied := update.Fields()[field.Key]; field.Required && supplied && strings.TrimSpace(value) == "" {
				record.Enabled = false
			}
		}
		if record.Enabled {
			return AdminProvider{}, ErrProviderUnconfigured
		}
	}
	// Saving configuration is independent from discovery or upstream availability.
	if configured {
		if adapter, err := m.buildAdapter(id, credentials); err != nil {
			return AdminProvider{}, err
		} else {
			m.cacheAdapter(ctx, id, credentials, adapter)
		}
	}
	if !hasAnyCredentials(spec, credentials) {
		record.CredentialSource = "none"
		record.CredentialsCiphertext = nil
	} else if changed {
		record.CredentialSource = "console"
		record.CredentialsCiphertext, err = m.encryptCredentials(credentials)
		if err != nil {
			return AdminProvider{}, err
		}
	}
	if err := m.repo.Upsert(ctx, record); err != nil {
		return AdminProvider{}, err
	}
	return m.describe(ctx, id, true)
}

func (m *Manager) describe(ctx context.Context, id string, checkProvider bool) (AdminProvider, error) {
	spec, ok := m.findSpec(id)
	if !ok {
		return AdminProvider{}, ErrUnsupportedProvider
	}
	id = canonicalConnectionID(id, spec)
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		return AdminProvider{}, err
	}
	credentials, err := m.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return AdminProvider{}, err
	}
	credentials = m.withDefaults(id, credentials)
	configured := spec.Configured(credentials)
	billingConfigured, _ := spec.GroupState(credentials, "billing")
	defaultTimeout := m.client.Timeout
	if defaultTimeout <= 0 {
		defaultTimeout = 30 * time.Second
	}
	result := AdminProvider{
		SupportsBillingCredentials: spec.Bills != nil,
		Fields:                     spec.Fields, Values: publicValues(spec, credentials, false), Hints: publicValues(spec, credentials, true), IconURL: IconURL(spec), IconMonochrome: spec.IconMonochrome, BillingHelp: spec.BillingHelp,
		DefaultTimeoutSeconds: max(1, int64(defaultTimeout/time.Second)),
		ID:                    id, Name: spec.Name, Description: spec.Description, APIType: spec.ID, Version: record.Version, Archived: record.Archived, Connection: publicConnection(credentials.Connection),
		Enabled: record.Enabled, Configured: configured, CredentialSource: record.CredentialSource,
		CredentialHint: maskCredential(credentials.APIKey), BaseURL: credentials.BaseURL,
		MusicConfigured: strings.TrimSpace(credentials.MusicAPIKey) != "", MusicCredentialHint: maskCredential(credentials.MusicAPIKey),
		MusicBaseURL: credentials.MusicBaseURL, Region: m.providerRegion(id, credentials.BaseURL),
		BillingConfigured: billingConfigured, BillingAccessKeyHint: maskCredential(credentials.BillingAccessKeyID),
		Status: "not_configured", StatusMessage: "Add an API key to connect this provider.",
		MediaCounts: map[string]int{}, UpdatedAt: record.UpdatedAt,
	}
	if record.Name != "" {
		result.Name = record.Name
	}
	if !configured {
		return result, nil
	}
	if !checkProvider {
		if record.Enabled {
			result.Status = "connected"
			result.StatusMessage = "Provider enabled."
			if spec.PublicCatalog {
				result.Status = "unverified"
				result.StatusMessage = "Public catalog. API key and model access are not verified; run a generation to test."
			}
		} else {
			result.Status = "disabled"
			result.StatusMessage = "Provider disabled and hidden from Playground."
		}
		return result, nil
	}
	started := time.Now()
	now := time.Now().UTC()
	result.LastCheckedAt = &now
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	adapter, err := m.resolve(checkCtx, id, false)
	result.CheckLatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = "error"
		result.StatusMessage = err.Error()
		return result, nil
	}
	models, err := listAdapterModels(checkCtx, adapter)
	result.CheckLatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = "error"
		result.StatusMessage = err.Error()
		return result, nil
	}
	result.Models = models
	result.ModelCount = len(models)
	for _, model := range models {
		result.MediaCounts[model.MediaType]++
	}
	if record.Enabled {
		result.Status = "connected"
		result.StatusMessage = "Catalog loaded. Provider enabled."
		if spec.PublicCatalog {
			result.Status = "unverified"
			result.StatusMessage = "Public catalog loaded. API key and model access are not verified; run a generation to test."
		}
		if credentials.Connection != nil && (credentials.Connection.ManualCatalog || len(credentials.Connection.Models) > 0) {
			result.Status = "unverified"
			result.StatusMessage = "Manual catalog. Save does not verify model access; run a generation to test."
		}
	} else {
		result.Status = "disabled"
		result.StatusMessage = "Configuration saved. Provider is disabled."
	}
	return result, nil
}

func (m *Manager) resolve(ctx context.Context, id string, requireEnabled bool, expectedIdentity ...string) (core.MediaProvider, error) {
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	if requireEnabled && (!record.Enabled || record.Archived) {
		return nil, ErrProviderDisabled
	}
	credentials, err := m.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return nil, err
	}
	credentials = m.withDefaults(id, credentials)
	spec, ok := m.findSpec(id)
	if !ok {
		return nil, ErrUnsupportedProvider
	}
	if len(expectedIdentity) > 0 && expectedIdentity[0] != "" && !m.isFree(id) {
		expected := expectedIdentity[0]
		if connectionIdentity(id, spec, credentials) != expected {
			revisions, ok := m.repo.(providerconfig.Revisions)
			if !ok {
				return nil, core.ErrProviderChanged
			}
			raw, err := revisions.GetRevision(ctx, id, expected)
			if err != nil {
				return nil, core.ErrProviderChanged
			}
			credentials, err = m.decryptCredentials(raw)
			if err != nil {
				return nil, err
			}
			if connectionIdentity(id, spec, credentials) != expected {
				return nil, core.ErrProviderChanged
			}
		}
	}
	if !spec.Configured(credentials) {
		return nil, ErrProviderUnconfigured
	}
	fingerprint := credentialFingerprint(credentials)
	m.mu.Lock()
	cached, ok := m.adapters[m.cacheKey(ctx, id)]
	m.mu.Unlock()
	if ok && cached.fingerprint == fingerprint {
		return cached.adapter, nil
	}
	adapter, err := m.buildAdapter(id, credentials)
	if err != nil {
		return nil, err
	}
	m.cacheAdapter(ctx, id, credentials, adapter)
	return adapter, nil
}

func (m *Manager) cacheAdapter(ctx context.Context, id string, credentials Credentials, adapter core.MediaProvider) {
	m.mu.Lock()
	m.adapters[m.cacheKey(ctx, id)] = cachedAdapter{fingerprint: credentialFingerprint(credentials), adapter: adapter}
	m.mu.Unlock()
}

func (m *Manager) cacheKey(ctx context.Context, id string) string {
	return identity.ProviderOwnerID(ctx) + ":" + id
}

func (m *Manager) buildAdapter(id string, credentials Credentials) (core.MediaProvider, error) {
	d, ok := m.findSpec(id)
	if !ok {
		return nil, ErrUnsupportedProvider
	}
	clientCopy := *m.client
	if clientCopy.Timeout <= 0 {
		clientCopy.Timeout = 30 * time.Second
	}
	if credentials.Connection != nil && credentials.Connection.TimeoutSeconds != nil {
		clientCopy.Timeout = time.Duration(*credentials.Connection.TimeoutSeconds) * time.Second
	}
	client := &clientCopy
	if credentials.Connection != nil {
		client = configurationClient(client, credentials)
	}
	adapter, err := d.Factory(credentials, m.runtimeSettings(d), client)
	if err != nil {
		return nil, err
	}
	// Error/result redaction applies to builtin settings as well as configured connections.
	return &connectionAdapter{MediaProvider: adapter, credentials: credentials, definition: d}, nil
}
func (m *Manager) runtimeSettings(d providerkit.Definition) providerkit.Settings {
	s := providerkit.Settings{}
	for k, v := range d.RuntimeDefaults {
		s[k] = v
	}
	for k, v := range m.cfg.Providers[d.ID] {
		s[k] = v
	}
	return s
}

type credentialChecker interface {
	CheckCredentials(context.Context) error
}

func validateAdapter(ctx context.Context, adapter core.MediaProvider) error {
	if checker, ok := adapter.(credentialChecker); ok {
		return checker.CheckCredentials(ctx)
	}
	_, err := listAdapterModels(ctx, adapter)
	return err
}

func listAdapterModels(ctx context.Context, adapter core.MediaProvider) ([]core.MediaModel, error) {
	catalog, ok := adapter.(core.ModelCatalog)
	if !ok {
		return nil, errors.New("provider does not expose a model catalog")
	}
	return catalog.ListModels(ctx)
}

func (m *Manager) encryptCredentials(credentials Credentials) ([]byte, error) {
	value, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	return m.cipher.Encrypt(value)
}

func (m *Manager) decryptCredentials(value []byte) (Credentials, error) {
	if len(value) == 0 {
		return Credentials{}, nil
	}
	plaintext, err := m.cipher.Decrypt(value)
	if err != nil {
		return Credentials{}, err
	}
	var credentials Credentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return Credentials{}, errors.New("stored provider credentials are invalid")
	}
	return credentials, nil
}

func (m *Manager) environmentCredentials(id string) Credentials {
	d, _ := m.findSpec(id)
	c := Credentials{}
	for _, f := range d.Fields {
		if v := m.cfg.Providers[id][f.Key]; v != "" {
			c.Set(f.Key, v)
		}
	}
	return d.Defaults(c)
}
func (m *Manager) withDefaults(id string, c Credentials) Credentials {
	d, _ := m.findSpec(id)
	return d.Defaults(c)
}
func mergeCredentials(d providerkit.Definition, c *Credentials, u ProviderUpdate) (changed, inferenceChanged bool) {
	for _, f := range d.Fields {
		value, ok := u.Fields()[f.Key]
		value = strings.TrimSpace(value)
		if !ok || value == c.Get(f.Key) {
			continue
		}
		c.Set(f.Key, value)
		changed = true
		if f.Group != "billing" && f.Group != "assets" {
			inferenceChanged = true
		}
	}
	return
}

func hasAnyCredentials(d providerkit.Definition, c Credentials) bool {
	if c.Connection != nil {
		return true
	}
	for _, f := range d.Fields {
		if value := strings.TrimSpace(c.Get(f.Key)); value != "" && (f.Secret || value != f.Default) {
			return true
		}
	}
	return false
}

func credentialFingerprint(credentials Credentials) string {
	value, _ := json.Marshal(credentials)
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func maskCredential(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "••••"
	}
	return "••••" + value[len(value)-4:]
}

func (m *Manager) providerRegion(id, raw string) string {
	d, _ := m.findSpec(id)
	return d.Location(raw)
}
func (m *Manager) findSpec(id string) (providerkit.Definition, bool) {
	if prefix, _, ok := strings.Cut(id, "--"); ok {
		id = prefix
	}
	if m.registry != nil {
		return m.registry.Find(id)
	}
	return registry.Find(id)
}
func (m *Manager) isFree(id string) bool { d, ok := m.findSpec(id); return ok && d.Free }

// IconURL identifies embedded provider artwork with a content-versioned URL.
func IconURL(d providerkit.Definition) string {
	if len(d.Icon) == 0 {
		return ""
	}
	ext := "png"
	if d.IconType == "image/svg+xml" {
		ext = "svg"
	}
	digest := sha256.Sum256(d.Icon)
	return "/provider-assets/" + d.ID + "/icon." + ext + "?v=" + hex.EncodeToString(digest[:8])
}
func publicValues(d providerkit.Definition, c Credentials, secrets bool) map[string]string {
	v := map[string]string{}
	for _, f := range d.Fields {
		if f.Secret == secrets {
			value := c.Get(f.Key)
			if secrets {
				value = maskCredential(value)
			}
			v[f.Key] = value
		}
	}
	return v
}
func validateUpdate(d providerkit.Definition, u ProviderUpdate) error {
	for k, v := range u.Fields() {
		if strings.TrimSpace(v) == "" {
			continue
		}
		found := false
		for _, f := range d.Fields {
			if f.Key == k {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported provider setting %q", k)
		}
	}
	return nil
}

type managedProvider struct {
	id      string
	name    string
	manager *Manager
}

func (p *managedProvider) Name() string { return p.id }

func (p *managedProvider) ProviderIcon() (string, bool) {
	spec, ok := p.manager.findSpec(p.id)
	if !ok {
		return "", false
	}
	return IconURL(spec), spec.IconMonochrome
}

func (p *managedProvider) DisplayName() string {
	if p.name != "" {
		return p.name
	}
	spec, ok := p.manager.findSpec(p.id)
	if ok {
		return spec.Name
	}
	return p.id
}

func (p *managedProvider) Availability(ctx context.Context) (bool, bool, error) {
	record, err := p.manager.loadRecord(ctx, p.id)
	if err != nil {
		return false, false, err
	}
	if p.manager.isFree(p.id) {
		return record.Enabled, true, nil
	}
	credentials, err := p.manager.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return false, false, err
	}
	d, _ := p.manager.findSpec(p.id)
	return record.Enabled && !record.Archived, d.Configured(credentials), nil
}

func (p *managedProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	adapter, err := p.manager.resolve(ctx, p.id, true)
	if err != nil {
		return nil, err
	}
	models, err := listAdapterModels(ctx, adapter)
	models = append([]core.MediaModel(nil), models...)
	if err != nil {
		return nil, err
	}
	quote := p.referenceQuoter(ctx, adapter)
	state, err := p.catalogState(ctx)
	if err != nil {
		return nil, err
	}
	baseQuote := quote
	quote = func(model core.MediaModel, request core.MediaRequest) (core.TaskCost, error) {
		cost, err := baseQuote(model, request)
		return applyCatalogQuote(cost, err, model, request, state)
	}
	for i := range models {
		models[i] = p.modelReference(models[i], quote)
		models[i] = applyCatalogModel(models[i], state)
	}
	return models, nil
}

func (p *managedProvider) Generate(ctx context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	adapter, err := p.manager.resolve(ctx, p.id, request.ProviderIdentity == "", request.ProviderIdentity)
	if err != nil {
		return core.ProviderTask{}, err
	}
	allowed, err := p.manager.referencePolicy(ctx, request.Options)
	if err != nil {
		return core.ProviderTask{}, err
	}
	if direct, ok := p.manager.referenceImages.(interface {
		ResolveDirect(context.Context, core.MediaOptions) (core.MediaOptions, error)
	}); ok {
		request.Options, err = direct.ResolveDirect(ctx, request.Options)
		if err != nil {
			return core.ProviderTask{}, err
		}
	}
	inline := false
	if preference, ok := adapter.(interface{ PreferInlineImages() bool }); ok {
		inline = preference.PreferInlineImages()
	}
	if allowed && inline && p.manager.referenceImages != nil && len(request.Options.ImageURLs) > 0 {
		request.Options.ImageURLs, err = p.manager.referenceImages.Resolve(ctx, request.Options.ImageURLs)
		if err != nil {
			return core.ProviderTask{}, err
		}
	}
	if request.Options.TimeoutSeconds != nil {
		if err := core.ValidateTimeoutSeconds(request.Options.TimeoutSeconds); err != nil {
			return core.ProviderTask{}, err
		}
		ctx = httpclient.WithRequestTimeout(ctx, time.Duration(*request.Options.TimeoutSeconds)*time.Second)
	}
	return adapter.Generate(ctx, request)
}

func (p *managedProvider) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	adapter, err := p.manager.resolve(ctx, p.id, false, core.ProviderRevision(ctx))
	if err != nil {
		return core.ProviderResult{}, err
	}
	return adapter.GetTask(ctx, id)
}

func (p *managedProvider) ValidateRequest(ctx context.Context, request core.MediaRequest) error {
	if _, err := p.manager.referencePolicy(ctx, request.Options); err != nil {
		return err
	}
	adapter, err := p.manager.resolve(ctx, p.id, true)
	if err != nil {
		return err
	}
	if validator, ok := adapter.(core.RequestValidator); ok {
		return validator.ValidateRequest(ctx, request)
	}
	return nil
}

func (p *managedProvider) IsFree() bool { return p.manager.isFree(p.id) }
func (p *managedProvider) FailureIsFree() bool {
	d, _ := p.manager.findSpec(p.id)
	return d.FailureNoCharge
}

func (m *Manager) definitions() []providerkit.Definition {
	if m.registry != nil {
		return m.registry.All()
	}
	return registry.All()
}
