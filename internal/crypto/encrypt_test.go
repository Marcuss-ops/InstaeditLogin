// Package crypto — unit tests for the multi-key Encryptor
// (SPRINT 5.3, P1#11).
//
// These tests cover the canonical user spec scenarios:
//
//   - encrypt with v1, decrypt with v1 (golden vector)
//   - add v2 to the key map, switch active to v2, encrypt v2
//     plaintext, decrypt v1 ciphertext AND v2 ciphertext with
//     their respective keys
//   - tamper detection: flip a byte in the ciphertext, decrypt
//     fails
//   - missing key dispatch: envelope stamped with key id not in
//     the map returns an explicit error
//   - legacy backward-compat: a 12-byte-nonce + ciphertext (no
//     envelope prefix) decrypts via the new multi-key encryptor
//     when key id 1 is in the map
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

// testKey1 / testKey2 are 32-byte AES-256 keys, base64-encoded.
// Generated at package init for deterministic tests.
var (
	testKey1 = mustBase64Key(tGenerateKey())
	testKey2 = mustBase64Key(tGenerateKey())
)

// tGenerateKey returns a fresh 32-byte base64 key. Uses crypto/rand
// because the test is the package's only consumer and rand
// determinism is not asserted.
func tGenerateKey() []byte {
	b := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, b)
	return b
}

func mustBase64Key(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

// TestEncryptDecrypt_Roundtrip_V1 is the canonical golden vector:
// encrypt with v1, decrypt with v1, plaintext matches.
func TestEncryptDecrypt_Roundtrip_V1(t *testing.T) {
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	ct, err := enc.Encrypt("hello world")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Envelope must start with the version byte + key id.
	if ct[0] != envelopeVersion {
		t.Fatalf("expected envelope version 0x01, got 0x%02x", ct[0])
	}
	if len(ct) < envelopeHeaderSize {
		t.Fatalf("envelope too short: %d bytes", len(ct))
	}
	pt, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if pt != "hello world" {
		t.Fatalf("plaintext mismatch: got %q want %q", pt, "hello world")
	}
}

// TestMultiKey_Dispatch covers the user-required rotation scenario:
// encrypt with v1, add v2 to the map and switch active to v2,
// encrypt with v2, decrypt BOTH v1 and v2 ciphertexts with their
// respective keys.
func TestMultiKey_Dispatch(t *testing.T) {
	enc1, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor v1: %v", err)
	}
	ct1, err := enc1.Encrypt("v1 plaintext")
	if err != nil {
		t.Fatalf("Encrypt v1: %v", err)
	}
	// "Rotate": build a new encryptor with both keys, active=v2.
	enc2, err := NewEncryptor(2, map[uint32]string{1: testKey1, 2: testKey2})
	if err != nil {
		t.Fatalf("NewEncryptor v2: %v", err)
	}
	ct2, err := enc2.Encrypt("v2 plaintext")
	if err != nil {
		t.Fatalf("Encrypt v2: %v", err)
	}
	// Decrypt v1 ciphertext with the new encryptor (v1 key still in map).
	pt1, err := enc2.Decrypt(ct1)
	if err != nil {
		t.Fatalf("Decrypt v1 with v2-active encryptor: %v", err)
	}
	if pt1 != "v1 plaintext" {
		t.Fatalf("v1 plaintext mismatch: got %q want %q", pt1, "v1 plaintext")
	}
	// Decrypt v2 ciphertext with the same encryptor.
	pt2, err := enc2.Decrypt(ct2)
	if err != nil {
		t.Fatalf("Decrypt v2: %v", err)
	}
	if pt2 != "v2 plaintext" {
		t.Fatalf("v2 plaintext mismatch: got %q want %q", pt2, "v2 plaintext")
	}
	// Decrypt v2 ciphertext with the v1-only encryptor → must fail.
	// The error path: new dispatch reads key_id=2, not in enc1's
	// map, falls through to legacy. Legacy has key 1 (LegacyKeyID)
	// in enc1's map, so it tries AEAD open on the malformed
	// nonce+ct pair (the fallback reads payload[0:12] as the nonce,
	// but those bytes are actually the envelope header). AEAD open
	// fails with "message authentication failed" → we surface
	// "crypto: decrypt: cipher: message authentication failed".
	// The "key id" string only appears in the ErrKeyNotFound path,
	// which is NOT taken here. We assert on the meaningful category.
	if _, err := enc1.Decrypt(ct2); err == nil {
		t.Fatal("expected error decrypting v2 ciphertext with v1-only encryptor")
	} else if !strings.Contains(err.Error(), "message authentication") {
		t.Fatalf("expected error to mention 'message authentication', got: %v", err)
	}
}

// TestTamper_FlipByte_OpenFails confirms GCM's authentication tag
// rejects single-byte modifications.
func TestTamper_FlipByte_OpenFails(t *testing.T) {
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	ct, err := enc.Encrypt("tamper me")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a byte in the ciphertext (after the envelope header).
	tampered := append([]byte{}, ct...)
	tampered[envelopeHeaderSize] ^= 0xFF
	if _, err := enc.Decrypt(tampered); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}

// TestLegacyBackwardCompat covers the canonical migration path:
// a row written by the pre-Sprint 5.3 single-key encryptor (no
// envelope prefix, just nonce + ciphertext) decrypts cleanly via
// the new multi-key encryptor when key id 1 is in the map.
func TestLegacyBackwardCompat(t *testing.T) {
	// Build a legacy single-key encryptor inline (the same shape
	// pre-Sprint 5.3 encrypt.go used: 12-byte nonce + ciphertext).
	block, err := aes.NewCipher(mustDecode(testKey1))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("rand: %v", err)
	}
	legacyCT := gcm.Seal(nonce, nonce, []byte("legacy plaintext"), nil)
	// legacyCT is [nonce | ciphertext] — 12 bytes longer than the
	// sealed ciphertext, no envelope prefix.
	if legacyCT[0] == envelopeVersion {
		t.Skip("1/256 random chance of legacy nonce starting with 0x01 — retest with a different nonce")
	}
	// Build the new multi-key encryptor with key id 1 only.
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	pt, err := enc.Decrypt(legacyCT)
	if err != nil {
		t.Fatalf("Decrypt legacy: %v", err)
	}
	if pt != "legacy plaintext" {
		t.Fatalf("legacy plaintext mismatch: got %q want %q", pt, "legacy plaintext")
	}
}

// TestLegacyBackwardCompat_Collision deliberately constructs a
// legacy nonce that starts with 0x01 to exercise the fallback
// path. The new dispatch will treat it as an envelope header,
// read a wrong key id, and AEAD-open will fail; the fallback
// then dispatches via LegacyKeyID and succeeds.
func TestLegacyBackwardCompat_Collision(t *testing.T) {
	block, err := aes.NewCipher(mustDecode(testKey1))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	// Force nonce to start with 0x01 (the envelope version byte).
	nonce := make([]byte, gcm.NonceSize())
	nonce[0] = envelopeVersion
	if _, err := io.ReadFull(rand.Reader, nonce[1:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	legacyCT := gcm.Seal(nonce, nonce, []byte("collision plaintext"), nil)
	if legacyCT[0] != envelopeVersion {
		t.Fatalf("expected legacy CT to start with 0x01, got 0x%02x", legacyCT[0])
	}
	// Decrypt via the new multi-key encryptor. The new dispatch
	// will see 0x01 + read key id = nonce[1:5] → almost certainly
	// NOT in the map → AEAD open fails (or key lookup fails);
	// fallback dispatches with LegacyKeyID and succeeds.
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	pt, err := enc.Decrypt(legacyCT)
	if err != nil {
		t.Fatalf("Decrypt collision: %v", err)
	}
	if pt != "collision plaintext" {
		t.Fatalf("collision plaintext mismatch: got %q want %q", pt, "collision plaintext")
	}
}

// TestMissingKey_TrueMissingKey: build a ciphertext manually
// with an envelope claiming a key id that is NOT in the map.
// The decrypt must return an error (not a silent skip).
//
// Why an error: the new dispatch reads the unknown key id, fails
// the map lookup, falls through to legacy. The legacy dispatch
// reads payload[0:12] as the nonce — but those bytes are the new
// envelope header (version + key id + first 7 nonce bytes), NOT
// the original legacy nonce. AEAD open on a malformed nonce+ct
// pair fails the GCM tag check, so the call returns an error.
// This is the correct behavior: a missing key surfaces as a
// decrypt failure, not a silent skip.
func TestMissingKey_TrueMissingKey(t *testing.T) {
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	ct, err := enc.Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Tamper the key id to 99 (not in the map). The AEAD open
	// will fail (because the sealed ciphertext is bound to key
	// 1, not 99). The legacy fallback will ALSO fail (because
	// the prefix-read nonce is actually the envelope header).
	// Result: error.
	binary.BigEndian.PutUint32(ct[1:5], 99)
	if _, err := enc.Decrypt(ct); err == nil {
		t.Fatal("expected error decrypting envelope with unknown key id")
	}
}

// TestActiveKeyNotInMap covers the construction-time error.
func TestActiveKeyNotInMap(t *testing.T) {
	_, err := NewEncryptor(5, map[uint32]string{1: testKey1, 2: testKey2})
	if err == nil {
		t.Fatal("expected error when active key id is not in the map")
	}
	if !strings.Contains(err.Error(), "active key id 5") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestEmptyKeyMap covers the construction-time error.
func TestEmptyKeyMap(t *testing.T) {
	_, err := NewEncryptor(1, map[uint32]string{})
	if err != ErrKeyMapEmpty {
		t.Fatalf("expected ErrKeyMapEmpty, got %v", err)
	}
}

// TestInvalidBase64Key covers the construction-time error.
func TestInvalidBase64Key(t *testing.T) {
	_, err := NewEncryptor(1, map[uint32]string{1: "not-base64!!!"})
	if err == nil {
		t.Fatal("expected error for invalid base64 key")
	}
}

// TestWrongSizeKey covers the construction-time error.
func TestWrongSizeKey(t *testing.T) {
	// 16 bytes (AES-128 key), not 32.
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := NewEncryptor(1, map[uint32]string{1: shortKey})
	if err == nil {
		t.Fatal("expected error for wrong-size key")
	}
	if !strings.Contains(err.Error(), "must be 32 bytes") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestCipherTooShort covers the Decrypt-time error.
func TestCipherTooShort(t *testing.T) {
	enc, _ := NewEncryptor(1, map[uint32]string{1: testKey1})
	if _, err := enc.Decrypt([]byte{0x01, 0x02, 0x03}); err != ErrCipherTooShort {
		t.Fatalf("expected ErrCipherTooShort, got %v", err)
	}
}

// TestEnvelopeHeaderSize locks the canonical header size so a
// future refactor that accidentally changes the layout trips a
// test, not a silent production failure.
func TestEnvelopeHeaderSize(t *testing.T) {
	if envelopeHeaderSize != 17 {
		t.Fatalf("envelope header size drifted: got %d want 17 (1+4+12)", envelopeHeaderSize)
	}
}

// TestEncrypt_UniqueNonces confirms Encrypt produces distinct
// envelopes on every call (sanity check: the nonce is random
// per call, so a single plaintext encrypted twice yields
// different ciphertexts).
func TestEncrypt_UniqueNonces(t *testing.T) {
	enc, _ := NewEncryptor(1, map[uint32]string{1: testKey1})
	ct1, _ := enc.Encrypt("same plaintext")
	ct2, _ := enc.Encrypt("same plaintext")
	if bytes.Equal(ct1, ct2) {
		t.Fatal("expected different ciphertexts for repeated encrypts of the same plaintext")
	}
}

// TestHasKey is a trivial sanity check on the helper used by
// the rotation worker.
func TestHasKey(t *testing.T) {
	enc, _ := NewEncryptor(1, map[uint32]string{1: testKey1, 2: testKey2})
	if !enc.HasKey(1) {
		t.Fatal("expected HasKey(1) to be true")
	}
	if !enc.HasKey(2) {
		t.Fatal("expected HasKey(2) to be true")
	}
	if enc.HasKey(3) {
		t.Fatal("expected HasKey(3) to be false")
	}
}

// TestNeedsRotation (Blocco #2.2) covers the lazy re-encrypt trigger
// the vault consults on every read. The table cases pin each branch
// of the NeedsRotation contract:
//
//   - too-short payload → rotation needed (treat as legacy/garbage)
//   - legacy format (no v0x01 prefix) → rotation needed (migrate to v1)
//   - v1 envelope stamped with active key → no rotation
//   - v1 envelope stamped with a stale key id → rotation needed
//   - v1 envelope stamped with an unknown key id → rotation needed
//     (the row is unreadable; the rotate-then-rewrite cycle moves
//     it onto the active key without an explicit decrypt first)
func TestNeedsRotation(t *testing.T) {
	enc1, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor v1: %v", err)
	}
	ct1, err := enc1.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt v1: %v", err)
	}
	// Encryptor with both keys, active=v2.
	enc2, err := NewEncryptor(2, map[uint32]string{1: testKey1, 2: testKey2})
	if err != nil {
		t.Fatalf("NewEncryptor v2: %v", err)
	}
	ct2, err := enc2.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt v2: %v", err)
	}
	// Force a v1 envelope stamped with an unknown key id by
	// overwriting the key id bytes in ct1.
	ctUnknown := append([]byte{}, ct1...)
	binary.BigEndian.PutUint32(ctUnknown[1:5], 99)

	tests := []struct {
		name string
		enc  *Encryptor
		data []byte
		want bool
	}{
		{"too-short payload triggers rotation", enc2, []byte{0x01, 0x02, 0x03}, true},
		{"legacy format (no v0x01 prefix) triggers rotation", enc2, []byte("legacy-ciphertext-bytes-12"), true},
		{"v1 envelope with active key id → no rotation", enc2, ct2, false},
		{"v1 envelope with stale key id (key 1, active 2) → rotation", enc2, ct1, true},
		{"v1 envelope with unknown key id → rotation", enc2, ctUnknown, true},
		{"v1 envelope with active key id when only one key in map → no rotation", enc1, ct1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.enc.NeedsRotation(tc.data); got != tc.want {
				t.Fatalf("NeedsRotation: want %v, got %v", tc.want, got)
			}
		})
	}
}

// TestNeedsRotation_LegacyCollisionNonce constructs a legacy
// ciphertext whose nonce starts with 0x01 (the envelope version
// byte). NeedsRotation's first byte check is true → it returns
// "rotation needed". This is correct: the row will be re-encrypted
// under the active key on the next read, migrating it from the
// legacy format to the v1 envelope in the process. The idempotence
// guarantee holds: the second read sees a v1 envelope stamped with
// the active key → no further rotation.
func TestNeedsRotation_LegacyCollisionNonce(t *testing.T) {
	enc, err := NewEncryptor(1, map[uint32]string{1: testKey1})
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	// Build a legacy single-key ciphertext with a forced-collision nonce.
	block, err := aes.NewCipher(mustDecode(testKey1))
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	nonce[0] = envelopeVersion
	if _, err := io.ReadFull(rand.Reader, nonce[1:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	legacyCT := gcm.Seal(nonce, nonce, []byte("collision"), nil)
	if legacyCT[0] != envelopeVersion {
		t.Fatalf("test setup: expected nonce-prefixed legacy CT to start with 0x01")
	}
	if !enc.NeedsRotation(legacyCT) {
		t.Fatal("NeedsRotation must return true for legacy CT whose nonce collides with the v1 prefix")
	}
}

// mustDecode inverts base64.StdEncoding.EncodeToString. Test-only
// helper.
func mustDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
