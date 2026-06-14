package rpcidem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalRequestHash normalizes v to key-sorted JSON and returns its SHA-256
// hash. Callers must zero idempotency keys before calling so retry keys are not
// part of the semantic request identity.
func CanonicalRequestHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var norm any
	if err := json.Unmarshal(b, &norm); err != nil {
		return ""
	}
	nb, err := json.Marshal(norm)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(nb)
	return "sha256:" + hex.EncodeToString(sum[:])
}
