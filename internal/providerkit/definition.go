// Package providerkit is the contract between provider modules and the host.
package providerkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Credentials struct {
	Connection             *Connection       `json:"connection,omitempty"`
	Extra                  map[string]string `json:"-"`
	APIKey                 string            `json:"api_key,omitempty"`
	BaseURL                string            `json:"base_url,omitempty"`
	MusicAPIKey            string            `json:"music_api_key,omitempty"`
	MusicBaseURL           string            `json:"music_base_url,omitempty"`
	BillingAccessKeyID     string            `json:"billing_access_key_id,omitempty"`
	BillingAccessKeySecret string            `json:"billing_access_key_secret,omitempty"`
}

type ProviderUpdate struct {
	Name                   *string           `json:"name,omitempty"`
	Archived               *bool             `json:"archived,omitempty"`
	Version                *int64            `json:"version,omitempty"`
	Connection             *Connection       `json:"connection,omitempty"`
	Extra                  map[string]string `json:"-"`
	Enabled                *bool             `json:"enabled,omitempty"`
	APIKey                 *string           `json:"api_key,omitempty"`
	BaseURL                *string           `json:"base_url,omitempty"`
	MusicAPIKey            *string           `json:"music_api_key,omitempty"`
	MusicBaseURL           *string           `json:"music_base_url,omitempty"`
	BillingAccessKeyID     *string           `json:"billing_access_key_id,omitempty"`
	BillingAccessKeySecret *string           `json:"billing_access_key_secret,omitempty"`
}

type Settings map[string]string

func (s Settings) Duration(key string, fallback time.Duration) time.Duration {
	if d, e := time.ParseDuration(s[key]); e == nil && d > 0 {
		return d
	}
	return fallback
}

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Group    string `json:"group,omitempty"`
	Default  string `json:"default,omitempty"`
	Env      string `json:"-"`
}
type Definition struct {
	AssetLibrary   func(Credentials, *http.Client) (providerassets.Client, error)
	CatalogSources []catalogsync.Source
	// Exact public source aliases preserve existing schedules while recording the actual fetched URL.
	CatalogSourceAliases       map[string]string
	CatalogInterpret           func(catalogsync.Source, string) ([]catalogsync.Entry, error)
	CatalogVerificationSources map[string][]catalogsync.Source
	CatalogReviews             map[string]catalogsync.DocumentReview
	CatalogAliases             map[string]string // Exact reviewed snapshots sharing a published model tariff.
	CatalogRegions             map[string]string // Reviewed endpoint contexts for pure local tariff validation.
	PublicCatalog              bool              // Discovery does not authenticate credentials or verify model access.
	AuthenticatedCheck         bool              // Explicit connection checks authenticate using a reviewed, non-generating API.
	LegacyIdentity             bool              // Preserve pre-registry task fingerprints for existing modules.
	ID                         string
	Name                       string
	Description                string
	Aliases                    []string
	Free                       bool
	Default                    bool
	DefaultModel               string
	ModelEnv                   string
	Fields                     []Field
	RuntimeEnv                 map[string]string
	RuntimeDefaults            Settings
	BillingHelp                string
	Icon                       []byte
	IconType                   string
	IconMonochrome             bool
	Profiles                   func() []core.MediaModel
	Factory                    func(Credentials, Settings, *http.Client) (core.MediaProvider, error)
	Quote                      func(string, core.MediaModel, core.MediaRequest, time.Time) (core.TaskCost, error)
	Bills                      func(context.Context, Credentials, string, *http.Client) ([]map[string]any, error)
	NormalizeBill              func(string, map[string]any) (core.BillRecord, error)
	Region                     func(string) string
	FailureNoCharge            bool
}

func (d Definition) Defaults(c Credentials) Credentials {
	for _, f := range d.Fields {
		if c.Get(f.Key) == "" && f.Default != "" {
			c.Set(f.Key, f.Default)
		}
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.MusicBaseURL = strings.TrimRight(strings.TrimSpace(c.MusicBaseURL), "/")
	return c
}
func (d Definition) Configured(c Credentials) bool {
	for _, f := range d.Fields {
		if f.Required && strings.TrimSpace(c.Get(f.Key)) == "" {
			return false
		}
	}
	return true
}
func (d Definition) Identity(c Credentials) string {
	if d.Free {
		return d.ID
	}
	// Retain the identity of accepted tasks created before the registry migration.
	value := d.ID + "\x00" + c.BaseURL + "\x00" + c.APIKey
	if !d.LegacyIdentity {
		inference := map[string]string{}
		for _, f := range d.Fields {
			if f.Group != "billing" && f.Group != "assets" {
				inference[f.Key] = c.Get(f.Key)
			}
		}
		b, _ := json.Marshal(inference)
		value = d.ID + "\x00" + string(b)
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func (d Definition) Location(raw string) string {
	if d.Region != nil {
		return d.Region(raw)
	}
	if u, e := url.Parse(raw); e == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
func (c Credentials) Get(key string) string {
	switch key {
	case "api_key":
		return c.APIKey
	case "base_url":
		return c.BaseURL
	case "music_api_key":
		return c.MusicAPIKey
	case "music_base_url":
		return c.MusicBaseURL
	case "billing_access_key_id":
		return c.BillingAccessKeyID
	case "billing_access_key_secret":
		return c.BillingAccessKeySecret
	}
	return c.Extra[key]
}
func (c *Credentials) Set(key, value string) {
	switch key {
	case "api_key":
		c.APIKey = value
	case "base_url":
		c.BaseURL = value
	case "music_api_key":
		c.MusicAPIKey = value
	case "music_base_url":
		c.MusicBaseURL = value
	case "billing_access_key_id":
		c.BillingAccessKeyID = value
	case "billing_access_key_secret":
		c.BillingAccessKeySecret = value
	default:
		if c.Extra == nil {
			c.Extra = map[string]string{}
		}
		c.Extra[key] = value
	}
}
func (c Credentials) MarshalJSON() ([]byte, error) {
	values := map[string]any{}
	for _, key := range []string{"api_key", "base_url", "music_api_key", "music_base_url", "billing_access_key_id", "billing_access_key_secret"} {
		if value := c.Get(key); value != "" {
			values[key] = value
		}
	}
	for key, value := range c.Extra {
		values[key] = value
	}
	if c.Connection != nil {
		values["connection"] = c.Connection
	}
	return json.Marshal(values)
}
func (c *Credentials) UnmarshalJSON(b []byte) error {
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}
	*c = Credentials{}
	for key, raw := range values {
		if key == "connection" {
			if err := json.Unmarshal(raw, &c.Connection); err != nil {
				return err
			}
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		c.Set(key, value)
	}
	return nil
}
func (d Definition) ValidateRuntime(s Settings) error {
	for k, v := range s {
		if strings.Contains(k, "timeout") || k == "delay" {
			if t, e := time.ParseDuration(v); e != nil || t <= 0 {
				return fmt.Errorf("provider %s: %s must be a positive duration", d.ID, k)
			}
		}
	}
	return nil
}

func (u ProviderUpdate) Fields() map[string]string {
	v := map[string]string{}
	pairs := map[string]*string{"api_key": u.APIKey, "base_url": u.BaseURL, "music_api_key": u.MusicAPIKey, "music_base_url": u.MusicBaseURL, "billing_access_key_id": u.BillingAccessKeyID, "billing_access_key_secret": u.BillingAccessKeySecret}
	for k, p := range pairs {
		if p != nil {
			v[k] = *p
		}
	}
	for k, s := range u.Extra {
		v[k] = s
	}
	return v
}
func (u *ProviderUpdate) UnmarshalJSON(b []byte) error {
	v := map[string]json.RawMessage{}
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	*u = ProviderUpdate{Extra: map[string]string{}}
	for k, b := range v {
		switch k {
		case "name":
			if err := json.Unmarshal(b, &u.Name); err != nil {
				return err
			}
			continue
		case "archived":
			if err := json.Unmarshal(b, &u.Archived); err != nil {
				return err
			}
			continue
		case "version":
			if err := json.Unmarshal(b, &u.Version); err != nil {
				return err
			}
			continue
		case "connection":
			if err := json.Unmarshal(b, &u.Connection); err != nil {
				return err
			}
			continue
		}
		if k == "enabled" {
			if e := json.Unmarshal(b, &u.Enabled); e != nil {
				return e
			}
			continue
		}
		var s string
		if e := json.Unmarshal(b, &s); e != nil {
			return fmt.Errorf("provider setting %s must be a string", k)
		}
		switch k {
		case "api_key":
			u.APIKey = &s
		case "base_url":
			u.BaseURL = &s
		case "music_api_key":
			u.MusicAPIKey = &s
		case "music_base_url":
			u.MusicBaseURL = &s
		case "billing_access_key_id":
			u.BillingAccessKeyID = &s
		case "billing_access_key_secret":
			u.BillingAccessKeySecret = &s
		default:
			u.Extra[k] = s
		}
	}
	return nil
}

// GroupState requires all fields of an optional group once any is supplied.
func (d Definition) GroupState(c Credentials, group string) (configured, partial bool) {
	total, set := 0, 0
	for _, f := range d.Fields {
		if f.Group == group {
			total++
			if strings.TrimSpace(c.Get(f.Key)) != "" {
				set++
			}
		}
	}
	return total > 0 && set == total, set > 0 && set < total
}
