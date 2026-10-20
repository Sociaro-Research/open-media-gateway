package auth

import (
	"testing"
	"time"
)

func TestOAuthStateIsSignedAndExpires(t *testing.T) {
	manager := &Manager{signingKey: []byte("a-long-test-signing-secret-value")}
	want := statePayload{State: "state", Nonce: "nonce", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	sealed, err := manager.sealState(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manager.openState(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	if _, err := manager.openState(sealed + "tampered"); err == nil {
		t.Fatal("tampered OAuth state was accepted")
	}
}
