package workspaceauth

import (
	"net"
	"net/url"
	"strconv"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

// CompanyAddress is display metadata, never installation or domain ownership.
// Keep the submitted origin unchanged. Legacy challenges may use non-loopback
// HTTP display origins even though trusted public configuration requires HTTPS.
func CompanyAddress(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ErrUnavailable
	}
	// Reuse the public-origin authority normalization without treating a legacy
	// HTTP address as a newly trusted HTTPS origin.
	scheme := u.Scheme
	u.Scheme = "https"
	normalized, err := installation.NormalizeOrigin(u.String())
	if err != nil {
		return "", ErrUnavailable
	}
	address, err := url.Parse(normalized)
	if err != nil {
		return "", ErrUnavailable
	}
	if scheme == "http" {
		port, _ := strconv.Atoi(u.Port()) // NormalizeOrigin already validated it.
		if port == 80 {
			address.Host = address.Hostname()
			if net.ParseIP(address.Host) != nil && net.ParseIP(address.Host).To4() == nil {
				address.Host = "[" + address.Host + "]"
			}
		} else if port == 443 {
			address.Host = net.JoinHostPort(address.Hostname(), "443")
		}
	}
	return address.Host, nil
}
