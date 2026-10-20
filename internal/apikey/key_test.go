package apikey

import (
	"strings"
	"testing"
	"time"
)

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
