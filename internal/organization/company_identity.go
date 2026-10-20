package organization

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

func CompanyName(name string) string { return strings.Join(strings.Fields(name), " ") }

// CompanyDomain identifies the website by hostname, without www, scheme or page path.
// Validation is local; creating a company never fetches the supplied website.
func CompanyDomain(website string) (string, error) {
	raw := strings.TrimSpace(website)
	if raw == "" || len(raw) > 2048 {
		return "", ErrInvalid
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
		return "", ErrInvalid
	}
	host, e := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")))
	if e != nil {
		return "", ErrInvalid
	}
	host = strings.TrimPrefix(host, "www.")
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return "", ErrInvalid
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return "", ErrInvalid
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", ErrInvalid
			}
		}
	}
	return host, nil
}
