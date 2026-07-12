// Package crypto — AES-256-GCM encryption with multi-key support
// (SPRINT 5.3, P1#11).
//
// SPRINT 5.3 motivation: before this change, Encryptor wrapped a
// single AES-256-GCM key — rotating the key required a full
// re-encryption of every row and was a single-point-of-failure for
// the multi-replica deployment (one replica deploying the new key
// could not read rows written by an older replica). The new
// Encryptor carries a map of key_id → AEAD and a self-describing
// envelope format that names the key id in the ciphertext itself,
// so any replica with the historical key in its map can read any
// row regardless of which key was used to write it.
//
// Envelope byte layout (envelopeVersion = 0x01):
//
//	[0x01 (1 byte)] [KeyID (4 bytes, uint32 BE)] [Nonce (12 bytes)] [Ciphertext+Tag]
//
// Legacy (pre-Sprint 5.3) ciphertexts have no version prefix:
//
//	[Nonce (12 bytes)] [Ciphertext+Tag]
//
// Decrypt first checks for the version prefix. If present, it
// parses the new envelope. If the new-envelope parse fails (key id
// not in the map) OR the AEAD open fails, it falls back to the
// legacy format using LegacyKeyID. The fallback handles the 1/256
// random chance of a legacy nonce starting with 0x01 — opening a
// legacy envelope with the new dispatch yields AEAD failure, and
// the fallback recovers.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Envelope version byte for the multi-key envelope format.
// Bumping this byte is the migration boundary: v1 envelopes are
// self-describing; future formats (KCV, header AEAD, etc) would
// use a different version byte and the dispatch would route them
// to a different handler.
const envelopeVersion byte = 0x01

// LegacyKeyID is the default key id assumed for legacy (pre-Sprint
// 5.3) ciphertexts that don't carry the envelope prefix. Operators
// must keep the original single key (the one shipped with
// ENCRYPTION_KEY before Sprint 5.3) in the key map under id=1 for
// the lifetime of the deployment — losing it makes every legacy
// row unreadable.
const LegacyKeyID = uint32(1)

// nonceSize is the AES-256-GCM nonce size. Duplicated from
// cipher.AEAD.NonceSize() to keep Decrypt's offset math readable.
const nonceSize = 12

// keyIDSize is the envelope key-id field size. 4 bytes uint32 BE
// supports up to ~4 billion key rotations before the field needs
// widening.
const keyIDSize = 4

// envelopeHeaderSize is the prefix size (version + key id + nonce).
const envelopeHeaderSize = 1 + keyIDSize + nonceSize

// ErrKeyNotFound is returned when Decrypt encounters an envelope
// whose embedded key id is not in the encryptor's key map. The
// operator's response: add the missing key to the ENCRYPTION_KEYS
// env var, redeploy, retry.
var ErrKeyNotFound = errors.New("crypto: key id not in key map")

// ErrCipherTooShort is returned when the ciphertext is shorter
// than the minimum envelope (a legacy nonce alone).
var ErrCipherTooShort = errors.New("crypto: ciphertext too short")

// ErrKeyMapEmpty is returned by NewEncryptor when the keys map is
// empty. Empty key maps are always a configuration error.
var ErrKeyMapEmpty = errors.New("crypto: keys map is empty")

// Encryptor handles AES-256-GCM encryption and decryption with
// multi-key support. Each plaintext is encrypted with the active
// key; the resulting envelope is self-describing (carries the
// key_version). Decrypt reads the embedded key id and dispatches
// to the right key.
type Encryptor struct {
	aeads       map[uint32]cipher.AEAD
	activeKeyID uint32
}

// NewEncryptor constructs a multi-key encryptor. The map MUST
// contain an entry for activeKeyID. All keys MUST be 32 bytes
// (AES-256) base64-encoded. Returns an error if the map is empty,
// the active key is missing, any key is not valid base64, or any
// key is not 32 bytes.
//
// Backward-compat with the legacy single-key ENCRYPTION_KEY env
// var: cmd/server/main.go calls this with
//
//	NewEncryptor(1, map[uint32]string{1: <ENCRYPTION_KEY>})
//
// when ENCRYPTION_KEYS / ACTIVE_ENCRYPTION_KEY_ID are unset, so
// pre-Sprint 5.3 deployments roll forward without an env-var
// rename.
func NewEncryptor(activeKeyID uint32, keys map[uint32]string) (*Encryptor, error) {
	if len(keys) == 0 {
		return nil, ErrKeyMapEmpty
	}
	if _, ok := keys[activeKeyID]; !ok {
		return nil, fmt.Errorf("crypto: active key id %d not in keys map (have %v)", activeKeyID, mapKeysSorted(keys))
	}
	aeads := make(map[uint32]cipher.AEAD, len(keys))
	for id, b64Key := range keys {
		raw, err := base64.StdEncoding.DecodeString(b64Key)
		if err != nil {
			return nil, fmt.Errorf("crypto: key id %d not valid base64: %w", id, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("crypto: key id %d must be 32 bytes (got %d)", id, len(raw))
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, fmt.Errorf("crypto: key id %d AES cipher: %w", id, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("crypto: key id %d GCM: %w", id, err)
		}
		aeads[id] = aead
	}
	return &Encryptor{aeads: aeads, activeKeyID: activeKeyID}, nil
}

// ActiveKeyID returns the key id used for new Encrypt calls. The
// vault stamps this onto the Token row's key_version column so
// the rotation worker can quickly identify rows needing rotation.
func (e *Encryptor) ActiveKeyID() uint32 { return e.activeKeyID }

// HasKey returns true when the supplied key id is in the key map.
// Used by the rotation worker to detect config drift (a row
// stamped with a key id no replica knows about is a critical
// operational alert).
func (e *Encryptor) HasKey(keyID uint32) bool {
	_, ok := e.aeads[keyID]
	return ok
}

// NeedsRotation reports whether a stored ciphertext should be
// re-encrypted under the active key. Blocco #2.2 lazy re-encrypt:
// the vault's Get() path calls this on every read; if true, the
// vault decrypts + re-encrypts + persists the new ciphertext
// atomically (idempotent UPDATE WHERE encrypted_token = $old
// guards against concurrent re-encrypts).
//
// Returns true when:
//   - payload is too short to be a v1 envelope
//   - payload doesn't carry the v0x01 envelope prefix (legacy
//     single-key ciphertexts; rotating re-wraps them as v1)
//   - the embedded key id is not the active key id
//
// The 1/256 chance of a legacy nonce starting with 0x01 is
// NOT a rotation trigger here: a payload with the v0x01 prefix
// whose embedded key id == active is NOT stale, even if the
// "true" history was a legacy ciphertext with a collision
// nonce. The vault's Decrypt() already handles that ambiguity
// via the legacy fallback; re-encrypting a successfully-decrypted
// v1 envelope with the active key is a no-op idempotent rewrite.
func (e *Encryptor) NeedsRotation(cipherData []byte) bool {
	if len(cipherData) < envelopeHeaderSize {
		// Too short to be a v1 envelope → either garbage or
		// a legacy ciphertext (nonce + ct only). Either way,
		// re-encrypting is a safe upgrade.
		return true
	}
	if cipherData[0] != envelopeVersion {
		// Legacy format (no envelope prefix). Re-encrypt to
		// migrate the row to the v1 envelope format.
		return true
	}
	embedded := binary.BigEndian.Uint32(cipherData[1 : 1+keyIDSize])
	return embedded != e.activeKeyID
}

// Encrypt encrypts plaintext with the active key. The returned
// envelope is self-describing: the receiving Decrypt call can
// dispatch to the right key without external state.
func (e *Encryptor) Encrypt(plaintext string) ([]byte, error) {
	aead, ok := e.aeads[e.activeKeyID]
	if !ok {
		// Defensive: NewEncryptor rejects missing-active at
		// construction, so this is unreachable in practice.
		return nil, fmt.Errorf("crypto: active key %d not loaded", e.activeKeyID)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: generate nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, []byte(plaintext), nil)
	envelope := make([]byte, 0, envelopeHeaderSize+len(ct))
	envelope = append(envelope, envelopeVersion)
	var keyIDBuf [keyIDSize]byte
	binary.BigEndian.PutUint32(keyIDBuf[:], e.activeKeyID)
	envelope = append(envelope, keyIDBuf[:]...)
	envelope = append(envelope, nonce...)
	envelope = append(envelope, ct...)
	return envelope, nil
}

// Decrypt parses the envelope and dispatches to the right key.
// Legacy (no version prefix) ciphertexts are dispatched to
// LegacyKeyID.
//
// Dispatch order:
//  1. If len(payload) < nonceSize → ErrCipherTooShort.
//  2. If payload[0] == envelopeVersion AND len(payload) >=
//     envelopeHeaderSize AND the embedded key id is in the map →
//     try AEAD open with that key. On success, return.
//  3. Otherwise (no prefix, missing key, or AEAD open failure) →
//     fall back to legacy format with LegacyKeyID. This handles
//     the 1/256 chance of a legacy nonce starting with 0x01
//     (AEAD open fails on the new dispatch, fallback succeeds).
func (e *Encryptor) Decrypt(encrypted []byte) (string, error) {
	if len(encrypted) < nonceSize {
		return "", ErrCipherTooShort
	}
	// Try the new envelope first if the prefix matches and the
	// payload is long enough.
	if encrypted[0] == envelopeVersion && len(encrypted) >= envelopeHeaderSize {
		keyID := binary.BigEndian.Uint32(encrypted[1 : 1+keyIDSize])
		nonce := encrypted[1+keyIDSize : envelopeHeaderSize]
		ct := encrypted[envelopeHeaderSize:]
		if aead, ok := e.aeads[keyID]; ok {
			pt, err := aead.Open(nil, nonce, ct, nil)
			if err == nil {
				return string(pt), nil
			}
			// Open failed: most likely a 1/256 random legacy
			// nonce that started with 0x01. Fall through to
			// the legacy dispatch.
		}
		// Key not in map OR open failed: fall through.
	}
	// Legacy format: assume LegacyKeyID. The first nonceSize
	// bytes are the nonce; the rest is ciphertext+tag.
	nonce := encrypted[:nonceSize]
	ct := encrypted[nonceSize:]
	aead, ok := e.aeads[LegacyKeyID]
	if !ok {
		return "", fmt.Errorf("%w: legacy key id %d", ErrKeyNotFound, LegacyKeyID)
	}
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	return string(pt), nil
}

// mapKeysSorted returns the keys of m in ascending order, used
// only for diagnostic error messages. Kept private.
func mapKeysSorted(m map[uint32]string) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple insertion sort — keys are uint32, small N (typically 1-3)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
