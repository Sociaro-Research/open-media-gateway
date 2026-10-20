package webhookurl

import (
	"net/netip"
	"testing"
)

func TestNonPublicDestinations(t *testing.T) {
	for _, raw := range []string{
		"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.100.100.200", "127.0.0.1",
		"168.63.129.16", "169.254.169.254", "172.16.0.1", "192.168.1.1", "192.0.0.1",
		"192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1",
		"224.0.0.1", "240.0.0.1", "255.255.255.255", "::", "::1", "fc00::1",
		"fe80::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "2001:db8::1",
		"64:ff9b::a00:1", "64:ff9b:1::1", "2002:7f00:1::1", "2001::1", "3fff::1",
	} {
		t.Run(raw, func(t *testing.T) {
			ip := netip.MustParseAddr(raw)
			if PublicIP(ip) {
				t.Fatal("non-public IP allowed")
			}
			host := raw
			if ip.Is6() {
				host = "[" + raw + "]"
			}
			if Validate("https://"+host+"/hook") == nil {
				t.Fatal("non-public URL allowed")
			}
		})
	}
	for _, raw := range []string{"https://localhost/a", "http://LOCALHOST.:80", "http://foo.localhost", "https://[fe80::1%25en0]", "file:///etc/passwd", "http://user:pass@example.com", "http://:80", "http://example.com:0", "https://example.com:65536"} {
		if Validate(raw) == nil {
			t.Errorf("invalid URL allowed: %s", raw)
		}
	}
}

func TestPublicDestinations(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !PublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("public IP blocked: %s", raw)
		}
	}
	for _, raw := range []string{"https://hooks.example.com/events", "http://hooks.example.com:8080/events?token=abc", "https://[2606:4700:4700::1111]/events"} {
		if err := Validate(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
