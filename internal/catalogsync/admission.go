package catalogsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// AdmissionFingerprint covers the applied model/rate contract, not warning or
// scheduling bookkeeping. The server computes it before preparing a new task.
func AdmissionFingerprint(state State) string {
	models := state.Models
	if models == nil {
		models = map[string]Entry{}
	}
	raw, _ := json.Marshal(struct {
		Version int64
		Models  map[string]Entry
	}{state.Version, models})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
