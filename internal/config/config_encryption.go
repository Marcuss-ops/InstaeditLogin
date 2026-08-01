package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// resolveEncryptionConfig normalises the two encryption surfaces
// (legacy single ENCRYPTION_KEY vs multi-key ENCRYPTION_KEYS +
// ACTIVE_ENCRYPTION_KEY_ID) into the unified EncryptionKeys map +
// ActiveEncryptionKeyID uint32 fields. It also rejects the
// ambiguous "both set" case and the "neither set" case.
//
// The matrix of valid inputs:
//
//	ENCRYPTION_KEY only             → EncryptionKeys={1: key}, ActiveID=1
//	ENCRYPTION_KEYS + ACTIVE_*      → parsed CSV, validated, verified
//	ENCRYPTION_KEY  + ENCRYPTION_KEYS → rejected (ambiguous)
//	(neither)                       → rejected (required)
//
// The CSV format is `id:base64key,id:base64key,...`. Each entry
// must be valid base64 and decode to exactly 32 bytes (AES-256).
// Duplicate ids in the CSV are rejected (would silently overwrite
// in a map literal, defeating the rotation audit log).
//
// On success, the post-validated fields are c.EncryptionKeys and
// c.ActiveEncryptionKeyID. The legacy c.EncryptionKey field is
// preserved (NOT cleared) so the rest of the codebase that still
// reads it for telemetry/diagnostics continues to work.
func (c *Config) resolveEncryptionConfig() error {
	hasLegacy := c.EncryptionKey != ""
	hasMulti := c.EncryptionKeysRaw != ""

	switch {
	case hasLegacy && hasMulti:
		return fmt.Errorf("ambiguous encryption config: set EITHER ENCRYPTION_KEY OR ENCRYPTION_KEYS+ACTIVE_ENCRYPTION_KEY_ID, not both")
	case !hasLegacy && !hasMulti:
		return fmt.Errorf("ENCRYPTION_KEY is required (or set ENCRYPTION_KEYS+ACTIVE_ENCRYPTION_KEY_ID for multi-key mode)")
	case hasLegacy:
		// Legacy single-key path: promote the single key into the
		// unified map under id=1. Re-validate the same shape the
		// pre-Blocco #2.2 validate() did (base64 + 32 bytes) so
		// operators get the same error messages.
		if err := validateSingleBase64Key(c.EncryptionKey); err != nil {
			return fmt.Errorf("ENCRYPTION_KEY: %w", err)
		}
		c.EncryptionKeys = map[uint32]string{1: c.EncryptionKey}
		c.ActiveEncryptionKeyID = 1
		return nil
	default: // hasMulti
		keys, err := parseEncryptionKeysCSV(c.EncryptionKeysRaw)
		if err != nil {
			return fmt.Errorf("ENCRYPTION_KEYS: %w", err)
		}
		active, err := parseActiveKeyID(c.ActiveEncryptionKeyIDRaw)
		if err != nil {
			return fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID: %w", err)
		}
		if _, ok := keys[active]; !ok {
			return fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID=%d not in ENCRYPTION_KEYS (have %v)", active, SortedKeyIDs(keys))
		}
		c.EncryptionKeys = keys
		c.ActiveEncryptionKeyID = active
		return nil
	}
}

// validateSingleBase64Key checks a single key string is valid
// base64 and decodes to exactly 32 bytes (AES-256). Extracted so
// the legacy path and the multi-key path share one source of
// truth for the length check (so a future "AES-128" support
// change touches one function, not two).
//
// Error-message contract (Blocco #2.2 follow-up): the test suite
// (TestValidate_EncryptionKeyLength) pins the operator-facing
// shape: the error MUST contain both the actual length ("got N")
// AND the expected shape ("exactly 32 bytes"). Changing either
// substring is a contract change that requires updating the
// tests in the same commit.
func validateSingleBase64Key(s string) error {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("not valid base64: %w", err)
	}
	if len(raw) != aesKeyBytes {
		return fmt.Errorf("must be exactly 32 bytes (got %d)", len(raw))
	}
	return nil
}

// parseEncryptionKeysCSV parses the ENCRYPTION_KEYS env var
// ("id:base64key,id:base64key,...") into a map[uint32]string with
// every key validated (base64 + 32 bytes). Duplicate ids are
// rejected so an operator typo (e.g. "1:key1,1:key2") doesn't
// silently overwrite the first entry — that would defeat the
// rotation audit log.
func parseEncryptionKeysCSV(s string) (map[uint32]string, error) {
	out := make(map[uint32]string)
	for i, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("entry %d is empty (trailing comma or extra whitespace?)", i+1)
		}
		colon := strings.IndexByte(entry, ':')
		if colon <= 0 || colon == len(entry)-1 {
			return nil, fmt.Errorf("entry %d (%q) must be in the form 'id:base64key'", i+1, entry)
		}
		idStr := strings.TrimSpace(entry[:colon])
		keyStr := strings.TrimSpace(entry[colon+1:])
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("entry %d: key id %q is not a uint32: %w", i+1, idStr, err)
		}
		if err := validateSingleBase64Key(keyStr); err != nil {
			return nil, fmt.Errorf("entry %d (id=%d): %w", i+1, uint32(id), err)
		}
		if _, dup := out[uint32(id)]; dup {
			return nil, fmt.Errorf("entry %d: duplicate key id %d in ENCRYPTION_KEYS", i+1, id)
		}
		out[uint32(id)] = keyStr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ENCRYPTION_KEYS is empty (set at least one id:base64key entry)")
	}
	return out, nil
}

// parseActiveKeyID parses the ACTIVE_ENCRYPTION_KEY_ID env var.
// Empty is rejected (the operator must pick a key — silently
// defaulting to 1 would re-introduce a class of "I forgot to set
// the active id" bugs).
func parseActiveKeyID(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID is required when ENCRYPTION_KEYS is set")
	}
	id, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a uint32: %w", s, err)
	}
	return uint32(id), nil
}

// SortedKeyIDs returns the key ids in ascending order, exported so
// internal/bootstrap can log them at startup as a diagnostic
// breadcrumb (operators can confirm "the active id is 2, and the
// key map has ids 1 and 2" by reading the boot log).
func SortedKeyIDs(m map[uint32]string) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
