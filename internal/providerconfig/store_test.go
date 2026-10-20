package providerconfig

import (
	"bytes"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	cipher, err := NewCipher("a-long-local-provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"api_key":"secret"}`)
	encrypted, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext credential")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q", decrypted)
	}
}

func TestCipherRejectsShortSecret(t *testing.T) {
	if _, err := NewCipher("short"); err == nil {
		t.Fatal("expected short secret to be rejected")
	}
}
