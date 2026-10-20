package auth

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"strings"
	"testing"
)

func TestProfileContactsValidation(t *testing.T) {
	repo := &profileFixture{}
	manager := &Manager{repo: repo}
	for _, contacts := range [][]identity.Contact{
		{{Label: "", URL: "https://example.test"}},
		{{Label: strings.Repeat("é", 81), URL: "https://example.test"}},
		{{Label: "Line\nbreak", URL: "https://example.test"}},
		{{Label: "Unsafe", URL: "javascript:alert(1)"}},
		{{Label: "Unsafe", URL: "data:text/html,test"}},
		{{Label: "Unsafe", URL: "//example.test"}},
		{{Label: "Unsafe", URL: "https://user:pass@example.test"}},
		{{Label: "Unsafe", URL: "mailto:a@example.test?body=%0d%0aInjected"}},
		{{Label: "Empty", URL: "tel:"}},
		{{Label: "Too long", URL: "https://example.test/" + strings.Repeat("a", 2048)}},
		make([]identity.Contact, 21),
	} {
		if _, err := manager.UpdateProfile(context.Background(), "self", ProfileUpdate{Contacts: &contacts}); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("accepted %+v: %v", contacts, err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid contact reached repository")
	}
	for _, value := range []string{"https://t.me/person", "https://wa.me/123", "https://cal.example.test/meet", "mailto:a@example.test", "tel:+35712345678"} {
		contacts := []identity.Contact{{Label: " Contact ", URL: " " + value + " "}}
		if _, err := manager.UpdateProfile(context.Background(), "self", ProfileUpdate{Contacts: &contacts}); err != nil {
			t.Fatal(value, err)
		}
	}
	empty := []identity.Contact{}
	if _, err := manager.UpdateProfile(context.Background(), "self", ProfileUpdate{Contacts: &empty}); err != nil {
		t.Fatal(err)
	}
}
