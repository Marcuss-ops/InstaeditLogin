// Package auth / jwt_random.go — hex helpers.
//
// randomHex + the exported RandomHex alias used by JWT JTI
// generation and by callers (e.g. SessionsService). Extracted from
// jwt.go (split per responsabilità, 2026-08).
package auth

import (
	"crypto/rand"
	"encoding/hex"
)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandomHex is the exported alias used by callers (e.g. SessionsService)
// that need to generate opaque identifiers without importing crypto
// packages directly.
func RandomHex(n int) (string, error) { return randomHex(n) }
