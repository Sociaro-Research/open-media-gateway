package workspaceauth

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

func TestCompanyAddress(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, test := range []struct{ origin, want string }{
		{"https://example.test", "example.test"},
		{"https://EXAMPLE.test.:0443/", "example.test"},
		{"https://example.test:08443", "example.test:8443"},
		{"https://[2001:0db8::1]:8443", "[2001:db8::1]:8443"},
		{"https://[2001:db8::1]:443", "[2001:db8::1]"},
		{"https://bücher.example", "xn--bcher-kva.example"},
		{"https://" + long + ":65535", long + ":65535"},
		{"http://localhost:080", "localhost"},
		{"http://[::1]:080", "[::1]"},
		{"http://[::1]:0443", "[::1]:443"},
		{"http://same-display.lan:080", "same-display.lan"},
		{"http://same-display.lan:0443", "same-display.lan:443"},
		{"http://same-display.lan:8080", "same-display.lan:8080"},
	} {
		t.Run(test.origin, func(t *testing.T) {
			got, err := CompanyAddress(test.origin)
			if err != nil || got != test.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, origin := range []string{"", "host.test", "ftp://example.test", "https://user@example.test", "https://example.test/path", "https://example.test?query=1", "https://example.test#fragment", "https://example.test:", "https://example.test:65536", "https://" + strings.Repeat("a", 64) + ".test"} {
		if got, err := CompanyAddress(origin); err == nil {
			t.Fatal("invalid display origin", origin, got)
		}
	}
	if _, err := installation.NormalizeOrigin("http://same-display.lan"); err == nil {
		t.Fatal("display normalization broadened trusted-origin policy")
	}
}
