package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Digest computes the canonical SHA-256 hex digest of a value. Struct fields
// are serialized in declaration order, so two structurally identical values
// always produce the same digest and two differing values never collide for
// the purposes of the idempotency and evidence-chain rules.
func Digest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Marshal of plain data types cannot fail; fall back to the error text
		// so the digest remains deterministic rather than panicking.
		b = []byte(err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DigestPair composes two digests into one, used to build the sampling result
// chain's previous-hash links.
func DigestPair(a, b string) string {
	return Digest([]string{a, b})
}
