package mock

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"net/http"
	"time"
)

func Definition() providerkit.Definition {
	return providerkit.Definition{
		ID: "mock", Name: "Test provider", Description: "Free local generation to test the complete Gateway flow.", DefaultModel: "mock-video-v1", ModelEnv: "MOCK_MODEL",
		Fields:     []providerkit.Field{},
		RuntimeEnv: map[string]string{"delay": "MOCK_DELAY", "result_base_url": "MOCK_RESULT_BASE_URL"}, RuntimeDefaults: providerkit.Settings{"delay": "2s", "result_base_url": "embedded"},
		Free: true, Default: true, FailureNoCharge: true,
		Factory: func(c providerkit.Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			return New(s.Duration("delay", 2*time.Second), s["result_base_url"]), nil
		},
	}
}

func (p *Adapter) DisplayName() string { return Definition().Name }
func (p *Adapter) IsFree() bool        { return Definition().Free }
func (p *Adapter) FailureIsFree() bool { return Definition().FailureNoCharge }
