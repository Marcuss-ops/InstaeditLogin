// API key generation, hashing, and prefix parsing.
//
// Package auth is the natural home for these helpers because the
// JWT middleware already lives here and the Bearer middleware (commit
// 3) will chain: API-key detection → SHA-256 hash → repository
// lookup → context injection. Keeping the helpers in package auth
// means the middleware file can stay focused on HTTP wiring without
// pulling in crypto/* internally.
//
// Threat model — what Generate / Hash defend against:
//
//   * Database compromise: an attacker with read access to api_keys
//     cannot replay a plaintext key because we never store one. They
//     can attempt an offline SHA-256 brute-force on the hash, but
//     the 32-byte (256-bit) secret is exactly 256 bits of entropy
//     (bijective under base32, where 5 bits per char × 52 chars =
//     260 bits of encoding headroom over 256 bits of input) —
//     brute-forcing the search space is computationally infeasible.
//
//   * Log leakage: the helper here returns the full plaintext key
//     ONLY via Generate. Callers MUST NOT log the returned value
//     anywhere — the dashboard SPA is the only legitimate recipient.
//     The repository layer's Create method takes the hash, not the
//     plaintext.
//
//   * Prefix forgery: an attacker who knows a valid key_prefix
//     (visible in the dashboard) cannot recover the secret because
//     the visible prefix is 8 chars of a 52-char base32 secret —
//     44 chars (~216 bits of the underlying 256-bit random source)
//     remain unrevealed.
//
//   * Mistaken-tenant collisions: two orgs rotating keys in the same
//     second could (with astronomically low probability) generate
//     identical secrets. The key_hash UNIQUE constraint forces a
//     conflict error before insertion; the caller retries.

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Pre-key environment tokens. These are the literal byte sequences
// expected at the start of every plaintext API key. They are
// exported so the Bearer middleware can detect "Authorization: Bearer
// sk_*" without parsing the env a second time.
const (
	KeyPrefixTest = "sk_test_"
	KeyPrefixLive = "sk_live_"
)

// KeySecretBytes is the byte length of the random secret we generate
// per key. 32 bytes (256 bits) at the source → 52 base32 chars
// without padding after encoding. This is the value the entropy
// argument rests on; do NOT lower it without re-running the threat
// model above.
const KeySecretBytes = 32

// KeyVisiblePrefixExtraChars is the number of secret characters
// appended to the env token to form the dashboard-visible key_prefix.
// 8 chars of a 52-char secret = (52-8) = 44 chars of unrevealed
// secret, equivalent to ~216 bits of the 256-bit source (base32
// encodes 5 bits per character) — well past any plausible brute-force
// threshold for an attacker who has read the key_hash column.
const KeyVisiblePrefixExtraChars = 8

// ErrMalformedApiKey is returned by ParseFullKey when the input
// doesn't match the sk_test_<secret> or sk_live_<secret> shape.
// The middleware maps this to HTTP 401 with the static message
// "invalid api key" so attackers can't enumerate "almost right".
var ErrMalformedApiKey = errors.New("malformed API key")

// Generate returns a fresh plaintext key + the visible prefix to
// store on the api_keys row. Format:
//
//	sk_test_<52-char-base32>   (test environment)
//	sk_live_<52-char-base32>   (live environment)
//
// The secret is 32 bytes from crypto/rand, encoded lowercase
// base32 (StdEncoding without padding) → 52 chars. Caller MUST:
//  1. Save the FULL plaintext through Create (the result is the
//     "shown once" response body). The repository layer is
//     responsible for hashing before persistence.
//  2. Save the keyPrefix on the api_keys row (for dashboard display
//     and audit logs).
//  3. Never log the plaintext. Slog calls on the result of Generate
//     are forbidden — the API layer is the only legitimate carrier.
//
// env must be exactly models.ApiKeyEnvironmentTest or
// models.ApiKeyEnvironmentLive. Other values are rejected with
// a descriptive error so a future "staging" entry point doesn't
// accidentally mint a key with a confusing env token.
func Generate(env string) (fullKey string, keyPrefix string, err error) {
	if !models.IsApiKeyEnvironment(env) {
		return "", "", fmt.Errorf("invalid environment %q (must be %q or %q)",
			env, models.ApiKeyEnvironmentTest, models.ApiKeyEnvironmentLive)
	}
	b := make([]byte, KeySecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	// base32 lowercase, no padding. StdEncoding.EncodeToString emits
	// uppercase; we fold to lowercase so the env token and secret have
	// a consistent case (sk_test_aB3xY9K2..., not sk_test_AB3X...).
	secret := strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
	envPrefix := KeyPrefixTest
	if env == models.ApiKeyEnvironmentLive {
		envPrefix = KeyPrefixLive
	}
	full := envPrefix + secret
	// Visible prefix for the dashboard: env token + 8 chars of secret
	// → "sk_test_aB3xY9K2" (16 chars). Long enough to be unique in
	// UI lists, short enough not to leak enough entropy to be useful
	// to a brute-forcer.
	return full, full[:len(envPrefix)+KeyVisiblePrefixExtraChars], nil
}

// Hash returns the 32-byte SHA-256 of plaintext as a slice. Stable
// across processes (no salting, no peppering) so the same input
// always maps to the same output — required for the lookup-time
// equality check `SELECT ... WHERE key_hash = $1` in the middleware.
//
// Returns nil for empty plaintext. We don't want a silent
// "" → 0xe3b0c4… mapping; an empty key is always a misuse. In
// practice, ParseFullKey rejects any input without the sk_test_ /
// sk_live_ prefix before Hash is ever called — but if the chain
// ever loosens, nil is the safe sentinel that will not match any
// stored row (no api_key row can have key_hash = NULL because the
// column is NOT NULL).
func Hash(plaintext string) []byte {
	if plaintext == "" {
		return nil
	}
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// ParseFullKey takes the raw Authorization Bearer value (without
// the "Bearer " prefix) and returns (env, secret).
//
//   - sk_test_… → ("test", "<secret>")
//   - sk_live_… → ("live", "<secret>")
//   - anything else → ("", "", ErrMalformedApiKey)
//
// Trims surrounding whitespace so a key copied with a trailing
// newline from a terminal still validates; the case-sensitivity
// is preserved (we don't fold so a typo case-mismatch is caught
// as ErrMalformedApiKey rather than silently becoming live or test).
func ParseFullKey(raw string) (env string, secret string, err error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, KeyPrefixTest):
		return models.ApiKeyEnvironmentTest, raw[len(KeyPrefixTest):], nil
	case strings.HasPrefix(raw, KeyPrefixLive):
		return models.ApiKeyEnvironmentLive, raw[len(KeyPrefixLive):], nil
	default:
		return "", "", ErrMalformedApiKey
	}
}

// IsApiKeyBearer reports whether the raw Authorization header value
// (after the "Bearer " prefix has already been stripped) looks like
// an API key rather than a JWT. Fast string-prefix check, no parse.
// The full middleware chain (commit 3) uses this to dispatch to
// either the API-key path or the JWT path.
func IsApiKeyBearer(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), KeyPrefixTest) ||
		strings.HasPrefix(strings.TrimSpace(raw), KeyPrefixLive)
}
