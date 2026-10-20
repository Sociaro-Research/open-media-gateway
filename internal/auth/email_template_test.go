package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountEmailTemplates(t *testing.T) {
	for _, tc := range []struct{ purpose, heading, ttl string }{{"verify", "Welcome", "1 hour"}, {"reset", "Reset your password", "30 minutes"}} {
		t.Run(tc.purpose, func(t *testing.T) {
			link := "https://example.test/#email_" + tc.purpose + "=" + strings.Repeat("a", 43)
			mail, err := renderAccountEmail(`customer+<tag>@example.test`, tc.purpose, link)
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{mail.HTML, mail.Text} {
				if !strings.Contains(body, tc.heading) || !strings.Contains(body, tc.ttl) || !strings.Contains(body, link) {
					t.Fatal("missing action, lifetime or link")
				}
			}
			if strings.Count(mail.HTML, `href="`+link+`"`) != 2 {
				t.Fatal("CTA and fallback must use the original token link")
			}
			if mail.HasLogo || strings.Contains(mail.HTML, "<img") || strings.Contains(mail.HTML, "cid:") || strings.Contains(mail.HTML, "#ZgotmplZ") {
				t.Fatal("unconfigured email contains a logo")
			}
			if strings.Contains(mail.HTML, "customer+<tag>") || !strings.Contains(mail.HTML, "&lt;tag&gt;") {
				t.Fatal("recipient was not escaped")
			}
			if len(mail.HTML) > 16000 {
				t.Fatal("unexpectedly large account email")
			}
		})
	}
	for _, purpose := range []string{"", "invite", "unknown"} {
		if _, err := renderAccountEmail("user@example.test", purpose, "https://example.test/#token=x"); err == nil {
			t.Fatal("unknown purpose silently became verification email")
		}
	}
	for _, link := range []string{"javascript:alert(1)", "//example.test", "https://user:password@example.test/"} {
		if _, err := renderAccountEmail("user@example.test", "verify", link); err == nil {
			t.Fatal("invalid action URL accepted")
		}
	}
}

// Local previews use exactly the delivered unbranded HTML.
// No real recipient, challenge or sending service is used.
func TestAccountEmailPreviews(t *testing.T) {
	dir := os.Getenv("GATEWAY_EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GATEWAY_EMAIL_PREVIEW_DIR to render local email previews")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	for _, purpose := range []string{"verify", "reset"} {
		mail, err := renderAccountEmail("alex@example.com", purpose, "https://example.test/#email_"+purpose+"="+strings.Repeat("preview", 7)+"0")
		if err != nil {
			t.Fatal(err)
		}
		html := mail.HTML
		for suffix, content := range map[string]string{".html": html, ".txt": mail.Text} {
			if err := os.WriteFile(filepath.Join(dir, purpose+suffix), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
