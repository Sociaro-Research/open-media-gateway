package payments

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCompanyCheckoutReturnUsesOnlyConfiguredCentralCallback(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	target, err := CompanyCheckoutReturn("https://console.sociaro.ai", state)
	if err != nil {
		t.Fatal(err)
	}
	success, cancel, err := target.URLs()
	want := "https://console.sociaro.ai" + CompanyPaymentReturnPath + "?state=" + state
	if err != nil || success != want || cancel != want {
		t.Fatal(success, cancel, err)
	}
	for _, origin := range []string{"", "http://console.sociaro.ai", "https://console.sociaro.ai/", "https://console.sociaro.ai/other", "https://user@console.sociaro.ai", "https://console.sociaro.ai?return_url=https://evil.invalid", "https://console.sociaro.ai#fragment", " https://console.sociaro.ai"} {
		if _, err := CompanyCheckoutReturn(origin, state); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted origin %q: %v", origin, err)
		}
	}
	for _, nonce := range []string{"", state + "=", state[:42], state + "x", state + "&success=1", state + "\n", strings.Repeat("_", 43)} {
		if _, err := CompanyCheckoutReturn("https://console.sociaro.ai", nonce); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted state %q: %v", nonce, err)
		}
	}
	var forged CheckoutReturn
	if err := json.Unmarshal([]byte(`{"destination":"https://evil.invalid","success":"https://evil.invalid"}`), &forged); err != nil {
		t.Fatal(err)
	}
	if _, _, err := forged.URLs(); !errors.Is(err, ErrInvalid) {
		t.Fatal("HTTP JSON populated private target", err)
	}
	raw, _ := json.Marshal(target)
	if string(raw) != "{}" {
		t.Fatal("target exposed through JSON", string(raw))
	}
}
