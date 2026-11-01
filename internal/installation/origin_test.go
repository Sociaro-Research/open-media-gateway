package installation

import (
	"context"
	"testing"
)

func TestTrustedOriginRoleBoundaries(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://console.sociaro.ai", "central"}, {"https://CONSOLE.SOCIARO.AI:443/", "central"}, {"https://console.sociaro.ai.:0443/", "central"},
		{"https://console.sociaro.ai:8443", "ordinary"}, {"https://console.sociaro.ai.attacker.invalid", "ordinary"}, {"https://private.fixture.test", "ordinary"}, {"", "ordinary"}, {"http://localhost:8088/", "ordinary"}, {"https://[::1]:443/", "ordinary"},
	} {
		got, err := Describe(tc.raw)
		if err != nil || got.Role != tc.want || got.CentralOrigin != DefaultCentralOrigin {
			t.Fatalf("role %q: %+v %v", tc.raw, got, err)
		}
	}
	for _, raw := range []string{"not a URL", "http://console.sociaro.ai", "https://user:password@console.sociaro.ai", "https://console.sociaro.ai/path", "https://console.sociaro.ai/?q=1", "https://console.sociaro.ai/?", "https://console.sociaro.ai/#", "https://console.sociaro.ai/#fragment", " https://console.sociaro.ai", "https://console.sociaro.ai:", "https://console.sociaro.ai:0", "https://console.sociaro.ai:65536", "https://console.sociaro.ai../"} {
		got, err := Describe(raw)
		if err == nil || got.Role == "central" {
			t.Fatal("invalid configuration granted a role")
		}
	}
	if FromContext(context.Background()).Role != "ordinary" {
		t.Fatal("missing metadata granted central role")
	}
}
