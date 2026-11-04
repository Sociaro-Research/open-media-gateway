package core

// ReferenceBinding is server-resolved admission/retention evidence, never a
// client capability. Provider payloads and public task views must exclude it.
type ReferenceBinding struct {
	OwnerID        string `json:"owner_id"`
	ReferenceID    string `json:"reference_id"`
	StorageOwnerID string `json:"storage_owner_id"`
	StorageKey     string `json:"storage_key"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
}
