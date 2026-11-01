package workspaceauth

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDeviceProofAndPublicProjection(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		secret, err := DeviceCode()
		if err != nil || !ValidDevice(secret) || seen[secret] {
			t.Fatal("invalid or repeated random proof", err)
		}
		seen[secret] = true
		code := UserCode(secret)
		if len(code) != 13 || code == secret || NormalizeCode(code[:4]+"-"+code[4:]) != code {
			t.Fatal("invalid user code")
		}
	}
	for _, invalid := range []string{"", "known", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="} {
		if ValidDevice(invalid) {
			t.Fatal("invalid device accepted")
		}
	}
	public, err := json.Marshal(Challenge{Code: "USERCODE", Actor: "private-actor", TokenCipher: []byte("private-cipher")})
	if err != nil || bytes.Contains(public, []byte("private-")) {
		t.Fatal("private fields in consent response")
	}
}
