package alibaba

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"net/http"
	"net/url"
	"strings"
)

func Definition() providerkit.Definition {
	return providerkit.Definition{
		CatalogSources:             catalogSources(),
		CatalogInterpret:           interpretVideoAPI,
		CatalogVerificationSources: videoVerificationSources(),
		CatalogRegions:             map[string]string{"Singapore": "https://dashscope-intl.aliyuncs.com/api/v1", "Beijing": "https://dashscope.aliyuncs.com/api/v1"},
		LegacyIdentity:             true,
		Profiles:                   func() []core.MediaModel { return mediaModels(true) },
		Region:                     providerRegion,
		ID:                         "alibaba", Name: "Alibaba Cloud Model Studio", Description: "Images, video, speech and music through the native DashScope API.", DefaultModel: "wan2.7-t2v", ModelEnv: "DASHSCOPE_MODEL",
		Fields:     []providerkit.Field{{Key: "api_key", Label: "API key", Secret: true, Required: true, Env: "DASHSCOPE_API_KEY"}, {Key: "base_url", Label: "API endpoint", Default: "https://dashscope-intl.aliyuncs.com/api/v1", Env: "DASHSCOPE_BASE_URL"}, {Key: "music_api_key", Label: "Music API key", Secret: true, Env: "DASHSCOPE_MUSIC_API_KEY"}, {Key: "music_base_url", Label: "Music endpoint", Env: "DASHSCOPE_MUSIC_BASE_URL"}, {Key: "billing_access_key_id", Label: "Billing AccessKey ID", Secret: true, Group: "billing", Env: "ALIBABA_BILLING_ACCESS_KEY_ID"}, {Key: "billing_access_key_secret", Label: "Billing AccessKey Secret", Secret: true, Group: "billing", Env: "ALIBABA_BILLING_ACCESS_KEY_SECRET"}},
		RuntimeEnv: map[string]string{}, RuntimeDefaults: providerkit.Settings{},
		Quote: quotePublic, Bills: fetchBills, NormalizeBill: normalizeBill,
		BillingHelp: "Add a separate Alibaba RAM AccessKey with AliyunBSSReadOnlyAccess.",
		Aliases:     []string{"dashscope"}, FailureNoCharge: true,
		Factory: func(c providerkit.Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			return New(c.APIKey, c.BaseURL, c.MusicAPIKey, c.MusicBaseURL, client)
		},
	}
}

func (p *Adapter) DisplayName() string { return Definition().Name }
func (p *Adapter) IsFree() bool        { return Definition().Free }
func (p *Adapter) FailureIsFree() bool { return Definition().FailureNoCharge }
func providerRegion(rawURL string) string {
	host := rawURL
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}

	switch {
	case strings.Contains(host, "ap-southeast-1") || strings.Contains(host, "dashscope-intl"):
		return "Singapore · ap-southeast-1"
	case strings.Contains(host, "cn-beijing") || host == "dashscope.aliyuncs.com":
		return "China · cn-beijing"
	case strings.Contains(host, "eu-central-1"):
		return "Germany · eu-central-1"
	case strings.Contains(host, "ap-northeast-1"):
		return "Japan · ap-northeast-1"
	case strings.Contains(host, "cn-hongkong"):
		return "Hong Kong · cn-hongkong"
	case strings.Contains(host, "us-east-1"):
		return "US · us-east-1"
	}
	return host
}
