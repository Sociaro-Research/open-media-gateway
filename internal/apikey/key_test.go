package apikey

import (
	"strings"
	"testing"
	"time"
)

// GW-352: a key name must show something and must not reverse the text around it.
func TestKeyNameMustBeVisible(t *testing.T) {
	for _, name := range []string{" ", "\u200b", "\u200f", "\u202eQA"} {
		if _, _, err := Generate(CreateInput{Name: name}, time.Now()); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{"Production bot", "Ключ 项目 🙂"} {
		if _, _, err := Generate(CreateInput{Name: name}, time.Now()); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestRandomSecretsAndNormalizedMetadata(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		key, secret, err := Generate(CreateInput{Name: "  Production bot  ", Scopes: []string{Generation, Generation, KeysManage}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(secret) != 46 || !strings.HasPrefix(secret, "gw_") || seen[secret] {
			t.Fatal("invalid or repeated secret")
		}
		seen[secret] = true
		if key.Name != "Production bot" || len(key.Scopes) != 2 || key.Prefix != secret[:11] || len(Hash(secret)) != 32 {
			t.Fatal("invalid metadata")
		}
	}
}
