package bytedance

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"net/http"
	"net/url"
	"strings"
)

func Definition() providerkit.Definition {
	return providerkit.Definition{
		AssetLibrary:   newAssetLibrary,
		CatalogSources: catalogSources(),
		LegacyIdentity: true,
		Profiles: func() []core.MediaModel {
			var result []core.MediaModel
			for _, id := range []string{"seedream-4-0-250828", "seedream-4-5-251128", "seedance-1-0-pro-fast-251015", "seedance-1-5-pro-251215", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-5-260628"} {
				result = append(result, describeModel(id))
			}
			return result
		},
		Region: providerRegion,
		ID:     "bytedance", Name: "BytePlus ModelArk", Description: "Seedream and Seedance through the native ModelArk API.", DefaultModel: "seedance-1-0-pro-fast-251015", ModelEnv: "BYTEDANCE_MODEL",
		Fields: []providerkit.Field{{Key: "api_key", Label: "API key", Secret: true, Required: true, Env: "BYTEDANCE_API_KEY"}, {Key: "base_url", Label: "API endpoint", Default: "https://ark.ap-southeast.bytepluses.com/api/v3", Env: "BYTEDANCE_BASE_URL"}, {Key: "asset_access_key_id", Label: "Asset library AccessKey ID (optional)", Secret: true, Group: "assets", Env: "BYTEPLUS_ASSET_ACCESS_KEY_ID"},
			{Key: "asset_access_key_secret", Label: "Asset library secret (optional)", Secret: true, Group: "assets", Env: "BYTEPLUS_ASSET_SECRET_ACCESS_KEY"},
			{Key: "asset_project", Label: "Asset library project", Group: "assets", Default: "default", Env: "BYTEPLUS_ASSET_PROJECT"},
			{Key: "asset_region", Label: "Asset library region", Group: "assets", Default: "ap-southeast-1", Env: "BYTEPLUS_ASSET_REGION"},
			{Key: "billing_access_key_id", Label: "Billing AccessKey ID", Secret: true, Group: "billing", Env: "BYTEPLUS_BILLING_ACCESS_KEY_ID"}, {Key: "billing_access_key_secret", Label: "Billing AccessKey Secret", Secret: true, Group: "billing", Env: "BYTEPLUS_BILLING_SECRET_ACCESS_KEY"}},
		Icon: Icon(), IconType: "image/png", IconMonochrome: false,
		Quote: quotePublic, Bills: fetchBills, NormalizeBill: normalizeBill,
		BillingHelp: "Add separate BytePlus Billing AccessKey credentials; the ModelArk inference key does not grant billing access.",
		Factory: func(c providerkit.Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			return New(c.APIKey, c.BaseURL, client, WithImageTimeout(client.Timeout))
		},
	}
}

func (p *Adapter) DisplayName() string      { return Definition().Name }
func (p *Adapter) IsFree() bool             { return Definition().Free }
func (p *Adapter) FailureIsFree() bool      { return Definition().FailureNoCharge }
func (p *Adapter) PreferInlineImages() bool { return true }
func providerRegion(rawURL string) string {
	host := rawURL
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}

	if strings.Contains(host, "eu-west-1") {
		return "Europe · eu-west-1"
	}
	if strings.Contains(host, "ap-southeast") {
		return "Asia Pacific · ap-southeast-1"
	}

	return host
}
