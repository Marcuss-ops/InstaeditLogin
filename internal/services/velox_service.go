package services

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// GenerateVeloxDestinationID mints a unique opaque ULID-shaped id
// for external_destinations.id. Strategy mirrors GenerateVeloxDeliveryID
// with a different prefix: 7-char prefix ("extdst_") + 3-char ULID
// legacy timestamp segment ("01J") + 16 bytes (128 bits) of
// crypto-rand encoded as 26-char base32 (StdEncoding without padding).
// Total = 36 chars.
//
// Used by the user-facing POST
// /api/v1/integrations/velox/destinations (Phase 2). The
// repository's Create method stores the byte payload verbatim;
// callers consume the opaque id as a stable reference.
//
// Returns (id, error). Errors only occur on crypto/rand.Read
// failures (extremely rare; usually means the OS entropy source
// is broken — fatal at boot, but defensive here so the handler
// returns 500 instead of panicking).
func GenerateVeloxDestinationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("velox destination id mint: rand.Read: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "extdst_01J" + strings.ToLower(encoded), nil
}

// GenerateVeloxDeliveryID mints a unique opaque ULID-shaped id
// for the social_delivery_id column. Strategy: 5-char prefix
// ("sdel_") + 3-char ULID legacy timestamp segment ("01J")
// + 16 bytes (128 bits) of crypto-rand encoded as 26-char
// base32 (StdEncoding without padding). Total = 34 chars.
//
// NOT a true ULID — the "01J" segment is a fixed marker in
// this implementation (no time-decoding). The collision
// surface is 2^128, more than enough for any realistic
// volume.
//
// Returns (id, error). Errors only occur on
// crypto/rand.Read failures.
func GenerateVeloxDeliveryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("velox delivery id mint: rand.Read: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "sdel_01J" + strings.ToLower(encoded), nil
}

// IsNonEmptyJSONObject returns true when raw is a non-empty
// JSON object (lenient — accepts trailing/leading whitespace).
// Rejects empty objects ("{}"), arrays ("[1,2]"), null, and
// non-object types. The caller uses this to enforce the
// "metadata must be a non-empty JSON object" rule so the
// downstream publish_worker has at least one field to extract.
func IsNonEmptyJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	// Strip leading/trailing whitespace.
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return false
	}
	return len(m) > 0
}

// MergeVeloxDestinationMetadata merges destination default
// metadata into the Velox-provided metadata payload and ensures
// target_account_ids points to the destination's platform
// account. Mutates and returns a fresh JSON blob.
func MergeVeloxDestinationMetadata(dest *models.ExternalDestination, raw json.RawMessage) json.RawMessage {
	meta := make(map[string]any)
	_ = json.Unmarshal(raw, &meta)
	defaults, err := dest.DefaultMetadataAsMap()
	if err == nil {
		for k, v := range defaults {
			if _, exists := meta[k]; !exists {
				meta[k] = v
			}
		}
	}
	// The destination row, not Velox, chooses the actual InstaEdit target.
	if _, exists := meta["target_account_ids"]; !exists {
		meta["target_account_ids"] = []int64{dest.PlatformAccountID}
	}
	merged, err := json.Marshal(meta)
	if err != nil {
		return raw
	}
	return merged
}

// ExtractVeloxMetaString best-effort-parses the JSONB metadata blob
// the producer carries from Velox. Returns "" when the blob is empty,
// unparsable, or the requested key is missing / non-string. Used
// to forward title / description / privacy_status to the downloader's
// worker.VeloxDownloadJob payload.
//
// Best-effort is intentional: the producer accepts arbitrary
// metadata and only validates the SHA256/size/mime externally-
// controlled triple. An unparsable metadata blob would surface
// at the YouTube publish call as "missing title" — operator-
// diagnosable via the dashboard without breaking the 500ms SLA.
func ExtractVeloxMetaString(metadata json.RawMessage, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(metadata, &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}
