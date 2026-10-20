package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/google/uuid"
)

var ErrSettingsNotFound = errors.New("storage settings not found")

type SettingsRecord struct {
	Driver       string
	ConnectionID string
	Ciphertext   []byte
}

type SettingsRepository interface {
	Get(context.Context) (SettingsRecord, error)
	Connection(context.Context, string) (SettingsRecord, error)
	Save(context.Context, SettingsRecord) error
}

type Settings struct {
	BrowserUploadsPending bool   `json:"browser_uploads_pending,omitempty"`
	Driver                string `json:"driver"`
	Endpoint              string `json:"endpoint"`
	Region                string `json:"region"`
	Bucket                string `json:"bucket"`
	Prefix                string `json:"prefix"`
	PathStyle             bool   `json:"path_style"`
	AccessKeyID           string `json:"access_key_id"`
	CredentialsConfigured bool   `json:"credentials_configured"`
}

type Update struct {
	// BrowserOrigin is supplied by the HTTP handler, never by the JSON body.
	BrowserOrigin   string `json:"-"`
	Driver          string `json:"driver"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	PathStyle       bool   `json:"path_style"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

type Manager struct {
	repo     SettingsRepository
	cipher   *providerconfig.Cipher
	local    *Local
	staging  *Local
	client   *http.Client
	maxBytes int64
}

func NewManager(repo SettingsRepository, cipher *providerconfig.Cipher, localPath string, client *http.Client, maxBytes int64) (*Manager, error) {
	local, err := NewLocal(localPath, client, maxBytes)
	if err != nil {
		return nil, err
	}
	if repo == nil || cipher == nil {
		return nil, errors.New("storage settings repository and cipher are required")
	}
	return &Manager{repo: repo, cipher: cipher, local: local, staging: &Local{root: stagingRoot(), client: client, stagingOnly: true}, client: client, maxBytes: maxBytes}, nil
}

func (m *Manager) current(ctx context.Context) (SettingsRecord, S3Config, error) {
	record, err := m.repo.Get(ctx)
	if errors.Is(err, ErrSettingsNotFound) {
		driver := "local"
		if identity.OrganizationFromContext(ctx).ID != "" && identity.StorageOwnerID(ctx) == identity.OrganizationFromContext(ctx).ID {
			driver = "gateway"
		}
		return SettingsRecord{Driver: driver}, S3Config{}, nil
	}
	if err != nil {
		return SettingsRecord{}, S3Config{}, err
	}
	c, err := m.decode(record)
	return record, c, err
}
func (m *Manager) decode(record SettingsRecord) (S3Config, error) {
	var c S3Config
	if len(record.Ciphertext) == 0 {
		return c, nil
	}
	raw, err := m.cipher.Decrypt(record.Ciphertext)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}
func settingsView(driver string, c S3Config) Settings {
	return Settings{Driver: driver, Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle, AccessKeyID: c.AccessKeyID, CredentialsConfigured: c.AccessKeyID != "" && c.SecretAccessKey != ""}
}
func (m *Manager) Get(ctx context.Context) (Settings, error) {
	r, c, err := m.current(ctx)
	if err != nil {
		return Settings{}, err
	}
	return settingsView(r.Driver, c), nil
}
func sameDestination(a, b S3Config) bool {
	return a.Endpoint == b.Endpoint && a.Region == b.Region && a.Bucket == b.Bucket && a.Prefix == b.Prefix && a.PathStyle == b.PathStyle
}
func (m *Manager) prepare(ctx context.Context, input Update) (SettingsRecord, S3Config, error) {
	if input.BrowserOrigin != "" && !validBrowserOrigin(input.BrowserOrigin) {
		return SettingsRecord{}, S3Config{}, fmt.Errorf("%w: invalid browser origin", ErrInvalidConfig)
	}
	if input.Driver != "local" && input.Driver != "s3" && input.Driver != "gateway" {
		return SettingsRecord{}, S3Config{}, fmt.Errorf("%w: driver must be local, s3 or gateway", ErrInvalidConfig)
	}
	r, old, err := m.current(ctx)
	if err != nil {
		return r, old, err
	}
	r.Driver = input.Driver
	if input.Driver == "local" || input.Driver == "gateway" {
		return r, old, nil
	}
	c := S3Config{Endpoint: input.Endpoint, Region: input.Region, Bucket: input.Bucket, Prefix: input.Prefix, PathStyle: input.PathStyle, AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey}
	if c.AccessKeyID == "" {
		c.AccessKeyID = old.AccessKeyID
	}
	if c.SecretAccessKey == "" {
		c.SecretAccessKey = old.SecretAccessKey
	}
	if err := c.normalize(); err != nil {
		return r, c, err
	}
	// Never send saved credentials to a newly entered server without explicit replacements.
	if c.Endpoint != old.Endpoint && old.AccessKeyID != "" && (input.AccessKeyID == "" || input.SecretAccessKey == "") {
		return r, c, fmt.Errorf("%w: re-enter both credentials when changing the endpoint", ErrInvalidConfig)
	}
	if r.ConnectionID == "" || !sameDestination(c, old) {
		r.ConnectionID = uuid.NewString()
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return r, c, err
	}
	r.Ciphertext, err = m.cipher.Encrypt(raw)
	return r, c, err
}
func (m *Manager) Check(ctx context.Context, input Update) error {
	_, c, err := m.prepare(ctx, input)
	if err != nil {
		return err
	}
	if input.Driver == "local" || input.Driver == "gateway" {
		return nil
	}
	store, err := NewS3(c, m.client, m.maxBytes)
	if err != nil {
		return err
	}
	if err := store.Check(ctx); err != nil {
		return err
	}
	return store.CheckBrowser(ctx, input.BrowserOrigin)
}
func (m *Manager) Update(ctx context.Context, input Update) (Settings, error) {
	r, c, err := m.prepare(ctx, input)
	if err != nil {
		return Settings{}, err
	}
	pending := false
	if input.Driver == "s3" {
		store, err := NewS3(c, m.client, m.maxBytes)
		if err != nil {
			return Settings{}, err
		}
		if err := store.Check(ctx); err != nil {
			return Settings{}, fmt.Errorf("%w: %s", ErrInvalidConfig, err)
		}
		pending, err = store.EnsureBrowser(ctx, input.BrowserOrigin)
		if err != nil {
			return Settings{}, err
		}
	}
	if err := m.repo.Save(ctx, r); err != nil {
		return Settings{}, err
	}
	view := settingsView(r.Driver, c)
	view.BrowserUploadsPending = pending
	return view, nil
}

// ForArchive fixes one destination for all artifacts of a generation. Reads
// always resolve the connection recorded in the key, not the current selection.
func (m *Manager) ForArchive(ctx context.Context) (Store, error) {
	r, c, err := m.current(ctx)
	if err != nil {
		return nil, err
	}
	if r.Driver == "gateway" {
		return m.ForArchive(identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), identity.SharedOwnerID))
	}
	if r.Driver == "local" {
		return &versionedStore{Store: m.local, ownerID: identity.OwnerID(ctx), storageOwnerID: identity.StorageOwnerID(ctx), manager: m}, nil
	}
	if r.Driver != "s3" || r.ConnectionID == "" {
		return nil, errors.New("invalid saved storage configuration")
	}
	s, err := NewS3(c, m.client, m.maxBytes)
	if err != nil {
		return nil, err
	}
	return &versionedStore{Store: s, connectionID: r.ConnectionID, ownerID: identity.OwnerID(ctx), storageOwnerID: identity.StorageOwnerID(ctx), manager: m}, nil
}
func (m *Manager) Archive(ctx context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	s, err := m.ForArchive(ctx)
	if err != nil {
		return core.StoredMedia{}, err
	}
	return s.Archive(ctx, taskID, sourceURL)
}
func (m *Manager) Open(ctx context.Context, key string) (Object, error) {
	if !strings.HasPrefix(key, "s3:") {
		return m.local.Open(ctx, key)
	}
	s, objectKey, err := m.objectS3(ctx, key)
	if err != nil {
		return Object{}, err
	}
	return s.Open(ctx, objectKey)
}

type versionedStore struct {
	storageOwnerID string
	Store
	connectionID string
	ownerID      string
	manager      *Manager
}

func (s *versionedStore) Archive(ctx context.Context, taskID, sourceURL string) (core.StoredMedia, error) {
	ctx = identity.WithResourceOwners(ctx, identity.ProviderOwnerID(ctx), s.storageOwnerID)
	media, err := s.Store.Archive(ctx, s.ownerID+"/results/"+taskID, sourceURL)
	if err == nil && s.connectionID != "" {
		media.StorageKey = "s3:" + s.connectionID + "/" + media.StorageKey
	}
	if err == nil && s.manager != nil {
		// The object already exists. Worker cancellation must not suppress its
		// quota record or make it invisible to the task's normal result deletion.
		accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = s.manager.record(accounting, media, strings.Split(taskID, "/")[0])
	}
	return media, err
}
