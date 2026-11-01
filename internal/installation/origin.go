// Package installation derives deployment role exclusively from trusted public configuration.
package installation

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// DefaultCentralOrigin is public release configuration, not an environment role flag.
const DefaultCentralOrigin = "https://console.sociaro.ai"

type Metadata struct {
	Role          string `json:"installation_role"`
	CentralOrigin string `json:"central_origin"`
}
type metadataKey struct{}

func FromContext(ctx context.Context) Metadata {
	if value, ok := ctx.Value(metadataKey{}).(Metadata); ok {
		return value
	}
	return Metadata{Role: "ordinary", CentralOrigin: DefaultCentralOrigin}
}
func WithContext(ctx context.Context, value Metadata) context.Context {
	return context.WithValue(ctx, metadataKey{}, value)
}

// NormalizeOrigin validates an origin, never a request Host or forwarded header.
// HTTP is accepted only for the existing loopback development configuration.
func NormalizeOrigin(raw string) (string, error) {
	invalid := func() (string, error) {
		return "", fmt.Errorf("public URL must be an HTTPS origin; HTTP is allowed only on localhost")
	}
	if raw == "" || raw != strings.TrimSpace(raw) || strings.Contains(raw, "#") {
		return invalid()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return invalid()
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, "..") {
		return invalid()
	}
	host = strings.TrimSuffix(host, ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || len(host) == 0 || len(host) > 253 {
			return invalid()
		}
		for _, part := range strings.Split(host, ".") {
			if len(part) == 0 || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
				return invalid()
			}
		}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return invalid()
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return invalid()
	}
	if port != "" {
		number, e := strconv.Atoi(port)
		if e != nil || number < 1 || number > 65535 {
			return invalid()
		}
		port = strconv.Itoa(number)
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}
func Describe(ownOrigin string) (Metadata, error) {
	value := Metadata{Role: "ordinary", CentralOrigin: DefaultCentralOrigin}
	if ownOrigin == "" {
		return value, nil
	}
	own, err := NormalizeOrigin(ownOrigin)
	if err != nil {
		return value, err
	}
	central, err := NormalizeOrigin(DefaultCentralOrigin)
	if err != nil {
		return value, err
	}
	value.CentralOrigin = central
	if own == central {
		value.Role = "central"
	}
	return value, nil
}
