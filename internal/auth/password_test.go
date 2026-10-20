package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashAndValidation(t *testing.T) {
	password := "A long Unicode passphrase 🔐"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := hashPassword(password)
	if hash == other || !checkPassword(password, hash) || checkPassword(password+"wrong", hash) {
		t.Fatal("password verification or random salt failed")
	}
	for _, invalid := range []string{"short", strings.Repeat("a", 129), string([]byte{255, 255})} {
		if validPassword(invalid) {
			t.Fatal("accepted invalid password")
		}
	}
	if !validPassword(password) || !validPassword(strings.Repeat("é", 128)) {
		t.Fatal("rejected Unicode passphrase")
	}
	for _, bad := range []string{"plaintext", hash + "bad", strings.Replace(hash, "m=19456", "m=99999999", 1)} {
		if checkPassword(password, bad) {
			t.Fatal("accepted malformed hash")
		}
	}
}
