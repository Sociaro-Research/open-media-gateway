package providers

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func credentialOrigins(c Credentials) map[string]bool {
	result := map[string]bool{}
	urls := []string{c.BaseURL, c.MusicBaseURL}
	if c.Connection != nil {
		for _, endpoint := range c.Connection.Endpoints {
			urls = append(urls, endpoint)
		}
	}
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil && u.IsAbs() && u.Host != "" {
			result[strings.ToLower(u.Scheme+"://"+u.Host)] = true
		}
	}
	return result
}

// Hidden stored secrets must not be moved to a new receiver by someone who
// cannot supply them. Explicit replacement keeps custom multi-origin APIs usable.
func validateDestinationSecrets(d providerkit.Definition, old, next Credentials, input ProviderUpdate) error {
	oldOrigins := credentialOrigins(old)
	changed := false
	for origin := range credentialOrigins(next) {
		if !oldOrigins[origin] {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	fields := input.Fields()
	for _, field := range d.Fields {
		if !field.Secret || field.Group == "billing" || old.Get(field.Key) == "" || next.Get(field.Key) == "" {
			continue
		}
		if value, supplied := fields[field.Key]; !supplied || strings.TrimSpace(value) == "" {
			return errors.New("re-enter provider secrets when changing an API destination")
		}
	}
	if old.Connection != nil && next.Connection != nil {
		oldHeaders := map[string]bool{}
		for _, h := range old.Connection.Headers {
			if h.Value != nil && *h.Value != "" {
				oldHeaders[h.Operation+"/"+strings.ToLower(h.Name)] = true
			}
		}
		for _, h := range next.Connection.Headers {
			if oldHeaders[h.Operation+"/"+strings.ToLower(h.Name)] && h.Value == nil {
				return errors.New("re-enter saved header values when changing an API destination")
			}
		}
	}
	return nil
}
