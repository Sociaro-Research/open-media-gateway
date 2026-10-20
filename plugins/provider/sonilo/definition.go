// Package sonilo adapts Sonilo music, sound effects and audio post-production.
package sonilo

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const DefaultBaseURL = "https://api.sonilo.com/v1"
const DefaultModel = "text-to-music"

func Definition() providerkit.Definition {
	return providerkit.Definition{
		CatalogSources: catalogSources(),
		Icon:           Icon(), IconType: "image/png",
		ID: "sonilo", Name: "Sonilo", DefaultModel: DefaultModel, ModelEnv: "SONILO_MODEL",
		Description: "Music and sound effects from text or video, scored video outputs, combined soundtracks and narration ducking.",
		Fields: []providerkit.Field{
			{Key: "api_key", Label: "API key", Secret: true, Required: true, Env: "SONILO_API_KEY"},
			{Key: "base_url", Label: "API endpoint", Default: DefaultBaseURL, Env: "SONILO_BASE_URL"},
			{Key: "video_music_price", Label: "Video output music price (USD/s)", Env: "SONILO_VIDEO_MUSIC_PRICE"},
			{Key: "video_sfx_price", Label: "Video output sound effects price (USD/s)", Env: "SONILO_VIDEO_SFX_PRICE"},
			{Key: "sound_price", Label: "Combined audio price (USD/s)", Env: "SONILO_SOUND_PRICE"},
			{Key: "video_sound_price", Label: "Combined video price (USD/s)", Env: "SONILO_VIDEO_SOUND_PRICE"},
		},
		Profiles:    catalog,
		BillingHelp: "Standard per-second estimates include Sonilo's minimum billed duration. Video inputs reserve the endpoint's maximum duration. Set account-confirmed tariffs for video-output and combined-sound operations: their rates are not published separately. Trial allowances and account discounts are not assumed. Account usage is aggregated; no public invoice-detail API is documented.",
		Factory: func(c providerkit.Credentials, _ providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			a, err := New(c.Get("api_key"), c.Get("base_url"), client)
			if err != nil {
				return nil, err
			}
			if err = a.setPrices(c); err != nil {
				return nil, err
			}
			return a, nil
		},
	}
}
