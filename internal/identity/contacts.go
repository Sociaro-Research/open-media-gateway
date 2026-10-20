package identity

import (
	"net/url"
	"strings"
	"unicode"
)

// Contact is an opt-in, shared way to reach a person, never a login credential.
type Contact struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func ValidContactURL(value string) bool {
	if value == "" || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Hostname() != "" && u.Opaque == ""
	case "mailto", "tel":
		return u.Opaque != "" && !strings.ContainsAny(strings.ToLower(value), "\r\n") && !strings.Contains(strings.ToLower(value), "%0a") && !strings.Contains(strings.ToLower(value), "%0d")
	}
	return false
}
