package worker

import (
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// verifySHAConstantTime performs a constant-time equality check on
// two hex-encoded SHA-256 strings. Returns nil on match, otherwise
// a PermanentError{Code: ARTIFACT_SHA256_MISMATCH} with the actual
// vs expectedSHA message detail operators need to triage.
//
// The hex decode + byte comparison is preferred over direct string
// compare to:
//
//   - tolerate upstream-mismatched casing (canonical form is
//     lowercase hex from sha256.Sum256().hex.EncodeToString() but
//     upstream MAY submit uppercase)
//   - constant-time the BYTE comparison (subtle.ConstantTimeCompare);
//     string compare is constant-time per-byte too but the hex
//     decode path is more idiomatic in this codebase (date format
//     is lowercase hex everywhere else — credential fingerprints,
//     request SHA, etc.)
func verifySHAConstantTime(expectedHex, actualHex string) error {
	expected, err := hexDecodeStrictLower(expectedHex)
	if err != nil {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("expected sha hex parse: %v", err),
		}
	}
	actual, err := hexDecodeStrictLower(actualHex)
	if err != nil {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("actual sha hex parse: %v", err),
		}
	}
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("sha mismatch (expected %s, got %s)", expectedHex, actualHex),
		}
	}
	return nil
}

// verifySizeExact checks the streamed byte count against the
// upstream-declared expected size. Strict equality (==), not "≤".
// A SHORT body means the upstream truncated mid-stream (it broke
// its own contract) and is just as permanent as an over-size body.
func verifySizeExact(actual, expected int64) error {
	if actual != expected {
		return PermanentError{
			Code: CodeArtifactSizeMismatch,
			Message: fmt.Sprintf(
				"size mismatch (expected exactly %d bytes, got %d)",
				expected, actual,
			),
		}
	}
	return nil
}

// hexDecodeStrictLower decodes 64-char lowercase hex into 32 bytes.
// The hex package's stdlib DecodeString already accepts upper or
// lower case, but we ALSO reject any non-hex character BEFORE
// calling DecodeString so the error message is precise (not the
// stdlib's "invalid hex character") — the worker logs that message
// directly to operators and the precision helps triage data-entry
// bugs upstream.
//
// We do NOT use bytes.Equal — subtle.ConstantTimeCompare is the
// crypto-safe choice here even though the compared values are
// 32-byte public hashes: defense-in-depth against timing attacks
// would matter if the expected sha ever came from an untrusted
// source. Today it's the external_delivery row at handoff time,
// which IS user-controlled but bounded by the 64-hex regex.
func hexDecodeStrictLower(s string) ([]byte, error) {
	if len(s) != 64 {
		return nil, fmt.Errorf("sha256 hex must be 64 chars (got %d)", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return nil, fmt.Errorf("sha256 hex must be lowercase (got char %q at pos %d)", c, i)
		}
	}
	out, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("sha256 hex decode: %w", err)
	}
	return out, nil
}
