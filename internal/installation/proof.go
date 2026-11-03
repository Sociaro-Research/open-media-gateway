package installation

import "crypto/sha256"

// ProofDigest is the existing durable installation identity domain. Both legacy
// generation-only onboarding and company-interface approval must use these exact
// bytes, so a new purpose cannot silently provision a second company.
func ProofDigest(proof string) []byte {
	sum := sha256.Sum256([]byte("installation:" + proof))
	return sum[:]
}
