package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerationAdmissionPrivateReferenceBindingsStayPrivate(t *testing.T) {
	options := MediaOptions{ReferenceSources: []ReferenceBinding{{OwnerID: "private-owner", ReferenceID: "private-ref", StorageOwnerID: "private-data-owner", StorageKey: "private-physical-key", SHA256: "private-checksum", SizeBytes: 42}}}
	stored, e := json.Marshal(options)
	if e != nil || !strings.Contains(string(stored), "_reference_sources") {
		t.Fatal("durable retention evidence lost", e)
	}
	public, e := json.Marshal(GenerationView(Task{ID: "fixture", Options: options}))
	if e != nil {
		t.Fatal(e)
	}
	provider, e := json.Marshal(options.ProviderOptions())
	if e != nil {
		t.Fatal(e)
	}
	for _, raw := range [][]byte{public, provider} {
		for _, secret := range []string{"_reference_sources", "private-owner", "private-ref", "private-data-owner", "private-physical-key", "private-checksum"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private admission evidence exposed", secret, string(raw))
			}
		}
	}
}
