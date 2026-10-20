package auth

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"html/template"
	"net/url"
)

// One shell is used for every account email; purpose-specific copy stays here.
// All dynamic values are escaped by html/template. The logo CID is constant,
// never derived from a caller-controlled URL or user-supplied HTML.
//
//go:embed templates/account.html
var accountEmailFiles embed.FS
var accountEmailTemplate = template.Must(template.ParseFS(accountEmailFiles, "templates/account.html"))

const accountEmailLogoCID = "brand-logo"

type accountEmailCopy struct {
	Subject, Preheader, Heading, Intro, Action, Expiry, Reassurance string
	Recipient, Link                                                 string
	Logo                                                            template.URL
	Name                                                            string
	Palette                                                         map[string]string
	HasLogo                                                         bool
}
type renderedAccountEmail struct {
	Subject, HTML, Text string
	HasLogo             bool
}

func renderAccountEmail(to, purpose, link string) (renderedAccountEmail, error) {
	return renderBrandedAccountEmail(to, purpose, link, branding.Defaults())
}
func renderBrandedAccountEmail(to, purpose, link string, settings branding.Settings) (renderedAccountEmail, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return renderedAccountEmail{}, errors.New("invalid account email link")
	}
	_, hasLogo := accountBrandLogo(settings)
	c := accountEmailCopy{Recipient: to, Link: link, Logo: template.URL("cid:" + accountEmailLogoCID), Name: settings.Name, Palette: settings.Light, HasLogo: hasLogo}
	switch purpose {
	case "verify":
		c.Subject = "Confirm your email"
		if settings.Name != "" {
			c.Subject += " for " + settings.Name
		}
		c.Preheader = "Confirm your email to finish signing up."
		c.Heading = "Welcome"
		if settings.Name != "" {
			c.Heading += " to " + settings.Name
		}
		c.Intro = "Thank you for signing up. Please confirm your email address below, then choose a password to complete your account."
		c.Action = "Confirm email"
		c.Expiry = "This one-time link is valid for 1 hour."
		c.Reassurance = "If you didn’t sign up, you can safely ignore this email."
	case "reset":
		c.Subject = "Reset your password"
		if settings.Name != "" {
			c.Subject = "Reset your " + settings.Name + " password"
		}
		c.Preheader = "A link to help you get back into your account."
		c.Heading = "Reset your password"
		c.Intro = "Hello! Please use the button below to choose a new password. You can then sign in with it."
		c.Action = "Reset password"
		c.Expiry = "This one-time link is valid for 30 minutes."
		c.Reassurance = "If you didn’t request this, you can safely ignore this email. Your password will stay the same."
	default:
		return renderedAccountEmail{}, errors.New("unsupported account email purpose")
	}
	var out bytes.Buffer
	if err = accountEmailTemplate.Execute(&out, c); err != nil {
		return renderedAccountEmail{}, errors.New("account email could not be rendered")
	}
	text := fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s\n\n%s\n\nSent to %s\n", c.Heading, c.Intro, c.Link, c.Expiry, c.Reassurance, c.Recipient)
	return renderedAccountEmail{Subject: c.Subject, HTML: out.String(), Text: text, HasLogo: hasLogo}, nil
}

// Account mail has a fixed light canvas; choose a compatible raster once for
// both the HTML and its attachment so their fallbacks cannot diverge.
func accountBrandLogo(settings branding.Settings) (branding.Asset, bool) {
	if a, ok := settings.Assets["email_logo"]; ok {
		return a, true
	}
	for _, key := range []string{"logo_light", "logo"} {
		if a, ok := settings.Assets[key]; ok && (a.MIME == "image/png" || a.MIME == "image/jpeg") {
			return a, true
		}
	}
	return branding.Asset{}, false
}
