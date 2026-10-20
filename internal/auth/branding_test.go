package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/branding"
)

func TestBrandedAccountEmailPayload(t *testing.T) {
	s, err := NewResend("fixture-key", "Old Brand <login@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	b := branding.Defaults()
	b.Name = "New & <Brand>"
	b.Light["accent"] = "#226644"
	b.Light["bg"] = "#eeeeee"
	b.Assets["email_logo"] = branding.Asset{MIME: "image/png", Data: testEmailLogo(t)}
	s.brand = &b
	s.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			From, Subject, HTML, Text string
			Attachments               []map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.From, "login@example.test") || !strings.Contains(payload.Subject, b.Name) || !strings.Contains(payload.Text, b.Name) || !strings.Contains(payload.HTML, "New &amp; &lt;Brand&gt;") || !strings.Contains(payload.HTML, "#226644") || strings.Contains(payload.HTML, "ZgotmplZ") {
			t.Fatal("incorrect branded email", payload)
		}
		if len(payload.Attachments) != 1 {
			t.Fatal(payload.Attachments)
		}
		logo, err := base64.StdEncoding.DecodeString(payload.Attachments[0]["content"])
		if err != nil || !bytes.Equal(logo, testEmailLogo(t)) {
			t.Fatal("custom attachment missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"fixture"}`))}, nil
	})
	if err = s.Send(context.Background(), "user@example.test", "verify", "https://example.test/#email_verify=fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	b.Assets = map[string]branding.Asset{"logo": {MIME: "image/svg+xml", Data: []byte(`<svg/>`)}}
	mail, err := renderBrandedAccountEmail("user@example.test", "verify", "https://example.test/#email_verify=fixture", b)
	if err != nil || mail.HasLogo || strings.Contains(mail.HTML, "cid:") {
		t.Fatal("SVG-only brand should use a text email heading", mail, err)
	}
	if dir := os.Getenv("GATEWAY_EMAIL_PREVIEW_DIR"); dir != "" {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "branded.html"), []byte(mail.HTML), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAccountEmailUsesRasterLightLogo(t *testing.T) {
	s := branding.Defaults()
	s.Name = "New Brand"
	s.Assets["logo_light"] = branding.Asset{MIME: "image/png", Data: testEmailLogo(t)}
	logo, ok := accountBrandLogo(s)
	if !ok || !bytes.Equal(logo.Data, testEmailLogo(t)) {
		t.Fatal("light raster logo was not used")
	}
	mail, err := renderBrandedAccountEmail("user@example.test", "verify", "https://example.test/#email_verify=x", s)
	if err != nil || !mail.HasLogo {
		t.Fatal("light logo omitted from HTML", err)
	}
	s.Assets["email_logo"] = branding.Asset{MIME: "image/jpeg", Data: []byte("explicit-raster")}
	logo, ok = accountBrandLogo(s)
	if !ok || logo.MIME != "image/jpeg" {
		t.Fatal("explicit email logo lost priority")
	}
	delete(s.Assets, "email_logo")
	s.Assets["logo_light"] = branding.Asset{MIME: "image/svg+xml", Data: []byte(`<svg/>`)}
	if _, ok = accountBrandLogo(s); ok {
		t.Fatal("SVG-only brand must use site-name fallback")
	}
}

// A generated neutral fixture keeps tests independent of installation assets.
func testEmailLogo(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
